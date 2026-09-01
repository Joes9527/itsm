package service

import (
	"context"
	"testing"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestBuildIncidentActionsMirrorsIncidentCommandRules(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	authorization.InvalidateAllPermissionCaches()
	ctx := context.Background()
	tenant, err := createIncidentTestTenant(ctx, client, "action-rules")
	require.NoError(t, err)
	actorUser, err := createIncidentTestUser(ctx, client, tenant.ID, "action-rules")
	require.NoError(t, err)
	workItem := createIncidentAuthorizationWorkItem(t, ctx, client, tenant.ID, actorUser.ID, "action-rules", "incident")

	actor := ActionActor{Client: client, TenantID: tenant.ID, UserID: actorUser.ID, Role: "super_admin"}
	inProgress := incidentEntityWithStatus(common.IncidentStatusInProgress, workItem.ID)
	resolved := incidentEntityWithStatus(common.IncidentStatusResolved, workItem.ID)
	closed := incidentEntityWithStatus(common.IncidentStatusClosed, workItem.ID)
	cancelled := incidentEntityWithStatus(common.IncidentStatusCancelled, workItem.ID)

	require.True(t, BuildIncidentActions(ctx, actor, inProgress)["resolve"].Allowed)
	require.False(t, BuildIncidentActions(ctx, actor, resolved)["resolve"].Allowed)
	require.True(t, BuildIncidentActions(ctx, actor, closed)["reopen"].Allowed)
	require.False(t, BuildIncidentActions(ctx, actor, closed)["assign"].Allowed)
	cancelledActions := BuildIncidentActions(ctx, actor, cancelled)
	require.False(t, cancelledActions["assign"].Allowed)
	require.False(t, cancelledActions["markMajorIncident"].Allowed)
	require.False(t, cancelledActions["convertToProblem"].Allowed)
}

func TestCanConvertToProblemFailsClosed(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })
	authorization.InvalidateAllPermissionCaches()
	ctx := context.Background()
	tenant, err := createIncidentTestTenant(ctx, client, "convert-source")
	require.NoError(t, err)
	actorUser, err := createIncidentTestUser(ctx, client, tenant.ID, "convert-source")
	require.NoError(t, err)
	actor := ActionActor{Client: client, TenantID: tenant.ID, UserID: actorUser.ID, Role: "super_admin"}
	validWorkItem := createIncidentAuthorizationWorkItem(t, ctx, client, tenant.ID, actorUser.ID, "valid", "incident")

	t.Run("valid Incident WorkItem", func(t *testing.T) {
		permission := CanConvertToProblem(ctx, actor, incidentEntityWithStatus(common.IncidentStatusInProgress, validWorkItem.ID))
		require.True(t, permission.Allowed, permission.Reason)
	})

	t.Run("missing WorkItem", func(t *testing.T) {
		permission := CanConvertToProblem(ctx, actor, incidentEntityWithStatus(common.IncidentStatusInProgress))
		require.False(t, permission.Allowed)
		require.NotEmpty(t, permission.Reason)
	})

	t.Run("orphan WorkItem ID", func(t *testing.T) {
		permission := CanConvertToProblem(ctx, actor, incidentEntityWithStatus(common.IncidentStatusInProgress, validWorkItem.ID+100000))
		require.False(t, permission.Allowed)
	})

	t.Run("deleted Incident WorkItem", func(t *testing.T) {
		workItem := createIncidentAuthorizationWorkItem(t, ctx, client, tenant.ID, actorUser.ID, "deleted", "incident")
		_, err := client.Ticket.UpdateOneID(workItem.ID).SetDeletedAt(time.Now()).Save(ctx)
		require.NoError(t, err)
		permission := CanConvertToProblem(ctx, actor, incidentEntityWithStatus(common.IncidentStatusInProgress, workItem.ID))
		require.False(t, permission.Allowed)
	})

	t.Run("foreign tenant Incident WorkItem", func(t *testing.T) {
		foreignTenant, err := createIncidentTestTenant(ctx, client, "convert-foreign")
		require.NoError(t, err)
		foreignUser, err := createIncidentTestUser(ctx, client, foreignTenant.ID, "convert-foreign")
		require.NoError(t, err)
		workItem := createIncidentAuthorizationWorkItem(t, ctx, client, foreignTenant.ID, foreignUser.ID, "foreign", "incident")
		permission := CanConvertToProblem(ctx, actor, incidentEntityWithStatus(common.IncidentStatusInProgress, workItem.ID))
		require.False(t, permission.Allowed)
	})

	t.Run("wrong class WorkItem", func(t *testing.T) {
		workItem := createIncidentAuthorizationWorkItem(t, ctx, client, tenant.ID, actorUser.ID, "wrong-class", "problem")
		permission := CanConvertToProblem(ctx, actor, incidentEntityWithStatus(common.IncidentStatusInProgress, workItem.ID))
		require.False(t, permission.Allowed)
	})

	t.Run("live investigated_by relation", func(t *testing.T) {
		source := createIncidentAuthorizationWorkItem(t, ctx, client, tenant.ID, actorUser.ID, "related-source", "incident")
		target := createIncidentAuthorizationWorkItem(t, ctx, client, tenant.ID, actorUser.ID, "related-target", "problem")
		_, err := client.WorkItemRelation.Create().
			SetTenantID(actor.TenantID).
			SetSourceWorkItemID(source.ID).
			SetTargetWorkItemID(target.ID).
			SetRelationType("investigated_by").
			SetCreatedByID(actor.UserID).
			Save(context.Background())
		require.NoError(t, err)

		permission := CanConvertToProblem(ctx, actor, incidentEntityWithStatus(common.IncidentStatusInProgress, source.ID))
		require.False(t, permission.Allowed)
		require.Equal(t, "已经转为问题", permission.Reason)
	})

	t.Run("relation lookup is tenant scoped and live only", func(t *testing.T) {
		source := createIncidentAuthorizationWorkItem(t, ctx, client, tenant.ID, actorUser.ID, "relation-scope-source", "incident")
		target := createIncidentAuthorizationWorkItem(t, ctx, client, tenant.ID, actorUser.ID, "relation-scope-target", "problem")
		_, err := client.WorkItemRelation.Create().
			SetTenantID(actor.TenantID + 1).
			SetSourceWorkItemID(source.ID).
			SetTargetWorkItemID(target.ID).
			SetRelationType("investigated_by").
			SetCreatedByID(actor.UserID).
			Save(context.Background())
		require.NoError(t, err)
		_, err = client.WorkItemRelation.Create().
			SetTenantID(actor.TenantID).
			SetSourceWorkItemID(source.ID).
			SetTargetWorkItemID(target.ID).
			SetRelationType("investigated_by").
			SetCreatedByID(actor.UserID).
			SetDeletedAt(time.Now()).
			Save(context.Background())
		require.NoError(t, err)

		permission := CanConvertToProblem(ctx, actor, incidentEntityWithStatus(common.IncidentStatusInProgress, source.ID))
		require.True(t, permission.Allowed, permission.Reason)
	})

	require.NoError(t, client.Close())
	t.Run("relation lookup error", func(t *testing.T) {
		permission := CanConvertToProblem(ctx, actor, incidentEntityWithStatus(common.IncidentStatusInProgress, validWorkItem.ID))
		require.False(t, permission.Allowed)
		require.Equal(t, "无法确认事件是否已转为问题", permission.Reason)
	})
}

