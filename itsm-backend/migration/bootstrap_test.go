package migration

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingBootstrapMigrator struct {
	t             *testing.T
	events        *[]string
	inspectErr    error
	ensureErr     error
	runErr        error
	invariantsErr error
}

func (m *recordingBootstrapMigrator) EnsureMigrationsTable(context.Context) error {
	*m.events = append(*m.events, "ledger")
	return m.ensureErr
}

func (m *recordingBootstrapMigrator) RunMigrations(_ context.Context, migrations []Migration) (int, error) {
	*m.events = append(*m.events, "post-schema")
	if m.runErr != nil {
		return 0, m.runErr
	}
	require.Equal(m.t, PostSchemaMigrations(), migrations)
	return len(migrations), nil
}

func TestRunCanonicalBootstrapOrdersEveryPhase(t *testing.T) {
	var events []string
	runner := &recordingBootstrapMigrator{t: t, events: &events}

	err := RunCanonicalBootstrap(context.Background(), CanonicalBootstrap{
		Prepare: func(context.Context) error {
			events = append(events, "prepare")
			return nil
		},
		CreateSchema: func(context.Context) error {
			events = append(events, "schema")
			return nil
		},
		Migrator: runner,
		Seed: func(context.Context) error {
			events = append(events, "seed")
			return nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"inspect", "ledger", "prepare", "schema", "inspect", "ledger", "post-schema", "invariants", "seed"}, events)
}

func TestRunCanonicalBootstrapFailsClosedBeforePostSchemaWithoutSchema(t *testing.T) {
	var events []string
	runner := &recordingBootstrapMigrator{t: t, events: &events}
	err := RunCanonicalBootstrap(context.Background(), CanonicalBootstrap{Migrator: runner})
	require.ErrorContains(t, err, "schema creator is required")
	require.Empty(t, events)
}

func TestRunCanonicalBootstrapDoesNotSeedAfterPostSchemaFailure(t *testing.T) {
	var events []string
	runner := &recordingBootstrapMigrator{t: t, events: &events, runErr: errors.New("post-schema failed")}
	seeded := false
	err := RunCanonicalBootstrap(context.Background(), CanonicalBootstrap{
		CreateSchema: func(context.Context) error { return nil },
		Migrator:     runner,
		Seed: func(context.Context) error {
			seeded = true
			return nil
		},
	})
	require.ErrorContains(t, err, "run post-schema migrations")
	require.False(t, seeded)
}

func (m *recordingBootstrapMigrator) ReconcileSchemaInvariants(context.Context) error {
	*m.events = append(*m.events, "invariants")
	return m.invariantsErr
}

func TestRunCanonicalBootstrapDoesNotSeedAfterInvariantFailure(t *testing.T) {
	var events []string
	runner := &recordingBootstrapMigrator{t: t, events: &events, invariantsErr: errors.New("invalid existing access policy")}
	seeded := false
	err := RunCanonicalBootstrap(context.Background(), CanonicalBootstrap{
		CreateSchema: func(context.Context) error { return nil },
		Migrator:     runner,
		Seed:         func(context.Context) error { seeded = true; return nil },
	})
	require.ErrorContains(t, err, "reconcile schema invariants")
	require.False(t, seeded)
}

func (m *recordingBootstrapMigrator) InspectMigrationTarget(context.Context) error {
	*m.events = append(*m.events, "inspect")
	return m.inspectErr
}
func (m *recordingBootstrapMigrator) WithMigrationLock(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func TestControlledBootstrapRejectsBeforeEveryWrite(t *testing.T) {
	for _, reason := range []string{"invalid ledger", "existing target missing ledger"} {
		t.Run(reason, func(t *testing.T) {
			var events []string
			runner := &recordingBootstrapMigrator{t: t, events: &events, inspectErr: errors.New(reason)}
			write := func(context.Context) error { events = append(events, "WRITE"); return nil }
			err := RunCanonicalBootstrap(context.Background(), CanonicalBootstrap{Prepare: write, CreateSchema: write, Migrator: runner, Seed: write})
			require.ErrorContains(t, err, reason)
			require.Equal(t, []string{"inspect"}, events)
		})
	}
}
