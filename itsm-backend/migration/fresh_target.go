package migration

import (
	"context"
	"fmt"

	entmigrate "itsm-backend/ent/migrate"
)

type freshTargetPhase uint8

const (
	freshTargetEmpty freshTargetPhase = iota
	freshTargetPrepared
	freshTargetEntSchema
	freshTargetCurrentRelease
)

var currentBaselineRelations = []string{
	"change_approval_chains",
	"change_approvals",
	"change_implementation_plans",
	"change_risk_assessments",
	"change_rollback_executions",
	"change_rollback_plans",
	"initialization_component_attempts",
	"initialization_installations",
	"initialization_managed_records",
	"initialization_runs",
	"schema_migrations",
	"schema_state",
}

func freshPrepareRelationNames() []string {
	return []string{"ai_feedbacks", "vectors"}
}

func freshEntRelationNames() []string {
	result := make([]string, 0, len(entmigrate.Tables))
	for _, table := range entmigrate.Tables {
		if table != nil {
			result = append(result, table.Name)
		}
	}
	return result
}

func freshBaselineRelationNames() []string {
	return append([]string(nil), currentBaselineRelations...)
}

func classifyFreshTargetRelations(relations []string) (freshTargetPhase, error) {
	wantPrepared := relationSet(freshPrepareRelationNames()...)
	wantEnt := relationSet(append(freshPrepareRelationNames(), freshEntRelationNames()...)...)
	wantCurrent := relationSet(append(append(freshPrepareRelationNames(), freshEntRelationNames()...), freshBaselineRelationNames()...)...)
	actual := relationSet(relations...)
	switch {
	case len(actual) == 0:
		return freshTargetEmpty, nil
	case equalRelationSets(actual, wantPrepared):
		return freshTargetPrepared, nil
	case equalRelationSets(actual, wantEnt):
		return freshTargetEntSchema, nil
	case equalRelationSets(actual, wantCurrent):
		return freshTargetCurrentRelease, nil
	default:
		return 0, fmt.Errorf("fresh target is neither empty nor a verified current-release phase")
	}
}

