package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// ScopeResearchDispatch is the only permission an API key can hold: claim research tasks, renew and end
// their runs, and read their status.
const ScopeResearchDispatch = "research:dispatch"

// APIKey is an owner-created credential for the plain JSON API. The key itself is never stored.
type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

const apiKeyColumns = `id,name,prefix,scopes,created_at,last_used_at,revoked_at`

func scanAPIKey(row pgx.Row) (APIKey, error) {
	var k APIKey
	err := row.Scan(&k.ID, &k.Name, &k.Prefix, &k.Scopes, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt)
	return k, err
}

// CreateAPIKey stores a new key and returns it with its secret, which cannot be read again.
func (db *DB) CreateAPIKey(ctx context.Context, name string, scopes []string) (APIKey, string, error) {
	if err := ValidateContextHeader("name", name, true); err != nil || utf8.RuneCountInString(name) > 100 {
		return APIKey{}, "", fmt.Errorf("name must be 1 to 100 characters on one line")
	}
	if len(scopes) == 0 || slices.ContainsFunc(scopes, func(s string) bool { return s != ScopeResearchDispatch }) {
		return APIKey{}, "", fmt.Errorf("scopes must be %s", ScopeResearchDispatch)
	}
	random, err := randomToken()
	if err != nil {
		return APIKey{}, "", err
	}
	secret := "ledger_" + random
	hash := sha256.Sum256([]byte(secret))
	key, err := scanAPIKey(db.Pool.QueryRow(ctx, `INSERT INTO api_key(name,prefix,hash,scopes) VALUES($1,$2,$3,$4) RETURNING `+apiKeyColumns,
		name, secret[:14], hash[:], slices.Compact(slices.Sorted(slices.Values(scopes)))))
	return key, secret, err
}

func (db *DB) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := db.Pool.Query(ctx, `SELECT `+apiKeyColumns+` FROM api_key ORDER BY revoked_at IS NOT NULL, created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (APIKey, error) { return scanAPIKey(row) })
}

// RevokeAPIKey stops a key working at once. Revoking twice is harmless.
func (db *DB) RevokeAPIKey(ctx context.Context, id int64) (APIKey, error) {
	return scanAPIKey(db.Pool.QueryRow(ctx, `UPDATE api_key SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 RETURNING `+apiKeyColumns, id))
}

// LookupAPIKey resolves a live key. Last use is recorded at most once a minute, since a dispatcher
// polls continuously.
func (db *DB) LookupAPIKey(ctx context.Context, secret string) (APIKey, error) {
	hash := sha256.Sum256([]byte(secret))
	key, err := scanAPIKey(db.Pool.QueryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_key WHERE hash=$1 AND revoked_at IS NULL`, hash[:]))
	if err == nil && (key.LastUsedAt == nil || time.Since(*key.LastUsedAt) > time.Minute) {
		_, err = db.Pool.Exec(ctx, `UPDATE api_key SET last_used_at=now() WHERE id=$1`, key.ID)
	}
	return key, err
}
