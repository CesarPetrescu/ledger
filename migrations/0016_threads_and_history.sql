-- Replies: an entry can answer another. No foreign key, so a thread survives
-- its root going to Trash and coming back (entry IDs are never reused).
ALTER TABLE entry ADD COLUMN reply_to bigint;
CREATE INDEX entry_reply_to_idx ON entry(reply_to) WHERE reply_to IS NOT NULL;
-- Where an agent wrote from (repo, branch, session, link), when it says.
ALTER TABLE entry ADD COLUMN context text NOT NULL DEFAULT '' CHECK (char_length(context) <= 300);

-- The owner's label corrections, for an entry's history.
CREATE TABLE entry_label_edit (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  entry_id bigint NOT NULL,
  fields text[] NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX entry_label_edit_entry_idx ON entry_label_edit(entry_id, created_at);
