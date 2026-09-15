# Task 8

See [the shared atomic Tasks 2, 5, 7, and 8 report](task-2-report.md). Task 8 enqueue-plan validation, optionality snapshot, durable terminal outcomes, and callback effect gates were included in the single cutover commit.

Fix round 1 is recorded in the shared report: non-optional blocked callbacks do
not advance; only the engine may turn a blocked effect into an optional skip
from its persisted definition snapshot.

Fix round 2 shared CAB gate evidence is recorded in task-2-report.md.
