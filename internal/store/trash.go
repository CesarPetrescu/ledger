package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TrashRetention is how long deleted projects and entries can be restored.
const TrashRetention = 30 * 24 * time.Hour

// UndoWindow is how long an owner action stays undoable.
const UndoWindow = 7 * 24 * time.Hour

var (
	ErrNotUndoable  = errors.New("this action can no longer be undone")
	ErrProjectGone  = errors.New("restore the project first: it is not in Ledger")
	ErrProjectTaken = errors.New("a project with this slug exists; rename or delete it first")
)

// UndoConflict explains why an action cannot be undone safely.
type UndoConflict struct{ Reason string }

func (e *UndoConflict) Error() string { return e.Reason }

// TrashItem is one deleted project or entry awaiting restore or purge.
type TrashItem struct {
	ID          int64     `json:"-"`
	Kind        string    `json:"kind"`
	Label       string    `json:"label"`
	ProjectSlug string    `json:"project_slug"`
	EntryCount  int       `json:"entry_count"`
	DeletedAt   time.Time `json:"deleted_at"`
	PurgeAt     time.Time `json:"purge_at"`
}

// OwnerAction is one quick action the owner took, for the recent-actions list.
type OwnerAction struct {
	ID          int64      `json:"-"`
	Kind        string     `json:"kind"`
	Label       string     `json:"label"`
	ProjectSlug string     `json:"project_slug"`
	CreatedAt   time.Time  `json:"created_at"`
	UndoneAt    *time.Time `json:"undone_at,omitempty"`
	Undoable    bool       `json:"undoable"`
}

// DeletionPreview is what deleting a project takes with it.
type DeletionPreview struct {
	Name     string `json:"name"`
	Entries  int    `json:"entries"`
	Handoffs int    `json:"handoffs"`
	Files    int    `json:"files"`
}

func recordAction(ctx context.Context, tx pgx.Tx, kind string, entryID *int64, slug, label string, undo any) (int64, error) {
	payload, err := json.Marshal(undo)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO owner_action(kind,entry_id,project_slug,label,undo) VALUES($1,$2,$3,$4,$5) RETURNING id`,
		kind, entryID, slug, truncateRunes(label, 200), payload).Scan(&id)
	return id, err
}

// requeue asks the indexer to rebuild (or, for a removed ref, drop) chunks.
func requeue(ctx context.Context, tx pgx.Tx, refs ...string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO chunk_dirty(ref) SELECT unnest($1::text[]) ON CONFLICT(ref) DO UPDATE SET queued_at=now()`, refs); err != nil {
		return err
	}
	// Wake the indexer on commit, as the insert triggers do.
	_, err := tx.Exec(ctx, `SELECT pg_notify('chunk_dirty',ref) FROM unnest($1::text[]) ref`, refs)
	return err
}

// entryPayload captures an entry with its metadata and triage state. The
// vector is dropped (it is rebuilt) and duplicate links are rechecked.
const entryPayload = `jsonb_build_object(
 'entry',to_jsonb(e),
 'meta',(SELECT (to_jsonb(m)-'embedding')||'{"embed_model":"","duplicate_checked":false,"duplicate_of":null,"duplicate_threshold":null}'::jsonb FROM entry_meta m WHERE m.entry_id=e.id),
 'owner',(SELECT to_jsonb(o) FROM entry_owner_state o WHERE o.entry_id=e.id),
 'receipts',(SELECT jsonb_agg(to_jsonb(w)) FROM entry_write_receipt w WHERE w.entry_id=e.id),
 'resolved_by',(SELECT r.entry_id FROM entry_meta r WHERE r.resolves=e.id))`

// lockEntries locks the entries matching cond (on alias e) and their triage
// rows, so a snapshot taken afterwards includes writes that were in flight.
func lockEntries(ctx context.Context, tx pgx.Tx, cond string, arg any) error {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM entry e WHERE `+cond+` FOR UPDATE`, arg); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT 1 FROM entry_owner_state o JOIN entry e ON e.id=o.entry_id WHERE `+cond+` FOR UPDATE OF o`, arg)
	return err
}

