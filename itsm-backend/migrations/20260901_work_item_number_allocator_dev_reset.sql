DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM tickets LIMIT 1) THEN
        RAISE EXCEPTION
            'reset requires an empty tickets table; run development reset first';
    END IF;
END $$;
TRUNCATE TABLE work_item_number_sequences RESTART IDENTITY;
