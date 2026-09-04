package migration

import (
	"context"
	"fmt"
	"strings"

	entmigrate "itsm-backend/ent/migrate"

	"entgo.io/ent/dialect"
	entschema "entgo.io/ent/dialect/sql/schema"
	"entgo.io/ent/schema/field"
	"github.com/lib/pq"
)

// VerifyCurrentSchema checks the embedded current-baseline definitions, the
// compiled Ent table/column contract, and the complete schema_state storage
// contract. Both fresh and upgrade paths call this exact verifier immediately
// before privilege provisioning and promotion.
func VerifyCurrentSchema(ctx context.Context, db DBTX, release ReleaseManifest) error {
	if err := verifyCurrentReleaseBaseline(release); err != nil {
		return err
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
	return nil
}

func verifyCurrentReleaseBaseline(release ReleaseManifest) error {
	if _, err := release.Checksum(); err != nil {
		return fmt.Errorf("validate current release manifest: %w", err)
	}
	expected := CurrentRelease()
	expectedChecksum, err := expected.Checksum()
	if err != nil {
		return fmt.Errorf("build current release manifest: %w", err)
	}
	actualChecksum, err := release.Checksum()
	if err != nil {
		return fmt.Errorf("validate current release manifest: %w", err)
	}
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("current release baseline asset or manifest identity mismatch")
	}
	return nil
}

func verifyCurrentEntSchema(ctx context.Context, db DBTX) error {
	tableNames := make([]string, 0, len(entmigrate.Tables))
	columnTables := make([]string, 0)
	columnNames := make([]string, 0)
	columnTypes := make([]string, 0)
	columnNotNull := make([]bool, 0)
	columnIdentity := make([]string, 0)
	for _, table := range entmigrate.Tables {
		if table == nil {
			return fmt.Errorf("verify current Ent schema: compiled table is nil")
		}
		tableNames = append(tableNames, table.Name)
		for _, column := range table.Columns {
			if column == nil {
				return fmt.Errorf("verify current Ent schema: compiled column is nil")
			}
			columnTables = append(columnTables, table.Name)
			columnNames = append(columnNames, column.Name)
			columnType, err := currentPostgresColumnType(column)
			if err != nil {
				return fmt.Errorf("verify current Ent schema: %w", err)
			}
			columnTypes = append(columnTypes, columnType)
			columnNotNull = append(columnNotNull, !column.Nullable)
			identity := ""
			if column.Increment && column.Default == nil && !strings.Contains(columnType, "serial") {
				identity = "d"
			}
			columnIdentity = append(columnIdentity, identity)
		}
	}

	var expectedTables, matchingTables, expectedColumns, matchingColumns, actualColumns int64
	if err := db.QueryRowContext(ctx, `
		/* current_ent_schema_catalog */
		WITH expected_tables(table_name) AS (
			SELECT unnest($1::text[])
		), expected_columns(table_name, column_name, formatted_type, not_null, identity_kind) AS (
			SELECT * FROM unnest($2::text[], $3::text[], $4::text[], $5::boolean[], $6::text[])
		), actual_tables AS (
			SELECT relation.relname AS table_name, relation.oid
			FROM pg_class relation
			JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = current_schema()
			  AND relation.relkind IN ('r', 'p')
		), actual_columns AS (
			SELECT relation.relname AS table_name, attribute.attname AS column_name,
			       format_type(attribute.atttypid, attribute.atttypmod) AS formatted_type,
			       attribute.attnotnull AS not_null, attribute.attidentity::text AS identity_kind
			FROM pg_attribute attribute
			JOIN pg_class relation ON relation.oid = attribute.attrelid
			JOIN expected_tables expected ON expected.table_name = relation.relname
			JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
			WHERE namespace.nspname = current_schema()
			  AND relation.relkind IN ('r', 'p')
			  AND attribute.attnum > 0
			  AND NOT attribute.attisdropped
		)
		SELECT
			(SELECT COUNT(*) FROM expected_tables),
			(SELECT COUNT(*) FROM expected_tables expected JOIN actual_tables actual USING (table_name)),
			(SELECT COUNT(*) FROM expected_columns),
			(SELECT COUNT(*) FROM expected_columns expected JOIN actual_columns actual
			   ON actual.table_name = expected.table_name
			  AND actual.column_name = expected.column_name
			  AND actual.formatted_type = expected.formatted_type
			  AND actual.not_null = expected.not_null
			  AND actual.identity_kind = expected.identity_kind),
			(SELECT COUNT(*) FROM actual_columns)
	`, pq.Array(tableNames), pq.Array(columnTables), pq.Array(columnNames), pq.Array(columnTypes),
		pq.Array(columnNotNull), pq.Array(columnIdentity)).Scan(
		&expectedTables, &matchingTables, &expectedColumns, &matchingColumns, &actualColumns,
	); err != nil {
		return fmt.Errorf("verify current Ent schema catalog: %w", err)
	}
	if expectedTables == 0 || matchingTables != expectedTables {
		return fmt.Errorf("current Ent schema table invariant failed")
	}
	if expectedColumns == 0 || matchingColumns != expectedColumns || actualColumns != expectedColumns {
		return fmt.Errorf("current Ent schema column invariant failed")
	}
	return nil
}

func currentPostgresColumnType(column *entschema.Column) (string, error) {
	if override := strings.ToLower(strings.TrimSpace(column.SchemaType[dialect.Postgres])); override != "" {
		switch override {
		case "timestamptz":
			return "timestamp with time zone", nil
		case "timestamp":
			return "timestamp without time zone", nil
		case "varchar":
			return "character varying", nil
		case "int8", "bigserial":
			return "bigint", nil
		case "int4", "serial":
			return "integer", nil
		case "float8":
			return "double precision", nil
		default:
			return override, nil
		}
	}
	switch column.Type {
	case field.TypeBool:
		return "boolean", nil
	case field.TypeInt8, field.TypeUint8, field.TypeInt16:
		return "smallint", nil
	case field.TypeUint16, field.TypeInt32:
		return "integer", nil
	case field.TypeUint32, field.TypeInt, field.TypeUint, field.TypeInt64, field.TypeUint64:
		return "bigint", nil
	case field.TypeFloat32:
		return "real", nil
	case field.TypeFloat64:
		return "double precision", nil
	case field.TypeBytes:
		return "bytea", nil
	case field.TypeUUID:
		return "uuid", nil
	case field.TypeJSON:
		return "jsonb", nil
	case field.TypeString:
		if column.Size > 10<<20 {
			return "text", nil
		}
		return "character varying", nil
	case field.TypeEnum:
		return "character varying", nil
	case field.TypeTime:
		return "timestamp with time zone", nil
	default:
		return "", fmt.Errorf("unsupported compiled type %q for %s", column.Type, column.Name)
	}
}