func relationSet(names ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

func equalRelationSets(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for name := range left {
		if _, exists := right[name]; !exists {
			return false
		}
	}
	return true
}

func verifyNoUnexpectedFreshSchemaObjects(ctx context.Context, db DBTX) error {
	var sequenceCount, routineCount, typeCount, triggerCount int64
	if err := db.QueryRowContext(ctx, `
		/* fresh_bootstrap_target_standalone_objects */
		WITH unexpected_sequences AS (
			SELECT sequence_relation.oid
			FROM pg_class sequence_relation
			JOIN pg_namespace namespace ON namespace.oid = sequence_relation.relnamespace
			WHERE namespace.nspname = current_schema()
			  AND sequence_relation.relkind = 'S'
			  AND NOT EXISTS (
				SELECT 1
				FROM pg_depend dependency
				JOIN pg_class owner_relation ON owner_relation.oid = dependency.refobjid
				JOIN pg_namespace owner_namespace ON owner_namespace.oid = owner_relation.relnamespace
				WHERE dependency.classid = 'pg_class'::regclass
				  AND dependency.objid = sequence_relation.oid
				  AND dependency.refclassid = 'pg_class'::regclass
				  AND dependency.refobjsubid > 0
				  AND dependency.deptype IN ('a', 'i')
				  AND owner_namespace.nspname = current_schema()
			  )
			  AND NOT EXISTS (
				SELECT 1 FROM pg_depend dependency
				WHERE dependency.classid = 'pg_class'::regclass
				  AND dependency.objid = sequence_relation.oid
				  AND dependency.refclassid = 'pg_extension'::regclass
				  AND dependency.deptype = 'e'
			  )
		), unexpected_routines AS (
			SELECT routine.oid
			FROM pg_proc routine
			JOIN pg_namespace namespace ON namespace.oid = routine.pronamespace
			WHERE namespace.nspname = current_schema()
			  AND NOT EXISTS (
				SELECT 1 FROM pg_depend dependency
				WHERE dependency.classid = 'pg_proc'::regclass
				  AND dependency.objid = routine.oid
				  AND dependency.refclassid = 'pg_extension'::regclass
				  AND dependency.deptype = 'e'
			  )
		), unexpected_types AS (
			SELECT type_record.oid
			FROM pg_type type_record
			JOIN pg_namespace namespace ON namespace.oid = type_record.typnamespace
			WHERE namespace.nspname = current_schema()
			  AND (
				type_record.typtype IN ('d', 'e', 'r', 'm')
				OR (
					type_record.typtype = 'c'
					AND EXISTS (
						SELECT 1 FROM pg_class composite_relation
						WHERE composite_relation.oid = type_record.typrelid
						  AND composite_relation.relkind = 'c'
					)
				)
			  )
			  AND NOT EXISTS (
				SELECT 1 FROM pg_depend dependency
				WHERE dependency.classid = 'pg_type'::regclass
				  AND dependency.objid = type_record.oid
				  AND dependency.refclassid = 'pg_extension'::regclass
				  AND dependency.deptype = 'e'
			  )
		), unexpected_triggers AS (
			SELECT trigger_record.oid
			FROM pg_trigger trigger_record
			JOIN pg_class relation ON relation.oid = trigger_record.tgrelid
			JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = current_schema()
			  AND NOT trigger_record.tgisinternal
			  AND NOT EXISTS (
				SELECT 1 FROM pg_depend dependency
				WHERE dependency.classid = 'pg_trigger'::regclass
				  AND dependency.objid = trigger_record.oid
				  AND dependency.refclassid = 'pg_extension'::regclass
				  AND dependency.deptype = 'e'
			  )
		)
		SELECT (SELECT COUNT(*) FROM unexpected_sequences),
		       (SELECT COUNT(*) FROM unexpected_routines),
		       (SELECT COUNT(*) FROM unexpected_types),
		       (SELECT COUNT(*) FROM unexpected_triggers)
	`).Scan(&sequenceCount, &routineCount, &typeCount, &triggerCount); err != nil {
		return fmt.Errorf("inspect fresh target standalone objects: %w", err)
	}
	if sequenceCount != 0 || routineCount != 0 || typeCount != 0 || triggerCount != 0 {
		return fmt.Errorf("fresh target contains an unexpected standalone schema object")
	}
	return nil
}

// VerifyFreshBootstrapTarget is the read-only gate run immediately before
// PrepareCurrentInfrastructure opens its DDL transaction. It permits a truly
// empty schema or an exact, verified committed phase of this same fresh
// release. Mixed, legacy, and definition-drifted schemas fail closed.
func VerifyFreshBootstrapTarget(ctx context.Context, db BootstrapConnection, release ReleaseManifest) error {
	if db == nil {
		return fmt.Errorf("fresh target database is required")
	}
	if err := ValidateCurrentReleaseArtifact(release); err != nil {
		return fmt.Errorf("validate fresh target release artifact: %w", err)
	}
	rows, err := db.QueryContext(ctx, `
		/* fresh_bootstrap_target_relations */
		SELECT relation.relname
		FROM pg_class relation
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = current_schema()
		  AND relation.relkind IN ('r', 'p', 'v', 'm', 'f')
		ORDER BY relation.relname
	`)
	if err != nil {
		return fmt.Errorf("inspect fresh target relations: %w", err)
	}
	defer rows.Close()
	var relations []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("inspect fresh target relation: %w", err)
		}
		relations = append(relations, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect fresh target relations: %w", err)
	}
	phase, err := classifyFreshTargetRelations(relations)
	if err != nil {
		return err
	}
	if err := verifyNoUnexpectedFreshSchemaObjects(ctx, db); err != nil {
		return err
	}
	if phase == freshTargetEmpty {
		return nil
	}
	if err := VerifyFreshMigrationHistory(ctx, db); err != nil {
		return err
	}
	parts, err := loadCurrentBaseline()
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, parts.PrepareVerify); err != nil {
		return fmt.Errorf("verify committed fresh preparation phase: %w", err)
	}
	if phase >= freshTargetEntSchema {
		if err := verifyCurrentEntSchema(ctx, db); err != nil {
			return fmt.Errorf("verify committed fresh Ent phase: %w", err)
		}
	}
	if phase != freshTargetCurrentRelease {
		return nil
	}
	if err := VerifyCurrentSchema(ctx, db, release); err != nil {
		return fmt.Errorf("verify committed fresh current-release phase: %w", err)
	}
	var stateRows int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_state`).Scan(&stateRows); err != nil {
		return fmt.Errorf("inspect committed fresh schema state: %w", err)
	}
	if stateRows == 0 {
		return nil
	}
	if stateRows != 1 {
		return fmt.Errorf("committed fresh schema state row invariant failed")
	}
	state, err := ReadSchemaState(ctx, db)
	if err != nil {
		return err
	}
	if err := VerifySchemaState(state, release); err != nil {
		return fmt.Errorf("committed fresh schema state does not match current release: %w", err)
	}
	return nil
}
