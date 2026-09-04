package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestVerifySchemaStateStorageAcceptsRequiredShapeAndSingleton(t *testing.T) {
	db := openSchemaStateInvariantTestDB(t, validSchemaStateInvariantFixture())
	require.NoError(t, VerifySchemaStateStorage(context.Background(), db))
}

func TestVerifySchemaStateStorageRejectsMalformedStorage(t *testing.T) {
	tests := map[string]func(*schemaStateInvariantFixture){
		"missing relation": func(fixture *schemaStateInvariantFixture) { fixture.catalog[0] = int64(0) },
		"extra relation":   func(fixture *schemaStateInvariantFixture) { fixture.catalog[0] = int64(2) },
		"extra column":     func(fixture *schemaStateInvariantFixture) { fixture.catalog[1] = int64(7) },
		"wrong column":     func(fixture *schemaStateInvariantFixture) { fixture.catalog[2] = int64(5) },
		"missing PK":       func(fixture *schemaStateInvariantFixture) { fixture.catalog[3] = int64(0) },
		"wrong PK":         func(fixture *schemaStateInvariantFixture) { fixture.catalog[3] = int64(2) },
		"missing ID check": func(fixture *schemaStateInvariantFixture) { fixture.catalog[4] = "(id > 0)" },
		"missing default":  func(fixture *schemaStateInvariantFixture) { fixture.catalog[5] = "" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := validSchemaStateInvariantFixture()
			mutate(&fixture)
			db := openSchemaStateInvariantTestDB(t, fixture)
			require.Error(t, VerifySchemaStateStorage(context.Background(), db))
		})
	}
}

func TestVerifySchemaStateStorageRejectsParentWithInheritingChild(t *testing.T) {
	fixture := validSchemaStateInvariantFixture()
	fixture.inheritingChildCount = 1
	fixture.hasSubclass = true
	db := openSchemaStateInvariantTestDB(t, fixture)

	require.ErrorContains(t, VerifySchemaStateStorage(context.Background(), db), "standalone relation")
}

func TestVerifySchemaStateStorageRejectsInheritedChild(t *testing.T) {
	fixture := validSchemaStateInvariantFixture()
	fixture.inheritanceParentCount = 1
	db := openSchemaStateInvariantTestDB(t, fixture)

	require.ErrorContains(t, VerifySchemaStateStorage(context.Background(), db), "standalone relation")
}

func TestVerifySchemaStateStorageRejectsInvalidOrMultipleRows(t *testing.T) {
	tests := map[string][]driver.Value{
		"multiple rows": {int64(2), int64(0)},
		"invalid ID":    {int64(1), int64(1)},
	}
	for name, rowFacts := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := validSchemaStateInvariantFixture()
			fixture.rows = rowFacts
			db := openSchemaStateInvariantTestDB(t, fixture)
			require.Error(t, VerifySchemaStateStorage(context.Background(), db))
		})
	}
}

func TestVerifySchemaStateStorageRejectsMissingStore(t *testing.T) {
	require.Error(t, VerifySchemaStateStorage(context.Background(), nil))
}

func TestSchemaReleaseMigrationCreatesEnforcedSingletonWithoutDCL(t *testing.T) {
	db := openSchemaStateTestDB(t)

	_, err := db.Exec(`
		INSERT INTO schema_state
			(id, release_id, schema_version, baseline_version, release_manifest_checksum)
		VALUES (2, 'release', 'schema', 'baseline', '` + strings.Repeat("a", 64) + `')
	`)
	require.Error(t, err)

	sqlText := strings.ToUpper(GetMigrationSQL("028_schema_release_state"))
	require.NotContains(t, sqlText, "GRANT ")
	require.NotContains(t, sqlText, "REVOKE ")
	require.NotContains(t, sqlText, "ITSM_")
}

