package migration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// DBTX is implemented by *sql.DB and *sql.Tx.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// SchemaState is the single authoritative database release marker.
type SchemaState struct {
	ID                      int16
	ReleaseID               string
	SchemaVersion           string
	BaselineVersion         string
	ReleaseManifestChecksum string
	UpdatedAt               time.Time
}

// VerifySchemaStateStorage verifies the concrete PostgreSQL storage contract
// before privileges are provisioned or a release marker is promoted. Task 4's
// full current-schema verifier composes this invariant with the remaining
// release invariants.
func VerifySchemaStateStorage(ctx context.Context, db DBTX) error {
	if db == nil {
		return fmt.Errorf("schema state store is required")
	}

	var relationCount, columnCount, matchingColumnCount, primaryKeyCount int64
	var inheritanceParentCount, inheritingChildCount int64
	var hasSubclass bool
	var checkExpressions, updatedAtDefault string
	if err := db.QueryRowContext(ctx, `
		/* schema_state_storage_catalog */
		WITH target_relation AS (
			SELECT relation.oid, relation.relhassubclass
			FROM pg_class relation
			JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = current_schema()
			  AND relation.relname = 'schema_state'
			  AND relation.relkind = 'r'
		),
		expected_columns(name, formatted_type) AS (
			VALUES
				('id', 'smallint'),
				('release_id', 'character varying(128)'),
				('schema_version', 'character varying(255)'),
				('baseline_version', 'character varying(64)'),
				('release_manifest_checksum', 'character(64)'),
				('updated_at', 'timestamp with time zone')
		),
		actual_columns AS (
			SELECT attribute.attname AS name,
			       format_type(attribute.atttypid, attribute.atttypmod) AS formatted_type,
			       attribute.attnotnull AS not_null,
			       attribute.attnum AS number
			FROM pg_attribute attribute
			WHERE attribute.attrelid = (SELECT oid FROM target_relation)
			  AND attribute.attnum > 0
			  AND NOT attribute.attisdropped
		)
		SELECT
			(SELECT COUNT(*) FROM target_relation),
			(SELECT COUNT(*) FROM actual_columns),
			(SELECT COUNT(*)
			 FROM actual_columns actual
			 JOIN expected_columns expected
			   ON expected.name = actual.name
			  AND expected.formatted_type = actual.formatted_type
			 WHERE actual.not_null),
			(SELECT COUNT(*)
			 FROM pg_constraint constraint_record
			 WHERE constraint_record.conrelid = (SELECT oid FROM target_relation)
			   AND constraint_record.contype = 'p'
			   AND constraint_record.conkey = ARRAY[(
				SELECT number FROM actual_columns WHERE name = 'id'
			   )]::SMALLINT[]),
			COALESCE((
				SELECT string_agg(
					pg_get_expr(constraint_record.conbin, constraint_record.conrelid),
					E'\n'
				)
				FROM pg_constraint constraint_record
				WHERE constraint_record.conrelid = (SELECT oid FROM target_relation)
				  AND constraint_record.contype = 'c'
				  AND constraint_record.convalidated
			), ''),
			COALESCE((
				SELECT pg_get_expr(default_record.adbin, default_record.adrelid)
				FROM pg_attrdef default_record
				JOIN actual_columns actual ON actual.number = default_record.adnum
				WHERE default_record.adrelid = (SELECT oid FROM target_relation)
				  AND actual.name = 'updated_at'
			), ''),
			(SELECT COUNT(*)
			 FROM pg_inherits inheritance_record
			 WHERE inheritance_record.inhrelid = (SELECT oid FROM target_relation)),
			(SELECT COUNT(*)
			 FROM pg_inherits inheritance_record
			 WHERE inheritance_record.inhparent = (SELECT oid FROM target_relation)),
			COALESCE((SELECT relhassubclass FROM target_relation), FALSE)
	`).Scan(
		&relationCount,
		&columnCount,
		&matchingColumnCount,
		&primaryKeyCount,
		&checkExpressions,
		&updatedAtDefault,
		&inheritanceParentCount,
		&inheritingChildCount,
		&hasSubclass,
	); err != nil {
		return fmt.Errorf("verify schema state storage catalog: %w", err)
	}
	if relationCount != 1 {
		return fmt.Errorf("schema state storage relation invariant failed")
	}
	if inheritanceParentCount != 0 || inheritingChildCount != 0 || hasSubclass {
		return fmt.Errorf("schema state storage standalone relation invariant failed")
	}
	if columnCount != 6 || matchingColumnCount != 6 {
		return fmt.Errorf("schema state storage column invariant failed")
	}
	if primaryKeyCount != 1 {
		return fmt.Errorf("schema state storage primary key invariant failed")
	}
	if !containsSchemaStateIDCheck(checkExpressions) {
		return fmt.Errorf("schema state storage singleton check invariant failed")
	}
	if !isSchemaStateTimestampDefault(updatedAtDefault) {
		return fmt.Errorf("schema state storage timestamp default invariant failed")
	}

	var rowCount, invalidIDCount int64
	if err := db.QueryRowContext(ctx, `
		/* schema_state_storage_rows */
		SELECT COUNT(*), COUNT(*) FILTER (WHERE id <> 1)
		FROM schema_state
	`).Scan(&rowCount, &invalidIDCount); err != nil {
		return fmt.Errorf("verify schema state singleton rows: %w", err)
	}
	if rowCount > 1 || invalidIDCount != 0 {
		return fmt.Errorf("schema state storage row invariant failed")
	}
	return nil
}

