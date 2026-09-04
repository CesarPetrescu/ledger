CREATE TABLE admin_session (
  hash bytea PRIMARY KEY CHECK (octet_length(hash) = 32),
  csrf_token text NOT NULL CHECK (csrf_token <> ''),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_session_expires_idx ON admin_session(expires_at);
