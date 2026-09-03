package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrInvalidGrant = errors.New("invalid_grant")

type OAuthClient struct {
	ClientID     string    `json:"client_id"`
	Kind         string    `json:"kind"`
	RedirectURIs []string  `json:"redirect_uris"`
	Name         string    `json:"client_name"`
	CreatedAt    time.Time `json:"created_at"`
	LastUsedAt   time.Time `json:"last_used_at"`
}

func (db *DB) GetClient(ctx context.Context, id string) (OAuthClient, error) {
	var c OAuthClient
	err := db.Pool.QueryRow(ctx, `SELECT client_id,kind,redirect_uris,name,created_at,last_used_at FROM oauth_client WHERE client_id=$1`, id).
		Scan(&c.ClientID, &c.Kind, &c.RedirectURIs, &c.Name, &c.CreatedAt, &c.LastUsedAt)
	return c, err
}

func (db *DB) PutClient(ctx context.Context, c OAuthClient) (OAuthClient, error) {
	err := db.Pool.QueryRow(ctx, `INSERT INTO oauth_client(client_id,kind,redirect_uris,name) VALUES($1,$2,$3,$4)
ON CONFLICT(client_id) DO UPDATE SET kind=EXCLUDED.kind,redirect_uris=EXCLUDED.redirect_uris,name=EXCLUDED.name,last_used_at=now()
RETURNING client_id,kind,redirect_uris,name,created_at,last_used_at`, c.ClientID, c.Kind, c.RedirectURIs, c.Name).
		Scan(&c.ClientID, &c.Kind, &c.RedirectURIs, &c.Name, &c.CreatedAt, &c.LastUsedAt)
	return c, err
}

func (db *DB) TouchClient(ctx context.Context, id string) error {
	_, err := db.Pool.Exec(ctx, `UPDATE oauth_client SET last_used_at=now() WHERE client_id=$1`, id)
	return err
}

