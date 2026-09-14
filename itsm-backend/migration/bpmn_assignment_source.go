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

CREATE OR REPLACE FUNCTION prevent_process_task_assignee_source_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.assignee_source IS DISTINCT FROM OLD.assignee_source THEN
        RAISE EXCEPTION 'process_tasks.assignee_source is immutable';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS process_tasks_assignee_source_immutable ON process_tasks;
CREATE TRIGGER process_tasks_assignee_source_immutable
    BEFORE UPDATE OF assignee_source ON process_tasks
    FOR EACH ROW
    EXECUTE FUNCTION prevent_process_task_assignee_source_update();
`
