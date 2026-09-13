package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	ticketrepo "itsm-backend/repository/ticket"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"testing"
	"time"
)

func TestIntakeApprovedToolCreationRecoversAcknowledgement(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	q := service.NewToolQueue(f.client, nil, f.app, nil, 1, zap.NewNop().Sugar(), executionfixture.Standard())
	defer q.Close()
	inv := f.client.ToolInvocation.Create().SetTenantID(f.identity.TenantID).SetUserID(f.identity.ActorID).SetToolName("create_ticket").SetArguments(`{"title":"Approved AI request","description":"Verified requested work","priority":"high"}`).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(f.identity.ActorID).SetApprovedAt(time.Now()).SetStatus("pending").SaveX(ctx)
	failed := false
	f.client.ToolInvocation.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if typed, ok := m.(*ent.ToolInvocationMutation); ok && !failed {
				if status, ok := typed.Status(); ok && status == "done" {
					failed = true
					return nil, errors.New("injected tool acknowledgement failure")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	job := service.ToolJob{InvocationID: inv.ID, TenantID: f.identity.TenantID}
	require.Error(t, q.ProcessJob(ctx, job))
	require.True(t, failed)
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
	require.NoError(t, q.ProcessJob(ctx, job))
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
	recorded := f.client.ToolInvocation.GetX(ctx, inv.ID)
	require.Equal(t, "done", recorded.Status)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(*recorded.Result), &result))
	require.ElementsMatch(t, []string{"workItemId", "number", "recordClass", "professionalReference", "workflowStartStatus", "replayed"}, mapKeys(result))
	require.Equal(t, "generic", result["recordClass"])
	require.Equal(t, map[string]any{"type": "", "id": float64(0)}, result["professionalReference"])
	require.True(t, result["replayed"].(bool))
	f.client.User.UpdateOneID(f.identity.ActorID).SetActive(false).SaveX(ctx)
	require.Error(t, q.ProcessJob(ctx, job))
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
func TestIntakeToolCreationRequiresApprovedTenantInvocation(t *testing.T) {
	for _, state := range []string{"pending", "rejected", "wrong_tenant", "missing_actor", "malformed"} {
		t.Run(state, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			q := service.NewToolQueue(f.client, nil, f.app, nil, 1, zap.NewNop().Sugar(), executionfixture.Standard())
			defer q.Close()
			builder := f.client.ToolInvocation.Create().SetTenantID(f.identity.TenantID).SetToolName("create_ticket").SetArguments(`{"title":"Approved AI request","description":"Request","priority":"high"}`).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(f.identity.ActorID).SetApprovedAt(time.Now()).SetStatus("pending")
			if state != "missing_actor" {
				builder.SetUserID(f.identity.ActorID)
			}
			if state == "pending" || state == "rejected" {
				builder.SetApprovalState(state)
			}
			if state == "malformed" {
				builder.SetArguments(`{"title":"one","title":"two"}`)
			}
			inv := builder.SaveX(ctx)
			tenantID := f.identity.TenantID
			if state == "wrong_tenant" {
				tenantID += 100
			}
			require.Error(t, q.ProcessJob(ctx, service.ToolJob{InvocationID: inv.ID, TenantID: tenantID}))
			assertNoEntryGraph(t, f.client)
		})
	}
}

