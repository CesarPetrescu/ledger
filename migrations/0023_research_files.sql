-- Short-lived download links for handoff files, so a person can fetch a file an agent points them to.
-- Only a hash of each link's secret is stored.
CREATE TABLE file_link (
  hash bytea PRIMARY KEY CHECK (octet_length(hash) = 32),
  file_id bigint NOT NULL REFERENCES handoff_file(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL
);
CREATE INDEX file_link_expires_idx ON file_link(expires_at);

-- Files a research run uploads over HTTP before it submits. submit moves the ones it names into its
-- result; the research sweep removes the rest once the run is over.
CREATE TABLE research_upload (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  handoff_id bigint NOT NULL REFERENCES research_task(handoff_id) ON DELETE CASCADE,
  attempt integer NOT NULL,
  filename text NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 255 AND position('/' in filename) = 0 AND position(chr(92) in filename) = 0),
  media_type text NOT NULL CHECK (char_length(media_type) BETWEEN 1 AND 255),
  size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 26214400),
  sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
  data bytea NOT NULL CHECK (octet_length(data) = size_bytes),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX research_upload_run_idx ON research_upload(handoff_id, attempt);
