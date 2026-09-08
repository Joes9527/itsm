-- Historical empty digests never authorize verified-access replay.
ALTER TABLE kaf_task_action_ledgers ADD COLUMN IF NOT EXISTS request_digest varchar NOT NULL DEFAULT '';
