CREATE TABLE handoff (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  project_slug text REFERENCES project(slug) ON DELETE SET NULL,
  title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200 AND position(E'\n' in title) = 0 AND position(E'\r' in title) = 0),
  description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
  scope text NOT NULL DEFAULT '' CHECK (char_length(scope) <= 500),
  source text NOT NULL CHECK (char_length(source) BETWEEN 1 AND 200),
  client_id text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz,
  tsv tsvector GENERATED ALWAYS AS (to_tsvector('public.ledger_ts'::regconfig, title || ' ' || description || ' ' || scope)) STORED
);
CREATE INDEX handoff_updated_idx ON handoff(updated_at DESC, id DESC);
CREATE INDEX handoff_project_updated_idx ON handoff(project_slug, updated_at DESC, id DESC);
CREATE INDEX handoff_tsv_idx ON handoff USING gin(tsv);

CREATE TABLE handoff_message (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  handoff_id bigint NOT NULL REFERENCES handoff(id) ON DELETE CASCADE,
  body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 100000),
  target text NOT NULL DEFAULT '' CHECK (char_length(target) <= 100 AND position(E'\n' in target) = 0 AND position(E'\r' in target) = 0),
  work_state text NOT NULL CHECK (work_state IN ('draft', 'ready', 'in_progress', 'blocked', 'done')),
  source text NOT NULL CHECK (char_length(source) BETWEEN 1 AND 200),
  client_id text NOT NULL,
  seen_at timestamptz,
  seen_source text,
  seen_client_id text,
  claimed_at timestamptz,
  claimed_source text,
  claimed_client_id text,
  status_updated_at timestamptz NOT NULL DEFAULT now(),
  status_updated_source text NOT NULL,
  status_updated_client_id text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  tsv tsvector GENERATED ALWAYS AS (to_tsvector('public.ledger_ts'::regconfig, body)) STORED,
  CHECK ((seen_at IS NULL AND seen_source IS NULL AND seen_client_id IS NULL) OR (seen_at IS NOT NULL AND seen_source IS NOT NULL AND seen_client_id IS NOT NULL)),
  CHECK ((claimed_at IS NULL AND claimed_source IS NULL AND claimed_client_id IS NULL) OR (claimed_at IS NOT NULL AND claimed_source IS NOT NULL AND claimed_client_id IS NOT NULL))
);
CREATE INDEX handoff_message_handoff_created_idx ON handoff_message(handoff_id, created_at DESC, id DESC);
CREATE INDEX handoff_message_queue_idx ON handoff_message(work_state, target, created_at DESC) WHERE work_state IN ('ready', 'in_progress', 'blocked');
CREATE INDEX handoff_message_tsv_idx ON handoff_message USING gin(tsv);

CREATE TABLE handoff_file (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  message_id bigint NOT NULL REFERENCES handoff_message(id) ON DELETE CASCADE,
  filename text NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 255 AND position('/' in filename) = 0 AND position(chr(92) in filename) = 0),
  media_type text NOT NULL CHECK (char_length(media_type) BETWEEN 1 AND 255),
  size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 26214400),
  sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
  data bytea NOT NULL CHECK (octet_length(data) = size_bytes),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX handoff_file_message_idx ON handoff_file(message_id, created_at, id);

CREATE TRIGGER handoff_admin_event AFTER INSERT OR UPDATE OR DELETE ON handoff
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('handoff');
CREATE TRIGGER handoff_message_admin_event AFTER INSERT OR UPDATE OR DELETE ON handoff_message
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('handoff_message');
CREATE TRIGGER handoff_file_admin_event AFTER INSERT OR DELETE ON handoff_file
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('handoff_file');
