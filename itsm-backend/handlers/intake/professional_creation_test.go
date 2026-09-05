package intake

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	changehandler "itsm-backend/handlers/change"
	"itsm-backend/handlers/common/workitemcreation"
	problemhandler "itsm-backend/handlers/problem"
	cataloghandler "itsm-backend/handlers/service_catalog"
	srhandler "itsm-backend/handlers/service_request"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
)

// This catches missing domain-owned preparation and extension persistence;
// registry stubs cannot satisfy the persisted semantic assertions below.
func TestAuthoritativeProfessionalGraph(t *testing.T) {
	for _, class := range []string{"generic", "problem", "incident", "change_request", "service_request_item"} {
		t.Run(class, func(t *testing.T) {
			client, app, identity, command, _, _ := intakeFixture(t)
			command.RecordClass, command.IntakeKind = class, class
			command.Description, command.Priority = "Professional description", "high"
			var domain any
			switch class {
			case "generic":
				domain = &service.TicketService{}
			case "problem":
				domain = problemhandler.NewService(nil, zap.NewNop().Sugar())
				command.Problem = &workitemcreation.ProblemInput{RootCause: "route failure", Impact: "employees"}
			case "incident":
				domain = service.NewIncidentService(client, zap.NewNop().Sugar(), workitemnumber.NewPostgreSQLAllocator())
				command.Incident = &workitemcreation.IncidentInput{Type: "security", Impact: "critical", Urgency: "high", Severity: "critical", DetectedAt: "2026-09-04T01:00:00Z", ImpactAnalysis: &workitemcreation.ImpactAnalysis{BusinessImpact: &workitemcreation.BusinessImpact{RevenueImpact: json.Number("9007199254740993.125")}, TechnicalImpact: "vpn gateway"}}
				parent := client.TicketCategory.Create().SetTenantID(identity.TenantID).SetCode("network").SetName("Network").SaveX(context.Background())
				client.TicketCategory.Create().SetTenantID(identity.TenantID).SetCode("vpn").SetName("VPN").SetParentID(parent.ID).SaveX(context.Background())
				command.Incident.Category = "Network"
				command.Incident.Subcategory = "VPN"

			case "service_request_item":
				domain = srhandler.NewService(nil, nil, nil, client, workitemnumber.NewPostgreSQLAllocator(), zap.NewNop().Sugar(), nil, service.NewApprovalChainResolver(client, zap.NewNop().Sugar()), nil)
				catalog := client.ServiceCatalog.Create().SetTenantID(identity.TenantID).SetName("VPN").SetTargetClass("service_request_item").SetRequiresApproval(false).SaveX(context.Background())
				command.CatalogItemID = &catalog.ID
				command.CatalogVersion = "1"
				command.FormSchemaVersion = "1"
				command.IntakeKind = "catalog_item"
				command.ServiceRequest = &workitemcreation.ServiceRequestInput{CostCenter: "IT", ContactEmail: "user@example.test", Amount: json.Number("9007199254740993.125")}
			case "change_request":
				domain = changehandler.NewService(nil, client, zap.NewNop().Sugar())
				command.Change = &workitemcreation.ChangeInput{Type: "normal", Justification: "security patch", ImplementationPlan: "deploy", RollbackPlan: "restore", PlannedStartDate: "2026-09-07T01:00:00Z", PlannedEndDate: "2026-09-07T02:00:00Z"}
			}
			business := map[string]string{"generic": "ticket", "problem": "problem", "incident": "incident", "change_request": "change", "service_request_item": "service_request"}[class]
			client.ProcessBinding.Create().SetTenantID(identity.TenantID).SetBusinessType(business).SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(context.Background())
			catalogOwner := cataloghandler.NewService(nil, client, zap.NewNop().Sugar())
			app.resolver = NewResolver(catalogOwner, service.NewProcessBindingService(client), service.NewConfigurationItemService(client, zap.NewNop().Sugar(), nil, nil), service.NewTicketCategoryService(client))
			if command.CatalogItemID != nil {
				tx, err := client.Tx(context.Background())
				require.NoError(t, err)
				catalog, _, err := catalogOwner.ResolveCreationCatalog(context.Background(), tx, identity, *command.CatalogItemID)
				require.NoError(t, err)
				require.NoError(t, tx.Rollback())
				command.CatalogVersion, command.FormSchemaVersion = catalog.Version, catalog.FormSchemaVersion
			}
			creator, ok := domain.(workitemcreation.ProfessionalCreator)
			require.True(t, ok, "owning service must implement professional creation")
			app.registry = NewCreatorRegistry()
			require.NoError(t, app.registry.Register(creator))
			app.workItems = NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator())
			result, err := app.Create(context.Background(), identity, command)
			require.NoError(t, err)
			item := client.Ticket.GetX(context.Background(), result.WorkItemID)
			require.Equal(t, "high", item.Priority)
			require.Equal(t, "Professional description", item.Description)
			require.Equal(t, "", item.Type)
			status := map[string]string{"generic": "new", "problem": "open", "incident": "new", "change_request": "draft", "service_request_item": "new"}[class]
			require.Equal(t, status, item.Status)
			switch class {
			case "generic":
				require.Equal(t, workitemcreation.ProfessionalReference{}, result.ProfessionalReference)
			case "problem":
				require.Equal(t, "route failure", client.Problem.Query().OnlyX(context.Background()).RootCause)
			case "incident":
				incident := client.Incident.Query().OnlyX(context.Background())
				require.Equal(t, "security", incident.Type)
				require.Equal(t, "VPN", client.TicketCategory.GetX(context.Background(), item.CategoryID).Name)
				require.Equal(t, item.TicketNumber, incident.IncidentNumber)
				// Read raw JSON so Ent's map decoder cannot conceal precision lost before persistence.
				var raw []string
				require.NoError(t, client.Incident.Query().Select("impact_analysis").Scan(context.Background(), &raw))
				require.Len(t, raw, 1)
				require.Contains(t, raw[0], "9007199254740993.125")
				require.Equal(t, 1, client.IncidentEvent.Query().CountX(context.Background()))
				require.Equal(t, 1, client.OutboxEvent.Query().CountX(context.Background()))
			case "service_request_item":
				require.Equal(t, "IT", client.ServiceRequest.Query().OnlyX(context.Background()).CostCenter)
			case "change_request":
				require.Equal(t, "security patch", client.Change.Query().OnlyX(context.Background()).Justification)
			}
			replay, err := app.Create(context.Background(), identity, command)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, result.WorkItemID, replay.WorkItemID)
			require.Equal(t, 1, client.WorkItemNumberSequence.Query().CountX(context.Background()))
		})
	}
}

