package migration

import (
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func TestServiceRequestAuthorityOperationalSQLMatchesStream(t *testing.T) {
	const version = "028_service_request_work_item_authority"
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
	verify, err := os.ReadFile("../migrations/" + version + "_verify.sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(serviceRequestWorkItemAuthorityVerifySQL), strings.TrimSpace(string(verify)))
	versions := []string{}
	for _, m := range RegisteredMigrations {
		versions = append(versions, m.Version)
	}
	require.Contains(t, versions, version)
}

func TestServiceRequestAuthorityAcceptsPreviouslyApplied028(t *testing.T) {
	ledger := []Migration{}
	for _, migration := range RegisteredMigrations {
		migration.Checksum = checksumSQL(GetMigrationSQL(migration.Version))
		if migration.Version == "028_service_request_work_item_authority" {
			// Actual retained pre-C4 deployment ledger, written before verifier hardening.
			migration.Checksum = "c145c16991841983599da34362f003d2db0a785ff5ed89918c6b7fb0b58571c5"
		}
		ledger = append(ledger, migration)
		if migration.Version == "028_service_request_work_item_authority" {
			break
		}
	}
	require.NoError(t, validateMigrationLedger(ledger))
}
