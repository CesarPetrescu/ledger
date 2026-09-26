-- Derived, replaceable metadata for immutable entries. Rows with origin
-- 'model' come from the LLM extractor and may be regenerated; 'owner' rows
-- record console actions and are never overwritten by the extractor.
CREATE TABLE entry_meta (
  entry_id bigint PRIMARY KEY REFERENCES entry(id) ON DELETE CASCADE,
  title text NOT NULL DEFAULT '' CHECK (char_length(title) <= 120),
  tags text[] NOT NULL DEFAULT '{}' CHECK (cardinality(tags) <= 6),
  priority text NOT NULL DEFAULT '' CHECK (priority IN ('', 'low', 'normal', 'high')),
  refs text[] NOT NULL DEFAULT '{}' CHECK (cardinality(refs) <= 10),
  resolves bigint REFERENCES entry(id) ON DELETE SET NULL CHECK (resolves <> entry_id),
  origin text NOT NULL DEFAULT 'model' CHECK (origin IN ('model', 'owner')),
  model text NOT NULL DEFAULT '',
  attempts integer NOT NULL DEFAULT 0,
  error text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX entry_meta_resolves_idx ON entry_meta(resolves) WHERE resolves IS NOT NULL;
CREATE INDEX entry_meta_tags_idx ON entry_meta USING gin(tags);
CREATE INDEX entry_created_idx ON entry(created_at DESC, id DESC);

CREATE TRIGGER entry_meta_admin_event AFTER INSERT OR UPDATE OR DELETE ON entry_meta
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('entry_meta');
