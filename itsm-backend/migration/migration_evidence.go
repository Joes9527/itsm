package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type MigrationTarget struct {
	DeploymentID string
	Database     string
	Schema       string
}
type MigrationEvidence struct {
	Target                  MigrationTarget
	CatalogRevision         string
	LedgerDigest            string
	ApplicationDigest       string
	InventoryDigest         string
	BackupDigest            string
	RestoreReportDigest     string
	JourneyReportDigest     string
	ObservationReportDigest string
	Operator                string
	ChangeRecord            string
}

// PreparationInventory is a read-only snapshot, not approval or a receipt.
type PreparationInventory struct {
	Target          MigrationTarget
	LedgerDigest    string
	InventoryDigest string
}

func evidenceDigest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return checksumSQL(string(b)), nil
}
func (m *Migrator) preparationTarget(ctx context.Context, q migrationQuery) (MigrationTarget, error) {
	if strings.TrimSpace(m.controlConfig.DeploymentID) == "" {
		return MigrationTarget{}, fmt.Errorf("trusted migration deployment identity is not configured")
	}
	schema, err := migrationTargetSchema(ctx, q)
	if err != nil {
		return MigrationTarget{}, err
	}
	target := MigrationTarget{DeploymentID: m.controlConfig.DeploymentID, Schema: schema}
	err = q.QueryRowContext(ctx, `SELECT current_database()`).Scan(&target.Database)
	return target, err
}
func validatePreparationEvidence(e MigrationEvidence, i PreparationInventory) error {
	if e.Target != i.Target {
		return fmt.Errorf("migration evidence target mismatch")
	}
	if e.CatalogRevision != ControlledCatalogRevision {
		return fmt.Errorf("migration evidence catalog revision mismatch")
	}
	if e.LedgerDigest != i.LedgerDigest || e.InventoryDigest != i.InventoryDigest {
		return fmt.Errorf("migration evidence ledger or inventory digest changed")
	}
	// P precedes current journeys and observation; those reports belong to the
	// later retirement evidence gate and may truthfully be absent here.
	for _, v := range []string{e.ApplicationDigest, e.BackupDigest, e.RestoreReportDigest, e.Operator, e.ChangeRecord, e.LedgerDigest, e.InventoryDigest} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("migration evidence contains an empty required field")
		}
	}
	return nil
}

// MigrationControlConfig is trusted operator configuration, separate from submitted
// evidence. Every nonowner table privilege must be explicitly reviewed.
type MigrationControlConfig struct {
	DeploymentID   string
	ReviewedGrants []MigrationRoleGrant
}
type MigrationRoleGrant struct {
	Role       string
	Table      string
	Privileges []string
}
