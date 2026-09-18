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
	return m.WithMigrationLock(ctx, func(ctx context.Context) error {
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := inspectMigrationTarget(ctx, tx, m.controlConfig); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, serviceRequestWorkItemAuthorityVerifySQL); err != nil {
			return fmt.Errorf("ServiceRequest WorkItem invariants: %w", err)
		}
		if _, err := tx.ExecContext(ctx, accessSchemaInvariantsSQL); err != nil {
			return fmt.Errorf("finite access invariants: %w", err)
		}
		return tx.Commit()
	})
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
-- Parse the canonical expression against the actual column types in a private
-- temporary relation. Compare the deparsed validated CHECK exactly, preserving
-- existing identities. Never drop/repair an incompatible installed constraint.
DO $$
DECLARE target_table text; target_constraint text; expression text; expected text; actual text; target_schema text := current_schema();
BEGIN
 FOR target_table,target_constraint,expression IN SELECT * FROM (VALUES
('catalog_access_policies','catalog_access_policy_finite',$check$version>0 AND provider='graph' AND btrim(external_system)<>'' AND btrim(group_id)<>'' AND btrim(duration_field)<>'' AND itsm_finite_access_options(duration_options)$check$),
('service_request_access_snapshots','access_snapshot_finite',$check$policy_version>0 AND provider='graph' AND btrim(external_system)<>'' AND btrim(subject_id)<>'' AND btrim(group_id)<>'' AND btrim(duration_key)<>'' AND duration_seconds>0 AND duration_seconds<=9223372036$check$),
('service_request_access_results','access_result_verified',$check$provider='graph' AND btrim(subject_id)<>'' AND btrim(group_id)<>'' AND btrim(evidence_ref)<>'' AND verified_at>'0001-01-01T00:00:00Z'::timestamptz AND ((outcome='granted' AND baseline='not_member' AND expires_at IS NOT NULL AND expires_at>verified_at) OR(outcome='already_present' AND baseline='member' AND expires_at IS NULL))$check$)) AS checks(table_name,constraint_name,expression) LOOP
 EXECUTE format('CREATE TEMP TABLE itsm_access_invariant_shape (LIKE %I.%I) ON COMMIT DROP',target_schema,target_table);
 EXECUTE format('ALTER TABLE pg_temp.itsm_access_invariant_shape ADD CONSTRAINT expected CHECK(%s)',expression);
 SELECT pg_get_constraintdef(oid) INTO expected FROM pg_constraint WHERE conrelid='pg_temp.itsm_access_invariant_shape'::regclass AND conname='expected';
 SELECT pg_get_constraintdef(oid) INTO actual FROM pg_constraint WHERE conrelid=format('%I.%I',target_schema,target_table)::regclass AND conname=target_constraint;
 IF actual IS NULL THEN
 EXECUTE format('ALTER TABLE %I.%I ADD CONSTRAINT %I CHECK(%s)',target_schema,target_table,target_constraint,expression);
 ELSIF actual IS DISTINCT FROM expected THEN
 RAISE EXCEPTION 'incompatible access constraint %.%',target_table,target_constraint;
 END IF;
 DROP TABLE pg_temp.itsm_access_invariant_shape;
 END LOOP;
END $$;
`
