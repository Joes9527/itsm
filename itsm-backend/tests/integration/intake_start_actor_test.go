package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processauditlog"
	creation "itsm-backend/handlers/common/workitemcreation"
	catalogdomain "itsm-backend/handlers/service_catalog"
	"itsm-backend/service"
)

func entryCatalogCommand(t *testing.T, f *unifiedIntakeFixture, class, process string) creation.CreateWorkItemCommand {
	t.Helper()
	ctx := context.Background()
	catalog := f.client.ServiceCatalog.Create().SetTenantID(f.identity.TenantID).SetName("Configured service").SetCategory("general").SetDescription("Configured request").SetTargetClass(class).SetRequiresApproval(false).SetStatus("enabled").SetProcessDefinitionKey(process).SaveX(ctx)
	tx, err := f.client.Tx(ctx)
	require.NoError(t, err)
	resolved, _, err := catalogdomain.NewService(nil, f.client, zap.NewNop().Sugar()).ResolveCreationCatalog(ctx, tx, f.identity, catalog.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	command := f.command
	command.IntakeKind, command.RecordClass = "catalog_item", class
	command.CatalogItemID, command.CatalogVersion, command.FormSchemaVersion = &catalog.ID, resolved.Version, resolved.FormSchemaVersion
	return command
}

// Actual creation must supply the frozen actor identity consumed by the real
// start handler and engine. Raw engine/context audit tests remain separate.
func TestIntakeCreationDurableStartPreservesActorAndCanonicalIdentity(t *testing.T) {
	for _, class := range []string{"generic", "incident", "service_request_item"} {
		t.Run(class, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			restrictEntryPermissions(t, f)
			ctx := context.Background()
			// Offset the shared ID sequence through Intake, so an extension ID cannot
			// accidentally pass the canonical WorkItem business identity assertion.
			_, err := f.app.Create(ctx, f.identity, f.command)
			require.NoError(t, err)
			requester := f.client.User.Create().SetTenantID(f.identity.TenantID).SetUsername("requested-for").SetName("Requested For").SetEmail("requested@example.test").SetPasswordHash("unused").SetRole("requester").SaveX(ctx)
			actor := f.client.User.GetX(ctx, f.identity.ActorID)
			f.identity.RequesterID = requester.ID
			business := map[string]string{"generic": "ticket", "incident": "incident", "service_request_item": "service_request"}[class]
			entryDefinition(t, f, "configured", f.identity.TenantID, "")
			command := f.command
			command.RecordClass, command.IntakeKind, command.IdempotencyKey = class, class, "actor-creation"
			if class == "service_request_item" {
				command = entryCatalogCommand(t, f, class, "configured")
				command.IdempotencyKey = "actor-creation"
			} else {
				bindEntryDefinition(t, f, business, "configured")
				command.AdHocFields = []creation.AdHocFieldDefinition{{Name: "triggered_by", Label: "Submitted actor label"}}
				command.FormValues = map[string]any{"triggered_by": fmt.Sprint(requester.ID)}
			}
			require.Empty(t, command.WorkflowDefinitionKey, "ordinary user follows server-owned configuration")
			result, err := f.app.Create(ctx, f.identity, command)
			require.NoError(t, err)
			require.Equal(t, "pending", result.WorkflowStartStatus)
			item := f.client.Ticket.GetX(ctx, result.WorkItemID)
			require.Equal(t, class, item.RecordClass)
			require.Equal(t, requester.ID, item.RequesterID)
			require.Equal(t, actor.ID, item.OpenedByID)
			require.Equal(t, f.identity.TenantID, item.TenantID)
			require.Equal(t, map[string]string{"generic": "itsm_web", "incident": "itsm_web", "service_request_item": "service_catalog"}[class], item.Source)
			if class != "generic" {
				require.NotEqual(t, result.WorkItemID, result.ProfessionalReference.ID)
			}
			receipt := f.client.IntakeRequest.Query().Where(intakerequest.WorkItemIDEQ(item.ID)).OnlyX(ctx)
			require.Equal(t, actor.ID, receipt.ActorID)
			require.Equal(t, requester.ID, receipt.RequesterID)
			require.Equal(t, f.identity.TenantID, receipt.TenantID)
			require.Equal(t, f.identity.Channel, receipt.Channel)
			event := f.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("workflow.start.requested")).OnlyX(ctx)
			var payload struct {
				ActorID     int            `json:"actorId"`
				TenantID    int            `json:"tenantId"`
				RecordClass string         `json:"recordClass"`
				WorkItemID  int            `json:"workItemId"`
				Variables   map[string]any `json:"variables"`
			}
			require.NoError(t, json.Unmarshal(event.Payload, &payload))
			require.Equal(t, actor.ID, payload.ActorID)
			require.Equal(t, item.TenantID, payload.TenantID)
			require.Equal(t, class, payload.RecordClass)
			require.Equal(t, item.ID, payload.WorkItemID)
			require.Equal(t, fmt.Sprint(actor.ID), fmt.Sprint(payload.Variables["triggered_by"]))
			require.Equal(t, fmt.Sprint(requester.ID), fmt.Sprint(payload.Variables["requester_id"]))
			require.Zero(t, f.client.ProcessInstance.Query().CountX(ctx))
			require.Zero(t, f.client.ProcessAuditLog.Query().CountX(ctx))
			replay, err := f.app.Create(ctx, f.identity, command)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, result.WorkItemID, replay.WorkItemID)
			engine := service.NewCustomProcessEngine(f.client, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
			handler := service.NewWorkflowStartOutboxHandler(f.client, engine, f.client)
			require.NoError(t, handler.Deliver(ctx, event))
			require.NoError(t, handler.Deliver(ctx, event))
			instance := f.client.ProcessInstance.Query().OnlyX(ctx)
			require.Equal(t, fmt.Sprint(actor.ID), instance.Initiator)
			require.Equal(t, item.TenantID, instance.TenantID)
			require.Equal(t, business, instance.BusinessType)
			require.Equal(t, fmt.Sprintf("%s:%d", business, item.ID), instance.BusinessKey)
			require.Equal(t, item.ID, instance.BusinessID)
			audit := f.client.ProcessAuditLog.Query().Where(processauditlog.ActionEQ(service.AuditActionProcessStarted)).OnlyX(ctx)
			require.Equal(t, actor.ID, audit.UserID)
			require.Equal(t, actor.Name, audit.UserName)
			require.Equal(t, item.TenantID, audit.TenantID)
			auditCount := f.client.ProcessAuditLog.Query().CountX(ctx)
			replay, err = f.app.Create(ctx, f.identity, command)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.NoError(t, handler.Deliver(ctx, event))
			require.Equal(t, auditCount, f.client.ProcessAuditLog.Query().CountX(ctx))
			require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(ctx))
			require.Equal(t, 1, f.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("workflow.start.requested")).CountX(ctx))
			// A body/payload actor substitution must not replace authenticated identity.
			var forged map[string]any
			require.NoError(t, json.Unmarshal(event.Payload, &forged))
			forged["variables"].(map[string]any)["triggered_by"] = fmt.Sprint(requester.ID)
			invalid := *event
			invalid.Payload, err = json.Marshal(forged)
			require.NoError(t, err)
			require.Error(t, handler.Deliver(ctx, &invalid))
			require.Equal(t, auditCount, f.client.ProcessAuditLog.Query().CountX(ctx))
		})
	}
}