// TrashEntry moves an entry to the trash and records an undoable action.
func (db *DB) TrashEntry(ctx context.Context, entryID int64) (trashID, actionID int64, err error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	var slug, label string
	var payload []byte
	if err := lockEntries(ctx, tx, "e.id=$1", entryID); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT e.slug,COALESCE(NULLIF(m.title,''),left(e.body,120)),`+entryPayload+`
FROM entry e LEFT JOIN entry_meta m ON m.entry_id=e.id WHERE e.id=$1 FOR UPDATE OF e`, entryID).Scan(&slug, &label, &payload); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO trash(kind,label,project_slug,payload) VALUES('entry',$1,$2,$3) RETURNING id`, label, slug, payload).Scan(&trashID); err != nil {
		return 0, 0, err
	}
	// Repeats of this entry lose their root; let them find a new one.
	if _, err := tx.Exec(ctx, `UPDATE entry_meta SET duplicate_of=NULL,duplicate_checked=false WHERE duplicate_of=$1`, entryID); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM entry WHERE id=$1`, entryID); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_digest WHERE slug=$1`, slug); err != nil {
		return 0, 0, err
	}
	if err := requeue(ctx, tx, fmt.Sprintf("entry:%d", entryID)); err != nil {
		return 0, 0, err
	}
	if actionID, err = recordAction(ctx, tx, "trash", &entryID, slug, "Deleted: "+label, map[string]int64{"trash_id": trashID}); err != nil {
		return 0, 0, err
	}
	return trashID, actionID, tx.Commit(ctx)
}

// ProjectDeletionPreview counts what deleting a project removes or unlinks.
func (db *DB) ProjectDeletionPreview(ctx context.Context, slug string) (DeletionPreview, error) {
	var p DeletionPreview
	err := db.Pool.QueryRow(ctx, `SELECT p.name,(SELECT count(*) FROM entry WHERE slug=p.slug),
 (SELECT count(*) FROM handoff WHERE project_slug=p.slug),
 (SELECT count(*) FROM handoff_file f JOIN handoff_message m ON m.id=f.message_id JOIN handoff h ON h.id=m.handoff_id WHERE h.project_slug=p.slug)
FROM project p WHERE p.slug=$1`, slug).Scan(&p.Name, &p.Entries, &p.Handoffs, &p.Files)
	return p, err
}

// TrashProject moves a project and all its entries to the trash. Its
// handoffs (and their files) stay, unlinked, and are relinked on restore.
func (db *DB) TrashProject(ctx context.Context, slug string) (trashID, actionID int64, err error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	var name string
	var count int
	var payload []byte
	var refs []string
	if _, err := tx.Exec(ctx, `SELECT 1 FROM project WHERE slug=$1 FOR UPDATE`, slug); err != nil {
		return 0, 0, err
	}
	if err := lockEntries(ctx, tx, "e.slug=$1", slug); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT p.name,(SELECT count(*) FROM entry WHERE slug=p.slug),jsonb_build_object(
 'project',to_jsonb(p),
 'entries',COALESCE((SELECT jsonb_agg(`+entryPayload+` ORDER BY e.id) FROM entry e WHERE e.slug=p.slug),'[]'::jsonb),
 'handoffs',COALESCE((SELECT jsonb_agg(h.id) FROM handoff h WHERE h.project_slug=p.slug),'[]'::jsonb)),
 ARRAY['project:'||p.slug]||COALESCE((SELECT array_agg('entry:'||id) FROM entry WHERE slug=p.slug),'{}')
FROM project p WHERE p.slug=$1 FOR UPDATE`, slug).Scan(&name, &count, &payload, &refs); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO trash(kind,label,project_slug,entry_count,payload) VALUES('project',$1,$2,$3,$4) RETURNING id`, name, slug, count, payload).Scan(&trashID); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project WHERE slug=$1`, slug); err != nil {
		return 0, 0, err
	}
	if err := requeue(ctx, tx, refs...); err != nil {
		return 0, 0, err
	}
	if actionID, err = recordAction(ctx, tx, "trash", nil, slug, "Deleted project: "+name, map[string]int64{"trash_id": trashID}); err != nil {
		return 0, 0, err
	}
	return trashID, actionID, tx.Commit(ctx)
}

// ListTrash returns deleted items, newest first.
func (db *DB) ListTrash(ctx context.Context) ([]TrashItem, error) {
	rows, err := db.Pool.Query(ctx, `SELECT id,kind,label,project_slug,entry_count,deleted_at FROM trash ORDER BY deleted_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (TrashItem, error) {
		var t TrashItem
		err := row.Scan(&t.ID, &t.Kind, &t.Label, &t.ProjectSlug, &t.EntryCount, &t.DeletedAt)
		t.PurgeAt = t.DeletedAt.Add(TrashRetention)
		return t, err
	})
}

