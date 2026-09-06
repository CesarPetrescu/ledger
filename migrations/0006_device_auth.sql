ALTER TABLE oauth_client DROP CONSTRAINT oauth_client_kind_check;
ALTER TABLE oauth_client ADD CHECK (kind IN ('cimd', 'dcr', 'device'));
ALTER TABLE oauth_client DROP CONSTRAINT oauth_client_redirect_uris_check;
ALTER TABLE oauth_client ADD CHECK ((kind = 'device' AND cardinality(redirect_uris) = 0) OR (kind <> 'device' AND cardinality(redirect_uris) > 0));

CREATE TABLE oauth_device (
  hash bytea PRIMARY KEY,
  user_code text NOT NULL UNIQUE,
  client_id text NOT NULL REFERENCES oauth_client(client_id) ON DELETE CASCADE,
  scope text NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','denied','used')),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL DEFAULT now() + interval '10 minutes',
  poll_after timestamptz NOT NULL DEFAULT now() + interval '5 seconds',
  poll_interval integer NOT NULL DEFAULT 5 CHECK (poll_interval >= 5)
);
CREATE INDEX oauth_device_client_idx ON oauth_device(client_id);
