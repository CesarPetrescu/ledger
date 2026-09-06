package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrAuthorizationPending = errors.New("authorization_pending")
	ErrSlowDown             = errors.New("slow_down")
	ErrAccessDenied         = errors.New("access_denied")
	ErrExpiredToken         = errors.New("expired_token")
)

type DeviceRequest struct {
	UserCode   string    `json:"user_code"`
	ClientName string    `json:"client_name"`
	Scope      string    `json:"scope"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func NormalizeUserCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}

func (db *DB) CreateDevice(ctx context.Context, clientID string, scopes []string) (string, string, error) {
	raw, err := randomToken()
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256([]byte(raw))
	// 40 bits of human-readable entropy; issuance and lookups are also rate limited.
	alphabet := "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	b := make([]byte, 8)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	code := string(b)
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx)
	if err = lockOAuthIssuance(ctx, tx); err != nil {
		return "", "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO oauth_device(hash,user_code,client_id,scope) VALUES($1,$2,$3,$4)`, hash[:], code, clientID, strings.Join(scopes, " "))
	if err != nil {
		return "", "", err
	}
	return raw, code[:4] + "-" + code[4:], tx.Commit(ctx)
}

func (db *DB) LookupDevice(ctx context.Context, code string) (DeviceRequest, error) {
	var d DeviceRequest
	err := db.Pool.QueryRow(ctx, `SELECT d.user_code,c.name,d.scope,d.status,d.created_at,d.expires_at FROM oauth_device d JOIN oauth_client c USING(client_id) WHERE d.user_code=$1 AND d.expires_at>now() AND d.status='pending'`, NormalizeUserCode(code)).Scan(&d.UserCode, &d.ClientName, &d.Scope, &d.Status, &d.CreatedAt, &d.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrInvalidGrant
	}
	return d, err
}

func (db *DB) DecideDevice(ctx context.Context, code string, approve bool) error {
	status := "denied"
	if approve {
		status = "approved"
	}
	result, err := db.Pool.Exec(ctx, `UPDATE oauth_device SET status=$2 WHERE user_code=$1 AND status='pending' AND expires_at>now()`, NormalizeUserCode(code), status)
	if err == nil && result.RowsAffected() != 1 {
		return ErrInvalidGrant
	}
	return err
}

func (db *DB) ExchangeDevice(ctx context.Context, raw, clientID string) (TokenPair, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return TokenPair{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockOAuthIssuance(ctx, tx); err != nil {
		return TokenPair{}, err
	}
	hash := sha256.Sum256([]byte(raw))
	var owner, scope, status string
	var expires, pollAfter time.Time
	var interval int
	err = tx.QueryRow(ctx, `SELECT client_id,scope,status,expires_at,poll_after,poll_interval FROM oauth_device WHERE hash=$1 FOR UPDATE`, hash[:]).Scan(&owner, &scope, &status, &expires, &pollAfter, &interval)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenPair{}, ErrInvalidGrant
	}
	if err != nil {
		return TokenPair{}, err
	}
	if owner != clientID || status == "used" {
		return TokenPair{}, ErrInvalidGrant
	}
	if time.Now().After(expires) {
		return TokenPair{}, ErrExpiredToken
	}
	if status == "denied" {
		return TokenPair{}, ErrAccessDenied
	}
	pending := ErrAuthorizationPending
	if time.Now().Before(pollAfter) {
		interval += 5
		pending = ErrSlowDown
	}
	if pending == ErrSlowDown || status == "pending" {
		_, err = tx.Exec(ctx, `UPDATE oauth_device SET poll_after=now()+make_interval(secs=>$2::int),poll_interval=$2 WHERE hash=$1`, hash[:], interval)
		if err != nil {
			return TokenPair{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return TokenPair{}, err
		}
		return TokenPair{}, pending
	}
	pair, err := issuePair(ctx, tx, clientID, strings.Fields(scope), familyFromCodeHash(hash))
	if err != nil {
		return TokenPair{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE oauth_device SET status='used' WHERE hash=$1`, hash[:]); err != nil {
		return TokenPair{}, err
	}
	return pair, tx.Commit(ctx)
}

// RevokeToken revokes the family proved by the submitted token, including its rotations.
func (db *DB) RevokeToken(ctx context.Context, raw, clientID string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockOAuthIssuance(ctx, tx); err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(raw))
	var family string
	err = tx.QueryRow(ctx, `SELECT family::text FROM oauth_token WHERE hash=$1 AND client_id=$2`, hash[:], clientID).Scan(&family)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = lockTokenFamily(ctx, tx, family); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE oauth_token SET revoked=true WHERE family=$1::uuid`, family); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
