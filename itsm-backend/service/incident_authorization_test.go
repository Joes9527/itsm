package service

import (
	"context"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestBuildIncidentActionsMirrorsIncidentCommandRules(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	middleware.InvalidateAllPermissionCaches()

	workItemID := 101
	actor := ActionActor{Client: client, TenantID: 1, UserID: 7, Role: "super_admin"}
	inProgress := &ent.Incident{Status: common.IncidentStatusInProgress, WorkItemID: workItemID}
	resolved := &ent.Incident{Status: common.IncidentStatusResolved, WorkItemID: workItemID}
	closed := &ent.Incident{Status: common.IncidentStatusClosed, WorkItemID: workItemID}

	require.True(t, BuildIncidentActions(actor, inProgress)["resolve"].Allowed)
	require.False(t, BuildIncidentActions(actor, resolved)["resolve"].Allowed)
	require.True(t, BuildIncidentActions(actor, closed)["reopen"].Allowed)
	require.False(t, BuildIncidentActions(actor, closed)["assign"].Allowed)
}

func TestCanConvertToProblemFailsClosed(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	middleware.InvalidateAllPermissionCaches()
	actor := ActionActor{Client: client, TenantID: 1, UserID: 7, Role: "super_admin"}

	t.Run("missing WorkItem", func(t *testing.T) {
		permission := CanConvertToProblem(actor, &ent.Incident{Status: common.IncidentStatusInProgress})
		require.False(t, permission.Allowed)
		require.NotEmpty(t, permission.Reason)
	})

	t.Run("live investigated_by relation", func(t *testing.T) {
		_, err := client.WorkItemRelation.Create().
			SetTenantID(actor.TenantID).
			SetSourceWorkItemID(101).
			SetTargetWorkItemID(202).
			SetRelationType("investigated_by").
			SetCreatedByID(actor.UserID).
			Save(context.Background())
		require.NoError(t, err)

		permission := CanConvertToProblem(actor, &ent.Incident{
			Status:     common.IncidentStatusInProgress,
			WorkItemID: 101,
		})
		require.False(t, permission.Allowed)
		require.Equal(t, "已经转为问题", permission.Reason)
	})

	t.Run("relation lookup is tenant scoped and live only", func(t *testing.T) {
		_, err := client.WorkItemRelation.Create().
			SetTenantID(actor.TenantID + 1).
			SetSourceWorkItemID(303).
			SetTargetWorkItemID(404).
			SetRelationType("investigated_by").
			SetCreatedByID(actor.UserID).
			Save(context.Background())
		require.NoError(t, err)
		_, err = client.WorkItemRelation.Create().
			SetTenantID(actor.TenantID).
			SetSourceWorkItemID(303).
			SetTargetWorkItemID(505).
			SetRelationType("investigated_by").
			SetCreatedByID(actor.UserID).
			SetDeletedAt(time.Now()).
			Save(context.Background())
		require.NoError(t, err)

		permission := CanConvertToProblem(actor, &ent.Incident{
			Status:     common.IncidentStatusInProgress,
			WorkItemID: 303,
		})
		require.True(t, permission.Allowed, permission.Reason)
	})

	require.NoError(t, client.Close())
	t.Run("relation lookup error", func(t *testing.T) {
		permission := CanConvertToProblem(actor, &ent.Incident{
			Status:     common.IncidentStatusInProgress,
			WorkItemID: 303,
		})
		require.False(t, permission.Allowed)
		require.Equal(t, "无法确认事件是否已转为问题", permission.Reason)
	})
}
