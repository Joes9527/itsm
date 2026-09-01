DO $$
DECLARE
    extension_table TEXT;
    shared_column TEXT;
BEGIN
    FOREACH extension_table IN ARRAY ARRAY['incidents', 'problems', 'changes'] LOOP
        IF to_regclass(current_schema() || '.' || extension_table) IS NULL THEN
            RAISE EXCEPTION 'required professional extension table % is missing', extension_table;
        END IF;

        FOREACH shared_column IN ARRAY ARRAY['title', 'description', 'status', 'priority'] LOOP
            IF EXISTS (
                SELECT 1
                FROM information_schema.columns
                WHERE table_schema = current_schema()
                  AND table_name = extension_table
                  AND column_name = shared_column
            ) THEN
                RAISE EXCEPTION 'WorkItem-owned column %.% still exists', extension_table, shared_column;
            END IF;
        END LOOP;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = current_schema()
              AND table_name = extension_table
              AND column_name = 'work_item_id'
              AND is_nullable = 'NO'
        ) THEN
            RAISE EXCEPTION '%.work_item_id must exist and be NOT NULL', extension_table;
        END IF;
    END LOOP;

    IF EXISTS (
        SELECT 1 FROM incidents extension
        LEFT JOIN tickets work_item ON work_item.id = extension.work_item_id
        WHERE work_item.id IS NULL OR work_item.record_class <> 'incident'
    ) THEN
        RAISE EXCEPTION 'incident extension has an invalid WorkItem link';
    END IF;
    IF EXISTS (
        SELECT 1 FROM problems extension
        LEFT JOIN tickets work_item ON work_item.id = extension.work_item_id
        WHERE work_item.id IS NULL OR work_item.record_class <> 'problem'
    ) THEN
        RAISE EXCEPTION 'problem extension has an invalid WorkItem link';
    END IF;
    IF EXISTS (
        SELECT 1 FROM changes extension
        LEFT JOIN tickets work_item ON work_item.id = extension.work_item_id
        WHERE work_item.id IS NULL OR work_item.record_class <> 'change_request'
    ) THEN
        RAISE EXCEPTION 'change extension has an invalid WorkItem link';
    END IF;
END $$;
