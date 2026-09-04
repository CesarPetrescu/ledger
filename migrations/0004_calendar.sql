CREATE TABLE calendar_account (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  server_url text NOT NULL CHECK (server_url <> ''),
  username text NOT NULL CHECK (username <> ''),
  password_ciphertext bytea NOT NULL CHECK (octet_length(password_ciphertext) > 28),
  selected_calendars text[] NOT NULL DEFAULT '{}',
  connected_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER calendar_account_admin_event AFTER INSERT OR UPDATE OR DELETE ON calendar_account
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('calendar');
