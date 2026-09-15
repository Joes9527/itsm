package migration

const kafActionRequestDigestSQL = `ALTER TABLE kaf_task_action_ledgers ADD COLUMN IF NOT EXISTS request_digest varchar NOT NULL DEFAULT '';
`
