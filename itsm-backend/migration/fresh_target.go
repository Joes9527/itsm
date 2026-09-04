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

// VerifyFreshBootstrapTarget is the read-only gate run immediately before
// PrepareCurrentInfrastructure opens its DDL transaction. It permits a truly
// empty schema or an exact, verified committed phase of this same fresh
// release. Mixed, legacy, and definition-drifted schemas fail closed.
func VerifyFreshBootstrapTarget(ctx context.Context, db BootstrapConnection, release ReleaseManifest) error {
	_, err := verifyFreshBootstrapTargetPhase(ctx, db, release)
	return err
}

func verifyFreshBootstrapTargetPhase(
	ctx context.Context,
	db BootstrapConnection,
	release ReleaseManifest,
) (freshTargetPhase, error) {
	if db == nil {
		return 0, fmt.Errorf("fresh target database is required")
	}
	if err := ValidateCurrentReleaseArtifact(release); err != nil {
		return 0, fmt.Errorf("validate fresh target release artifact: %w", err)
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
		return 0, fmt.Errorf("inspect fresh target relations: %w", err)
	}
	defer rows.Close()
	var relations []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return 0, fmt.Errorf("inspect fresh target relation: %w", err)
		}
		relations = append(relations, name)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("inspect fresh target relations: %w", err)
	}
	phase, err := classifyFreshTargetRelations(relations)
	if err != nil {
		return 0, err
	}
	if err := verifyFreshPhaseCatalog(ctx, db, phase); err != nil {
		return 0, err
	}
	if phase == freshTargetEmpty {
		return phase, nil
	}
	if err := VerifyFreshMigrationHistory(ctx, db); err != nil {
		return 0, err
	}
	parts, err := loadCurrentBaseline()
	if err != nil {
		return 0, err
	}
	if _, err := db.ExecContext(ctx, parts.PrepareVerify); err != nil {
		return 0, fmt.Errorf("verify committed fresh preparation phase: %w", err)
	}
	if phase >= freshTargetEntSchema {
		if err := verifyCurrentEntSchema(ctx, db); err != nil {
			return 0, fmt.Errorf("verify committed fresh Ent phase: %w", err)
		}
	}
	if phase != freshTargetCurrentRelease {
		return phase, nil
	}
	if err := VerifyCurrentSchema(ctx, db, release); err != nil {
		return 0, fmt.Errorf("verify committed fresh current-release phase: %w", err)
	}
	var stateRows int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_state`).Scan(&stateRows); err != nil {
		return 0, fmt.Errorf("inspect committed fresh schema state: %w", err)
	}
	if stateRows == 0 {
		return phase, nil
	}
	if stateRows != 1 {
		return 0, fmt.Errorf("committed fresh schema state row invariant failed")
	}
	state, err := ReadSchemaState(ctx, db)
	if err != nil {
		return 0, err
	}
	if err := VerifySchemaState(state, release); err != nil {
		return 0, fmt.Errorf("committed fresh schema state does not match current release: %w", err)
	}
	return phase, nil
}
