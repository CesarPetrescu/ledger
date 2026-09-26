package store

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// EntryMeta is derived metadata for one entry. It never changes the entry.
type EntryMeta struct {
	Title    string   `json:"title"`
	Tags     []string `json:"tags"`
	Priority string   `json:"priority,omitempty"`
	Refs     []string `json:"refs"`
	Resolves *int64   `json:"-"`
	Origin   string   `json:"origin"`
	Model    string   `json:"-"`
	Attempts int      `json:"-"`
	Error    string   `json:"-"`
}

// Resolution names the entry that closed a todo.
type Resolution struct {
	EntryID   int64     `json:"-"`
	Origin    string    `json:"origin"`
	CreatedAt time.Time `json:"created_at"`
}

// PendingEntry is an entry waiting for metadata extraction.
type PendingEntry struct {
	ID          int64
	Slug        string
	ProjectName string
	Kind        string
	Body        string
	Source      string
	CreatedAt   time.Time
}

// TodoCandidate is an open todo the extractor may mark as resolved.
type TodoCandidate struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

const MetaMaxAttempts = 3

var (
	ErrNotTodo         = errors.New("entry is not a todo")
	ErrAlreadyResolved = errors.New("todo is already resolved")
	ErrNotResolved     = errors.New("todo is not resolved")
)

