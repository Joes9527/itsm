package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ariga.io/atlas/sql/postgres"
	atlasschema "ariga.io/atlas/sql/schema"
	entmigrate "itsm-backend/ent/migrate"

	entschema "entgo.io/ent/dialect/sql/schema"
)

var errCurrentEntSchemaDrift = errors.New("compiled Ent schema differs from the database catalog")

// VerifyCurrentSchema checks the immutable current-baseline definitions, the
// full compiled Ent contract, and schema_state storage. Both fresh and upgrade
// paths call this exact read-only verifier before privilege provisioning and
// promotion.
func VerifyCurrentSchema(ctx context.Context, db DBTX, release ReleaseManifest) error {
	if err := ValidateCurrentReleaseArtifact(release); err != nil {
		return fmt.Errorf("validate current release artifact: %w", err)
	}
	if db == nil {
		return fmt.Errorf("current schema database is required")
	}
	parts, err := loadCurrentBaseline()
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, parts.PrepareVerify); err != nil {
		return fmt.Errorf("verify current baseline preparation assets: %w", err)
	}
	if _, err := db.ExecContext(ctx, parts.BaselineVerify); err != nil {
		return fmt.Errorf("verify current baseline assets: %w", err)
	}
	if err := verifyCurrentEntSchema(ctx, db); err != nil {
		return err
	}
	if err := VerifySchemaStateStorage(ctx, db); err != nil {
		return fmt.Errorf("verify current schema state storage: %w", err)
	}
	entry, err := CurrentReleaseCatalogEntry()
	if err != nil {
		return fmt.Errorf("resolve current release schema fingerprint: %w", err)
	}
	if err := VerifyCatalogedUpgradeSourceSchema(ctx, db, entry); err != nil {
		return fmt.Errorf("verify current release schema fingerprint: %w", err)
	}
	return nil
}

// verifyCurrentEntSchema asks Atlas for the complete transition from the live
// PostgreSQL catalog to the compiled Ent descriptor, then rejects any change
// from the diff hook before Atlas constructs or applies a migration plan. This
// covers primary keys, unique/index definitions and predicates, foreign-key
// references/actions, defaults/checks, types, nullability, and identity flags.
// A clean catalog returns an empty change list and performs no writes.
func verifyCurrentEntSchema(ctx context.Context, db DBTX) error {
	conn, ok := db.(BootstrapConnection)
	if !ok {
		return fmt.Errorf("verify current Ent schema: connection-capable read boundary is required")
	}
	client, err := NewEntClientOnConnection(conn)
	if err != nil {
		return fmt.Errorf("verify current Ent schema: %w", err)
	}
	defer client.Close()

	err = client.Schema.Create(ctx,
		entmigrate.WithDropColumn(true),
		entmigrate.WithDropIndex(true),
		entmigrate.WithForeignKeys(true),
		entschema.WithDiffHook(func(next entschema.Differ) entschema.Differ {
			return entschema.DiffFunc(func(current, desired *atlasschema.Schema) ([]atlasschema.Change, error) {
				changes, err := next.Diff(current, desired)
				if err != nil {
					return nil, err
				}
				changes = filterBaselineManagedEntDiffs(changes)
				if len(changes) != 0 {
					return nil, errCurrentEntSchemaDrift
				}
				return nil, nil
			})
		}),
	)
	if err != nil {
		return fmt.Errorf("verify current Ent schema catalog: %w", err)
	}
	return nil
}

func filterBaselineManagedEntDiffs(changes []atlasschema.Change) []atlasschema.Change {
	filtered := make([]atlasschema.Change, 0, len(changes))
	for _, change := range changes {
		table, ok := change.(*atlasschema.ModifyTable)
		if !ok {
			filtered = append(filtered, change)
			continue
		}
		nested := make([]atlasschema.Change, 0, len(table.Changes))
		for _, item := range table.Changes {
			if isVerifiedBaselineEntChange(table.T.Name, item) {
				continue
			}
			nested = append(nested, item)
		}
		if len(nested) != 0 {
			filtered = append(filtered, &atlasschema.ModifyTable{T: table.T, Changes: nested})
		}
	}
	return filtered
}

func isVerifiedBaselineEntChange(tableName string, change atlasschema.Change) bool {
	switch change := change.(type) {
	case *atlasschema.DropIndex:
		return (tableName == "process_instances" && change.I.Name == "idx_process_instances_running_unique") ||
			(tableName == "tool_invocations" && change.I.Name == "idx_tool_invocations_tenant")
	case *atlasschema.DropCheck:
		return tableName == "work_item_number_sequences" &&
			(change.C.Name == "work_item_number_sequences_last_value_check" ||
				change.C.Name == "work_item_number_sequences_period_check")
	case *atlasschema.ModifyIndex:
		if tableName != "work_item_relations" || change.Change != atlasschema.ChangeAttr ||
			change.From == nil || change.To == nil ||
			change.From.Name != "workitemrelation_tenant_id_source_work_item_id" ||
			change.To.Name != "workitemrelation_tenant_id_source_work_item_id" {
			return false
		}
		from, fromOK := postgresIndexPredicate(change.From)
		to, toOK := postgresIndexPredicate(change.To)
		return fromOK && toOK &&
			normalizePredicateWhitespace(from) == "((deleted_at IS NULL) AND ((relation_type)::text = 'investigated_by'::text))" &&
			normalizePredicateWhitespace(to) == "deleted_at IS NULL AND relation_type = 'investigated_by'"
	default:
		return false
	}
}

func postgresIndexPredicate(index *atlasschema.Index) (string, bool) {
	if index == nil {
		return "", false
	}
	for _, attr := range index.Attrs {
		if predicate, ok := attr.(*postgres.IndexPredicate); ok {
			return predicate.P, true
		}
	}
	return "", false
}

func normalizePredicateWhitespace(predicate string) string {
	return strings.Join(strings.Fields(predicate), " ")
}
