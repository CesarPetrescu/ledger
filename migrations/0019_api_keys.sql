-- Owner-created keys for the plain JSON API (/api/v1), used by servers such as Adastrion Core's
-- research dispatcher. Only a hash is stored; the key itself is shown once when it is created.
CREATE TABLE api_key (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100 AND position(E'\n' in name) = 0 AND position(E'\r' in name) = 0),
  prefix text NOT NULL,
  hash bytea NOT NULL UNIQUE CHECK (octet_length(hash) = 32),
  scopes text[] NOT NULL CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['research:dispatch']),
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked_at timestamptz
);

-- Why the brief last went back to the queue, so the next run's chat knows what it is continuing:
-- '' (never ran), retry_after_failure, revision, answered, retry, restarted, reopened.
ALTER TABLE research_task ADD COLUMN requeue_reason text NOT NULL DEFAULT ''
  CHECK (requeue_reason IN ('', 'retry_after_failure', 'revision', 'answered', 'retry', 'restarted', 'reopened'));
