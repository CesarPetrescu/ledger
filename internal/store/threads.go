package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// OwnerSource is the source of everything the owner writes from the console or phone.
const OwnerSource = "ledger-admin"

// MaxContextRunes bounds where an agent says an entry came from.
const MaxContextRunes = 300

var (
	ErrReplyNotFound  = errors.New("reply_to entry not found")
	ErrReplyElsewhere = errors.New("reply_to entry belongs to another project")
	ErrInvalidContext = errors.New("context must be at most 300 characters on one line")
)

// NewEntry is an entry to append. ReplyTo answers another entry (its thread
// root is used); Context says where an agent wrote from.
type NewEntry struct {
	Slug, Kind, Body, Source, ClientID string
	ReplyTo                            int64
	Context                            string
}

func (e NewEntry) validate() error {
	if err := ValidateProjectSlug(e.Slug); err != nil {
		return err
	}
	if err := ValidateEntry(e.Kind, e.Body); err != nil {
		return err
	}
	if utf8.RuneCountInString(e.Context) > MaxContextRunes || strings.ContainsAny(e.Context, "\r\n") {
		return ErrInvalidContext
	}
	if e.ReplyTo < 0 {
		return ErrReplyNotFound
	}
	return nil
}

// Append writes an entry. A reply from the owner to an open ask marks the ask
// handled (the returned action undoes that); a reply from an agent puts the
// thread back in front of the owner.
func (db *DB) Append(ctx context.Context, e NewEntry) (Entry, int64, error) {
	if err := e.validate(); err != nil {
		return Entry{}, 0, err
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return Entry{}, 0, err
	}
	defer tx.Rollback(ctx)
	entry, actionID, err := insertEntry(ctx, tx, e)
	if err != nil {
		return Entry{}, 0, err
	}
	return entry, actionID, tx.Commit(ctx)
}

