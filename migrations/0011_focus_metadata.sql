-- Short, type-shaped fields so lists never need an entry's full text. All are
-- derived and replaceable; `version` lets the extractor upgrade older rows.
ALTER TABLE entry_meta
  ADD COLUMN gist text NOT NULL DEFAULT '' CHECK (char_length(gist) <= 240),
  ADD COLUMN importance text NOT NULL DEFAULT '' CHECK (importance IN ('', 'routine', 'useful', 'important')),
  ADD COLUMN ask text NOT NULL DEFAULT '' CHECK (char_length(ask) <= 300),
  ADD COLUMN state text NOT NULL DEFAULT '' CHECK (state IN ('', 'done', 'in_progress', 'blocked')),
  ADD COLUMN next_step text NOT NULL DEFAULT '' CHECK (char_length(next_step) <= 240),
  ADD COLUMN blocker text NOT NULL DEFAULT '' CHECK (char_length(blocker) <= 240),
  ADD COLUMN why text NOT NULL DEFAULT '' CHECK (char_length(why) <= 300),
  ADD COLUMN size text NOT NULL DEFAULT '' CHECK (size IN ('', 'S', 'M', 'L')),
  ADD COLUMN due date,
  ADD COLUMN source_name text NOT NULL DEFAULT '' CHECK (char_length(source_name) <= 120),
  ADD COLUMN link text NOT NULL DEFAULT '' CHECK (char_length(link) <= 500 AND (link = '' OR link ~ '^https?://')),
  ADD COLUMN version integer NOT NULL DEFAULT 1;
CREATE INDEX entry_meta_ask_idx ON entry_meta(entry_id) WHERE ask <> '';

-- The owner's own triage of an entry. Kept apart from entry_meta, which the
-- extractor may regenerate, so reading, starring, handling, and snoozing
-- survive re-extraction.
CREATE TABLE entry_owner_state (
  entry_id bigint PRIMARY KEY REFERENCES entry(id) ON DELETE CASCADE,
  read_at timestamptz,
  starred boolean NOT NULL DEFAULT false,
  handled_at timestamptz,
  snoozed_until date,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER entry_owner_state_admin_event AFTER INSERT OR UPDATE OR DELETE ON entry_owner_state
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('entry_owner_state');
