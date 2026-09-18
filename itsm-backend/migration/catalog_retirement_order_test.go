package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActiveCatalogAcceptsDeferredRetirementAfterNewOrdinaryMigrations(t *testing.T) {
	require.NoError(t, validateMigrationCatalog(RegisteredMigrations, LegacyMigrations, GetMigrationSQL))
}

func TestActiveCatalogStillRejectsEarlyRetirementAndOrdinaryReordering(t *testing.T) {
	t.Run("retirement before ordinary migration", func(t *testing.T) {
		active := append([]Migration(nil), RegisteredMigrations...)
		last := len(active) - 1
		require.Equal(t, WorkItemRetireVersion, active[last].Version)
		active[last], active[last-1] = active[last-1], active[last]
		require.ErrorContains(t, validateMigrationCatalog(active, LegacyMigrations, GetMigrationSQL), "ordered")
	})
	t.Run("ordinary order remains strict", func(t *testing.T) {
		active := append([]Migration(nil), RegisteredMigrations...)
		last := len(active) - 1
		active[last-1], active[last-2] = active[last-2], active[last-1]
		require.ErrorContains(t, validateMigrationCatalog(active, LegacyMigrations, GetMigrationSQL), "ordered")
	})
}
