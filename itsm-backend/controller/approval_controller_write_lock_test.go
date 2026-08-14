package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupWriteLockControllerTest(t *testing.T) (*gin.Engine, *ent.Client) {
	gin.SetMode(gin.TestMode)
	dsn := "file:" + filepath.Join(t.TempDir(), "approval_write_lock_test.db") + "?_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	logger := zaptest.NewLogger(t).Sugar()
	approvalService := service.NewApprovalService(client, logger)
	approvalController := NewApprovalController(approvalService)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", 1)
		c.Set("user_id", 1)
		c.Next()
	})
	r.POST("/api/v1/approval-workflows", approvalController.CreateWorkflow)
	r.PUT("/api/v1/approval-workflows/:id", approvalController.UpdateWorkflow)
	r.PATCH("/api/v1/approval-workflows/:id", approvalController.PatchWorkflow)
	r.DELETE("/api/v1/approval-workflows/:id", approvalController.DeleteWorkflow)
	r.GET("/api/v1/approval-workflows", approvalController.ListWorkflows)
	r.POST("/api/v1/approval/submit", approvalController.SubmitApproval)

	return r, client
}

func lockControllerTestTenant(t *testing.T, client *ent.Client, tenantID int) {
	t.Helper()
	_, err := client.SystemConfig.Create().
		SetKey("legacyApprovalWriteLocked").
		SetValue("true").
		SetValueType("boolean").
		SetCategory("approval").
		SetTenantID(tenantID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(context.Background())
	require.NoError(t, err)
}

type createWorkflowBody struct {
	Name     string                    `json:"name"`
	IsActive bool                      `json:"isActive"`
	Nodes    []map[string]interface{} `json:"nodes"`
}

// 这些测试都走真实的 ctx.ShouldBindJSON，所以节点必须带上 dto.ApprovalNodeRequest
// 标了 binding:"required" 的三个字段（approverType/approvalMode/rejectAction，
// dto/ticket_approval_dto.go:24-39）——漏掉任何一个都会在到达锁检查之前就被参数校验
// 挡掉，返回 400 而不是这里要测的 403/200。

func TestApprovalController_CreateWorkflow_LockedTenantReturns403(t *testing.T) {
	r, client := setupWriteLockControllerTest(t)
	lockControllerTestTenant(t, client, 1)

	body, _ := json.Marshal(createWorkflowBody{
		Name:     "应该被拒绝",
		IsActive: true,
		Nodes: []map[string]interface{}{
			{"level": 1, "name": "审批", "approverType": "user", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any", "rejectAction": "end"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approval-workflows", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2003, resp.Code)
	assert.Contains(t, resp.Message, "已下线")
}

func TestApprovalController_DeleteWorkflow_LockedTenantReturns403(t *testing.T) {
	r, client := setupWriteLockControllerTest(t)

	// 先在未锁定状态下创建一条。
	body, _ := json.Marshal(createWorkflowBody{
		Name:     "先创建后锁定",
		IsActive: true,
		Nodes: []map[string]interface{}{
			{"level": 1, "name": "审批", "approverType": "user", "assigneeType": "user", "assigneeValue": "1", "approvalMode": "any", "rejectAction": "end"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approval-workflows", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var createResp struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))

	lockControllerTestTenant(t, client, 1)

	delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/approval-workflows/%d", createResp.Data.ID), nil)
	delW := httptest.NewRecorder()
	r.ServeHTTP(delW, delReq)

	require.Equal(t, http.StatusForbidden, delW.Code)
}

func TestApprovalController_ListWorkflows_UnaffectedByLock(t *testing.T) {
	r, client := setupWriteLockControllerTest(t)
	lockControllerTestTenant(t, client, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/approval-workflows", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "锁定不应该影响只读的 ListWorkflows")
}

func TestApprovalController_SubmitApproval_UnaffectedByLock(t *testing.T) {
	r, client := setupWriteLockControllerTest(t)
	lockControllerTestTenant(t, client, 1)

	body, _ := json.Marshal(map[string]interface{}{
		"ticketId":   1,
		"approvalId": 999, // 不存在的 ID——这里只验证请求不会因为"锁定"被拦在半路，
		                    // 会正常往后走到 approvalService.SubmitApproval 内部报
		                    // "找不到审批记录" 之类的业务错误，不是被 403 拦截。
		"action":     "approve",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approval/submit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusForbidden, w.Code, "锁定不应该拦截 SubmitApproval")
}
