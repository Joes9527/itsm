package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCurrentReleaseIncludesExactEmbeddedFreshBaselineAsset(t *testing.T) {
	release := CurrentRelease()
	wantChecksum := checksumSQL(CurrentBaselineSQL())
	var matches []ReleaseAsset
	for _, asset := range release.Assets {
		if asset.Name == CurrentBaselineAssetName {
			matches = append(matches, asset)
		}
	}
	require.Equal(t, []ReleaseAsset{{Name: CurrentBaselineAssetName, SHA256: wantChecksum}}, matches)
}

func TestCurrentReleaseIncludesExactEmbeddedSourceSchemaAsset(t *testing.T) {
	release := CurrentRelease()
	want := currentSourceSchemaAsset()
	var matches []ReleaseAsset
	for _, asset := range release.Assets {
		if asset.Name == CurrentSourceSchemaAssetName {
			matches = append(matches, asset)
		}
	}
	require.Equal(t, []ReleaseAsset{want}, matches)
}

func TestCurrentReleaseIncludesExactEmbeddedCatalogVerifierAsset(t *testing.T) {
	release := CurrentRelease()
	want := currentCatalogVerifierAsset()
	var matches []ReleaseAsset
	for _, asset := range release.Assets {
		if asset.Name == want.Name {
			matches = append(matches, asset)
		}
	}
	require.Equal(t, []ReleaseAsset{want}, matches)
}

func TestCatalogFingerprintVerifierIsOneReadOnlyStatement(t *testing.T) {
	normalized := strings.ToUpper(strings.TrimSpace(postgresCatalogFingerprintSQL))
	require.True(t, strings.HasPrefix(normalized, "WITH "))
	require.NotContains(t, normalized, ";")
	for _, mutation := range []string{
		" INSERT ", " UPDATE ", " DELETE ", " CREATE ", " ALTER ", " DROP ",
		" GRANT ", " REVOKE ", " TRUNCATE ", " CALL ",
	} {
		require.NotContains(t, normalized, mutation)
	}
}

func TestCatalogFingerprintVerifierCoversManagedSecurityBoundaries(t *testing.T) {
	for _, boundary := range []string{
		"'schema-security'",
		"'relation-security'",
		"'default-acl'",
		"'event-trigger'",
		"'publication'",
		"'publication-namespace'",
		"'schema-state-effective-writer-boundary'",
		"has_table_privilege",
		"pg_auth_members",
	} {
		require.Contains(t, postgresCatalogFingerprintSQL, boundary)
	}
}

func TestCurrentSourceSchemaAssetPinsExplicitExtensionInventory(t *testing.T) {
	asset, err := loadCurrentCatalogFingerprintAsset()
	require.NoError(t, err)
	require.Equal(t, []catalogExtensionIdentity{
		{Name: "plpgsql", Schema: "pg_catalog", Version: "1.0"},
	}, asset.Extensions.Empty)
	require.Equal(t, []catalogExtensionIdentity{
		{Name: "plpgsql", Schema: "pg_catalog", Version: "1.0"},
		{Name: "vector", Schema: "public", Version: "0.8.6"},
	}, asset.Extensions.Installed)
}

func TestReleasePlatformRequiresExactPostgresMajorAndVectorVersion(t *testing.T) {
	want := releasePlatformRequirement{PostgresMajor: 17, VectorVersion: "0.8.6"}
	require.NoError(t, validateReleasePlatform(platformSnapshot{
		PostgresMajor:           17,
		AvailableVectorVersions: []string{"0.8.5", "0.8.6"},
	}, want, false))
	require.NoError(t, validateReleasePlatform(platformSnapshot{
		PostgresMajor:           17,
		AvailableVectorVersions: []string{"0.8.6"},
		InstalledVectorVersion:  "0.8.6",
	}, want, true))

	for name, snapshot := range map[string]platformSnapshot{
		"server major": {
			PostgresMajor:           16,
			AvailableVectorVersions: []string{"0.8.6"},
		},
		"unavailable vector version": {
			PostgresMajor:           17,
			AvailableVectorVersions: []string{"0.8.5"},
		},
		"installed vector version": {
			PostgresMajor:           17,
			AvailableVectorVersions: []string{"0.8.6"},
			InstalledVectorVersion:  "0.8.5",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateReleasePlatform(snapshot, want, name == "installed vector version")
			require.ErrorContains(t, err, "unsupported PostgreSQL platform")
		})
	}
}

func TestCurrentBaselinePinsVectorExtensionVersion(t *testing.T) {
	parts, err := loadCurrentBaseline()
	require.NoError(t, err)
	require.Contains(t, parts.PrepareApply, "CREATE EXTENSION IF NOT EXISTS vector VERSION '0.8.6'")
}

