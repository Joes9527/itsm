package migration

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

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
