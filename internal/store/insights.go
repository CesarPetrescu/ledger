package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
)

// EmbedInput is an entry whose metadata is settled but has no vector yet.
type EmbedInput struct {
	ID   int64
	Text string
}

// EntriesToEmbed returns entries whose title is extracted (or whose extraction
// gave up) but which have no entry-level vector for the given model.
func (db *DB) EntriesToEmbed(ctx context.Context, model string, limit int) ([]EmbedInput, error) {
	rows, err := db.Pool.Query(ctx, `SELECT e.id,CASE WHEN m.title<>'' THEN m.title||E'\n' ELSE '' END||left(e.body,2000)
FROM entry_meta m JOIN entry e ON e.id=m.entry_id
WHERE (m.embedding IS NULL OR m.embed_model<>$1) AND (m.title<>'' OR m.attempts>=$2)
ORDER BY e.created_at DESC,e.id DESC LIMIT $3`, model, MetaMaxAttempts, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (EmbedInput, error) {
		var in EmbedInput
		return in, row.Scan(&in.ID, &in.Text)
	})
}

// SaveEntryEmbedding stores a vector and queues the entry for duplicate checks.
func (db *DB) SaveEntryEmbedding(ctx context.Context, entryID int64, model string, vector []float32) error {
	_, err := db.Pool.Exec(ctx, `UPDATE entry_meta SET embedding=$2,embed_model=$3,duplicate_of=NULL,duplicate_checked=false WHERE entry_id=$1`,
		entryID, pgvector.NewHalfVector(vector), model)
	return err
}

// LinkDuplicates checks the oldest unchecked entry (or one checked with a
// different threshold) against earlier settled entries of the same kind and
// project, and links it to the root of the most similar one when similarity
// reaches threshold. Working oldest first, one entry per call, keeps every
// link pointing at a root, so repeats never form chains. Todos are never
// folded: each one is resolved on its own, so similar todos stay separate.
// ponytail: exact vector scan per project; add an HNSW index past ~50k entries.
func (db *DB) LinkDuplicates(ctx context.Context, model string, threshold float64) (int64, error) {
	tag, err := db.Pool.Exec(ctx, `WITH p AS (
  SELECT m.entry_id,e.slug,e.kind,e.created_at,m.embedding FROM entry_meta m JOIN entry e ON e.id=m.entry_id
  WHERE e.kind<>'todo' AND m.embedding IS NOT NULL AND m.embed_model=$1 AND (NOT m.duplicate_checked OR m.duplicate_threshold IS DISTINCT FROM $2)
  ORDER BY e.created_at,e.id LIMIT 1
)
UPDATE entry_meta m SET duplicate_checked=true,duplicate_threshold=$2,duplicate_of=(
  SELECT COALESCE(o.duplicate_of,o.entry_id) FROM entry_meta o JOIN entry oe ON oe.id=o.entry_id
  WHERE oe.slug=p.slug AND oe.kind=p.kind AND (oe.created_at,oe.id)<(p.created_at,p.entry_id) AND o.embed_model=$1 AND o.embedding IS NOT NULL
    AND o.duplicate_checked AND o.duplicate_threshold=$2 AND 1-(o.embedding<=>p.embedding)>=$2
  ORDER BY o.embedding<=>p.embedding LIMIT 1)
FROM p WHERE m.entry_id=p.entry_id`, model, threshold)
	return tag.RowsAffected(), err
}

// Heartbeat records that a background worker is alive.
func (db *DB) Heartbeat(ctx context.Context, name string) error {
	_, err := db.Pool.Exec(ctx, `INSERT INTO worker_heartbeat(name) VALUES($1) ON CONFLICT(name) DO UPDATE SET seen_at=now()`, name)
	return err
}

// RelatedEntry is a similar entry with its cosine similarity.
type RelatedEntry struct {
	EntryWithProject
	Similarity float64 `json:"similarity"`
}