func TestParseCurrentBaselineRequiresEveryApplyAndVerifySection(t *testing.T) {
	_, err := parseCurrentBaseline([]byte("-- +itsm prepare apply\nSELECT 1;"))
	require.ErrorContains(t, err, "baseline section")

	parts, err := parseCurrentBaseline([]byte(`
-- +itsm prepare apply
SELECT 'prepare apply';
-- +itsm prepare verify
SELECT 'prepare verify';
-- +itsm baseline apply
SELECT 'baseline apply';
-- +itsm baseline verify
SELECT 'baseline verify';
`))
	require.NoError(t, err)
	require.Equal(t, "SELECT 'prepare apply';", parts.PrepareApply)
	require.Equal(t, "SELECT 'prepare verify';", parts.PrepareVerify)
	require.Equal(t, "SELECT 'baseline apply';", parts.BaselineApply)
	require.Equal(t, "SELECT 'baseline verify';", parts.BaselineVerify)
}

func TestCurrentBaselineRequiresPinnedDatabaseBoundary(t *testing.T) {
	require.ErrorContains(t, PrepareCurrentInfrastructure(context.Background(), nil), "database")
	require.ErrorContains(t, ApplyCurrentBaseline(context.Background(), nil), "database")
}

func TestVerifyFreshMigrationHistoryRejectsForgedRows(t *testing.T) {
	db := openFreshHistoryTestDB(t, true, 1)
	err := VerifyFreshMigrationHistory(context.Background(), db)
	require.ErrorContains(t, err, "must not contain migration history")
}

func TestVerifyFreshMigrationHistoryAcceptsAbsentOrEmptyLedger(t *testing.T) {
	for _, test := range []struct {
		name   string
		exists bool
		rows   int64
	}{
		{name: "absent", exists: false},
		{name: "empty", exists: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openFreshHistoryTestDB(t, test.exists, test.rows)
			require.NoError(t, VerifyFreshMigrationHistory(context.Background(), db))
		})
	}
}

var freshHistoryDriverSequence atomic.Uint64

func openFreshHistoryTestDB(t *testing.T, relationExists bool, rowCount int64) *sql.DB {
	t.Helper()
	driverName := fmt.Sprintf("fresh_history_%d", freshHistoryDriverSequence.Add(1))
	sql.Register(driverName, &freshHistoryTestDriver{relationExists: relationExists, rowCount: rowCount})
	db, err := sql.Open(driverName, "")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

type freshHistoryTestDriver struct {
	relationExists bool
	rowCount       int64
}

func (d *freshHistoryTestDriver) Open(string) (driver.Conn, error) {
	return &freshHistoryTestConn{relationExists: d.relationExists, rowCount: d.rowCount}, nil
}

type freshHistoryTestConn struct {
	relationExists bool
	rowCount       int64
}

func (*freshHistoryTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is unsupported")
}
func (*freshHistoryTestConn) Close() error { return nil }
func (*freshHistoryTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transaction is unsupported")
}
func (c *freshHistoryTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "fresh_migration_history_relation"):
		return &freshHistoryTestRows{columns: []string{"relation_exists"}, values: []driver.Value{c.relationExists}}, nil
	case strings.Contains(query, "fresh_migration_history_rows"):
		return &freshHistoryTestRows{columns: []string{"row_count"}, values: []driver.Value{c.rowCount}}, nil
	default:
		return nil, errors.New("unexpected fresh history query")
	}
}

type freshHistoryTestRows struct {
	columns []string
	values  []driver.Value
	read    bool
}

func (r *freshHistoryTestRows) Columns() []string { return r.columns }
func (*freshHistoryTestRows) Close() error        { return nil }
func (r *freshHistoryTestRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	copy(values, r.values)
	r.read = true
	return nil
}

type failingBaselineVerifierDB struct {
	err error
}

func (db failingBaselineVerifierDB) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, db.err
}

func (failingBaselineVerifierDB) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func TestVerifyCurrentSchemaRunsCanonicalBaselineVerificationBeforeSchemaState(t *testing.T) {
	verificationErr := errors.New("wrong baseline object")
	err := VerifyCurrentSchema(context.Background(), failingBaselineVerifierDB{err: verificationErr}, CurrentRelease())
	require.ErrorIs(t, err, verificationErr)
	require.ErrorContains(t, err, "verify current baseline")
}

func TestVerifyCurrentSchemaRejectsReleaseWithoutCurrentBaseline(t *testing.T) {
	release := CurrentRelease()
	for index := range release.Assets {
		if release.Assets[index].Name == CurrentBaselineAssetName {
			release.Assets[index].SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}
	}
	err := VerifyCurrentSchema(context.Background(), nil, release)
	require.ErrorContains(t, err, "baseline asset")
}
