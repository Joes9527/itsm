package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/controller"
	"itsm-backend/handlers/intake"
	catalogdomain "itsm-backend/handlers/service_catalog"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
)

func TestIntakeIncidentConfiguredMatrixAndReplay(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	restrictEntryPermissions(t, f)
	ctx := context.Background()
	logger := zap.NewNop().Sugar()
	owner := service.NewIncidentService(f.client, logger)
	matrix := service.NewPriorityMatrixService(logger)
	require.NoError(t, matrix.SetMatrix(f.identity.TenantID, service.PriorityMatrix{"medium": {"medium": "critical"}}))
	owner.SetPriorityMatrixService(matrix)
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(owner))
	resolver := intake.NewResolver(catalogdomain.NewService(nil, f.client, logger, nil), service.NewProcessBindingService(f.client), service.NewConfigurationItemService(f.client, logger, nil, nil), service.NewTicketCategoryService(f.client))
	f.app = intake.NewService(f.client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), sameTransactionDirectory{})
	h := controller.NewIncidentController(owner, owner.RuleEngine(), nil, nil, nil, logger)
	h.SetCreationApplication(f.app)
	body := `{"title":"Matrix decision","description":"Configured incident priority","impact":"medium","urgency":"medium"}`
	w, first := intakeHTTP(t, f, h.CreateIncident, body, "matrix", nil)
	require.Equal(t, 201, w.Code, w.Body.String())
	item := f.client.Ticket.GetX(ctx, first.WorkItemID)
	require.Equal(t, "critical", item.Priority)
	require.Equal(t, f.identity.TenantID, item.TenantID)
	require.Equal(t, f.identity.ActorID, item.OpenedByID)
	detail, err := owner.GetIncident(ctx, first.ProfessionalReference.ID, f.identity.TenantID)
	require.NoError(t, err)
	require.Equal(t, "critical", detail.Priority)
	receipt := f.client.IntakeRequest.Query().OnlyX(ctx)
	snapshot := f.client.IntakeResolutionSnapshot.Query().OnlyX(ctx)
	require.NoError(t, matrix.SetMatrix(f.identity.TenantID, service.PriorityMatrix{"medium": {"medium": "low"}}))
	w, replay := intakeHTTP(t, f, h.CreateIncident, body, "matrix", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.True(t, replay.Replayed)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	require.Equal(t, first.ProfessionalReference, replay.ProfessionalReference)
	require.Equal(t, "critical", f.client.Ticket.GetX(ctx, replay.WorkItemID).Priority)
	detail, err = owner.GetIncident(ctx, replay.ProfessionalReference.ID, f.identity.TenantID)
	require.NoError(t, err)
	require.Equal(t, "critical", detail.Priority)
	beforeReceipt, err := json.Marshal(receipt)
	require.NoError(t, err)
	afterReceipt, err := json.Marshal(f.client.IntakeRequest.Query().OnlyX(ctx))
	require.NoError(t, err)
	require.JSONEq(t, string(beforeReceipt), string(afterReceipt))
	beforeSnapshot, err := json.Marshal(snapshot)
	require.NoError(t, err)
	afterSnapshot, err := json.Marshal(f.client.IntakeResolutionSnapshot.Query().OnlyX(ctx))
	require.NoError(t, err)
	require.JSONEq(t, string(beforeSnapshot), string(afterSnapshot))
	w, fresh := intakeHTTP(t, f, h.CreateIncident, body, "fresh-matrix", nil)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.NotEqual(t, first.WorkItemID, fresh.WorkItemID)
	require.Equal(t, "low", f.client.Ticket.GetX(ctx, fresh.WorkItemID).Priority)
	detail, err = owner.GetIncident(ctx, fresh.ProfessionalReference.ID, f.identity.TenantID)
	require.NoError(t, err)
	require.Equal(t, "low", detail.Priority)
	explicit := `{"title":"Explicit priority","description":"Explicit priority overrides matrix","impact":"medium","urgency":"medium","priority":"high"}`
	w, chosen := intakeHTTP(t, f, h.CreateIncident, explicit, "explicit-matrix", nil)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "high", f.client.Ticket.GetX(ctx, chosen.WorkItemID).Priority)
	require.Equal(t, 3, f.client.IntakeRequest.Query().CountX(ctx))
	require.Equal(t, 3, f.client.Incident.Query().CountX(ctx))
}
