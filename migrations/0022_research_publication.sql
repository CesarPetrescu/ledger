-- When an accepted result was published to the project log. Accepted tasks without it (accepted before
-- publishing existed) are published by ledger-mcp's research sweep.
ALTER TABLE research_task ADD COLUMN published_at timestamptz;
