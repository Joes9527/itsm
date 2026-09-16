package router

import (
	"context"
	"database/sql"
	"fmt"

	"itsm-backend/migration"
	"itsm-backend/pkg/seeder"

	"github.com/lib/pq"
)

type initializationReadiness struct {
	Ready                   bool   `json:"ready"`
	SchemaVersion           string `json:"schemaVersion"`
	RequiredSchemaVersion   string `json:"requiredSchemaVersion"`
	BaselineVersion         string `json:"baselineVersion"`
	RequiredBaselineVersion string `json:"requiredBaselineVersion"`
	Reason                  string `json:"reason,omitempty"`
}

func checkInitializationReadiness(ctx context.Context, db *sql.DB, inspectSchema func(context.Context) error) initializationReadiness {
	var requiredSchemaVersion string
	for _, entry := range migration.ControlledMigrationCatalog() {
		if entry.Stage != migration.StageRetire {
			requiredSchemaVersion = entry.Migration.Version
		}
	}
	result := initializationReadiness{
		RequiredSchemaVersion:   requiredSchemaVersion,
		RequiredBaselineVersion: seeder.CurrentTenantTemplateVersion,
	}
	if db == nil {
		result.Reason = "database connection unavailable"
		return result
	}
	if inspectSchema == nil {
		result.Reason = "canonical schema inspection unavailable"
		return result
	}
	if err := inspectSchema(ctx); err != nil {
		result.Reason = "canonical schema admission failed"
		return result
	}
	result.SchemaVersion = requiredSchemaVersion
	var readyComponents int
	componentNames := seeder.ProductionComponentNames
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MIN(installed_version), '')
		FROM initialization_installations
		WHERE scope_type = 'platform' AND scope_id = 0
		  AND component = ANY($1)
		  AND status = 'succeeded'
		  AND installed_version = $2
	`, pq.Array(componentNames), result.RequiredBaselineVersion).Scan(
		&readyComponents,
		&result.BaselineVersion,
	); err != nil {
		result.Reason = fmt.Sprintf("platform baseline missing: %v", err)
		return result
	}
	if readyComponents != len(componentNames) {
		result.Reason = fmt.Sprintf(
			"platform baseline components ready: %d/%d",
			readyComponents,
			len(componentNames),
		)
		return result
	}
	if result.BaselineVersion != result.RequiredBaselineVersion {
		result.Reason = "platform baseline version is incompatible"
		return result
	}
	result.Ready = true
	return result
}
