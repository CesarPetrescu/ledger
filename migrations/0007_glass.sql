-- Block concurrent writers while backfilling and installing the change trigger.
LOCK TABLE entry IN SHARE ROW EXCLUSIVE MODE;

-- Capture retries return their original receipt; entries remain append-only.
CREATE TABLE entry_write_receipt (
  client_id text NOT NULL,
  request_id text NOT NULL,
  payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
  entry_id bigint NOT NULL REFERENCES entry(id) ON DELETE CASCADE,
  PRIMARY KEY (client_id, request_id)
);

-- Entry identity sequences are allocated BEFORE commit. A separate cursor is
-- allocated AFTER acquiring a transaction lock, so a reader cannot skip a late
-- commit with a smaller ID. Every writer (including the owner API) hits this.
CREATE TABLE entry_change (
  change_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  entry_id bigint NOT NULL UNIQUE REFERENCES entry(id) ON DELETE CASCADE
);
INSERT INTO entry_change(entry_id) SELECT id FROM entry ORDER BY id;
CREATE FUNCTION queue_entry_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(7103376);
  INSERT INTO entry_change(entry_id) VALUES(NEW.id);
  RETURN NEW;
END $$;
CREATE TRIGGER entry_changed AFTER INSERT ON entry
  FOR EACH ROW EXECUTE FUNCTION queue_entry_change();

-- Each granted device has an independent reader; unrelated digests are untouched.
CREATE TABLE glass_reader (
  client_id text NOT NULL REFERENCES oauth_client(client_id) ON DELETE CASCADE,
  reader text NOT NULL CHECK (reader ~ '^[a-z0-9-]{1,32}$'),
  checkpoint bigint NOT NULL DEFAULT 0 CHECK (checkpoint >= 0),
  delivered bigint NOT NULL DEFAULT 0 CHECK (delivered >= checkpoint),
  PRIMARY KEY(client_id, reader)
);
