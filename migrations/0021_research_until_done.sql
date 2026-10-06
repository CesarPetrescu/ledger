-- Research has one execution policy, until done: a run works until its acceptance items are met and it
-- submits, or it asks the owner. Stored turn, time, and token budgets no longer govern anything.
UPDATE research_task SET spec = (spec - 'budget') || jsonb_build_object('execution_mode', 'until_done');
