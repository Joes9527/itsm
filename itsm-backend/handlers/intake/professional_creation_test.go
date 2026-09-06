package intake

import (
	"context"
	"encoding/json"
	"fmt"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/ticket"
	"strconv"
	"testing"
	"time"

	changehandler "itsm-backend/handlers/change"
	"itsm-backend/handlers/common/workitemcreation"
	problemhandler "itsm-backend/handlers/problem"
	cataloghandler "itsm-backend/handlers/service_catalog"
	srhandler "itsm-backend/handlers/service_request"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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
				identity.Channel = "http"
				domain = service.NewIncidentService(client, zap.NewNop().Sugar())
				command.Incident = &workitemcreation.IncidentInput{Type: "security", Impact: "critical", Urgency: "high", Severity: "critical", DetectedAt: "2026-09-04T01:00:00Z", ImpactAnalysis: &workitemcreation.ImpactAnalysis{BusinessImpact: &workitemcreation.BusinessImpact{RevenueImpact: json.Number("9007199254740993.125")}, TechnicalImpact: "vpn gateway"}}
				parent := client.TicketCategory.Create().SetTenantID(identity.TenantID).SetCode("network").SetName("Network").SaveX(context.Background())
				client.TicketCategory.Create().SetTenantID(identity.TenantID).SetCode("vpn").SetName("VPN").SetParentID(parent.ID).SaveX(context.Background())
				command.Incident.Category = "Network"
				command.Incident.Subcategory = "VPN"

			case "service_request_item":
				domain = srhandler.NewService(nil, client, zap.NewNop().Sugar(), service.NewApprovalChainResolver(client, zap.NewNop().Sugar()))
				catalog := client.ServiceCatalog.Create().SetTenantID(identity.TenantID).SetName("VPN").SetTargetClass("service_request_item").SetRequiresApproval(false).SaveX(context.Background())
				command.CatalogItemID = &catalog.ID
				command.CatalogVersion = "1"
				command.FormSchemaVersion = "1"
				command.IntakeKind = "catalog_item"
				command.ServiceRequest = &workitemcreation.ServiceRequestInput{CostCenter: "IT", ContactEmail: "user@example.test", Amount: json.Number("9007199254740993.125")}
			case "change_request":
				domain = changehandler.NewService(nil, client, zap.NewNop().Sugar())
				command.Change = &workitemcreation.ChangeInput{Type: "normal", ImpactScope: "low", RiskLevel: "medium", Justification: "security patch", ImplementationPlan: "deploy", RollbackPlan: "restore", PlannedStartDate: "2026-09-07T01:00:00Z", PlannedEndDate: "2026-09-07T02:00:00Z"}
			}
			business := map[string]string{"generic": "ticket", "problem": "problem", "incident": "incident", "change_request": "change", "service_request_item": "service_request"}[class]
			client.ProcessBinding.Create().SetTenantID(identity.TenantID).SetBusinessType(business).SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(context.Background())
			catalogOwner := cataloghandler.NewService(nil, client, zap.NewNop().Sugar(), nil)
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
			require.Equal(t, class, item.RecordClass)
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
				require.Equal(t, item.TicketNumber, incident.QueryWorkItem().OnlyX(context.Background()).TicketNumber)
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
	require.Equal(t, "generic", client.Ticket.Query().OnlyX(context.Background()).RecordClass)
}

func TestIncidentNumbersAreScopedByWorkItemTenant(t *testing.T) {
	client, app, identity, command, _, _ := intakeFixture(t)
	identity.Channel = "http"
	ctx := context.Background()
	app.registry = NewCreatorRegistry()
	require.NoError(t, app.registry.Register(service.NewIncidentService(client, zap.NewNop().Sugar())))
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

func TestSourceConversationIsProvenanceNotEmailIdentity(t *testing.T) {
	for _, tc := range []struct{ name, conversation, secondProvider string }{
		{name: "absent conversation"},
		{name: "same non-email conversation", conversation: "chat-session"},
		{name: "different providers same conversation", conversation: "chat-session", secondProvider: "second-chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, app, identity, command, _, _ := intakeFixture(t)
			ctx := context.Background()
			for n := 0; n < 2; n++ {
				provider := "first-chat"
				if n == 1 && tc.secondProvider != "" {
					provider = tc.secondProvider
				}
				identity.Provider = provider
				command.IdempotencyKey = fmt.Sprintf("source-%d", n)
				command.SourceReference = &workitemcreation.SourceReference{Provider: provider, EventID: fmt.Sprintf("event-%d", n), ConversationID: tc.conversation}
				result, err := app.Create(ctx, identity, command)
				require.NoError(t, err)
				snapshot := client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemIDEQ(result.WorkItemID)).OnlyX(ctx)
				require.Equal(t, provider, snapshot.SourceProvider)
				require.Equal(t, command.SourceReference.EventID, snapshot.SourceEventID)
				require.Equal(t, tc.conversation, snapshot.SourceConversationID)
				replay, err := app.Create(ctx, identity, command)
				require.NoError(t, err)
				require.True(t, replay.Replayed)
				require.Equal(t, result.WorkItemID, replay.WorkItemID)
			}
			require.Equal(t, 2, client.Ticket.Query().Where(ticket.ConversationIDIsNil()).CountX(ctx))
		})
	}
}

