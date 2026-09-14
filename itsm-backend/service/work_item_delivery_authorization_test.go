package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"testing"
)

func TestWorkItemDeliveryCurrentAuthorization(t *testing.T) {
	for _, mode := range []string{"allow", "actor_inactive", "role_inactive", "read_revoked", "tenant_inactive", "role_timeout", "workitem_timeout"} {
		t.Run(mode, func(t *testing.T) {
			client, _, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "delivery")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "actor")
			require.NoError(t, err)
			actor.Update().SetRole("agent").ExecX(ctx)
			inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "delivery")
			role := client.Role.Create().SetTenantID(tenant.ID).SetCode("agent").SetName("Agent").SetIsActive(true).SaveX(ctx)
			permission := client.Permission.Create().SetTenantID(tenant.ID).SetCode("incident:read").SetName("Read").SetResource("incident").SetAction("read").SaveX(ctx)
			link := client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
			fail := ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) { return nil, context.DeadlineExceeded })
			})
			switch mode {
			case "actor_inactive":
				actor.Update().SetActive(false).ExecX(ctx)
			case "role_inactive":
				role.Update().SetIsActive(false).ExecX(ctx)
			case "read_revoked":
				client.RolePermission.DeleteOneID(link.ID).ExecX(ctx)
			case "tenant_inactive":
				tenant.Update().SetStatus("suspended").ExecX(ctx)
			case "role_timeout":
				client.Role.Intercept(fail)
			case "workitem_timeout":
				client.Ticket.Intercept(fail)
			}
			ctx = tenantctx.WithTenantID(ctx, tenant.ID)
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = authorizeWorkItemDelivery(ctx, tx, nil, actor.ID, tenant.ID, []int{inc.WorkItemID})
			if mode == "allow" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var blocked *outboxDeliveryBlockedError
			if mode == "role_timeout" || mode == "workitem_timeout" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.False(t, errors.As(err, &blocked))
			} else {
				require.True(t, errors.As(err, &blocked))
			}
		})
	}
}

func TestRelationNotificationContentUsesMutationEndpoint(t *testing.T) {
	mutation := &ent.Ticket{TicketNumber: "INC-CHANGED"}
	for _, removed := range []bool{false, true} {
		content := relationNotificationContent(RelationFacts{Type: "investigated_by", Removed: removed}, mutation)
		require.Contains(t, content, "INC-CHANGED")
		if removed {
			require.Contains(t, content, "解除")
		} else {
			require.Contains(t, content, "建立")
		}
	}
}

func TestWorkItemDeliveryErrorTaxonomy(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("temporary connection loss")} {
		actual := classifyWorkItemDeliveryError(err)
		require.ErrorIs(t, actual, err)
		var blocked *outboxDeliveryBlockedError
		require.False(t, errors.As(actual, &blocked))
	}
}

func TestRelationNotificationUsesOtherEndpointFromEitherMutationSide(t *testing.T) {
	items := map[int]*ent.Ticket{1: {TicketNumber: "INC-SOURCE"}, 2: {TicketNumber: "PRB-TARGET"}}
	for _, mutationID := range []int{1, 2} {
		for _, removed := range []bool{false, true} {
			facts := RelationFacts{SourceID: 1, TargetID: 2, MutationWorkItemID: mutationID, Type: "investigated_by", Removed: removed}
			counterpartID, err := relationCounterpart(facts)
			require.NoError(t, err)
			content := relationNotificationContent(facts, items[facts.MutationWorkItemID])
			require.Contains(t, content, items[mutationID].TicketNumber)
			require.NotContains(t, content, items[counterpartID].TicketNumber)
		}
	}
}

func TestOutcomeNotificationContentNamesChangedEndpoint(t *testing.T) {
	change := &ent.Ticket{TicketNumber: "CHG-RESULT"}
	problem := &ent.Ticket{TicketNumber: "PRB-TARGET"}
	incident := &ent.Ticket{TicketNumber: "INC-TARGET"}
	require.Equal(t, "变更 CHG-RESULT 已成功实施，请验证关联问题 PRB-TARGET 的修复结果。", changeOutcomeNotificationContent(change, problem))
	require.Equal(t, "关联问题 PRB-TARGET 已解决，请确认事件 INC-TARGET 的恢复结果。", problemResolvedNotificationContent(problem, incident))
}
