ALTER TABLE handoff ADD COLUMN kind text NOT NULL DEFAULT 'general' CHECK (kind IN ('general', 'research'));
ALTER TABLE project ADD COLUMN research_visible boolean NOT NULL DEFAULT false;

-- One row per research handoff. The brief (the handoff's first message) carries the queue state in its
-- work_state; this row adds the spec, the lease, and the run counters.
CREATE TABLE research_task (
  handoff_id bigint PRIMARY KEY REFERENCES handoff(id) ON DELETE CASCADE,
  message_id bigint NOT NULL UNIQUE REFERENCES handoff_message(id) ON DELETE CASCADE,
  spec jsonb NOT NULL CHECK (jsonb_typeof(spec) = 'object'),
  spec_version integer NOT NULL DEFAULT 1 CHECK (spec_version >= 1),
  depends_on bigint[] NOT NULL DEFAULT '{}',
  max_attempts integer NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 10),
  attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
  failures integer NOT NULL DEFAULT 0 CHECK (failures >= 0),
  phase text CHECK (phase IN ('question', 'review', 'dead')),
  lease_seconds integer NOT NULL DEFAULT 300 CHECK (lease_seconds BETWEEN 30 AND 3600),
  lease_until timestamptz,
  heartbeat_at timestamptz,
  progress text NOT NULL DEFAULT '' CHECK (char_length(progress) <= 300),
  last_error text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 2000),
  checkpoint text NOT NULL DEFAULT '' CHECK (char_length(checkpoint) <= 65536),
  checkpoint_attempt integer,
  checkpoint_at timestamptz
);
CREATE INDEX research_task_lease_idx ON research_task(lease_until) WHERE lease_until IS NOT NULL;

-- A run's bearer token, stored hashed. It works only while its task is in progress under the same attempt
-- with a live lease, so it dies with the run without any revocation step.
CREATE TABLE research_token (
  hash bytea PRIMARY KEY CHECK (octet_length(hash) = 32),
  handoff_id bigint NOT NULL REFERENCES research_task(handoff_id) ON DELETE CASCADE,
  attempt integer NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX research_token_task_idx ON research_token(handoff_id);