func TestHasIncidentProblemRelationUsesRequestContext(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	authorization.InvalidateAllPermissionCaches()
	setupCtx := context.Background()
	tenant, err := createIncidentTestTenant(setupCtx, client, "cancelled-context")
	require.NoError(t, err)
	actorUser, err := createIncidentTestUser(setupCtx, client, tenant.ID, "cancelled-context")
	require.NoError(t, err)
	workItem := createIncidentAuthorizationWorkItem(t, setupCtx, client, tenant.ID, actorUser.ID, "cancelled-context", "incident")
	actor := ActionActor{Client: client, TenantID: tenant.ID, UserID: actorUser.ID, Role: "super_admin"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = hasIncidentProblemRelation(ctx, actor, &ent.Incident{WorkItemID: workItem.ID})
	require.ErrorIs(t, err, context.Canceled)
}

func createIncidentAuthorizationWorkItem(
	t *testing.T,
	ctx context.Context,
	client *ent.Client,
	tenantID, requesterID int,
	suffix, recordClass string,
) *ent.Ticket {
	t.Helper()
	workItem, err := client.Ticket.Create().
		SetTitle("Authorization WorkItem " + suffix).
		SetTicketNumber("AUTH-WI-" + suffix).
		SetStatus("open").
		SetPriority("medium").
		SetRequesterID(requesterID).
		SetTenantID(tenantID).
		SetRecordClass(recordClass).
		Save(ctx)
	require.NoError(t, err)
	return workItem
}
