DO $$
DECLARE
    source_column record;
BEGIN
    SELECT is_nullable, column_default
      INTO source_column
      FROM information_schema.columns
     WHERE table_schema = current_schema()
       AND table_name = 'process_tasks'
       AND column_name = 'assignee_source';

    IF NOT FOUND OR source_column.is_nullable <> 'NO' OR source_column.column_default IS NULL THEN
        RAISE EXCEPTION 'process_tasks.assignee_source must be NOT NULL with an empty-string default';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'process_tasks_assignee_source_value_check'
           AND conrelid = 'process_tasks'::regclass
           AND pg_get_constraintdef(oid) LIKE '%work_item_assignee%'
    ) THEN
        RAISE EXCEPTION 'process_tasks assignee source value constraint is missing';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'process_tasks_assignee_source_participants_check'
           AND conrelid = 'process_tasks'::regclass
           AND pg_get_constraintdef(oid) LIKE '%candidate_users%'
           AND pg_get_constraintdef(oid) LIKE '%candidate_groups%'
    ) THEN
        RAISE EXCEPTION 'process_tasks bound participant conflict constraint is missing';
    END IF;
END $$;
