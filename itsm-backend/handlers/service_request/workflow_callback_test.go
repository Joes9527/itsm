package service_request_test

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	sr "itsm-backend/handlers/service_request"
	"itsm-backend/handlers/shared/workflowcallback"
	"testing"
	"time"
)

func TestCompletionNoteUpdatePreservesResolvedAt(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_completion_note_timestamp?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant := client.Tenant.Create().SetCode("completion-note").SetName("completion note").SaveX(ctx)
	requester := client.User.Create().SetTenantID(tenant.ID).SetUsername("completion-note").SetName("requester").SetEmail("completion-note@example.test").SetPasswordHash("test").SaveX(ctx)
	resolvedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	wi := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(requester.ID).SetTicketNumber("SR-NOTE").SetTitle("resolved request").SetRecordClass("service_request_item").SetStatus("resolved").SetResolvedAt(resolvedAt).SetVersion(7).SaveX(ctx)
	request := client.ServiceRequest.Create().SetTicketID(wi.ID).SetCatalogID(1).SetCompletedAt(resolvedAt).SetCompletionNote("original note").SaveX(ctx)
	service := sr.NewService(sr.NewEntRepository(client), client, zaptest.NewLogger(t).Sugar(), nil)
	result, err := service.ApplyServiceRequestWorkflowCallback(ctx, workflowcallback.ServiceRequestCommand{RequestID: request.ID, TenantID: tenant.ID, Action: "complete_request", CompletionNote: "corrected note"})
	require.NoError(t, err)
	require.Equal(t, workflowcallback.StatusApplied, result.Status)
	current := client.Ticket.GetX(ctx, wi.ID)
	require.Equal(t, "corrected note", client.ServiceRequest.GetX(ctx, request.ID).CompletionNote)
	require.Equal(t, wi.Version+1, current.Version)
	require.True(t, current.UpdatedAt.After(wi.UpdatedAt))
	require.Equal(t, resolvedAt, current.ResolvedAt, "editing a completion note must retain the original lifecycle time")
	require.Equal(t, resolvedAt, client.ServiceRequest.GetX(ctx, request.ID).CompletedAt)
}

func TestCompletionExtensionFailureRollsBackPriorWorkItemCAS(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_completion_extension_rollback?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant := client.Tenant.Create().SetCode("completion-rollback").SetName("completion rollback").SaveX(ctx)
	requester := client.User.Create().SetTenantID(tenant.ID).SetUsername("completion-rollback").SetName("requester").SetEmail("completion-rollback@example.test").SetPasswordHash("test").SaveX(ctx)
	wi := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(requester.ID).SetTicketNumber("SR-ROLLBACK").SetTitle("completion rollback").SetRecordClass("service_request_item").SetStatus("open").SetVersion(7).SaveX(ctx)
	request := client.ServiceRequest.Create().SetTicketID(wi.ID).SetCatalogID(1).SaveX(ctx)
	var versionBeforeExtensionFailure int
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(mutationCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if extension, ok := mutation.(*ent.ServiceRequestMutation); ok {
				owner, err := extension.Client().Ticket.Get(mutationCtx, wi.ID)
				if err != nil {
					return nil, err
				}
				versionBeforeExtensionFailure = owner.Version
				return nil, errors.New("injected completion extension failure")
			}
			return next.Mutate(mutationCtx, mutation)
		})
	})
	service := sr.NewService(sr.NewEntRepository(client), client, zaptest.NewLogger(t).Sugar(), nil)
	_, err := service.ApplyServiceRequestWorkflowCallback(ctx, workflowcallback.ServiceRequestCommand{RequestID: request.ID, TenantID: tenant.ID, Action: "complete_request", CompletionNote: "must rollback"})
	require.ErrorContains(t, err, "injected completion extension failure")
	require.Equal(t, wi.Version+1, versionBeforeExtensionFailure, "WorkItem CAS must execute before the failing extension write")
	current := client.Ticket.GetX(ctx, wi.ID)
	require.Equal(t, wi.Version, current.Version)
	require.Equal(t, wi.Status, current.Status)
	require.True(t, wi.UpdatedAt.Equal(current.UpdatedAt), "rolled-back WorkItem must retain the same update instant")
	require.True(t, current.ResolvedAt.IsZero())
	require.Empty(t, client.ServiceRequest.GetX(ctx, request.ID).CompletionNote)
	require.True(t, client.ServiceRequest.GetX(ctx, request.ID).CompletedAt.IsZero())
}
