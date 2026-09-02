DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM intake_requests LIMIT 1)
       OR EXISTS (SELECT 1 FROM intake_resolution_snapshots LIMIT 1)
       OR EXISTS (SELECT 1 FROM external_identities LIMIT 1) THEN
        RAISE EXCEPTION
            'reset requires empty intake_requests, intake_resolution_snapshots, and external_identities tables; run development reset first';
    END IF;
END $$;

DROP POLICY IF EXISTS intake_requests_tenant_isolation ON intake_requests;
ALTER TABLE intake_requests NO FORCE ROW LEVEL SECURITY;
ALTER TABLE intake_requests DISABLE ROW LEVEL SECURITY;
ALTER TABLE intake_requests DROP CONSTRAINT IF EXISTS intake_requests_completed_work_item_check;

DROP POLICY IF EXISTS intake_resolution_snapshots_tenant_isolation ON intake_resolution_snapshots;
ALTER TABLE intake_resolution_snapshots NO FORCE ROW LEVEL SECURITY;
ALTER TABLE intake_resolution_snapshots DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS external_identities_tenant_isolation ON external_identities;
ALTER TABLE external_identities NO FORCE ROW LEVEL SECURITY;
ALTER TABLE external_identities DISABLE ROW LEVEL SECURITY;
