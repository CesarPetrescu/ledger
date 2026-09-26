-- A todo is resolved by at most one entry. Keep the earliest resolution if
-- concurrent writers produced more than one.
UPDATE entry_meta m SET resolves=NULL
WHERE resolves IS NOT NULL AND EXISTS (SELECT 1 FROM entry_meta o WHERE o.resolves=m.resolves AND o.entry_id<m.entry_id);
DROP INDEX entry_meta_resolves_idx;
CREATE UNIQUE INDEX entry_meta_resolves_key ON entry_meta(resolves) WHERE resolves IS NOT NULL;

-- Duplicate links remember the similarity threshold they were computed with,
-- so changing LEDGER_DUPLICATE_SIMILARITY rechecks every entry. Existing links
-- may point at intermediate repeats; recompute them all against roots.
ALTER TABLE entry_meta ADD COLUMN duplicate_threshold double precision;
UPDATE entry_meta SET duplicate_checked=false, duplicate_of=NULL WHERE duplicate_checked OR duplicate_of IS NOT NULL;

-- Background workers report liveness so the console only promises progress
-- while a worker is actually running.
CREATE TABLE worker_heartbeat (
  name text PRIMARY KEY,
  seen_at timestamptz NOT NULL DEFAULT now()
);
