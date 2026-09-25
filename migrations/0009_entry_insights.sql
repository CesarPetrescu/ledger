-- Entry-level vectors (title and text, without the search chunk header) for
-- related entries and near-duplicate folding, a tag vocabulary for merging
-- synonyms, and per-project weekly digests. All rows are derived and
-- replaceable; entries themselves are never changed.
ALTER TABLE entry_meta
  ADD COLUMN embedding halfvec,
  ADD COLUMN embed_model text NOT NULL DEFAULT '',
  ADD COLUMN duplicate_of bigint REFERENCES entry(id) ON DELETE SET NULL CHECK (duplicate_of <> entry_id),
  ADD COLUMN duplicate_checked boolean NOT NULL DEFAULT false;
CREATE INDEX entry_meta_duplicate_idx ON entry_meta(duplicate_of) WHERE duplicate_of IS NOT NULL;

-- Every tag the consolidator has reviewed; canonical is set when the tag was
-- merged into another one.
CREATE TABLE tag_vocab (
  tag text PRIMARY KEY CHECK (char_length(tag) BETWEEN 1 AND 30),
  canonical text CHECK (canonical <> tag AND char_length(canonical) BETWEEN 1 AND 30),
  reviewed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE project_digest (
  slug text PRIMARY KEY REFERENCES project(slug) ON DELETE CASCADE,
  summary text NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 1200),
  entry_count integer NOT NULL,
  last_entry_id bigint NOT NULL,
  model text NOT NULL DEFAULT '',
  generated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER project_digest_admin_event AFTER INSERT OR UPDATE OR DELETE ON project_digest
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('project_digest');
