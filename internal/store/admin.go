package store

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminSessionTTL is the absolute lifetime of an operator session.
const AdminSessionTTL = 12 * time.Hour

// AdminSession carries the raw session identifier and CSRF token. Only the
// SHA-256 of ID is stored; the raw value exists solely in the browser cookie.
type AdminSession struct {
	ID        string
	CSRFToken string
	ExpiresAt time.Time
}

func (db *DB) CreateAdminSession(ctx context.Context) (AdminSession, error) {
	id, err := randomToken()
	if err != nil {
		return AdminSession{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return AdminSession{}, err
	}
	hash := sha256.Sum256([]byte(id))
	session := AdminSession{ID: id, CSRFToken: csrf}
	err = db.Pool.QueryRow(ctx, `INSERT INTO admin_session(hash,csrf_token,expires_at) VALUES($1,$2,now()+$3::interval) RETURNING expires_at`, hash[:], csrf, AdminSessionTTL.String()).Scan(&session.ExpiresAt)
	return session, err
}

// LookupAdminSession resolves a live session by its raw identifier and records activity.
func (db *DB) LookupAdminSession(ctx context.Context, id string) (AdminSession, error) {
	hash := sha256.Sum256([]byte(id))
	session := AdminSession{ID: id}
	err := db.Pool.QueryRow(ctx, `UPDATE admin_session SET last_seen_at=now() WHERE hash=$1 AND expires_at>now() RETURNING csrf_token,expires_at`, hash[:]).Scan(&session.CSRFToken, &session.ExpiresAt)
	return session, err
}

func (db *DB) DeleteAdminSession(ctx context.Context, id string) error {
	hash := sha256.Sum256([]byte(id))
	_, err := db.Pool.Exec(ctx, `DELETE FROM admin_session WHERE hash=$1`, hash[:])
	return err
}

// RevokeAdminSessions deletes every session, live or expired.
func (db *DB) RevokeAdminSessions(ctx context.Context) (int64, error) {
	result, err := db.Pool.Exec(ctx, `DELETE FROM admin_session`)
	return result.RowsAffected(), err
}

func (db *DB) ExpireAdminSessions(ctx context.Context) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM admin_session WHERE expires_at<=now()`)
	return err
}

func (db *DB) CountActiveAdminSessions(ctx context.Context) (int, error) {
	var count int
	err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM admin_session WHERE expires_at>now()`).Scan(&count)
	return count, err
}

type AdminCounts struct {
	Projects       int `json:"projects"`
	Entries        int `json:"entries"`
	Clients        int `json:"oauth_clients"`
	ActiveTokens   int `json:"active_access_tokens"`
	ActiveSessions int `json:"active_admin_sessions"`
}

func (db *DB) AdminCounts(ctx context.Context) (AdminCounts, error) {
	var c AdminCounts
	err := db.Pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM project),
(SELECT count(*) FROM entry),
(SELECT count(*) FROM oauth_client),
(SELECT count(*) FROM oauth_token WHERE kind='access' AND NOT revoked AND expires_at>now()),
(SELECT count(*) FROM admin_session WHERE expires_at>now())`).Scan(&c.Projects, &c.Entries, &c.Clients, &c.ActiveTokens, &c.ActiveSessions)
	return c, err
}

type EntryWithProject struct {
	Entry
	ProjectName string      `json:"project_name"`
	Meta        *EntryMeta  `json:"meta,omitempty"`
	DuplicateOf *int64      `json:"-"`
	ResolvedBy  *Resolution `json:"resolved_by,omitempty"`
}

func (db *DB) RecentEntries(ctx context.Context, limit int) ([]EntryWithProject, error) {
	return db.ListEntries(ctx, EntryFilter{Limit: limit})
}

// EntryFilter narrows the cross-project entry table. Empty fields match
// everything; Query is a case-insensitive substring of the body or extracted
// title. Status "open" or "done" keeps only todos in that state. Before is the
// ID of the last entry already shown; Limit <= 0 returns every match.
type EntryFilter struct {
	ProjectSlug string
	Kind        string
	Source      string
	Tag         string
	Status      string
	Query       string
	Before      *int64
	Limit       int
}

// ListEntries returns matching entries from every project, newest first,
// with their extracted metadata and, for todos, the entry that resolved them.
func (db *DB) ListEntries(ctx context.Context, f EntryFilter) ([]EntryWithProject, error) {
	var limit *int
	if f.Limit > 0 {
		limit = &f.Limit
	}
	rows, err := db.Pool.Query(ctx, `SELECT e.id,e.slug,e.kind,e.body,e.source,e.client_id,e.created_at,p.name,
 m.entry_id IS NOT NULL AND m.title<>'',COALESCE(m.title,''),COALESCE(m.tags,'{}'),COALESCE(m.priority,''),COALESCE(m.refs,'{}'),COALESCE(m.origin,''),m.duplicate_of,
 rb.entry_id,COALESCE(rb.origin,''),rb.created_at