func TestIntakeCreationRejectsUnavailableActorBeforeGraph(t *testing.T) {
	for _, class := range []string{"generic", "incident", "service_request_item"} {
		for _, invalid := range []string{"missing", "inactive", "wrong_tenant"} {
			t.Run(class+"/"+invalid, func(t *testing.T) {
				f := newUnifiedIntakeFixture(t)
				restrictEntryPermissions(t, f)
				ctx := context.Background()
				command := f.command
				command.RecordClass, command.IntakeKind = class, class
				if class == "service_request_item" {
					command = entryCatalogCommand(t, f, class, "")
				}
				switch invalid {
				case "missing":
					f.identity.ActorID = 0
				case "inactive":
					f.client.User.UpdateOneID(f.identity.ActorID).SetActive(false).ExecX(ctx)
				case "wrong_tenant":
					other := f.client.Tenant.Create().SetCode("other").SetName("Other").SaveX(ctx)
					actor := f.client.User.Create().SetTenantID(other.ID).SetUsername("foreign").SetName("Foreign").SetEmail("foreign@example.test").SetPasswordHash("unused").SetRole("requester").SaveX(ctx)
					f.identity.ActorID = actor.ID
				}
				_, err := f.app.Create(ctx, f.identity, command)
				require.Error(t, err)
				assertNoEntryGraph(t, f.client)
				require.Zero(t, f.client.Incident.Query().CountX(ctx))
				require.Zero(t, f.client.ServiceRequest.Query().CountX(ctx))
				require.Zero(t, f.client.ProcessInstance.Query().CountX(ctx))
				require.Zero(t, f.client.ProcessAuditLog.Query().CountX(ctx))
			})
		}
	}
}
