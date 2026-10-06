ALTER TABLE research_task ADD COLUMN continue_from_task_id bigint REFERENCES research_task(handoff_id);
ALTER TABLE research_task ADD COLUMN continue_from_attempt integer;
ALTER TABLE research_task ADD CONSTRAINT research_continuation_pair CHECK ((continue_from_task_id IS NULL) = (continue_from_attempt IS NULL));