func containsSchemaStateIDCheck(expressions string) bool {
	for _, expression := range strings.Split(expressions, "\n") {
		normalized := strings.NewReplacer(
			" ", "",
			"\t", "",
			"\r", "",
			"(", "",
			")", "",
			`"`, "",
		).Replace(strings.ToLower(expression))
		switch normalized {
		case "id=1", "id=1::smallint", "id=1::int2":
			return true
		}
	}
	return false
}

func isSchemaStateTimestampDefault(expression string) bool {
	normalized := strings.NewReplacer(
		" ", "",
		"\t", "",
		"\r", "",
		"\n", "",
		"(", "",
		")", "",
	).Replace(strings.ToLower(expression))
	return normalized == "current_timestamp" || normalized == "now"
}

// ReadSchemaState reads the only valid schema-state row.
func ReadSchemaState(ctx context.Context, db DBTX) (SchemaState, error) {
	if db == nil {
		return SchemaState{}, fmt.Errorf("schema state store is required")
	}
	var state SchemaState
	var updatedAt any
	err := db.QueryRowContext(ctx, `
		SELECT id, release_id, schema_version, baseline_version,
		       release_manifest_checksum, updated_at
		FROM schema_state
		WHERE id = 1
	`).Scan(
		&state.ID,
		&state.ReleaseID,
		&state.SchemaVersion,
		&state.BaselineVersion,
		&state.ReleaseManifestChecksum,
		&updatedAt,
	)
	if err != nil {
		return SchemaState{}, fmt.Errorf("read schema state: %w", err)
	}
	state.UpdatedAt, err = normalizeSchemaStateTime(updatedAt)
	if err != nil {
		return SchemaState{}, err
	}
	return state, nil
}

func normalizeSchemaStateTime(value any) (time.Time, error) {
	switch value := value.(type) {
	case time.Time:
		return value, nil
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05"} {
			parsed, err := time.Parse(layout, value)
			if err == nil {
				return parsed, nil
			}
		}
	case []byte:
		return normalizeSchemaStateTime(string(value))
	}
	return time.Time{}, fmt.Errorf("read schema state: invalid update timestamp")
}

// PromoteSchemaState atomically inserts or replaces the singleton marker with
// a checksum recomputed from the supplied, validated release manifest.
func PromoteSchemaState(ctx context.Context, db DBTX, release ReleaseManifest) error {
	if db == nil {
		return fmt.Errorf("schema state store is required")
	}
	checksum, err := release.Checksum()
	if err != nil {
		return fmt.Errorf("validate release manifest for schema promotion: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO schema_state
			(id, release_id, schema_version, baseline_version, release_manifest_checksum)
		VALUES (1, $1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			release_id = EXCLUDED.release_id,
			schema_version = EXCLUDED.schema_version,
			baseline_version = EXCLUDED.baseline_version,
			release_manifest_checksum = EXCLUDED.release_manifest_checksum,
			updated_at = CURRENT_TIMESTAMP
	`, release.ReleaseID, release.SchemaVersion, release.BaselineVersion, checksum)
	if err != nil {
		return fmt.Errorf("promote schema state: %w", err)
	}
	return nil
}

// VerifySchemaState requires an exact match to the supplied release identity.
func VerifySchemaState(state SchemaState, release ReleaseManifest) error {
	checksum, err := release.Checksum()
	if err != nil {
		return fmt.Errorf("validate required release manifest: %w", err)
	}
	if state.ID != 1 {
		return fmt.Errorf("schema state singleton mismatch")
	}
	if state.ReleaseID != release.ReleaseID {
		return fmt.Errorf("schema state release mismatch")
	}
	if state.SchemaVersion != release.SchemaVersion {
		return fmt.Errorf("schema state version mismatch")
	}
	if state.BaselineVersion != release.BaselineVersion {
		return fmt.Errorf("schema state baseline mismatch")
	}
	if state.ReleaseManifestChecksum != checksum {
		return fmt.Errorf("schema state manifest checksum mismatch")
	}
	return nil
}
