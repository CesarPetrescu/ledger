-- When an accepted result was published to the project log. Accepted tasks without it (accepted before
-- publishing existed) are published by ledger-mcp's research sweep.
ALTER TABLE research_task ADD COLUMN published_at timestamptz;

-- Marks Ledger's catch-all project for accepted research with no project. Only Ledger sets it, no project
-- setting can, and it travels with the project row through Trash and restore.
ALTER TABLE project ADD COLUMN research_catchall boolean NOT NULL DEFAULT false;
