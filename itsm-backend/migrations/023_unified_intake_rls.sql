DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'intake_requests_completed_work_item_check'
    ) THEN
        ALTER TABLE intake_requests
            ADD CONSTRAINT intake_requests_completed_work_item_check
            CHECK (status <> 'completed' OR (work_item_id IS NOT NULL AND completed_at IS NOT NULL));
    END IF;
END $$;

ALTER TABLE intake_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE intake_requests FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS intake_requests_tenant_isolation ON intake_requests;
CREATE POLICY intake_requests_tenant_isolation ON intake_requests
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint);

ALTER TABLE intake_resolution_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE intake_resolution_snapshots FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS intake_resolution_snapshots_tenant_isolation ON intake_resolution_snapshots;
CREATE POLICY intake_resolution_snapshots_tenant_isolation ON intake_resolution_snapshots
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint);

ALTER TABLE external_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE external_identities FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS external_identities_tenant_isolation ON external_identities;
CREATE POLICY external_identities_tenant_isolation ON external_identities
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint);