func TestIntakeToolCreationRejectsAdvertisedContractViolations(t *testing.T) {
	for name, arguments := range map[string]string{
		"missing title":    `{}`,
		"blank title":      `{"title":"  "}`,
		"unknown priority": `{"title":"request","priority":"highest"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			q := service.NewToolQueue(f.client, nil, f.app, nil, 1, zap.NewNop().Sugar(), executionfixture.Standard())
			defer q.Close()
			inv := f.client.ToolInvocation.Create().
				SetTenantID(f.identity.TenantID).
				SetUserID(f.identity.ActorID).
				SetToolName("create_ticket").
				SetArguments(arguments).
				SetNeedsApproval(true).
				SetApprovalState("approved").
				SetApprovedBy(f.identity.ActorID).
				SetApprovedAt(time.Now()).
				SetStatus("pending").
				SaveX(ctx)

			require.Error(t, q.ProcessJob(ctx, service.ToolJob{InvocationID: inv.ID, TenantID: f.identity.TenantID}))
			require.Equal(t, "failed", f.client.ToolInvocation.GetX(ctx, inv.ID).Status)
			assertNoEntryGraph(t, f.client)
		})
	}
}

func TestIntakeToolCreationNormalizesAcceptedAliases(t *testing.T) {
	for name, test := range map[string]struct {
		arguments string
		title     string
		priority  string
	}{
		"padded title":    {arguments: `{"title":"  Padded request  "}`, title: "Padded request", priority: "medium"},
		"padded priority": {arguments: `{"title":"Priority request","priority":" high "}`, title: "Priority request", priority: "high"},
		"blank priority":  {arguments: `{"title":"Default request","priority":"  "}`, title: "Default request", priority: "medium"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			q := service.NewToolQueue(f.client, nil, f.app, nil, 1, zap.NewNop().Sugar(), executionfixture.Standard())
			defer q.Close()
			inv := f.client.ToolInvocation.Create().
				SetTenantID(f.identity.TenantID).
				SetUserID(f.identity.ActorID).
				SetToolName("create_ticket").
				SetArguments(test.arguments).
				SetNeedsApproval(true).
				SetApprovalState("approved").
				SetApprovedBy(f.identity.ActorID).
				SetApprovedAt(time.Now()).
				SetStatus("pending").
				SaveX(ctx)

			require.NoError(t, q.ProcessJob(ctx, service.ToolJob{InvocationID: inv.ID, TenantID: f.identity.TenantID}))
			require.Equal(t, "done", f.client.ToolInvocation.GetX(ctx, inv.ID).Status)
			created := f.client.Ticket.Query().OnlyX(ctx)
			require.Equal(t, test.title, created.Title)
			require.Equal(t, test.priority, created.Priority)
		})
	}
}

func TestApprovedToolEditRejectsMalformedArguments(t *testing.T) {
	for _, suffix := range []string{`,"unknown":true}`, `,"status":null}`, `,"expectedVersion":1}`, `} {}`} {
		t.Run(suffix, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			item := f.client.Ticket.Create().SetTenantID(f.identity.TenantID).SetRequesterID(f.identity.ActorID).SetTicketNumber("EDIT-TOOL").SetTitle("Approved edit").SetRecordClass("generic").SetStatus("open").SaveX(ctx)
			svc := service.NewTicketService(&service.TicketServiceConfig{Client: f.client, Repository: ticketrepo.NewEntRepository(f.client, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: executionfixture.Standard()})
			q := service.NewToolQueue(f.client, nil, f.app, svc, 1, zap.NewNop().Sugar(), executionfixture.Standard())
			defer q.Close()
			raw := fmt.Sprintf(`{"ticket_id":%d,"expectedVersion":%d,"assignee_id":%d`, item.ID, item.Version, f.identity.ActorID) + suffix
			inv := f.client.ToolInvocation.Create().SetTenantID(f.identity.TenantID).SetUserID(f.identity.ActorID).SetToolName("update_ticket").SetArguments(raw).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(f.identity.ActorID).SetApprovedAt(time.Now()).SetStatus("pending").SaveX(ctx)
			before, err := json.Marshal(f.client.Ticket.GetX(ctx, item.ID))
			require.NoError(t, err)
			require.Error(t, q.ProcessJob(ctx, service.ToolJob{InvocationID: inv.ID, TenantID: f.identity.TenantID}))
			after, err := json.Marshal(f.client.Ticket.GetX(ctx, item.ID))
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestApprovedToolEditRecoversAcknowledgement(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	item := f.client.Ticket.Create().SetTenantID(f.identity.TenantID).SetRequesterID(f.identity.ActorID).SetTicketNumber("EDIT-RECOVERY").SetTitle("Approved edit").SetRecordClass("generic").SetStatus("open").SaveX(ctx)
	svc := service.NewTicketService(&service.TicketServiceConfig{Client: f.client, Repository: ticketrepo.NewEntRepository(f.client, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: executionfixture.Standard()})
	q := service.NewToolQueue(f.client, nil, f.app, svc, 1, zap.NewNop().Sugar(), executionfixture.Standard())
	defer q.Close()
	raw := fmt.Sprintf(`{"ticket_id":%d,"expectedVersion":%d,"assignee_id":%d}`, item.ID, item.Version, f.identity.ActorID)
	inv := f.client.ToolInvocation.Create().SetTenantID(f.identity.TenantID).SetUserID(f.identity.ActorID).SetToolName("update_ticket").SetArguments(raw).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(f.identity.ActorID).SetApprovedAt(time.Now()).SetStatus("pending").SaveX(ctx)
	failed := false
	f.client.ToolInvocation.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if typed, ok := m.(*ent.ToolInvocationMutation); ok && !failed {
				if status, ok := typed.Status(); ok && status == "done" {
					failed = true
					return nil, errors.New("injected edit acknowledgement failure")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	job := service.ToolJob{InvocationID: inv.ID, TenantID: f.identity.TenantID}
	require.Error(t, q.ProcessJob(ctx, job))
	require.True(t, failed)
	require.Equal(t, item.Version+1, f.client.Ticket.GetX(ctx, item.ID).Version)
	before, err := json.Marshal(f.client.Ticket.GetX(ctx, item.ID))
	require.NoError(t, err)
	auditCount := f.client.AuditLog.Query().CountX(ctx)
	require.NoError(t, q.ProcessJob(ctx, job))
	after, err := json.Marshal(f.client.Ticket.GetX(ctx, item.ID))
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
	require.Equal(t, auditCount, f.client.AuditLog.Query().CountX(ctx))
	recorded := f.client.ToolInvocation.GetX(ctx, inv.ID)
	require.Equal(t, "done", recorded.Status)
	require.Equal(t, raw, recorded.Arguments)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(*recorded.Result), &result))
	require.ElementsMatch(t, []string{"workItemId", "version", "status", "replayed"}, mapKeys(result))
	require.Equal(t, float64(item.Version+1), result["version"])
	require.Equal(t, true, result["replayed"])
	f.client.User.UpdateOneID(f.identity.ActorID).SetActive(false).ExecX(ctx)
	require.Error(t, q.ProcessJob(ctx, job))
	require.Equal(t, item.Version+1, f.client.Ticket.GetX(ctx, item.ID).Version)
}
