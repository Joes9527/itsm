package migration

import (
	"context"
	"fmt"
)

// ReconcileSchemaInvariants owns PostgreSQL invariants that Ent cannot express
// portably. Every canonical startup runs it after Ent and the versioned stream.
// Historical migration 030 is immutable; recorded installations are repaired too.
// Function and dependent CHECKs are installed atomically, before seeding.
// Existing invalid data aborts initialization rather than weakening enforcement.
func (m *Migrator) ReconcileSchemaInvariants(ctx context.Context) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, accessSchemaInvariantsSQL); err != nil {
		return fmt.Errorf("finite access invariants: %w", err)
	}
	return tx.Commit()
}

const accessSchemaInvariantsSQL = `-- Ent creates the typed tables/FKs before the registered migration stream.
-- Cross-owner invariants and immutable evidence are enforced here.
CREATE OR REPLACE FUNCTION itsm_finite_access_options(options jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE option jsonb; seen text[] := '{}'; seconds numeric;
BEGIN
 IF jsonb_typeof(options) <> 'array' OR jsonb_array_length(options)=0 THEN RETURN false; END IF;
 FOR option IN SELECT value FROM jsonb_array_elements(options) LOOP
  IF jsonb_typeof(option)<>'object' OR jsonb_typeof(option->'key')<>'string'
   OR jsonb_typeof(option->'label')<>'string' OR jsonb_typeof(option->'seconds')<>'number'
   OR COALESCE(btrim(option->>'key'),'')='' OR COALESCE(btrim(option->>'label'),'')=''
   OR (option->>'key')=ANY(seen) THEN RETURN false; END IF;
  seconds := (option->>'seconds')::numeric;
  IF seconds IS NULL OR seconds<>trunc(seconds) OR seconds<=0 OR seconds>9223372036 THEN RETURN false; END IF;
  seen:=array_append(seen,option->>'key');
 END LOOP;
 RETURN true;
END $$;
DO $$ BEGIN
 ALTER TABLE catalog_access_policies DROP CONSTRAINT IF EXISTS catalog_access_policy_finite;
 ALTER TABLE catalog_access_policies ADD CONSTRAINT catalog_access_policy_finite CHECK(version>0 AND provider='graph' AND btrim(external_system)<>'' AND btrim(group_id)<>'' AND btrim(duration_field)<>'' AND itsm_finite_access_options(duration_options));
 ALTER TABLE service_request_access_snapshots DROP CONSTRAINT IF EXISTS access_snapshot_finite;
 ALTER TABLE service_request_access_snapshots ADD CONSTRAINT access_snapshot_finite CHECK(policy_version>0 AND provider='graph' AND btrim(external_system)<>'' AND btrim(subject_id)<>'' AND btrim(group_id)<>'' AND btrim(duration_key)<>'' AND duration_seconds>0 AND duration_seconds<=9223372036);
 ALTER TABLE service_request_access_results DROP CONSTRAINT IF EXISTS access_result_verified;
 ALTER TABLE service_request_access_results ADD CONSTRAINT access_result_verified CHECK(provider='graph' AND btrim(subject_id)<>'' AND btrim(group_id)<>'' AND btrim(evidence_ref)<>'' AND verified_at>'0001-01-01T00:00:00Z'::timestamptz AND ((outcome='granted' AND baseline='not_member' AND expires_at IS NOT NULL AND expires_at>verified_at) OR(outcome='already_present' AND baseline='member' AND expires_at IS NULL)));
END $$;
`