func TestRoutingConsumesDomainEffectiveValues(t *testing.T) {
	for _, class := range []string{"incident", "change_request", "generic"} {
		t.Run(class, func(t *testing.T) {
			client, app, identity, command, _, _ := intakeFixture(t)
			ctx := context.Background()
			logger := zap.NewNop().Sugar()
			command.RecordClass, command.IntakeKind = class, class
			app.registry = NewCreatorRegistry()
			app.resolver = NewResolver(cataloghandler.NewService(nil, client, logger, nil), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
			business, subtype, priority := "ticket", "improvement", "medium"
			conditions := map[string]any{"no_process": true, "priority": "medium"}
			typeID := ""
			switch class {
			case "incident":
				identity.Channel = "http"
				owner := service.NewIncidentService(client, logger)
				owner.SetPriorityMatrixService(service.NewPriorityMatrixService(logger))
				require.NoError(t, app.registry.Register(owner))
				business, subtype, priority = "incident", "incident", "critical"
				conditions = map[string]any{"no_process": true, "priority": "critical", "severity": "medium", "impact": "critical", "urgency": "high"}
			case "change_request":
				require.NoError(t, app.registry.Register(changehandler.NewService(nil, client, logger)))
				business, subtype = "change", "normal"
				conditions["riskLevel"] = "medium"
			case "generic":
				require.NoError(t, app.registry.Register(&service.TicketService{}))
				configured := client.TicketType.Create().SetTenantID(int64(identity.TenantID)).SetCode(subtype).SetName("Improvement").SetDescription("").SetIcon("").SetColor("").SetAssignmentRules([]interface{}{}).SetNotificationConfig(map[string]interface{}{}).SetPermissionConfig(map[string]interface{}{}).SetCreatedBy(int64(identity.ActorID)).SetCreatedAt(time.Now()).SetUpdatedAt(time.Now()).SaveX(ctx)
				typeID = strconv.Itoa(configured.ID)
			}
			client.ProcessBinding.Create().SetTenantID(identity.TenantID).SetBusinessType(business).SetBusinessSubType(subtype).SetProcessDefinitionKey("none").SetConditions(conditions).SaveX(ctx)
			for _, explicit := range []bool{true, false} {
				command.IdempotencyKey = fmt.Sprintf("effective-%v", explicit)
				command.Priority = ""
				if explicit {
					command.Priority = priority
				}
				switch class {
				case "incident":
					command.Incident = &workitemcreation.IncidentInput{Impact: "critical", Urgency: "high"}
					if explicit {
						command.Incident.Type = subtype
						command.Incident.Severity = "medium"
					}
				case "change_request":
					command.Change = &workitemcreation.ChangeInput{ImpactScope: "low", RiskLevel: "medium", Justification: "Apply security patch", ImplementationPlan: "Back up and deploy patch", RollbackPlan: "Restore previous package"}
					if explicit {
						command.Change.Type = subtype
					}
				case "generic":
					command.Generic = &workitemcreation.GenericInput{TypeID: typeID}
					if explicit {
						command.Generic.Type = subtype
					}
				}
				result, err := app.Create(ctx, identity, command)
				require.NoError(t, err, "explicit=%v", explicit)
				require.Equal(t, priority, client.Ticket.GetX(ctx, result.WorkItemID).Priority)
				require.Equal(t, "not_required", result.WorkflowStartStatus)
			}
			require.Equal(t, 2, client.IntakeResolutionSnapshot.Query().CountX(ctx))
		})
	}
}
