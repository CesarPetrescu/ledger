CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS unaccent;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_ts_config WHERE cfgname = 'ledger_ts' AND cfgnamespace = 'public'::regnamespace) THEN
    CREATE TEXT SEARCH CONFIGURATION public.ledger_ts (COPY = pg_catalog.simple);
  END IF;
END $$;
ALTER TEXT SEARCH CONFIGURATION public.ledger_ts ALTER MAPPING FOR hword, hword_part, word WITH unaccent, simple;

CREATE TABLE project (
  slug text PRIMARY KEY CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,63}$'),
  name text NOT NULL CHECK (name <> ''),
  tier text NOT NULL CHECK (tier IN ('focus', 'maintain', 'park')),
  hours_wk integer NOT NULL DEFAULT 0 CHECK (hours_wk BETWEEN 0 AND 168),
  type text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  goal text NOT NULL DEFAULT '',
  deadline text NOT NULL DEFAULT '',
  needs_me text NOT NULL DEFAULT '',
  automate text NOT NULL DEFAULT '',
  stack text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE entry (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  slug text NOT NULL REFERENCES project(slug) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('decision', 'note', 'todo', 'status')),
  body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 4000),
  source text NOT NULL,
  client_id text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX entry_slug_created_idx ON entry(slug, created_at DESC, id DESC);

CREATE TABLE chunk (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  ref text NOT NULL CHECK (ref ~ '^(project:[a-z0-9][a-z0-9-]{1,63}|entry:[0-9]+)$'),
  ord integer NOT NULL CHECK (ord >= 0),
  text text NOT NULL,
  text_hash bytea NOT NULL CHECK (octet_length(text_hash) = 32),
  model text NOT NULL,
  embedding halfvec(4096),
  tsv tsvector GENERATED ALWAYS AS (to_tsvector('public.ledger_ts'::regconfig, text)) STORED,
  UNIQUE(ref, ord)
);
CREATE INDEX chunk_tsv_idx ON chunk USING gin(tsv);

CREATE TABLE chunk_dirty (
  ref text PRIMARY KEY,
  queued_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION queue_chunk_dirty() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE dirty_ref text;
BEGIN
  IF TG_TABLE_NAME = 'project' THEN
    dirty_ref := 'project:' || NEW.slug;
  ELSE
    dirty_ref := 'entry:' || NEW.id::text;
  END IF;
  INSERT INTO chunk_dirty(ref, queued_at) VALUES (dirty_ref, now())
    ON CONFLICT (ref) DO UPDATE SET queued_at = EXCLUDED.queued_at;
  PERFORM pg_notify('chunk_dirty', dirty_ref);
  RETURN NEW;
END $$;
CREATE TRIGGER project_dirty AFTER INSERT OR UPDATE ON project FOR EACH ROW EXECUTE FUNCTION queue_chunk_dirty();
CREATE TRIGGER entry_dirty AFTER INSERT ON entry FOR EACH ROW EXECUTE FUNCTION queue_chunk_dirty();

CREATE TABLE oauth_client (
  client_id text PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('cimd', 'dcr')),
  redirect_uris text[] NOT NULL CHECK (cardinality(redirect_uris) > 0),
  name text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_code (
  hash bytea PRIMARY KEY CHECK (octet_length(hash) = 32),
  client_id text NOT NULL REFERENCES oauth_client(client_id) ON DELETE CASCADE,
  redirect_uri text NOT NULL,
  code_challenge text NOT NULL,
  scope text NOT NULL,
  expires_at timestamptz NOT NULL,
  used boolean NOT NULL DEFAULT false
);

CREATE TABLE oauth_token (
  hash bytea PRIMARY KEY CHECK (octet_length(hash) = 32),
  kind text NOT NULL CHECK (kind IN ('access', 'refresh')),
  client_id text NOT NULL REFERENCES oauth_client(client_id) ON DELETE CASCADE,
  scope text NOT NULL,
  family uuid NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_token_family_idx ON oauth_token(family);
