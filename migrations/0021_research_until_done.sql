-- Research has one execution policy, until done: a run works until its acceptance items are met and it
-- submits, or it asks the owner. Stored turn, time, and token budgets no longer govern anything.
-- spec_version 2 is this shape (execution_mode, no budget), so a reader can tell it from version 1.
UPDATE research_task SET spec = (spec - 'budget') || jsonb_build_object('execution_mode', 'until_done'), spec_version = GREATEST(spec_version, 2);
ALTER TABLE research_task ALTER COLUMN spec_version SET DEFAULT 2;