// RelatedEntries finds the most similar other entries across all projects.
// Folded repeats are skipped so each story appears once, as does the
// original of the entry itself when it is a repeat.
func (db *DB) RelatedEntries(ctx context.Context, entryID int64, minSimilarity float64, limit int) ([]RelatedEntry, error) {
	rows, err := db.Pool.Query(ctx, `WITH q AS (SELECT embedding,embed_model,duplicate_of FROM entry_meta WHERE entry_id=$1 AND embedding IS NOT NULL)
SELECT e.id,e.slug,e.kind,e.body,e.source,e.client_id,e.created_at,p.name,m.title,1-(m.embedding<=>q.embedding)
FROM q JOIN entry_meta m ON m.embed_model=q.embed_model AND m.embedding IS NOT NULL JOIN entry e ON e.id=m.entry_id JOIN project p ON p.slug=e.slug
WHERE m.entry_id<>$1 AND m.duplicate_of IS NULL AND m.entry_id IS DISTINCT FROM q.duplicate_of AND 1-(m.embedding<=>q.embedding)>=$2
ORDER BY m.embedding<=>q.embedding LIMIT $3`, entryID, minSimilarity, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (RelatedEntry, error) {
		var r RelatedEntry
		r.Meta = &EntryMeta{Tags: []string{}, Refs: []string{}}
		return r, row.Scan(&r.ID, &r.Slug, &r.Kind, &r.Body, &r.Source, &r.ClientID, &r.CreatedAt, &r.ProjectName, &r.Meta.Title, &r.Similarity)
	})
}

// TagCount is a tag in use and whether the consolidator has reviewed it.
type TagCount struct {
	Tag      string `json:"tag"`
	Count    int    `json:"count"`
	Reviewed bool   `json:"-"`
}

func (db *DB) TagCounts(ctx context.Context) ([]TagCount, error) {
	rows, err := db.Pool.Query(ctx, `SELECT t.tag,count(*),EXISTS (SELECT 1 FROM tag_vocab v WHERE v.tag=t.tag)
FROM entry_meta m, unnest(m.tags) t(tag) GROUP BY t.tag ORDER BY count(*) DESC,t.tag`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (TagCount, error) {
		var c TagCount
		return c, row.Scan(&c.Tag, &c.Count, &c.Reviewed)
	})
}

