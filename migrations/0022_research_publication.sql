-- When an accepted result was published to the project log. Accepted tasks without it (accepted before
-- publishing existed) are published by ledger-mcp's research sweep.
ALTER TABLE research_task ADD COLUMN published_at timestamptz;

-- Which project is Ledger's catch-all for accepted research with no project. Only Ledger writes this row,
-- so no project setting can make an owner project look like the catch-all.
CREATE TABLE research_catchall (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  project_slug text NOT NULL REFERENCES project(slug) ON DELETE CASCADE ON UPDATE CASCADE
);
