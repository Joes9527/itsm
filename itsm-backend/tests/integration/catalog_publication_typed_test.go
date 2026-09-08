package integration

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	catalogdomain "itsm-backend/handlers/service_catalog"
	"testing"
)

func TestCatalogPublicationTypedChangeInputs(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	logger := zap.NewNop().Sugar()
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(changedomain.NewService(nil, f.client, logger)))
	catalogs := catalogdomain.NewService(catalogdomain.NewEntRepository(f.client), f.client, logger, sameTransactionDirectory{})
	catalogs.SetCreatorRegistry(registry)
	catalog, err := catalogs.Create(ctx, f.identity.TenantID, dto.CreateServiceCatalogRequest{Name: "Maintenance", Category: "IT", TargetClass: "change_request", Status: "enabled"})
	require.NoError(t, err)
	require.Empty(t, catalog.Fields, "professional typed inputs do not require duplicate custom definitions")
	cmd := creation.CreateWorkItemCommand{IntakeKind: creation.IntakeKindCatalogItem, CatalogItemID: &catalog.ID, CatalogVersion: catalog.CatalogVersion, FormSchemaVersion: catalog.FormSchemaVersion, RecordClass: "change_request", Confirmation: "confirmed", Title: "Service maintenance", Description: "Improve reliability", Priority: "high", IdempotencyKey: "missing-change-input", Change: &creation.ChangeInput{}}
	_, err = f.app.Create(ctx, f.identity, cmd)
	require.ErrorContains(t, err, "change.justification is required")
	assertNoEntryGraph(t, f.client)
	require.Zero(t, f.client.Change.Query().CountX(ctx))
	cmd.IdempotencyKey = "complete-change-input"
	cmd.Change = &creation.ChangeInput{Type: "normal", Justification: "Improve reliability", RiskLevel: "low", ImpactScope: "medium", ImplementationPlan: "Apply configuration", RollbackPlan: "Restore configuration", PlannedStartDate: "2026-09-06T08:00:00+08:00", PlannedEndDate: "2026-09-06T09:00:00+08:00"}
	result, err := f.app.Create(ctx, f.identity, cmd)
	require.NoError(t, err)
	require.Equal(t, "change_request", result.RecordClass)
	require.Equal(t, cmd.Change.Justification, f.client.Change.GetX(ctx, result.ProfessionalReference.ID).Justification)
}