// RestoreTrash puts a deleted project or entry back with its original IDs.
func (db *DB) RestoreTrash(ctx context.Context, trashID int64) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := restore(ctx, tx, trashID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type storedEntry struct {
	Entry      json.RawMessage `json:"entry"`
	Meta       json.RawMessage `json:"meta"`
	Owner      json.RawMessage `json:"owner"`
	Receipts   json.RawMessage `json:"receipts"`
	ResolvedBy *int64          `json:"resolved_by"`
}

func restore(ctx context.Context, tx pgx.Tx, trashID int64) error {
	var kind, slug string
	var payload []byte
	if err := tx.QueryRow(ctx, `SELECT kind,project_slug,payload FROM trash WHERE id=$1 FOR UPDATE`, trashID).Scan(&kind, &slug, &payload); err != nil {
		return err
	}
	var entries []storedEntry
	var handoffs []int64
	if kind == "project" {
		var p struct {
			Project  json.RawMessage `json:"project"`
			Entries  []storedEntry   `json:"entries"`
			Handoffs []int64         `json:"handoffs"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO project SELECT * FROM jsonb_populate_record(NULL::project,$1) ON CONFLICT(slug) DO NOTHING`, []byte(p.Project))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrProjectTaken
		}
		entries, handoffs = p.Entries, p.Handoffs
	} else {
		var e storedEntry
		if err := json.Unmarshal(payload, &e); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM project WHERE slug=$1)`, slug).Scan(&exists); err != nil || !exists {
			if err == nil {
				err = ErrProjectGone
			}
			return err
		}
		entries = []storedEntry{e}
	}
	// Entries first, so metadata links between restored entries resolve.
	for _, e := range entries {
		if _, err := tx.Exec(ctx, `INSERT INTO entry OVERRIDING SYSTEM VALUE SELECT * FROM jsonb_populate_record(NULL::entry,$1)`, []byte(e.Entry)); err != nil {
			return err
		}
	}
	for _, e := range entries {
		if len(e.Meta) > 0 && string(e.Meta) != "null" {
			// A resolution target that is gone or already resolved is dropped.
			if _, err := tx.Exec(ctx, `INSERT INTO entry_meta SELECT (jsonb_populate_record(NULL::entry_meta,
  CASE WHEN EXISTS (SELECT 1 FROM entry WHERE id=($1::jsonb->>'resolves')::bigint)
        AND NOT EXISTS (SELECT 1 FROM entry_meta WHERE resolves=($1::jsonb->>'resolves')::bigint)
   THEN $1::jsonb ELSE $1::jsonb||'{"resolves":null}' END)).*`, []byte(e.Meta)); err != nil {
				return err
			}
		}
		// Idempotent writes stay idempotent: a retried request finds its entry.
		if len(e.Receipts) > 0 && string(e.Receipts) != "null" {
			if _, err := tx.Exec(ctx, `INSERT INTO entry_write_receipt SELECT * FROM jsonb_populate_recordset(NULL::entry_write_receipt,$1) ON CONFLICT DO NOTHING`, []byte(e.Receipts)); err != nil {
				return err
			}
		}
		if len(e.Owner) > 0 && string(e.Owner) != "null" {
			if _, err := tx.Exec(ctx, `INSERT INTO entry_owner_state SELECT * FROM jsonb_populate_record(NULL::entry_owner_state,$1)`, []byte(e.Owner)); err != nil {
				return err
			}
		}
	}
	for _, e := range entries {
		// A restored todo is closed again by the entry that had resolved it.
		if e.ResolvedBy != nil {
			var id int64
			if err := json.Unmarshal(e.Entry, &struct{ ID *int64 }{&id}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE entry_meta SET resolves=$2 WHERE entry_id=$1 AND resolves IS NULL
 AND NOT EXISTS (SELECT 1 FROM entry_meta WHERE resolves=$2)`, *e.ResolvedBy, id); err != nil {
				return err
			}
		}
	}
	if len(handoffs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE handoff SET project_slug=$1 WHERE id=ANY($2) AND project_slug IS NULL`, slug, handoffs); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_digest WHERE slug=$1`, slug); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM trash WHERE id=$1`, trashID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE owner_action SET undone_at=now() WHERE kind='trash' AND (undo->>'trash_id')::bigint=$1 AND undone_at IS NULL`, trashID)
	return err
}

