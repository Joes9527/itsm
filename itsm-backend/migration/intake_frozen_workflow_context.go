package migration

// Existing snapshots are intentionally not reconstructed from current mutable configuration.
// Qualify the selected schema so a missing local table cannot target a later search_path entry.
const intakeFrozenWorkflowContextSQL = `DO $$
DECLARE selected_schema text := current_schema();
BEGIN
    IF selected_schema IS NULL THEN
        RAISE EXCEPTION '036 requires a selected schema';
    END IF;
    EXECUTE format('ALTER TABLE %I.intake_resolution_snapshots ADD COLUMN IF NOT EXISTS workflow_definition_digest character varying', selected_schema);
    EXECUTE format('ALTER TABLE %I.intake_resolution_snapshots ADD COLUMN IF NOT EXISTS workflow_variables jsonb', selected_schema);
END $$;`
