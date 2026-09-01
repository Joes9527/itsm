package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestApprovalGatewayReadsApplicationVariableName is a regression test:
// The application code uniformly uses 'approval_required' as the process variable name,
// and the four deployed seed BPMN processes must read the same variable name, not different ones.
// This test ensures that approval gateways correctly route to approval nodes when approval_required=true.
func TestApprovalGatewayReadsApplicationVariableName(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	// Create a test tenant
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test-tenant").
		SetDomain("test.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	reader, err := client.User.Create().
		SetUsername("approval-gateway-reader").
		SetEmail("approval-gateway-reader@example.test").
		SetName("Approval Gateway Reader").
		SetPasswordHash("test").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	logger := zap.NewNop().Sugar()
	engine := NewCustomProcessEngine(client, logger)
	injectEngineChangeCallbackTestService(t, engine, client)

	deploymentSvc := NewBPMNTemplateService(client)

	_, err = deploymentSvc.LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)

	// Set tenant ID in context for process execution
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	ctx = WithBPMNAccessScope(ctx, BPMNAccessScope{UserID: reader.ID, TenantID: tenant.ID, CanReadAllTasks: true})

	cases := []struct {
		processKey   string
		approvalNode string
		skipNode     string
	}{
		{"service_request_flow", "Activity_Approval", "Activity_Execute"},
		{"change_normal_flow", "Activity_CABApproval", "Activity_Schedule"},
	}

	for _, tc := range cases {
		t.Run(tc.processKey, func(t *testing.T) {
			businessType := ""
			businessID := 0
			if tc.processKey == "change_normal_flow" {
				workItem, _ := createUserTaskCallbackChange(t, client, ctx, tenant.ID, reader.ID, "CHG-GATE-"+tc.processKey)
				businessType, businessID = "change", workItem.ID
			}
			// Test case 1: approval_required=true should route to approval node
			t.Run("approval_required=true", func(t *testing.T) {
				instance, err := engine.StartProcess(ctx, tc.processKey, "test-business-key-approval-true-"+tc.processKey, businessType, businessID, map[string]interface{}{
					"approval_required": true,
				})
				require.NoError(t, err)

				// Get the current task (should be at first UserTask, e.g., Activity_Accept or Activity_Assessment)
				tasks, _, err := engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
					ProcessInstanceID: instance.ID,
					PageSize:          10,
				})
				require.NoError(t, err)
				require.Len(t, tasks, 1, "should have exactly one task pending after process start")

				// Complete the first task to let the gateway route
				err = engine.CompleteTask(ctx, tasks[0].TaskID, map[string]interface{}{})
				require.NoError(t, err)

				// Reload instance from DB to check where it advanced to after gateway routing
				updated, err := client.ProcessInstance.Get(ctx, instance.ID)
				require.NoError(t, err)

				require.Equal(t, tc.approvalNode, updated.CurrentActivityID,
					"approval_required=true should route to approval node at the gateway, not skip approval and move forward")
			})

			// Test case 2: approval_required=false should skip approval node and go to skip node
			t.Run("approval_required=false", func(t *testing.T) {
				instance, err := engine.StartProcess(ctx, tc.processKey, "test-business-key-approval-false-"+tc.processKey, businessType, businessID, map[string]interface{}{
					"approval_required": false,
				})
				require.NoError(t, err)

				// Get the current task (should be at first UserTask)
				tasks, _, err := engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
					ProcessInstanceID: instance.ID,
					PageSize:          10,
				})
				require.NoError(t, err)
				require.Len(t, tasks, 1, "should have exactly one task pending after process start")

				// Complete the first task to let the gateway route
				err = engine.CompleteTask(ctx, tasks[0].TaskID, map[string]interface{}{})
				require.NoError(t, err)

				// Reload instance from DB to check where it advanced to after gateway routing
				updated, err := client.ProcessInstance.Get(ctx, instance.ID)
				require.NoError(t, err)

				require.Equal(t, tc.skipNode, updated.CurrentActivityID,
					"approval_required=false should skip approval node and route to skip node")
			})
		})
	}
}
