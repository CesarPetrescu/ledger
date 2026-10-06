-- Owner-created keys for the plain JSON API (/api/v1), used by servers such as Adastrion Core's
-- research dispatcher. Only a hash is stored; the key itself is shown once when it is created.
CREATE TABLE api_key (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100 AND position(E'\n' in name) = 0 AND position(E'\r' in name) = 0),
  prefix text NOT NULL,
  hash bytea NOT NULL UNIQUE CHECK (octet_length(hash) = 32),
  scopes text[] NOT NULL CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['research:dispatch']),
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked_at timestamptz
);
-- The console's key list follows creates, revocations, and last use (written at most once a minute).
CREATE TRIGGER api_key_admin_event AFTER INSERT OR UPDATE OR DELETE ON api_key
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('api_key');

-- Why the brief last went back to the queue, so the next run's chat knows what it is continuing:
-- '' (never ran), retry_after_failure, revision, answered, retry, restarted, reopened, or requeued (ran
-- before this column existed, for an unknown reason).
ALTER TABLE research_task ADD COLUMN requeue_reason text NOT NULL DEFAULT ''
  CHECK (requeue_reason IN ('', 'retry_after_failure', 'revision', 'answered', 'retry', 'restarted', 'reopened', 'requeued'));
-- The OAuth token family that claimed the current run, so revoking that family (logout, replay) stops it.
ALTER TABLE research_task ADD COLUMN claim_family uuid;

UPDATE research_task SET requeue_reason=CASE WHEN failures > 0 AND last_error <> '' THEN 'retry_after_failure' ELSE 'requeued' END WHERE attempt > 0;

-- Runs in flight now were claimed before claim_family existed, so a token-family revocation could not
-- find them. Requeue them (their run tokens stop working); the next claims record their claimant fully.
WITH stopped AS (
  UPDATE handoff_message m SET work_state='ready', claimed_at=NULL, claimed_source=NULL, claimed_client_id=NULL,
    status_updated_at=now(), status_updated_source='ledger', status_updated_client_id='ledger'
  FROM research_task t WHERE t.message_id=m.id AND m.work_state='in_progress'
  RETURNING m.id
)
UPDATE research_task SET lease_until=NULL, progress='', requeue_reason='restarted' WHERE message_id IN (SELECT id FROM stopped);