// NextUnlabeledEntry returns the newest entry without metadata, including
// failed extractions that are due for a retry, or nil when none remain.
func (db *DB) NextUnlabeledEntry(ctx context.Context) (*PendingEntry, error) {
	var e PendingEntry
	err := db.Pool.QueryRow(ctx, `SELECT e.id,e.slug,p.name,e.kind,e.body,e.source,e.created_at
FROM entry e JOIN project p ON p.slug=e.slug LEFT JOIN entry_meta m ON m.entry_id=e.id
WHERE m.entry_id IS NULL OR (m.origin='model' AND m.title='' AND m.attempts<$1 AND m.updated_at<now()-interval '10 minutes')
ORDER BY e.created_at DESC,e.id DESC LIMIT 1`, MetaMaxAttempts).Scan(&e.ID, &e.Slug, &e.ProjectName, &e.Kind, &e.Body, &e.Source, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &e, err
}

// OpenTodos lists unresolved todos of a project written no later than before.
func (db *DB) OpenTodos(ctx context.Context, slug string, before time.Time, exclude int64, limit int) ([]TodoCandidate, error) {
	rows, err := db.Pool.Query(ctx, `SELECT e.id,COALESCE(NULLIF(m.title,''),left(e.body,300)) FROM entry e LEFT JOIN entry_meta m ON m.entry_id=e.id
WHERE e.slug=$1 AND e.kind='todo' AND e.id<>$3 AND e.created_at<=$2 AND NOT EXISTS (SELECT 1 FROM entry_meta r WHERE r.resolves=e.id)
ORDER BY e.created_at DESC,e.id DESC LIMIT $4`, slug, before, exclude, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (TodoCandidate, error) {
		var c TodoCandidate
		return c, row.Scan(&c.ID, &c.Text)
	})
}

// SaveEntryMeta stores extracted metadata unless the owner already set it. A
// todo that another entry resolved in the meantime is left to that entry.
func (db *DB) SaveEntryMeta(ctx context.Context, entryID int64, m EntryMeta) error {
	err := db.saveEntryMeta(ctx, entryID, m)
	if m.Resolves != nil && pgErrorCode(err) == "23505" {
		m.Resolves = nil
		err = db.saveEntryMeta(ctx, entryID, m)
	}
	return err
}

func (db *DB) saveEntryMeta(ctx context.Context, entryID int64, m EntryMeta) error {
	_, err := db.Pool.Exec(ctx, `INSERT INTO entry_meta(entry_id,title,tags,priority,refs,resolves,origin,model,attempts,error)
VALUES($1,$2,$3,$4,$5,$6,'model',$7,1,'')
ON CONFLICT(entry_id) DO UPDATE SET title=EXCLUDED.title,tags=EXCLUDED.tags,priority=EXCLUDED.priority,refs=EXCLUDED.refs,
 resolves=EXCLUDED.resolves,model=EXCLUDED.model,attempts=entry_meta.attempts+1,error='',updated_at=now(),
 embedding=NULL,embed_model='',duplicate_of=NULL,duplicate_checked=false,duplicate_threshold=NULL
WHERE entry_meta.origin='model'`, entryID, m.Title, nonNil(m.Tags), m.Priority, nonNil(m.Refs), m.Resolves, m.Model)
	return err
}

// RecordMetaFailure counts a failed extraction so bad entries stop retrying.
func (db *DB) RecordMetaFailure(ctx context.Context, entryID int64, model, message string) error {
	if utf8.RuneCountInString(message) > 500 {
		message = string([]rune(message)[:500])
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO entry_meta(entry_id,origin,model,attempts,error) VALUES($1,'model',$2,1,$3)
ON CONFLICT(entry_id) DO UPDATE SET attempts=entry_meta.attempts+1,model=EXCLUDED.model,error=EXCLUDED.error,updated_at=now()
WHERE entry_meta.origin='model'`, entryID, model, message)
	return err
}

// ClearModelMeta drops every model-derived row so the extractor redoes them,
// along with the digests written from that metadata.
func (db *DB) ClearModelMeta(ctx context.Context) (int64, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM entry_meta WHERE origin='model'`)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_digest`); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

// ResolveTodo appends an owner "Done" status entry that closes the todo.
func (db *DB) ResolveTodo(ctx context.Context, todoID int64, source, clientID string) (Entry, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return Entry{}, err
	}
	defer tx.Rollback(ctx)
	var slug, kind, text string
	var resolved bool
	if err := tx.QueryRow(ctx, `SELECT e.slug,e.kind,COALESCE(NULLIF(m.title,''),e.body),EXISTS (SELECT 1 FROM entry_meta r WHERE r.resolves=e.id)
FROM entry e LEFT JOIN entry_meta m ON m.entry_id=e.id WHERE e.id=$1 FOR UPDATE OF e`, todoID).Scan(&slug, &kind, &text, &resolved); err != nil {
		return Entry{}, err
	}
	if kind != "todo" {
		return Entry{}, ErrNotTodo
	}
	if resolved {
		return Entry{}, ErrAlreadyResolved
	}
	var e Entry
	if err := tx.QueryRow(ctx, `INSERT INTO entry(slug,kind,body,source,client_id) VALUES($1,'status',$2,$3,$4) RETURNING id,slug,kind,body,source,client_id,created_at`,
		slug, truncateRunes("Done: "+text, 4000), source, clientID).Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.ClientID, &e.CreatedAt); err != nil {
		return Entry{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entry_meta(entry_id,title,resolves,origin) VALUES($1,$2,$3,'owner')`, e.ID, truncateRunes("Done: "+text, 120), todoID); err != nil {
		// Another session resolved it after our check; the unique index decides.
		if pgErrorCode(err) == "23505" {
			return Entry{}, ErrAlreadyResolved
		}
		return Entry{}, err
	}
	return e, tx.Commit(ctx)
}

// ReopenTodo detaches the todo's resolution, pins that choice so the model
// cannot re-link it, and appends a "Reopened" status entry so the timeline,
// latest status, and digests record the reversal.
func (db *DB) ReopenTodo(ctx context.Context, todoID int64, source, clientID string) (Entry, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return Entry{}, err
	}
	defer tx.Rollback(ctx)
	var slug, text string
	if err := tx.QueryRow(ctx, `SELECT e.slug,COALESCE(NULLIF(m.title,''),e.body) FROM entry e LEFT JOIN entry_meta m ON m.entry_id=e.id WHERE e.id=$1 AND e.kind='todo' FOR UPDATE OF e`, todoID).Scan(&slug, &text); err != nil {
		if IsNotFound(err) {
			return Entry{}, ErrNotResolved
		}
		return Entry{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE entry_meta SET resolves=NULL,origin='owner',updated_at=now() WHERE resolves=$1`, todoID)
	if err != nil {
		return Entry{}, err
	}
	if tag.RowsAffected() == 0 {
		return Entry{}, ErrNotResolved
	}
	var e Entry
	if err := tx.QueryRow(ctx, `INSERT INTO entry(slug,kind,body,source,client_id) VALUES($1,'status',$2,$3,$4) RETURNING id,slug,kind,body,source,client_id,created_at`,
		slug, truncateRunes("Reopened: "+text, 4000), source, clientID).Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.ClientID, &e.CreatedAt); err != nil {
		return Entry{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO entry_meta(entry_id,title,origin) VALUES($1,$2,'owner')`, e.ID, truncateRunes("Reopened: "+text, 120)); err != nil {
		return Entry{}, err
	}
	return e, tx.Commit(ctx)
}

