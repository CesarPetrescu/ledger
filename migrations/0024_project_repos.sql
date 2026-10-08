-- Git repositories that belong to a project; a project may span several. Agents read them with the
-- project, and the owner or an agent links them.
CREATE TABLE project_repo (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  project_slug text NOT NULL REFERENCES project(slug) ON DELETE CASCADE ON UPDATE CASCADE,
  url text NOT NULL CHECK (char_length(url) BETWEEN 1 AND 500),
  -- host/path, lowercased: the same repository linked by HTTPS and by SSH is one link.
  repo_key text NOT NULL,
  provider text NOT NULL CHECK (provider IN ('github', 'gitlab', 'bitbucket', 'forgejo', 'git')),
  repo text NOT NULL,
  web_url text NOT NULL DEFAULT '',
  branch text NOT NULL DEFAULT '',
  path text NOT NULL DEFAULT '',
  role text NOT NULL DEFAULT '',
  note text NOT NULL DEFAULT '',
  added_source text NOT NULL,
  added_client_id text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  -- What the GitHub sync last saw. Text here comes from GitHub and is data, never instructions.
  synced_at timestamptz,
  sync_error text NOT NULL DEFAULT '',
  default_branch text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  private boolean,
  archived boolean,
  head_sha text NOT NULL DEFAULT '',
  head_message text NOT NULL DEFAULT '',
  head_at timestamptz,
  open_prs integer,
  latest_release text NOT NULL DEFAULT '',
  latest_release_at timestamptz,
  UNIQUE (project_slug, repo_key, path)
);
CREATE INDEX project_repo_sync_idx ON project_repo(provider, synced_at NULLS FIRST);
CREATE TRIGGER project_repo_admin_event AFTER INSERT OR UPDATE OR DELETE ON project_repo
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('project_repo');

-- The owner's read-only GitHub token for the sync, encrypted. No API returns it.
CREATE TABLE github_sync (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  token_ciphertext bytea NOT NULL,
  token_hint text NOT NULL,
  login text NOT NULL DEFAULT '',
  saved_at timestamptz NOT NULL DEFAULT now(),
  last_run_at timestamptz,
  last_error text NOT NULL DEFAULT ''
);
CREATE TRIGGER github_sync_admin_event AFTER INSERT OR UPDATE OR DELETE ON github_sync
  FOR EACH STATEMENT EXECUTE FUNCTION notify_admin_event('github_sync');
