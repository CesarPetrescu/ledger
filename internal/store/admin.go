package store

import (
	"context"
	"crypto/sha256"
	"time"
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
	ProjectName string `json:"project_name"`
}

func (db *DB) RecentEntries(ctx context.Context, limit int) ([]EntryWithProject, error) {
	rows, err := db.Pool.Query(ctx, `SELECT e.id,e.slug,e.kind,e.body,e.source,e.client_id,e.created_at,p.name FROM entry e JOIN project p ON p.slug=e.slug ORDER BY e.created_at DESC,e.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EntryWithProject{}
	for rows.Next() {
		var e EntryWithProject
		if err := rows.Scan(&e.ID, &e.Slug, &e.Kind, &e.Body, &e.Source, &e.ClientID, &e.CreatedAt, &e.ProjectName); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
