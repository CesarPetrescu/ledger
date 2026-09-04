CREATE OR REPLACE FUNCTION notify_admin_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('ledger_admin_event', json_build_object(
    'type', 'change',
    'entity', TG_ARGV[0],
    'operation', lower(TG_OP)
  )::text);
  RETURN COALESCE(NEW, OLD);
END $$;

CREATE TRIGGER project_admin_event AFTER INSERT OR UPDATE OR DELETE ON project
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('project');
CREATE TRIGGER entry_admin_event AFTER INSERT OR UPDATE OR DELETE ON entry
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('entry');
CREATE TRIGGER oauth_client_admin_event AFTER INSERT OR UPDATE OR DELETE ON oauth_client
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('oauth_client');
CREATE TRIGGER oauth_token_admin_event AFTER INSERT OR UPDATE OR DELETE ON oauth_token
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('oauth_token');
CREATE TRIGGER chunk_admin_event AFTER INSERT OR UPDATE OR DELETE ON chunk
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('chunk');
