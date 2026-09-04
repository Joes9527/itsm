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

type recordingBootstrapLock struct {
	events *[]string
	conn   BootstrapConnection
	err    error
}

func (l recordingBootstrapLock) WithLock(ctx context.Context, run func(BootstrapConnection) error) error {
	*l.events = append(*l.events, "lock")
	if l.err != nil {
		return l.err
	}
	return run(l.conn)
}

type traceBootstrapConnection struct{}

func (*traceBootstrapConnection) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("unexpected trace ExecContext call")
}

func (*traceBootstrapConnection) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected trace QueryContext call")
}

func (*traceBootstrapConnection) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func (*traceBootstrapConnection) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, errors.New("unexpected trace BeginTx call")
}

func TestRunFreshBootstrapUsesExactReleaseTraceOnOneLockedConnection(t *testing.T) {
	var events []string
	conn := &traceBootstrapConnection{}
	step := func(name string) BootstrapStep {
		return func(_ context.Context, got BootstrapConnection) error {
			require.Same(t, conn, got)
			events = append(events, name)
			return nil
		}
	}
	verify := func(_ context.Context, got DBTX, release ReleaseManifest) error {
		require.Same(t, conn, got)
		require.Equal(t, CurrentRelease(), release)
		events = append(events, "invariants")
		return nil
	}
	privileges := func(_ context.Context, got BootstrapConnection, roles SchemaStateRoles) error {
		require.Same(t, conn, got)
		require.Equal(t, SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"}, roles)
		events = append(events, "provision-schema-state-privileges")
		return nil
	}
	promote := func(_ context.Context, got DBTX, release ReleaseManifest) error {
		require.Same(t, conn, got)
		require.Equal(t, CurrentRelease(), release)
		events = append(events, "promote-state")
		return nil
	}

	err := RunFreshBootstrap(context.Background(), FreshBootstrap{
		Lock:            recordingBootstrapLock{events: &events, conn: conn},
		Prepare:         step("prepare"),
		CreateSchema:    step("ent-schema"),
		ApplyBaseline:   step("baseline"),
		VerifySchema:    verify,
		ApplyPrivileges: privileges,
		PromoteState:    promote,
		Seed:            step("seed"),
		Release:         CurrentRelease(),
		Roles:           SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{
		"lock",
		"prepare",
		"ent-schema",
		"baseline",
		"invariants",
		"provision-schema-state-privileges",
		"promote-state",
		"seed",
	}, events)
}

func TestRunUpgradeUsesExactTraceAndSkipsOnlyVerifiedCommittedMigrations(t *testing.T) {
	tests := []struct {
		name    string
		pending []Migration
		want    []string
	}{
		{
			name:    "upgrade",
			pending: []Migration{{Version: "028_schema_release_state"}},
			want: []string{
				"lock", "validate-lineage", "forward-migrations", "invariants",
				"provision-schema-state-privileges", "promote-state",
			},
		},
		{
			name: "restart after committed forward migration",
			want: []string{
				"lock", "validate-history", "invariants",
				"provision-schema-state-privileges", "promote-state",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var events []string
			conn := &traceBootstrapConnection{}
			validationEvent := "validate-lineage"
			if len(test.pending) == 0 {
				validationEvent = "validate-history"
			}
			err := RunUpgrade(context.Background(), UpgradeBootstrap{
				Lock: recordingBootstrapLock{events: &events, conn: conn},
				PlanForwardMigrations: func(_ context.Context, got BootstrapConnection) ([]Migration, error) {
					require.Same(t, conn, got)
					events = append(events, validationEvent)
					return test.pending, nil
				},
				ApplyForwardMigrations: func(_ context.Context, got BootstrapConnection, pending []Migration) error {
					require.Same(t, conn, got)
					require.Equal(t, test.pending, pending)
					events = append(events, "forward-migrations")
					return nil
				},
				VerifySchema: func(_ context.Context, got DBTX, release ReleaseManifest) error {
					require.Same(t, conn, got)
					require.Equal(t, CurrentRelease(), release)
					events = append(events, "invariants")
					return nil
				},
				ApplyPrivileges: func(_ context.Context, got BootstrapConnection, _ SchemaStateRoles) error {
					require.Same(t, conn, got)
					events = append(events, "provision-schema-state-privileges")
					return nil
				},
				PromoteState: func(_ context.Context, got DBTX, _ ReleaseManifest) error {
					require.Same(t, conn, got)
					events = append(events, "promote-state")
					return nil
				},
				Release: CurrentRelease(),
				Roles:   SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"},
			})
			require.NoError(t, err)
			require.Equal(t, test.want, events)
		})
	}
}

func TestRunFreshBootstrapStopsBeforePromotionAndSeedOnInvariantFailure(t *testing.T) {
	var events []string
	conn := &traceBootstrapConnection{}
	step := func(name string) BootstrapStep {
		return func(context.Context, BootstrapConnection) error {
			events = append(events, name)
			return nil
		}
	}
	err := RunFreshBootstrap(context.Background(), FreshBootstrap{
		Lock:          recordingBootstrapLock{events: &events, conn: conn},
		Prepare:       step("prepare"),
		CreateSchema:  step("ent-schema"),
		ApplyBaseline: step("baseline"),
		VerifySchema: func(context.Context, DBTX, ReleaseManifest) error {
			events = append(events, "invariants")
			return errors.New("wrong current definition")
		},
		ApplyPrivileges: func(context.Context, BootstrapConnection, SchemaStateRoles) error {
			events = append(events, "privileges")
			return nil
		},
		PromoteState: func(context.Context, DBTX, ReleaseManifest) error {
			events = append(events, "promote")
			return nil
		},
		Seed:    step("seed"),
		Release: CurrentRelease(),
		Roles:   SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"},
	})
	require.ErrorContains(t, err, "verify current schema")
	require.Equal(t, []string{"lock", "prepare", "ent-schema", "baseline", "invariants"}, events)
}

func TestRunUpgradeStopsBeforePromotionWhenPrivilegeProvisioningFails(t *testing.T) {
	var events []string
	conn := &traceBootstrapConnection{}
	privilegeErr := errors.New("controlled privilege failure")
	err := RunUpgrade(context.Background(), UpgradeBootstrap{
		Lock: recordingBootstrapLock{events: &events, conn: conn},
		PlanForwardMigrations: func(context.Context, BootstrapConnection) ([]Migration, error) {
			events = append(events, "validate-history")
			return nil, nil
		},
		ApplyForwardMigrations: func(context.Context, BootstrapConnection, []Migration) error {
			return nil
		},
		VerifySchema: func(context.Context, DBTX, ReleaseManifest) error {
			events = append(events, "invariants")
			return nil
		},
		ApplyPrivileges: func(context.Context, BootstrapConnection, SchemaStateRoles) error {
			events = append(events, "privileges")
			return privilegeErr
		},
		PromoteState: func(context.Context, DBTX, ReleaseManifest) error {
			events = append(events, "promote")
			return nil
		},
		Release: CurrentRelease(),
		Roles:   SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"},
	})
	require.ErrorIs(t, err, privilegeErr)
	require.Equal(t, []string{"lock", "validate-history", "invariants", "privileges"}, events)
}

func TestRunBootstrapsRequireConcreteDependenciesBeforeTakingLock(t *testing.T) {
	var events []string
	err := RunFreshBootstrap(context.Background(), FreshBootstrap{
		Lock: recordingBootstrapLock{events: &events, conn: &traceBootstrapConnection{}},
	})
	require.ErrorContains(t, err, "dependencies")
	require.Empty(t, events)

	err = RunUpgrade(context.Background(), UpgradeBootstrap{
		Lock: recordingBootstrapLock{events: &events, conn: &traceBootstrapConnection{}},
	})
	require.ErrorContains(t, err, "dependencies")
	require.Empty(t, events)
}

type recordingPostSchemaMigrator struct {
	ensureErr error
	runErr    error
	ensured   bool
	received  []Migration
}

func (m *recordingPostSchemaMigrator) EnsureMigrationsTable(context.Context) error {
	m.ensured = true
	return m.ensureErr
}

func (m *recordingPostSchemaMigrator) RunMigrations(_ context.Context, migrations []Migration) (int, error) {
	m.received = append([]Migration(nil), migrations...)
	return len(migrations), m.runErr
}

func TestRunPostSchemaMigrationsRetainsForwardStreamPrimitive(t *testing.T) {
	runner := &recordingPostSchemaMigrator{}
	require.NoError(t, RunPostSchemaMigrations(context.Background(), runner))
	require.True(t, runner.ensured)
	require.Equal(t, PostSchemaMigrations(), runner.received)
}

func TestRunPostSchemaMigrationsFailsClosed(t *testing.T) {
	t.Run("ledger", func(t *testing.T) {
		runner := &recordingPostSchemaMigrator{ensureErr: errors.New("ledger unavailable")}
		err := RunPostSchemaMigrations(context.Background(), runner)
		require.ErrorContains(t, err, "ensure migration ledger")
		require.Empty(t, runner.received)
	})
	t.Run("forward migration", func(t *testing.T) {
		runner := &recordingPostSchemaMigrator{runErr: errors.New("forward migration failed")}
		err := RunPostSchemaMigrations(context.Background(), runner)
		require.ErrorContains(t, err, "run post-schema migrations")
	})
}

func TestPostgresAdvisoryLockPinsOneConnectionAndAlwaysUnlocks(t *testing.T) {
	db, events := openAdvisoryLockTestDB(t, true)

	locker, err := NewPostgresAdvisoryLock(db)
	require.NoError(t, err)
	err = locker.WithLock(context.Background(), func(conn BootstrapConnection) error {
		_, ok := conn.(*sql.Conn)
		require.True(t, ok, "production lock must expose the pinned database/sql connection")
		_, err := conn.ExecContext(context.Background(), "SELECT 1")
		return err
	})
	require.NoError(t, err)
	require.Equal(t, []string{"lock", "phase", "unlock"}, *events)
}

func TestPostgresAdvisoryLockReleasesAfterPhaseFailure(t *testing.T) {
	db, events := openAdvisoryLockTestDB(t, true)

	locker, err := NewPostgresAdvisoryLock(db)
	require.NoError(t, err)
	phaseErr := errors.New("phase failed")
	err = locker.WithLock(context.Background(), func(BootstrapConnection) error { return phaseErr })
	require.ErrorIs(t, err, phaseErr)
	require.Equal(t, []string{"lock", "unlock"}, *events)
}

var advisoryLockDriverSequence atomic.Uint64

func openAdvisoryLockTestDB(t *testing.T, unlockResult bool) (*sql.DB, *[]string) {
	t.Helper()
	events := &[]string{}
	driverName := fmt.Sprintf("bootstrap_advisory_lock_%d", advisoryLockDriverSequence.Add(1))
	sql.Register(driverName, &advisoryLockTestDriver{events: events, unlockResult: unlockResult})
	db, err := sql.Open(driverName, "")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, events
}

type advisoryLockTestDriver struct {
	events       *[]string
	unlockResult bool
}

func (d *advisoryLockTestDriver) Open(string) (driver.Conn, error) {
	return &advisoryLockTestConn{events: d.events, unlockResult: d.unlockResult}, nil
}

type advisoryLockTestConn struct {
	events       *[]string
	unlockResult bool
}

func (*advisoryLockTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is unsupported")
}
func (*advisoryLockTestConn) Close() error { return nil }
func (*advisoryLockTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transaction is unsupported")
}
func (c *advisoryLockTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	switch {
	case strings.Contains(query, "pg_advisory_lock"):
		if len(args) != 1 || args[0].Value != bootstrapAdvisoryLockKey {
			return nil, errors.New("wrong advisory lock key")
		}
		*c.events = append(*c.events, "lock")
	case strings.Contains(query, "SELECT 1"):
		*c.events = append(*c.events, "phase")
	default:
		return nil, errors.New("unexpected advisory lock test exec")
	}
	return driver.RowsAffected(1), nil
}
func (c *advisoryLockTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(query, "pg_advisory_unlock") {
		return nil, errors.New("unexpected advisory lock test query")
	}
	if len(args) != 1 || args[0].Value != bootstrapAdvisoryLockKey {
		return nil, errors.New("wrong advisory unlock key")
	}
	*c.events = append(*c.events, "unlock")
	return &advisoryLockTestRows{value: c.unlockResult}, nil
}

type advisoryLockTestRows struct {
	value bool
	read  bool
}

func (*advisoryLockTestRows) Columns() []string { return []string{"unlocked"} }
func (*advisoryLockTestRows) Close() error      { return nil }
func (r *advisoryLockTestRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	values[0] = r.value
	r.read = true
	return nil
}
