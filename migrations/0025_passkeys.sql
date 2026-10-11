-- Owner passkeys (WebAuthn) for the console, and what each console session records about itself.

-- The owner's WebAuthn user handle: random, stable, never shown. Passkey providers file every Ledger
-- passkey under it, so after a reset a new passkey replaces the old one in the owner's vault.
-- gen_random_uuid() is the strong random source built into PostgreSQL 13+.
CREATE TABLE owner_webauthn (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  user_handle bytea NOT NULL CHECK (octet_length(user_handle) = 32)
);
INSERT INTO owner_webauthn(user_handle) VALUES (decode(replace(gen_random_uuid()::text || gen_random_uuid()::text, '-', ''), 'hex'));

-- One row per registered passkey. Only public material is stored: the COSE public key and the
-- credential ID. Synced passkeys (Bitwarden, iCloud, Google) keep sign_count at 0.
CREATE TABLE owner_passkey (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  credential_id bytea NOT NULL UNIQUE CHECK (octet_length(credential_id) BETWEEN 1 AND 1023),
  public_key bytea NOT NULL CHECK (octet_length(public_key) > 0),
  sign_count bigint NOT NULL DEFAULT 0 CHECK (sign_count BETWEEN 0 AND 4294967295),
  aaguid bytea NOT NULL CHECK (octet_length(aaguid) IN (0, 16)),
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60 AND name !~ '[[:cntrl:]]'),
  transports text[] NOT NULL DEFAULT '{}',
  attestation_format text NOT NULL DEFAULT '',
  backup_eligible boolean NOT NULL,
  backup_state boolean NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz
);
CREATE TRIGGER owner_passkey_admin_event AFTER INSERT OR UPDATE OR DELETE ON owner_passkey
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('owner_passkey');

-- A WebAuthn ceremony in progress: the library's session data for one challenge, keyed by the
-- SHA-256 of a random identifier the client holds. Rows are deleted when used, so each challenge
-- works once, and only for its purpose: a passkey sign-in, the passkey step after the password,
-- or registering a passkey from the console session that asked for it.
CREATE TABLE owner_webauthn_challenge (
  hash bytea PRIMARY KEY CHECK (octet_length(hash) = 32),
  purpose text NOT NULL CHECK (purpose IN ('login', 'password_login', 'register')),
  session_data jsonb NOT NULL,
  admin_session bytea REFERENCES admin_session(hash) ON DELETE CASCADE,
  remember boolean NOT NULL DEFAULT false,
  expires_at timestamptz NOT NULL,
  CHECK ((purpose = 'register') = (admin_session IS NOT NULL))
);
CREATE INDEX owner_webauthn_challenge_expires_idx ON owner_webauthn_challenge(expires_at);

-- Signed-in devices. public_id names a session in the console without revealing its cookie;
-- remember selects the 30-day sliding session capped at 90 days from created_at.
ALTER TABLE admin_session
  ADD COLUMN public_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
  ADD COLUMN remember boolean NOT NULL DEFAULT false,
  ADD COLUMN method text NOT NULL DEFAULT 'password' CHECK (method IN ('password', 'passkey', 'password+passkey')),
  ADD COLUMN label text NOT NULL DEFAULT '' CHECK (char_length(label) <= 60);
