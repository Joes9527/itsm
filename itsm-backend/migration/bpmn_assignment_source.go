package migration

const bpmnAssignmentSourceSQL = `ALTER TABLE process_tasks
    ADD COLUMN IF NOT EXISTS assignee_source varchar NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'process_tasks_assignee_source_value_check'
          AND conrelid = 'process_tasks'::regclass
    ) THEN
        ALTER TABLE process_tasks
            ADD CONSTRAINT process_tasks_assignee_source_value_check
            CHECK (assignee_source IN ('', 'work_item_assignee'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'process_tasks_assignee_source_participants_check'
          AND conrelid = 'process_tasks'::regclass
    ) THEN
        ALTER TABLE process_tasks
            ADD CONSTRAINT process_tasks_assignee_source_participants_check
            CHECK (
                assignee_source <> 'work_item_assignee'
                OR (
                    COALESCE(assignee, '') = ''
                    AND COALESCE(candidate_users, '') = ''
                    AND COALESCE(candidate_groups, '') = ''
                )
            );
    END IF;
END $$;
`