// TagAliases maps merged tags to the tag they were merged into.
func (db *DB) TagAliases(ctx context.Context) (map[string]string, error) {
	rows, err := db.Pool.Query(ctx, `SELECT tag,canonical FROM tag_vocab WHERE canonical IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	aliases := map[string]string{}
	for rows.Next() {
		var tag, canonical string
		if err := rows.Scan(&tag, &canonical); err != nil {
			return nil, err
		}
		aliases[tag] = canonical
	}
	return aliases, rows.Err()
}

// LastTagReview returns when tags were last consolidated, or zero if never.
func (db *DB) LastTagReview(ctx context.Context) (time.Time, error) {
	var at *time.Time
	err := db.Pool.QueryRow(ctx, `SELECT max(reviewed_at) FROM tag_vocab`).Scan(&at)
	if at == nil {
		return time.Time{}, err
	}
	return *at, err
}

// ApplyTagMerges records the reviewed tags and rewrites merged tags on every
// entry, keeping each entry's first-seen tag order.
func (db *DB) ApplyTagMerges(ctx context.Context, reviewed []string, merges map[string]string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, tag := range reviewed {
		var canonical *string
		if to, ok := merges[tag]; ok {
			canonical = &to
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tag_vocab(tag,canonical) VALUES($1,$2) ON CONFLICT(tag) DO UPDATE SET canonical=EXCLUDED.canonical,reviewed_at=now()`, tag, canonical); err != nil {
			return err
		}
	}
	// Earlier aliases whose target was just merged away follow it, so every
	// alias names a live canonical tag.
	for range 10 {
		tag, err := tx.Exec(ctx, `UPDATE tag_vocab v SET canonical=w.canonical FROM tag_vocab w WHERE v.canonical=w.tag AND w.canonical IS NOT NULL AND w.canonical<>v.tag`)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			break
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE entry_meta m SET tags=(
  SELECT COALESCE(array_agg(tag ORDER BY first),'{}') FROM (
    SELECT COALESCE(v.canonical,t.tag) tag,min(t.ord) first FROM unnest(m.tags) WITH ORDINALITY t(tag,ord)
    LEFT JOIN tag_vocab v ON v.tag=t.tag GROUP BY 1) merged)
WHERE m.tags && ARRAY(SELECT tag FROM tag_vocab WHERE canonical IS NOT NULL)`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DigestInput is a project whose weekly digest needs (re)writing.
type DigestInput struct {
	Slug        string
	ProjectName string
	LastEntryID int64
	Entries     []DigestEntry
}

type DigestEntry struct {
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Text      string    `json:"text"`
	Agent     string    `json:"agent"`
	CreatedAt time.Time `json:"at"`
	Done      bool      `json:"done,omitempty"`
}

const digestWindow = `interval '7 days'`

// NextStaleDigest picks one project with activity in the last week whose
// digest is missing, a day old, or behind newer entries for over 30 minutes.
// Entries awaiting extraction are skipped so digests use settled titles.
func (db *DB) NextStaleDigest(ctx context.Context, maxEntries int, skip []string) (*DigestInput, error) {
	var in DigestInput
	err := db.Pool.QueryRow(ctx, `SELECT p.slug,p.name,w.last_id FROM project p
JOIN LATERAL (SELECT max(id) last_id FROM entry WHERE slug=p.slug AND created_at>now()-`+digestWindow+`) w ON w.last_id IS NOT NULL
LEFT JOIN project_digest d ON d.slug=p.slug
WHERE (d.slug IS NULL OR d.generated_at<now()-interval '20 hours' OR (d.last_entry_id<w.last_id AND d.generated_at<now()-interval '30 minutes'))
AND NOT p.slug=ANY($2) AND NOT EXISTS (SELECT 1 FROM entry e LEFT JOIN entry_meta m ON m.entry_id=e.id WHERE e.slug=p.slug AND e.created_at>now()-`+digestWindow+` AND (m.entry_id IS NULL OR (m.title='' AND m.attempts<$1)))
ORDER BY d.generated_at NULLS FIRST,p.slug LIMIT 1`, MetaMaxAttempts, append([]string{}, skip...)).Scan(&in.Slug, &in.ProjectName, &in.LastEntryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := db.Pool.Query(ctx, `SELECT e.kind,COALESCE(m.title,''),left(e.body,600),e.source,e.created_at,
 e.kind='todo' AND EXISTS (SELECT 1 FROM entry_meta r WHERE r.resolves=e.id)
FROM entry e LEFT JOIN entry_meta m ON m.entry_id=e.id
WHERE e.slug=$1 AND e.created_at>now()-`+digestWindow+` AND m.duplicate_of IS NULL
ORDER BY e.created_at DESC,e.id DESC LIMIT $2`, in.Slug, maxEntries)
	if err != nil {
		return nil, err
	}
	in.Entries, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (DigestEntry, error) {
		var e DigestEntry
		return e, row.Scan(&e.Kind, &e.Title, &e.Text, &e.Agent, &e.CreatedAt, &e.Done)
	})
	return &in, err
}

func (db *DB) SaveDigest(ctx context.Context, slug, summary string, entryCount int, lastEntryID int64, model string) error {
	_, err := db.Pool.Exec(ctx, `INSERT INTO project_digest(slug,summary,entry_count,last_entry_id,model) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(slug) DO UPDATE SET summary=EXCLUDED.summary,entry_count=EXCLUDED.entry_count,last_entry_id=EXCLUDED.last_entry_id,model=EXCLUDED.model,generated_at=now()`,
		slug, truncateRunes(summary, 1200), entryCount, lastEntryID, model)
	return err
}

// DropQuietDigests removes digests of projects with no entries in the window.
func (db *DB) DropQuietDigests(ctx context.Context) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM project_digest d WHERE NOT EXISTS (SELECT 1 FROM entry e WHERE e.slug=d.slug AND e.created_at>now()-`+digestWindow+`)`)
	return err
}