// DeleteTrash permanently removes one trash item.
func (db *DB) DeleteTrash(ctx context.Context, trashID int64) error {
	tag, err := db.Pool.Exec(ctx, `DELETE FROM trash WHERE id=$1`, trashID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

// PurgeTrash permanently removes items past retention. It writes only when
// something is due, so an idle purge emits no change events.
func (db *DB) PurgeTrash(ctx context.Context) (int64, error) {
	var due bool
	cutoff := time.Now().Add(-TrashRetention)
	if err := db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM trash WHERE deleted_at<$1)`, cutoff).Scan(&due); err != nil || !due {
		return 0, err
	}
	tag, err := db.Pool.Exec(ctx, `DELETE FROM trash WHERE deleted_at<$1`, cutoff)
	return tag.RowsAffected(), err
}

// ListActions returns recent owner actions, newest first.
func (db *DB) ListActions(ctx context.Context, limit int) ([]OwnerAction, error) {
	// A deletion whose trash item was since removed for good cannot be undone.
	rows, err := db.Pool.Query(ctx, `SELECT id,kind,label,project_slug,created_at,undone_at,undone_at IS NULL AND created_at>$2
 AND (kind<>'trash' OR EXISTS (SELECT 1 FROM trash t WHERE t.id=(undo->>'trash_id')::bigint))
FROM owner_action WHERE created_at>$2 ORDER BY created_at DESC,id DESC LIMIT $1`, limit, time.Now().Add(-UndoWindow))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (OwnerAction, error) {
		var a OwnerAction
		return a, row.Scan(&a.ID, &a.Kind, &a.Label, &a.ProjectSlug, &a.CreatedAt, &a.UndoneAt, &a.Undoable)
	})
}

// ownerRow is the stored triage state an owner action changed.
type ownerRow struct {
	ReadAt       *time.Time `json:"read_at"`
	Starred      bool       `json:"starred"`
	HandledAt    *time.Time `json:"handled_at"`
	SnoozedUntil *string    `json:"snoozed_until"`
}

// UndoAction reverses one owner action if nothing has changed it since.
func (db *DB) UndoAction(ctx context.Context, actionID int64) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var kind string
	var raw []byte
	var undoable bool
	if err := tx.QueryRow(ctx, `SELECT kind,undo,undone_at IS NULL AND created_at>$2 FROM owner_action WHERE id=$1 FOR UPDATE`,
		actionID, time.Now().Add(-UndoWindow)).Scan(&kind, &raw, &undoable); err != nil {
		return err
	}
	if !undoable {
		return ErrNotUndoable
	}
	var undo struct {
		TodoID         int64     `json:"todo_id"`
		DoneEntryID    int64     `json:"done_entry_id"`
		ResolverID     int64     `json:"resolver_id"`
		ResolverOrigin string    `json:"resolver_origin"`
		ReopenedID     int64     `json:"reopened_entry_id"`
		EntryID        int64     `json:"entry_id"`
		Before         *ownerRow `json:"before"`
		After          ownerRow  `json:"after"`
		TrashID        int64     `json:"trash_id"`
	}
	if err := json.Unmarshal(raw, &undo); err != nil {
		return err
	}
	switch kind {
	case "resolve":
		var still bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM entry_meta WHERE entry_id=$1 AND resolves=$2)`, undo.DoneEntryID, undo.TodoID).Scan(&still); err != nil {
			return err
		}
		if !still {
			return &UndoConflict{"The todo was reopened or its Done entry deleted since."}
		}
		// The console's own bookkeeping entry is removed outright.
		if err := deleteBookkeeping(ctx, tx, undo.DoneEntryID); err != nil {
			return err
		}
	case "reopen":
		var todoExists, resolved, resolverExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM entry WHERE id=$1 FOR UPDATE),EXISTS (SELECT 1 FROM entry_meta WHERE resolves=$1),EXISTS (SELECT 1 FROM entry_meta WHERE entry_id=$2)`,
			undo.TodoID, undo.ResolverID).Scan(&todoExists, &resolved, &resolverExists); err != nil {
			return err
		}
		if !todoExists {
			return &UndoConflict{"The todo was deleted since. Restore it from Trash first."}
		}
		if resolved || !resolverExists {
			return &UndoConflict{"The todo was closed again or its Done entry deleted since."}
		}
		if _, err := tx.Exec(ctx, `UPDATE entry_meta SET resolves=$2,origin=$3,updated_at=now() WHERE entry_id=$1`, undo.ResolverID, undo.TodoID, undo.ResolverOrigin); err != nil {
			return err
		}
		if err := deleteBookkeeping(ctx, tx, undo.ReopenedID); err != nil {
			return err
		}
	case "owner":
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM entry WHERE id=$1 FOR UPDATE)`, undo.EntryID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return &UndoConflict{"The entry was deleted since. Restore it from Trash first."}
		}
		var current ownerRow
		err := tx.QueryRow(ctx, `SELECT read_at,starred,handled_at,to_char(snoozed_until,'YYYY-MM-DD') FROM entry_owner_state WHERE entry_id=$1 FOR UPDATE`, undo.EntryID).
			Scan(&current.ReadAt, &current.Starred, &current.HandledAt, &current.SnoozedUntil)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !sameTime(current.ReadAt, undo.After.ReadAt) || current.Starred != undo.After.Starred ||
			!sameTime(current.HandledAt, undo.After.HandledAt) || !sameDate(current.SnoozedUntil, undo.After.SnoozedUntil) {
			return &UndoConflict{"This entry's read, star, handled, or snooze state changed since."}
		}
		if undo.Before == nil {
			_, err = tx.Exec(ctx, `DELETE FROM entry_owner_state WHERE entry_id=$1`, undo.EntryID)
		} else {
			_, err = tx.Exec(ctx, `UPDATE entry_owner_state SET read_at=$2,starred=$3,handled_at=$4,snoozed_until=$5::date,updated_at=now() WHERE entry_id=$1`,
				undo.EntryID, undo.Before.ReadAt, undo.Before.Starred, undo.Before.HandledAt, undo.Before.SnoozedUntil)
		}
		if err != nil {
			return err
		}
	case "trash":
		if err := restore(ctx, tx, undo.TrashID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &UndoConflict{"It was already restored or permanently deleted."}
			}
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE owner_action SET undone_at=now() WHERE id=$1`, actionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func sameTime(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

func sameDate(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// deleteBookkeeping removes a console-written Done/Reopened entry.
func deleteBookkeeping(ctx context.Context, tx pgx.Tx, entryID int64) error {
	var slug string
	if err := tx.QueryRow(ctx, `DELETE FROM entry WHERE id=$1 RETURNING slug`, entryID).Scan(&slug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &UndoConflict{"The entry this action added was deleted since."}
		}
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_digest WHERE slug=$1`, slug); err != nil {
		return err
	}
	return requeue(ctx, tx, fmt.Sprintf("entry:%d", entryID))
}
