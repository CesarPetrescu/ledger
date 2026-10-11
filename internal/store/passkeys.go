package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// OwnerPasskey is a registered owner passkey. It is console-only and never an MCP output.
type OwnerPasskey struct {
	ID                int64
	CredentialID      []byte
	PublicKey         []byte
	SignCount         uint32
	AAGUID            []byte
	Name              string
	Transports        []string
	AttestationFormat string
	BackupEligible    bool
	BackupState       bool
	CreatedAt         time.Time
	LastUsedAt        *time.Time
}

// WebAuthn ceremony purposes. A challenge works only for the purpose it was issued for.
const (
	ChallengeLogin         = "login"
	ChallengePasswordLogin = "password_login"
	ChallengeRegister      = "register"
)

// WebAuthnChallengeTTL bounds every ceremony, and the pending password step, to five minutes.
const WebAuthnChallengeTTL = 5 * time.Minute

// ErrPasskeyCounter means an authenticator reported a non-zero signature counter that did not grow,
// the sign of a cloned authenticator.
var ErrPasskeyCounter = errors.New("passkey signature counter did not increase")

const ownerPasskeyColumns = `id,credential_id,public_key,sign_count,aaguid,name,transports,attestation_format,backup_eligible,backup_state,created_at,last_used_at`

func scanOwnerPasskey(row pgx.Row) (OwnerPasskey, error) {
	var p OwnerPasskey
	var count int64
	err := row.Scan(&p.ID, &p.CredentialID, &p.PublicKey, &count, &p.AAGUID, &p.Name, &p.Transports, &p.AttestationFormat, &p.BackupEligible, &p.BackupState, &p.CreatedAt, &p.LastUsedAt)
	p.SignCount = uint32(count)
	return p, err
}

// OwnerHandle is the owner's stable WebAuthn user handle.
func (db *DB) OwnerHandle(ctx context.Context) ([]byte, error) {
	var handle []byte
	err := db.Pool.QueryRow(ctx, `SELECT user_handle FROM owner_webauthn WHERE singleton`).Scan(&handle)
	return handle, err
}

func (db *DB) ListOwnerPasskeys(ctx context.Context) ([]OwnerPasskey, error) {
	rows, err := db.Pool.Query(ctx, `SELECT `+ownerPasskeyColumns+` FROM owner_passkey ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OwnerPasskey{}
	for rows.Next() {
		p, err := scanOwnerPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PasskeyLock serializes registering a passkey against password-only sign-ins and approvals.
// Registration holds it exclusively; a password-only path shares it while it checks that no passkey
// exists and writes, so a passkey registered meanwhile either waits for that write (and then signs
// the new session out) or commits first (and the password path sees it).
const PasskeyLock = 7103380

// ErrPasskeyRequired means a passkey exists, so the password alone may not sign in or approve.
var ErrPasskeyRequired = errors.New("a passkey is required")

func lockPasskeys(ctx context.Context, tx pgx.Tx, shared bool) error {
	query := `SELECT pg_advisory_xact_lock($1)`
	if shared {
		query = `SELECT pg_advisory_xact_lock_shared($1)`
	}
	_, err := tx.Exec(ctx, query, PasskeyLock)
	return err
}

// noPasskeyYet takes the shared lock and fails with ErrPasskeyRequired once a passkey exists.
func noPasskeyYet(ctx context.Context, tx pgx.Tx) error {
	if err := lockPasskeys(ctx, tx, true); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM owner_passkey)`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrPasskeyRequired
	}
	return nil
}

// OwnerHasPasskeys reports whether a passkey is registered, which turns password-only sign-in off.
func (db *DB) OwnerHasPasskeys(ctx context.Context) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM owner_passkey)`).Scan(&exists)
	return exists, err
}

// ErrPasskeyExists means the credential is already registered.
var ErrPasskeyExists = errors.New("passkey already registered")

// AddOwnerPasskey registers a passkey and, in the same transaction, signs out every other session
// that signed in with the password alone: once a passkey exists the password must not be enough,
// including in a session opened before the passkey existed. keep is the raw identifier of the
// session registering it, which has just proved the password and the passkey. It reports how many
// live sessions were signed out.
func (db *DB) AddOwnerPasskey(ctx context.Context, p OwnerPasskey, keep string) (OwnerPasskey, int64, error) {
	if p.Transports == nil {
		p.Transports = []string{}
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return OwnerPasskey{}, 0, err
	}
	defer tx.Rollback(ctx)
	if err := lockPasskeys(ctx, tx, false); err != nil {
		return OwnerPasskey{}, 0, err
	}
	saved, err := scanOwnerPasskey(tx.QueryRow(ctx, `INSERT INTO owner_passkey(credential_id,public_key,sign_count,aaguid,name,transports,attestation_format,backup_eligible,backup_state)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+ownerPasskeyColumns,
		p.CredentialID, p.PublicKey, int64(p.SignCount), p.AAGUID, p.Name, p.Transports, p.AttestationFormat, p.BackupEligible, p.BackupState))
	if pgErrorCode(err) == "23505" {
		return OwnerPasskey{}, 0, ErrPasskeyExists
	}
	if err != nil {
		return OwnerPasskey{}, 0, err
	}
	hash := sha256.Sum256([]byte(keep))
	var revoked int64
	if err := tx.QueryRow(ctx, revokeOtherAdminSessions, hash[:], MethodPassword).Scan(&revoked); err != nil {
		return OwnerPasskey{}, 0, err
	}
	return saved, revoked, tx.Commit(ctx)
}