func TestPromoteReadAndVerifySchemaState(t *testing.T) {
	db := openSchemaStateTestDB(t)
	release := CurrentRelease()

	require.NoError(t, PromoteSchemaState(context.Background(), db, release))
	state, err := ReadSchemaState(context.Background(), db)
	require.NoError(t, err)
	require.Equal(t, int16(1), state.ID)
	require.False(t, state.UpdatedAt.IsZero())
	require.NoError(t, VerifySchemaState(state, release))

	firstUpdatedAt := state.UpdatedAt
	time.Sleep(time.Millisecond)
	require.NoError(t, PromoteSchemaState(context.Background(), db, release))
	state, err = ReadSchemaState(context.Background(), db)
	require.NoError(t, err)
	require.False(t, state.UpdatedAt.Before(firstUpdatedAt))

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM schema_state`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestVerifySchemaStateRejectsEveryIdentityMismatch(t *testing.T) {
	release := CurrentRelease()
	checksum, err := release.Checksum()
	require.NoError(t, err)
	valid := SchemaState{
		ID:                      1,
		ReleaseID:               release.ReleaseID,
		SchemaVersion:           release.SchemaVersion,
		BaselineVersion:         release.BaselineVersion,
		ReleaseManifestChecksum: checksum,
		UpdatedAt:               time.Now().UTC(),
	}

	tests := map[string]SchemaState{
		"singleton id": func() SchemaState { state := valid; state.ID = 2; return state }(),
		"release":      func() SchemaState { state := valid; state.ReleaseID = "wrong"; return state }(),
		"schema":       func() SchemaState { state := valid; state.SchemaVersion = "wrong"; return state }(),
		"baseline":     func() SchemaState { state := valid; state.BaselineVersion = "wrong"; return state }(),
		"checksum": func() SchemaState {
			state := valid
			state.ReleaseManifestChecksum = strings.Repeat("0", 64)
			return state
		}(),
	}
	for name, state := range tests {
		t.Run(name, func(t *testing.T) {
			require.Error(t, VerifySchemaState(state, release))
		})
	}
}

func openSchemaStateTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NotEmpty(t, GetMigrationSQL("028_schema_release_state"))
	_, err = db.Exec(GetMigrationSQL("028_schema_release_state"))
	require.NoError(t, err)
	return db
}

type schemaStateInvariantFixture struct {
	catalog                []driver.Value
	rows                   []driver.Value
	inheritanceParentCount int64
	inheritingChildCount   int64
	hasSubclass            bool
}

func validSchemaStateInvariantFixture() schemaStateInvariantFixture {
	return schemaStateInvariantFixture{
		catalog: []driver.Value{
			int64(1),
			int64(6),
			int64(6),
			int64(1),
			"(id = 1)",
			"CURRENT_TIMESTAMP",
		},
		rows: []driver.Value{int64(1), int64(0)},
	}
}

var schemaStateInvariantDriverSequence atomic.Uint64

func openSchemaStateInvariantTestDB(t *testing.T, fixture schemaStateInvariantFixture) *sql.DB {
	t.Helper()
	driverName := fmt.Sprintf("schema_state_invariant_%d", schemaStateInvariantDriverSequence.Add(1))
	sql.Register(driverName, &schemaStateInvariantDriver{fixture: fixture})
	db, err := sql.Open(driverName, "")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

type schemaStateInvariantDriver struct {
	fixture schemaStateInvariantFixture
}

func (d *schemaStateInvariantDriver) Open(string) (driver.Conn, error) {
	return &schemaStateInvariantConn{fixture: d.fixture}, nil
}

type schemaStateInvariantConn struct {
	fixture schemaStateInvariantFixture
}

func (c *schemaStateInvariantConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("prepare is unsupported")
}
func (c *schemaStateInvariantConn) Close() error { return nil }
func (c *schemaStateInvariantConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("transaction is unsupported")
}
func (c *schemaStateInvariantConn) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "schema_state_storage_catalog"):
		columns := []string{"relation_count", "column_count", "matching_column_count", "primary_key_count", "check_expressions", "updated_at_default"}
		values := append([]driver.Value(nil), c.fixture.catalog...)
		if strings.Contains(query, "pg_inherits") {
			columns = append(columns, "inheritance_parent_count", "inheriting_child_count", "has_subclass")
			values = append(values, c.fixture.inheritanceParentCount, c.fixture.inheritingChildCount, c.fixture.hasSubclass)
		}
		return &schemaStateInvariantRows{
			columns: columns,
			values:  values,
		}, nil
	case strings.Contains(query, "schema_state_storage_rows"):
		return &schemaStateInvariantRows{
			columns: []string{"row_count", "invalid_id_count"},
			values:  c.fixture.rows,
		}, nil
	default:
		return nil, fmt.Errorf("unexpected schema state invariant query")
	}
}

type schemaStateInvariantRows struct {
	columns []string
	values  []driver.Value
	read    bool
}

func (r *schemaStateInvariantRows) Columns() []string { return r.columns }
func (r *schemaStateInvariantRows) Close() error      { return nil }
func (r *schemaStateInvariantRows) Next(destination []driver.Value) error {
	if r.read {
		return io.EOF
	}
	copy(destination, r.values)
	r.read = true
	return nil
}
