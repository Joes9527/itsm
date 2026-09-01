package migration

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingBootstrapMigrator struct {
	t         *testing.T
	events    *[]string
	ensureErr error
	runErr    error
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
	require.Equal(t, []string{"prepare", "schema", "ledger", "post-schema", "seed"}, events)
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
