-- What stops a worker right now (such as an unreachable AI model), so the
-- console can say why AI labelling is paused instead of showing entries as
-- pending with no reason.
ALTER TABLE worker_heartbeat
  ADD COLUMN problem text NOT NULL DEFAULT '' CHECK (char_length(problem) <= 200),
  ADD COLUMN problem_since timestamptz;
