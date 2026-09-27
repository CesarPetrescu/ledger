-- Deleted projects and entries move here whole (rows as JSON), so restore can
-- put them back with their original IDs, metadata, and triage state. Rows are
-- purged permanently after 30 days.
CREATE TABLE trash (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('entry', 'project')),
  label text NOT NULL,
  project_slug text NOT NULL,
  entry_count integer NOT NULL DEFAULT 1,
  payload jsonb NOT NULL,
  deleted_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX trash_deleted_idx ON trash(deleted_at);

-- The owner's quick actions, each with what is needed to undo it on its own.
-- entry_id is informational only: the entry may since have been deleted.
CREATE TABLE owner_action (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('resolve', 'reopen', 'owner', 'trash')),
  entry_id bigint,
  project_slug text NOT NULL DEFAULT '',
  label text NOT NULL,
  undo jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  undone_at timestamptz
);
CREATE INDEX owner_action_created_idx ON owner_action(created_at DESC);

CREATE TRIGGER trash_admin_event AFTER INSERT OR UPDATE OR DELETE ON trash
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('trash');
CREATE TRIGGER owner_action_admin_event AFTER INSERT OR UPDATE OR DELETE ON owner_action
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('owner_action');