func insertEntry(ctx context.Context, tx pgx.Tx, e NewEntry) (Entry, int64, error) {
	var root *int64
	// The entry answered directly: a follow-up question can itself ask the owner.
	var asked int64
	var askedTitle string
	if e.ReplyTo > 0 {
		// Project before entries, as everywhere else, so a reply racing a
		// project deletion cannot deadlock with it.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM project WHERE slug=$1 FOR KEY SHARE`, e.Slug); err != nil {
			return Entry{}, 0, err
		}
		var id int64
		var slug string
		// A reply's own root stays the root even while that root is in Trash, so
		// the thread is whole again when it is restored. Replies share a project.
		err := tx.QueryRow(ctx, `SELECT COALESCE(reply_to,id),slug FROM entry WHERE id=$1`, e.ReplyTo).Scan(&id, &slug)
		if errors.Is(err, pgx.ErrNoRows) {
			return Entry{}, 0, ErrReplyNotFound
		}
		if err != nil {
			return Entry{}, 0, err
		}
		if slug != e.Slug {
			return Entry{}, 0, ErrReplyElsewhere
		}
		root = &id
		// The owner's answer settles the open question it answers: the entry
		// replied to if it asks one, else the thread's root.
		for _, candidate := range []int64{e.ReplyTo, id} {
			var open bool
			var title string
			// Not labelled yet counts too: an ask found later is then already answered.
			err := tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(m.title,''),left(e.body,120)),
 o.handled_at IS NULL AND e.source<>$2 AND (COALESCE(m.ask,'')<>'' OR m.entry_id IS NULL OR m.title='')
FROM entry e LEFT JOIN entry_meta m ON m.entry_id=e.id LEFT JOIN entry_owner_state o ON o.entry_id=e.id WHERE e.id=$1 FOR UPDATE OF e`, candidate, OwnerSource).Scan(&title, &open)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return Entry{}, 0, err
			}
			if open {
				asked, askedTitle = candidate, title
				break
			}
		}
	}
	var entry Entry
	if err := tx.QueryRow(ctx, `INSERT INTO entry(slug,kind,body,source,client_id,reply_to,context) VALUES($1,$2,$3,$4,$5,$6,$7)
RETURNING id,slug,kind,body,source,client_id,created_at,reply_to,context`, e.Slug, e.Kind, e.Body, e.Source, e.ClientID, root, e.Context).
		Scan(&entry.ID, &entry.Slug, &entry.Kind, &entry.Body, &entry.Source, &entry.ClientID, &entry.CreatedAt, &entry.ReplyTo, &entry.Context); err != nil {
		return Entry{}, 0, err
	}
	if root == nil {
		return entry, 0, nil
	}
	if e.Source != OwnerSource {
		// A root in Trash has no row here; the follow-up still comes back.
		// An agent answered: the thread, and the follow-up it answers, need the owner again, snoozed or not.
		_, err := tx.Exec(ctx, `UPDATE entry_owner_state SET handled_at=NULL,snoozed_until=NULL,updated_at=now() WHERE entry_id IN ($1,$2) AND (handled_at IS NOT NULL OR snoozed_until IS NOT NULL)`, *root, e.ReplyTo)
		return entry, 0, err
	}
	if asked == 0 {
		return entry, 0, nil
	}
	handled := true
	_, before, after, err := applyOwnerPatch(ctx, tx, asked, OwnerPatch{Handled: &handled})
	if err != nil {
		return Entry{}, 0, err
	}
	actionID, err := recordAction(ctx, tx, "owner", &asked, e.Slug, "Replied, marked handled: "+askedTitle,
		map[string]any{"entry_id": asked, "before": before, "after": after})
	return entry, actionID, err
}

// HistoryEvent is one thing that happened to an entry.
type HistoryEvent struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // created, repeat, resolved, action, labels, reply
	Actor   string    `json:"actor"`
	Text    string    `json:"text"`
	EntryID *int64    `json:"entry_id,omitempty"`
	Undone  bool      `json:"undone,omitempty"`
}

// maxHistory bounds an entry's history; the newest events are kept.
const maxHistory = 200

// EntryHistory lists what happened to an entry, oldest first; truncated says
// older events were left out.
func (db *DB) EntryHistory(ctx context.Context, id int64) (events []HistoryEvent, truncated bool, err error) {
	rows, err := db.Pool.Query(ctx, `SELECT * FROM (
 SELECT e.created_at at,'created' kind,e.source actor,COALESCE(c.name,'') text,NULL::bigint entry_id,false undone
   FROM entry e LEFT JOIN oauth_client c ON c.client_id=e.client_id WHERE e.id=$1
 UNION ALL SELECT r.created_at,'repeat',r.source,COALESCE(NULLIF(m.title,''),left(r.body,120)),r.id,false
   FROM entry_meta m JOIN entry r ON r.id=m.entry_id WHERE m.duplicate_of=$1
 -- The owner's own "done" shows as their action below.
 UNION ALL SELECT r.created_at,'resolved',r.source,COALESCE(NULLIF(m.title,''),left(r.body,120)),r.id,false
   FROM entry_meta m JOIN entry r ON r.id=m.entry_id WHERE m.resolves=$1 AND r.source<>$2
 UNION ALL SELECT a.created_at,'action',$2,a.label,NULL,a.undone_at IS NOT NULL
   FROM owner_action a WHERE a.entry_id=$1 AND a.kind<>'read_all'
 UNION ALL SELECT l.created_at,'labels',$2,array_to_string(l.fields,', '),NULL,false
   FROM entry_label_edit l WHERE l.entry_id=$1
 -- Every entry in a thread shows the whole conversation.
 UNION ALL SELECT r.created_at,'reply',r.source,r.body,r.id,false FROM entry r
   WHERE r.reply_to=(SELECT COALESCE(reply_to,id) FROM entry WHERE id=$1) AND r.id<>$1
) h ORDER BY at DESC,kind DESC LIMIT $3`, id, OwnerSource, maxHistory+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []HistoryEvent{}
	for rows.Next() {
		var h HistoryEvent
		if err := rows.Scan(&h.At, &h.Kind, &h.Actor, &h.Text, &h.EntryID, &h.Undone); err != nil {
			return nil, false, err
		}
		// Action labels end with the entry's own title; the history is already about it.
		if h.Kind == "action" {
			h.Text, _, _ = strings.Cut(h.Text, ": ")
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated = len(out) > maxHistory
	out = out[:min(len(out), maxHistory)]
	slices.Reverse(out)
	return out, truncated, nil
}
