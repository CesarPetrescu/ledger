-- Richer derived fields. details holds structured extras (checklist, key
-- numbers, entities, links, decision chosen/rejected); category is a
-- per-project grouping; unsure lists fields the model was not confident in;
-- edited lists fields the owner has overridden.
ALTER TABLE entry_meta
  ADD COLUMN category text NOT NULL DEFAULT '' CHECK (char_length(category) <= 40),
  ADD COLUMN details jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(details) = 'object'),
  ADD COLUMN unsure text[] NOT NULL DEFAULT '{}',
  ADD COLUMN edited text[] NOT NULL DEFAULT '{}';

-- The owner's corrections to an entry's labels. They are reapplied after
-- every extraction, so they are never overwritten, and serve as examples
-- that teach the extractor the owner's judgement.
CREATE TABLE entry_meta_override (
  entry_id bigint PRIMARY KEY REFERENCES entry(id) ON DELETE CASCADE,
  fields jsonb NOT NULL CHECK (jsonb_typeof(fields) = 'object'),
  updated_at timestamptz NOT NULL DEFAULT now()
);

