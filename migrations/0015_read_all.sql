-- "Mark all read" is one undoable action over many entries.
ALTER TABLE owner_action DROP CONSTRAINT owner_action_kind_check;
ALTER TABLE owner_action ADD CONSTRAINT owner_action_kind_check CHECK (kind IN ('resolve', 'reopen', 'owner', 'trash', 'read_all'));