func TestGenericSubtypeHasOneAuthoritativeField(t *testing.T) {
	client, app, identity, command, _, _ := intakeFixture(t)
	app.registry = NewCreatorRegistry()
	require.NoError(t, app.registry.Register(&service.TicketService{}))
	command.Generic = &workitemcreation.GenericInput{Type: "improvement"}
	_, err := app.Create(context.Background(), identity, command)
	require.NoError(t, err)
	values, err := client.Ticket.Query().Select("generic_subtype").Strings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"improvement"}, values)
	require.Equal(t, "", client.Ticket.Query().OnlyX(context.Background()).Type)
}

func TestIncidentNumbersAreScopedByWorkItemTenant(t *testing.T) {
	client, app, identity, command, _, _ := intakeFixture(t)
	ctx := context.Background()
	app.registry = NewCreatorRegistry()
	require.NoError(t, app.registry.Register(service.NewIncidentService(client, zap.NewNop().Sugar(), workitemnumber.NewPostgreSQLAllocator())))
	app.workItems = NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator())
	command.RecordClass = "incident"
	command.IntakeKind = "incident"
	command.Priority = "high"
	first, err := app.Create(ctx, identity, command)
	require.NoError(t, err)
	other := client.Tenant.Create().SetName("Second").SetCode("second").SaveX(ctx)
	actor := client.User.Create().SetTenantID(other.ID).SetUsername("second").SetEmail("second@example.test").SetName("Second").SetPasswordHash("test").SetRole("requester").SaveX(ctx)
	seedCreationPermission(t, client, other.ID, "requester")
	identity.TenantID = other.ID
	identity.ActorID = actor.ID
	identity.RequesterID = actor.ID
	second, err := app.Create(ctx, identity, command)
	require.NoError(t, err)
	require.Equal(t, first.Number, second.Number)
	require.NotEqual(t, first.WorkItemID, second.WorkItemID)
	require.Equal(t, 2, client.Incident.Query().CountX(ctx))
}
