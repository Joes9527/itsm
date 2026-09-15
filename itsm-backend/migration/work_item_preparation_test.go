package migration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreparationEvidenceBindsTargetAndInputs(t *testing.T) {
	inv := PreparationInventory{Target: MigrationTarget{"deployment", "database", "schema"}, LedgerDigest: "ledger", InventoryDigest: "inventory"}
	e := MigrationEvidence{Target: inv.Target, CatalogRevision: ControlledCatalogRevision, LedgerDigest: inv.LedgerDigest, InventoryDigest: inv.InventoryDigest, ApplicationDigest: "app", BackupDigest: "backup", RestoreReportDigest: "restore", JourneyReportDigest: "journey", ObservationReportDigest: "observation", Operator: "operator", ChangeRecord: "change"}
	e.JourneyReportDigest = ""
	e.ObservationReportDigest = ""
	require.NoError(t, validatePreparationEvidence(e, inv), "P must not require future journey/observation reports")
	for name, mutate := range map[string]func(*MigrationEvidence){
		"deployment":  func(v *MigrationEvidence) { v.Target.DeploymentID = "other" },
		"database":    func(v *MigrationEvidence) { v.Target.Database = "other" },
		"schema":      func(v *MigrationEvidence) { v.Target.Schema = "other" },
		"catalog":     func(v *MigrationEvidence) { v.CatalogRevision = "other" },
		"ledger":      func(v *MigrationEvidence) { v.LedgerDigest = "other" },
		"inventory":   func(v *MigrationEvidence) { v.InventoryDigest = "other" },
		"application": func(v *MigrationEvidence) { v.ApplicationDigest = "" },
		"backup":      func(v *MigrationEvidence) { v.BackupDigest = "" },
		"restore":     func(v *MigrationEvidence) { v.RestoreReportDigest = "" },
		"operator":    func(v *MigrationEvidence) { v.Operator = "" },
		"change":      func(v *MigrationEvidence) { v.ChangeRecord = "" },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := e
			mutate(&wrong)
			require.Error(t, validatePreparationEvidence(wrong, inv))
		})
	}
}

func TestPreparationSQLRetainsHistoryAndLaterStageBoundaries(t *testing.T) {
	sql := strings.ToUpper(GetMigrationSQL(WorkItemPrepareVersion))
	require.NotEmpty(t, sql)
	for _, forbidden := range []string{"DROP TABLE", "DROP COLUMN", "CASCADE", "INSERT INTO", "UPDATE ", "INVESTIGATION_COMPLETED_AT"} {
		require.NotContains(t, sql, forbidden)
	}
	require.Equal(t, buildWorkItemPreparationSQL(), GetMigrationSQL(WorkItemPrepareVersion))
}