// MetaProgress reports how many entries have finished metadata extraction.
type MetaProgress struct {
	Total  int `json:"total"`
	Ready  int `json:"ready"`
	Failed int `json:"failed"`
	// Active reports whether the extractor has checked in recently; without
	// it, pending entries will not be processed.
	Active bool `json:"active"`
}

// ExtractorHeartbeat names the metadata extractor in worker_heartbeat.
const ExtractorHeartbeat = "extractor"

func (db *DB) MetaProgress(ctx context.Context) (MetaProgress, error) {
	var p MetaProgress
	err := db.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE m.title<>''),count(*) FILTER (WHERE m.title='' AND m.attempts>=$1),
 EXISTS (SELECT 1 FROM worker_heartbeat WHERE name=$2 AND seen_at>now()-interval '2 minutes')
FROM entry e LEFT JOIN entry_meta m ON m.entry_id=e.id`, MetaMaxAttempts, ExtractorHeartbeat).Scan(&p.Total, &p.Ready, &p.Failed, &p.Active)
	return p, err
}

// ProjectSummary is one row of the table's per-project view.
type ProjectSummary struct {
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	Tier         string     `json:"tier"`
	Deadline     string     `json:"deadline"`
	NeedsMe      string     `json:"needs_me"`
	LastEntryAt  *time.Time `json:"last_entry_at,omitempty"`
	OpenTodos    int        `json:"open_todos"`
	WeekEntries  int        `json:"week_entries"`
	WeekAgents   []string   `json:"week_agents"`
	StatusID     *int64     `json:"-"`
	StatusTitle  string     `json:"status_title"`
	StatusBody   string     `json:"status_body"`
	StatusAt     *time.Time `json:"status_at,omitempty"`
	StatusSource string     `json:"status_source"`
	Digest       string     `json:"digest"`
	DigestAt     *time.Time `json:"digest_at,omitempty"`
}

func (db *DB) ProjectSummaries(ctx context.Context) ([]ProjectSummary, error) {
	rows, err := db.Pool.Query(ctx, `SELECT p.slug,p.name,p.tier,p.deadline,p.needs_me,
 (SELECT max(created_at) FROM entry WHERE slug=p.slug),
 (SELECT count(*) FROM entry t WHERE t.slug=p.slug AND t.kind='todo' AND NOT EXISTS (SELECT 1 FROM entry_meta r WHERE r.resolves=t.id)),
 (SELECT count(*) FROM entry WHERE slug=p.slug AND created_at>now()-interval '7 days'),
 COALESCE((SELECT array_agg(DISTINCT source ORDER BY source) FROM entry WHERE slug=p.slug AND created_at>now()-interval '7 days'),'{}'),
 s.id,COALESCE(sm.title,''),COALESCE(s.body,''),s.created_at,COALESCE(s.source,''),COALESCE(d.summary,''),d.generated_at
FROM project p
LEFT JOIN project_digest d ON d.slug=p.slug
LEFT JOIN LATERAL (SELECT id,body,created_at,source FROM entry WHERE slug=p.slug AND kind='status' ORDER BY created_at DESC,id DESC LIMIT 1) s ON true
LEFT JOIN entry_meta sm ON sm.entry_id=s.id
ORDER BY 6 DESC NULLS LAST,p.slug`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ProjectSummary, error) {
		var s ProjectSummary
		return s, row.Scan(&s.Slug, &s.Name, &s.Tier, &s.Deadline, &s.NeedsMe, &s.LastEntryAt, &s.OpenTodos, &s.WeekEntries, &s.WeekAgents, &s.StatusID, &s.StatusTitle, &s.StatusBody, &s.StatusAt, &s.StatusSource, &s.Digest, &s.DigestAt)
	})
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit-1]) + "…"
}