func (db *DB) RenameOwnerPasskey(ctx context.Context, id int64, name string) (OwnerPasskey, error) {
	return scanOwnerPasskey(db.Pool.QueryRow(ctx, `UPDATE owner_passkey SET name=$2 WHERE id=$1 RETURNING `+ownerPasskeyColumns, id, name))
}

// DeleteOwnerPasskey removes one passkey; pgx.ErrNoRows when there is none with that id.
func (db *DB) DeleteOwnerPasskey(ctx context.Context, id int64) error {
	result, err := db.Pool.Exec(ctx, `DELETE FROM owner_passkey WHERE id=$1`, id)
	if err == nil && result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

// UseOwnerPasskey records a successful assertion. Synced passkeys always report a counter of 0,
// which is accepted; a non-zero counter must grow, checked and stored in one statement so two
// concurrent assertions cannot both pass with the same value.
func (db *DB) UseOwnerPasskey(ctx context.Context, credentialID []byte, counter uint32, backupState bool) error {
	result, err := db.Pool.Exec(ctx, `UPDATE owner_passkey SET sign_count=GREATEST(sign_count,$2), backup_state=$3, last_used_at=now()
WHERE credential_id=$1 AND ($2=0 OR sign_count<$2)`, credentialID, int64(counter), backupState)
	if err == nil && result.RowsAffected() == 0 {
		return ErrPasskeyCounter
	}
	return err
}

// ResetOwnerPasskeys deletes every passkey and every ceremony in progress, returning the console to
// password-only sign-in. It reports how many passkeys were deleted.
func (db *DB) ResetOwnerPasskeys(ctx context.Context) (int64, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM owner_webauthn_challenge`); err != nil {
		return 0, err
	}
	result, err := tx.Exec(ctx, `DELETE FROM owner_passkey`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), tx.Commit(ctx)
}

// CreateWebAuthnChallenge stores a ceremony's session data under a fresh random identifier, which
// is returned to the client and stored only as its SHA-256. A registration is bound to the raw
// admin session identifier that asked for it; sign-ins pass "". Expired ceremonies are purged here.
func (db *DB) CreateWebAuthnChallenge(ctx context.Context, purpose string, sessionData []byte, adminSession string, remember bool) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(id))
	// ponytail: unauthenticated sign-in ceremonies are bounded only by the per-address rate limit and
	// the five-minute expiry; add a global cap if the table ever grows under attack.
	_, err = db.Pool.Exec(ctx, `WITH expired AS (DELETE FROM owner_webauthn_challenge WHERE expires_at<=now())
INSERT INTO owner_webauthn_challenge(hash,purpose,session_data,admin_session,remember,expires_at) VALUES($1,$2,$3,$4,$5,now()+$6::interval)`,
		hash[:], purpose, sessionData, sessionHash(adminSession), remember, WebAuthnChallengeTTL.String())
	return id, err
}

// ConsumeWebAuthnChallenge deletes and returns a live ceremony issued for purpose (and, for a
// registration, for this admin session). pgx.ErrNoRows when it is unknown, used, expired, or for
// something else.
func (db *DB) ConsumeWebAuthnChallenge(ctx context.Context, id, purpose, adminSession string) (sessionData []byte, remember bool, err error) {
	hash := sha256.Sum256([]byte(id))
	err = db.Pool.QueryRow(ctx, `DELETE FROM owner_webauthn_challenge WHERE hash=$1 AND purpose=$2 AND admin_session IS NOT DISTINCT FROM $3 AND expires_at>now()
RETURNING session_data,remember`, hash[:], purpose, sessionHash(adminSession)).Scan(&sessionData, &remember)
	return sessionData, remember, err
}

func sessionHash(id string) any {
	if id == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(id))
	return hash[:]
}
