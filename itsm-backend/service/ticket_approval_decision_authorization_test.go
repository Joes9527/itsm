package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func withApprovalDecisionRolePermissions(t *testing.T, role string, permissions []middleware.Permission) {
	t.Helper()
	previousMode := middleware.PermissionConfig.Mode
	previousPermissions, existed := middleware.RolePermissions[role]
	middleware.PermissionConfig.Mode = middleware.PermissionConfigModeHardcodeOnly
	middleware.RolePermissions[role] = permissions
	t.Cleanup(func() {
		middleware.PermissionConfig.Mode = previousMode
		if existed {
			middleware.RolePermissions[role] = previousPermissions
		} else {
			delete(middleware.RolePermissions, role)
		}
	})
}

func createApprovalDecisionWorkItem(t *testing.T, client *ent.Client, tenant *ent.Tenant, requester *ent.User, recordClass, suffix string) *ent.Ticket {
	t.Helper()
	workItem, err := client.Ticket.Create().
		SetTitle("Approval history " + suffix).
		SetTicketNumber(fmt.Sprintf("APPROVAL-HISTORY-%s-%d", suffix, time.Now().UnixNano())).
		SetType(recordClass).
		SetRecordClass(recordClass).
		SetRequesterID(requester.ID).
		SetTenantID(tenant.ID).
		Save(context.Background())
	require.NoError(t, err)
	return workItem
}

func TestTicketWorkflowServiceGetApprovalDecisionsUsesRecordClassReadPolicy(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:approval_history_record_class?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("Approval history tenant").SetCode("approval-history-tenant").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("approval-history-requester").SetEmail("approval-history-requester@test.local").SetName("Requester").SetPasswordHash("x").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).SaveX(ctx)
	workflowService := NewTicketWorkflowService(client, zaptest.NewLogger(t).Sugar())

	tests := []struct {
		name        string
		recordClass string
		resource    string
	}{
		{name: "generic", recordClass: "generic", resource: "ticket"},
		{name: "incident", recordClass: "incident", resource: "incident"},
		{name: "problem", recordClass: "problem", resource: "problem"},
		{name: "change request", recordClass: "change_request", resource: "change"},
		{name: "service request item", recordClass: "service_request_item", resource: "service_request"},
		{name: "catalog task", recordClass: "catalog_task", resource: "service_request"},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			role := fmt.Sprintf("approval_history_reader_%d", index)
			withApprovalDecisionRolePermissions(t, role, []middleware.Permission{{Resource: test.resource, Action: "read"}})
			workItem := createApprovalDecisionWorkItem(t, client, tenant, requester, test.recordClass, fmt.Sprintf("%d", index))

			decisions, err := workflowService.GetApprovalDecisions(ctx, workItem.ID, ActionActor{
				TenantID: tenant.ID,
				UserID:   requester.ID,
				Role:     role,
			})
			require.NoError(t, err)
			require.Empty(t, decisions)

			wrongRole := role + "_wrong"
			withApprovalDecisionRolePermissions(t, wrongRole, []middleware.Permission{{Resource: "unrelated", Action: "read"}})
			_, err = workflowService.GetApprovalDecisions(ctx, workItem.ID, ActionActor{
				TenantID: tenant.ID,
				UserID:   requester.ID,
				Role:     wrongRole,
			})
			var appErr *common.AppError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, common.ErrCodeForbidden, appErr.Code)
		})
	}
}

func TestTicketWorkflowServiceGetApprovalDecisionsHidesForeignAndDeletedWorkItems(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:approval_history_visibility?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("Approval tenant A").SetCode("approval-tenant-a").SetStatus("active").SaveX(ctx)
	otherTenant := client.Tenant.Create().SetName("Approval tenant B").SetCode("approval-tenant-b").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("approval-visibility-requester").SetEmail("approval-visibility-requester@test.local").SetName("Requester").SetPasswordHash("x").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).SaveX(ctx)
	workItem := createApprovalDecisionWorkItem(t, client, tenant, requester, "generic", "visibility")
	deleted := createApprovalDecisionWorkItem(t, client, tenant, requester, "generic", "deleted")
	client.Ticket.UpdateOneID(deleted.ID).SetDeletedAt(time.Now()).ExecX(ctx)

	role := "approval_visibility_reader"
	withApprovalDecisionRolePermissions(t, role, []middleware.Permission{{Resource: "ticket", Action: "read"}})
	workflowService := NewTicketWorkflowService(client, zaptest.NewLogger(t).Sugar())
	for name, tc := range map[string]struct {
		id       int
		tenantID int
	}{
		"foreign tenant": {id: workItem.ID, tenantID: otherTenant.ID},
		"soft deleted":   {id: deleted.ID, tenantID: tenant.ID},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := workflowService.GetApprovalDecisions(ctx, tc.id, ActionActor{TenantID: tc.tenantID, UserID: requester.ID, Role: role})
			var appErr *common.AppError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, common.ErrCodeNotFound, appErr.Code)
		})
	}
}