FROM entry e JOIN project p ON p.slug=e.slug
LEFT JOIN entry_meta m ON m.entry_id=e.id
LEFT JOIN LATERAL (SELECT r.entry_id,r.origin,re.created_at FROM entry_meta r JOIN entry re ON re.id=r.entry_id WHERE r.resolves=e.id ORDER BY re.created_at,re.id LIMIT 1) rb ON e.kind='todo'
WHERE ($1='' OR e.slug=$1) AND ($2='' OR e.kind=$2) AND ($3='' OR e.source=$3) AND ($4='' OR m.tags @> ARRAY[$4])
AND ($5='' OR (e.kind='todo' AND ($5='open')=(rb.entry_id IS NULL)))
AND ($6='' OR strpos(lower(e.body),lower($6))>0 OR strpos(lower(COALESCE(m.title,'')),lower($6))>0)
AND ($7::bigint IS NULL OR (e.created_at,e.id) < (SELECT created_at,id FROM entry WHERE id=$7))
ORDER BY e.created_at DESC,e.id DESC LIMIT $8`, f.ProjectSlug, f.Kind, f.Source, f.Tag, f.Status, f.Query, f.Before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EntryWithProject{}
	for rows.Next() {
		var e EntryWithProject
		var hasMeta bool
		var meta EntryMeta
		var resolverID *int64
		var resolution Resolution
		var resolvedAt *time.Time
		if err := rows.Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.ClientID, &e.CreatedAt, &e.ProjectName,
			&hasMeta, &meta.Title, &meta.Tags, &meta.Priority, &meta.Refs, &meta.Origin, &e.DuplicateOf, &resolverID, &resolution.Origin, &resolvedAt); err != nil {
			return nil, err
		}
		if hasMeta {
			e.Meta = &meta
		}
		if resolverID != nil {
			resolution.EntryID, resolution.CreatedAt = *resolverID, *resolvedAt
			e.ResolvedBy = &resolution
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EntryTags lists the tags in use, most frequent first, for the tag filter.
func (db *DB) EntryTags(ctx context.Context, limit int) ([]string, error) {
	rows, err := db.Pool.Query(ctx, `SELECT tag FROM entry_meta, unnest(tags) tag GROUP BY tag ORDER BY count(*) DESC,tag LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// EntrySources lists every distinct writer name, for the table's agent filter.
func (db *DB) EntrySources(ctx context.Context) ([]string, error) {
	rows, err := db.Pool.Query(ctx, `SELECT DISTINCT source FROM entry ORDER BY source`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (db *DB) EntriesByID(ctx context.Context, ids []int64) (map[int64]EntryWithProject, error) {
	rows, err := db.Pool.Query(ctx, `SELECT e.id,e.slug,e.kind,e.body,e.source,e.client_id,e.created_at,p.name FROM entry e JOIN project p ON p.slug=e.slug WHERE e.id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]EntryWithProject{}
	for rows.Next() {
		var e EntryWithProject
		if err := rows.Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.ClientID, &e.CreatedAt, &e.ProjectName); err != nil {
			return nil, err
		}
		out[e.ID] = e
	}
	return out, rows.Err()
}

// ActiveTokenCounts reports live access tokens per OAuth client without exposing token material.
func (db *DB) ActiveTokenCounts(ctx context.Context) (map[string]int, error) {
	rows, err := db.Pool.Query(ctx, `SELECT client_id,count(*) FROM oauth_token WHERE kind='access' AND NOT revoked AND expires_at>now() GROUP BY client_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		out[id] = count
	}
	return out, rows.Err()
}

type ClientTokenCounts struct {
	ActiveAccessTokens  int `json:"active_access_tokens"`
	ActiveRefreshTokens int `json:"active_refresh_tokens"`
}

// ActiveTokenCountsForClients bounds the aggregation to one operator-console page.
func (db *DB) ActiveTokenCountsForClients(ctx context.Context, clientIDs []string) (map[string]ClientTokenCounts, error) {
	out := map[string]ClientTokenCounts{}
	if len(clientIDs) == 0 {
		return out, nil
	}
	rows, err := db.Pool.Query(ctx, `SELECT client_id,count(*) FILTER (WHERE kind='access'),count(*) FILTER (WHERE kind='refresh') FROM oauth_token WHERE NOT revoked AND expires_at>now() AND client_id=ANY($1) GROUP BY client_id`, clientIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var count ClientTokenCounts
		if err := rows.Scan(&id, &count.ActiveAccessTokens, &count.ActiveRefreshTokens); err != nil {
			return nil, err
		}
		out[id] = count
	}
	return out, rows.Err()
}