func (db *DB) ListClients(ctx context.Context) ([]OAuthClient, error) {
	rows, err := db.Pool.Query(ctx, `SELECT client_id,kind,redirect_uris,name,created_at,last_used_at FROM oauth_client ORDER BY created_at,client_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OAuthClient{}
	for rows.Next() {
		var c OAuthClient
		if err := rows.Scan(&c.ClientID, &c.Kind, &c.RedirectURIs, &c.Name, &c.CreatedAt, &c.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (db *DB) Revoke(ctx context.Context, clientID string, all bool) (int64, error) {
	if all {
		result, err := db.Pool.Exec(ctx, `UPDATE oauth_token SET revoked=true WHERE NOT revoked`)
		return result.RowsAffected(), err
	}
	result, err := db.Pool.Exec(ctx, `UPDATE oauth_token SET revoked=true WHERE client_id=$1 AND NOT revoked`, clientID)
	return result.RowsAffected(), err
}

func (db *DB) GC(ctx context.Context) (int64, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var count int64
	for _, query := range []string{
		`DELETE FROM oauth_code c
WHERE c.expires_at < now()
  AND (NOT c.used OR NOT EXISTS (
    SELECT 1 FROM oauth_token t
    WHERE t.family=encode(set_byte(set_byte(substring(c.hash FROM 1 FOR 16),6,(get_byte(c.hash,6)&15)|64),8,(get_byte(c.hash,8)&63)|128),'hex')::uuid
      AND NOT t.revoked AND t.expires_at >= now()
  ))`,
		`DELETE FROM oauth_token t
WHERE t.expires_at < now()
  AND (t.kind <> 'refresh' OR NOT t.revoked OR NOT EXISTS (
    SELECT 1 FROM oauth_token live
    WHERE live.family=t.family AND NOT live.revoked AND live.expires_at >= now()
  ))`,
		`DELETE FROM oauth_client c WHERE kind='dcr' AND last_used_at < now()-interval '60 days' AND NOT EXISTS(SELECT 1 FROM oauth_token t WHERE t.client_id=c.client_id AND t.expires_at >= now())`,
	} {
		result, err := tx.Exec(ctx, query)
		if err != nil {
			return 0, err
		}
		count += result.RowsAffected()
	}
	return count, tx.Commit(ctx)
}

func (db *DB) CreateCode(ctx context.Context, raw, clientID, redirectURI, challenge string, scopes []string) error {
	hash := sha256.Sum256([]byte(raw))
	_, err := db.Pool.Exec(ctx, `INSERT INTO oauth_code(hash,client_id,redirect_uri,code_challenge,scope,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '60 seconds')`, hash[:], clientID, redirectURI, challenge, strings.Join(scopes, " "))
	return err
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	Scopes       []string
	ClientID     string
	Family       string
}

func (db *DB) ExchangeCode(ctx context.Context, raw, clientID, redirectURI, verifier string, verify func(string, string) bool) (TokenPair, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return TokenPair{}, err
	}
	defer tx.Rollback(ctx)
	hash := sha256.Sum256([]byte(raw))
	family := familyFromCodeHash(hash)
	if err := lockTokenFamily(ctx, tx, family); err != nil {
		return TokenPair{}, err
	}
	var storedClient, storedRedirect, challenge string
	var scope string
	var expires time.Time
	var used bool
	err = tx.QueryRow(ctx, `SELECT client_id,redirect_uri,code_challenge,scope,expires_at,used FROM oauth_code WHERE hash=$1 FOR UPDATE`, hash[:]).
		Scan(&storedClient, &storedRedirect, &challenge, &scope, &expires, &used)
	if err != nil {
		if err == pgx.ErrNoRows {
			if _, err := tx.Exec(ctx, `UPDATE oauth_token SET revoked=true WHERE family=$1::uuid`, family); err != nil {
				return TokenPair{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return TokenPair{}, err
			}
			return TokenPair{}, ErrInvalidGrant
		}
		return TokenPair{}, err
	}
	if used {
		if _, err := tx.Exec(ctx, `UPDATE oauth_token SET revoked=true WHERE family=$1::uuid`, family); err != nil {
			return TokenPair{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return TokenPair{}, err
		}
		return TokenPair{}, ErrInvalidGrant
	}
	if time.Now().After(expires) || clientID != storedClient || redirectURI != storedRedirect || !verify(verifier, challenge) {
		return TokenPair{}, ErrInvalidGrant
	}
	pair, err := issuePair(ctx, tx, clientID, strings.Fields(scope), family)
	if err != nil {
		return TokenPair{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_code SET used=true WHERE hash=$1`, hash[:]); err != nil {
		return TokenPair{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_client SET last_used_at=now() WHERE client_id=$1`, clientID); err != nil {
		return TokenPair{}, err
	}
	return pair, tx.Commit(ctx)
}

func (db *DB) ExchangeRefresh(ctx context.Context, raw, clientID string) (TokenPair, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return TokenPair{}, err
	}
	defer tx.Rollback(ctx)
	hash := sha256.Sum256([]byte(raw))
	var storedClient, family string
	var scope string
	var expires time.Time
	var revoked bool
	if err := tx.QueryRow(ctx, `SELECT family::text FROM oauth_token WHERE hash=$1 AND kind='refresh'`, hash[:]).Scan(&family); err != nil {
		if err == pgx.ErrNoRows {
			return TokenPair{}, ErrInvalidGrant
		}
		return TokenPair{}, err
	}
	if err := lockTokenFamily(ctx, tx, family); err != nil {
		return TokenPair{}, err
	}
	err = tx.QueryRow(ctx, `SELECT client_id,scope,family::text,expires_at,revoked FROM oauth_token WHERE hash=$1 AND kind='refresh' FOR UPDATE`, hash[:]).
		Scan(&storedClient, &scope, &family, &expires, &revoked)
	if err != nil {
		if err == pgx.ErrNoRows {
			return TokenPair{}, ErrInvalidGrant
		}
		return TokenPair{}, err
	}
	if revoked {
		if _, err := tx.Exec(ctx, `UPDATE oauth_token SET revoked=true WHERE family=$1::uuid`, family); err != nil {
			return TokenPair{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return TokenPair{}, err
		}
		return TokenPair{}, ErrInvalidGrant
	}
	if time.Now().After(expires) || clientID != storedClient {
		return TokenPair{}, ErrInvalidGrant
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_token SET revoked=true WHERE hash=$1`, hash[:]); err != nil {
		return TokenPair{}, err
	}
	pair, err := issuePair(ctx, tx, clientID, strings.Fields(scope), family)
	if err != nil {
		return TokenPair{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_client SET last_used_at=now() WHERE client_id=$1`, clientID); err != nil {
		return TokenPair{}, err
	}
	return pair, tx.Commit(ctx)
}

func (db *DB) LookupAccess(ctx context.Context, raw string) (string, []string, error) {
	hash := sha256.Sum256([]byte(raw))
	var clientID string
	var scope string
	err := db.Pool.QueryRow(ctx, `SELECT client_id,scope FROM oauth_token WHERE hash=$1 AND kind='access' AND NOT revoked AND expires_at>now()`, hash[:]).Scan(&clientID, &scope)
	if err == nil {
		_, err = db.Pool.Exec(ctx, `UPDATE oauth_client SET last_used_at=now() WHERE client_id=$1`, clientID)
	}
	return clientID, strings.Fields(scope), err
}

func issuePair(ctx context.Context, tx pgx.Tx, clientID string, scopes []string, family string) (TokenPair, error) {
	access, err := randomToken()
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := randomToken()
	if err != nil {
		return TokenPair{}, err
	}
	accessHash := sha256.Sum256([]byte(access))
	refreshHash := sha256.Sum256([]byte(refresh))
	if _, err := tx.Exec(ctx, `INSERT INTO oauth_token(hash,kind,client_id,scope,family,expires_at) VALUES
($1,'access',$3,$4,$5::uuid,now()+interval '15 minutes'),($2,'refresh',$3,$4,$5::uuid,now()+interval '30 days')`, accessHash[:], refreshHash[:], clientID, strings.Join(scopes, " "), family); err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: access, RefreshToken: refresh, Scopes: scopes, ClientID: clientID, Family: family}, nil
}

func lockTokenFamily(ctx context.Context, tx pgx.Tx, family string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, family)
	return err
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func familyFromCodeHash(hash [32]byte) string {
	raw := hash[:16]
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
}
