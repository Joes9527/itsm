package change

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/predicate"
	"itsm-backend/middleware"
)

// setupTestHandler creates a test handler with in-memory repository
func setupTestHandler(t *testing.T) (*gin.Engine, *Handler, *mockRepository) {
	gin.SetMode(gin.TestMode)

	logger := zaptest.NewLogger(t).Sugar()
	repo := newMockRepository()
	svc := NewService(repo, nil, logger)
	handler := NewHandler(svc)

	r := gin.New()
	r.Use(gin.Recovery())

	// Add auth middleware mock
	r.Use(func(c *gin.Context) {
		if tid := c.GetHeader("X-Tenant-ID"); tid != "" {
			if id, err := strconv.Atoi(tid); err == nil {
				c.Set("tenant_id", id)
				c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: id})
			}
		} else {
			c.Set("tenant_id", 1)
			c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: 1})
		}
		if uid := c.GetHeader("X-User-ID"); uid != "" {
			if id, err := strconv.Atoi(uid); err == nil {
				c.Set("user_id", id)
			}
		} else {
			c.Set("user_id", 1)
		}
		if role := c.GetHeader("X-User-Role"); role != "" {
			c.Set("role", role)
		} else {
			c.Set("role", "super_admin")
		}
		if customer := c.GetHeader("X-MSP-Customer-ID"); customer != "" {
			if customerID, err := strconv.Atoi(customer); err == nil {
				mspCtx := &middleware.MSPContext{
					IsMSP:            true,
					MSPUserID:        c.GetInt("user_id"),
					CustomerTenantID: &customerID,
				}
				if allowed := c.GetHeader("X-MSP-Allowed-Customer-ID"); allowed != "" {
					if allowedID, err := strconv.Atoi(allowed); err == nil {
						mspCtx.AllowedCustomers = []int{allowedID}
					}
				}
				c.Set(middleware.MSPContextKey, mspCtx)
			}
		}
		c.Next()
	})

	// Register routes
	r.GET("/api/v1/changes", handler.ListChanges)
	r.POST("/api/v1/changes", handler.CreateChange)
	r.GET("/api/v1/changes/:id", handler.GetChange)
	r.PUT("/api/v1/changes/:id", handler.UpdateChange)
	r.DELETE("/api/v1/changes/:id", handler.DeleteChange)
	r.GET("/api/v1/changes/stats", handler.GetStats)
	r.POST("/api/v1/changes/:id/submit", handler.ExecuteAction)
	r.POST("/api/v1/changes/:id/assign", handler.AssignChange)
	r.GET("/api/v1/changes/:id/risk-assessment", handler.GetRiskAssessment)
	r.GET("/api/v1/changes/:id/cmdb-impact", handler.GetCMDBImpactSummary)

	return r, handler, repo
}

// mockRepository implements Repository interface for testing
type mockRepository struct {
	changes    map[int]*Change
	approvals  map[int]*ApprovalRecord
	riskAssess map[int]*RiskAssessment
	nextID     int
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		changes:    make(map[int]*Change),
		approvals:  make(map[int]*ApprovalRecord),
		riskAssess: make(map[int]*RiskAssessment),
		nextID:     1,
	}
}

func (m *mockRepository) Create(ctx context.Context, c *Change) (*Change, error) {
	c.ID = m.nextID
	m.nextID++
	c.CreatedAt = time.Now()
	c.UpdatedAt = time.Now()
	m.changes[c.ID] = c
	return c, nil
}

func (m *mockRepository) Get(ctx context.Context, id int, tenantID int) (*Change, error) {
	c, ok := m.changes[id]
	if !ok || c.TenantID != tenantID {
		return nil, http.ErrMissingFile
	}
	return c, nil
}

func (m *mockRepository) List(ctx context.Context, tenantID int, page, size int, status, search, riskLevel string, scope ...predicate.Ticket) ([]*Change, int, error) {
	var result []*Change
	for _, c := range m.changes {
		if c.TenantID != tenantID {
			continue
		}
		if status != "" && c.Status != status {
			continue
		}
		if riskLevel != "" && c.RiskLevel != riskLevel {
			continue
		}
		result = append(result, c)
	}
	return result, len(result), nil
}

func (m *mockRepository) GetStats(ctx context.Context, tenantID int) (*Stats, error) {
	stats := &Stats{}
	for _, c := range m.changes {
		if c.TenantID == tenantID {
			stats.Total++
			switch c.Status {
			case "pending":
				stats.Pending++
			case "approved":
				stats.Approved++
			case "in_progress":
				stats.InProgress++
			case "completed":
				stats.Completed++
			case "rolled_back":
				stats.RolledBack++
			case "rejected":
				stats.Rejected++
			case "cancelled":
				stats.Cancelled++
			}
		}
	}
	return stats, nil
}

func (m *mockRepository) GetApprovalHistory(ctx context.Context, changeID int, tenantID int) ([]*ApprovalRecord, error) {
	var result []*ApprovalRecord
	for _, a := range m.approvals {
		if a.ChangeID == changeID {
			result = append(result, a)
		}
	}
	return result, nil
}

func (m *mockRepository) ListByDateRange(ctx context.Context, tenantID int, startDate, endDate, status string) ([]*Change, error) {
	var result []*Change
	for _, c := range m.changes {
		if c.TenantID == tenantID {
			if status == "" || c.Status == status {
				result = append(result, c)
			}
		}
	}
	return result, nil
}

// Helper function to create test change
func createTestChange(repo *mockRepository, tenantID, userID int) *Change {
	c := &Change{
		Title:         "Test Change",
		Description:   "Test Description",
		Justification: "Test Justification",
		Type:          "normal",
		Status:        "draft",
		Priority:      "medium",
		ImpactScope:   "low",
		RiskLevel:     "low",
		CreatedBy:     userID,
		TenantID:      tenantID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	// Use dynamic ID instead of hardcoded 1
	c.ID = repo.nextID
	repo.changes[c.ID] = c
	repo.nextID++
	return c
}

// TestChangeController_ListChanges tests GET /api/v1/changes
func TestChangeController_ListChanges(t *testing.T) {
	_, r := newGovernedHandlerFixture(t)

	tests := []struct {
		name           string
		queryParams    string
		expectedStatus int
		expectedCode   int
	}{
		{
			name:           "成功获取变更列表",
			queryParams:    "",
			expectedStatus: http.StatusOK,
			expectedCode:   common.SuccessCode,
		},
		{
			name:           "带分页参数",
			queryParams:    "?page=1&pageSize=10",
			expectedStatus: http.StatusOK,
			expectedCode:   common.SuccessCode,
		},
		{
			name:           "按状态筛选",
			queryParams:    "?status=draft",
			expectedStatus: http.StatusOK,
			expectedCode:   common.SuccessCode,
		},
		{
			name:           "按风险等级筛选(snake_case)",
			queryParams:    "?risk_level=low",
			expectedStatus: http.StatusOK,
			expectedCode:   common.SuccessCode,
		},
		{
			name:           "按风险等级筛选(camelCase)",
			queryParams:    "?riskLevel=low",
			expectedStatus: http.StatusOK,
			expectedCode:   common.SuccessCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/api/v1/changes"+tt.queryParams, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response common.Response
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedCode, response.Code)

			if response.Code == common.SuccessCode {
				data := response.Data.(map[string]interface{})
				assert.Contains(t, data, "changes")
				assert.Contains(t, data, "total")
			}
		})
	}
}

func TestChangeService_GetCMDBImpactSummary_WithoutEntClient(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	repo := newMockRepository()
	createTestChange(repo, 1, 1)

	svc := NewService(repo, nil, logger)
	_, err := svc.GetCMDBImpactSummary(context.Background(), 1, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CMDB impact summary unavailable")
}

// TestChangeController_CreateChange tests POST /api/v1/changes. Creation now
// goes through the real shared Intake application: the HTTP route returns the
// canonical CreateWorkItemResult envelope (workItemId/number/recordClass/...),
// not the legacy Change DTO fields, so this needs a real ent-backed handler
// (mockRepository/nil client cannot satisfy the real Prepare/CreateExtension
// transaction) and repository inspection to assert persisted fields. This does not exercise
// detail HTTP authorization, which requires a separate endpoint check.
func TestChangeController_CreateChange(t *testing.T) {
	tests := []struct {
		name           string
		request        dto.CreateChangeRequest
		expectedStatus int
		expectedCode   int
	}{
		{
			name: "成功创建变更",
			request: dto.CreateChangeRequest{
				Title:              "新变更请求",
				Description:        "变更描述",
				Justification:      "变更理由",
				ImplementationPlan: "备份配置，应用变更并验证服务", RollbackPlan: "恢复变更前配置并验证服务",
				Type:        "normal",
				Priority:    "medium",
				ImpactScope: "low",
				RiskLevel:   "low",
			},
			expectedStatus: http.StatusCreated,
			expectedCode:   common.SuccessCode,
		},
		{
			name: "带计划时间的变更",
			request: dto.CreateChangeRequest{
				Title:              "计划变更",
				Description:        "带计划时间",
				Justification:      "理由",
				Type:               "standard",
				Priority:           "high",
				ImpactScope:        "medium",
				RiskLevel:          "medium",
				ImplementationPlan: "实施计划",
				RollbackPlan:       "回滚计划",
			},
			expectedStatus: http.StatusCreated,
			expectedCode:   common.SuccessCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, repo, _, tenantID, _ := setupChangeRegressionHandler(t, "change_controller_create_"+tt.name, "change_controller_create_"+tt.name)

			requestBody, err := json.Marshal(tt.request)
			require.NoError(t, err)

			req, _ := http.NewRequest("POST", "/api/v1/changes", bytes.NewBuffer(requestBody))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", uuid.NewString())

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response common.Response
			err = json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedCode, response.Code)

			if response.Code == common.SuccessCode {
				data := response.Data.(map[string]interface{})
				professionalReference := data["professionalReference"].(map[string]interface{})
				changeID := int(professionalReference["id"].(float64))
				stored, err := repo.Get(context.Background(), changeID, tenantID)
				require.NoError(t, err)
				assert.Equal(t, tt.request.Title, stored.Title)
				assert.Equal(t, "draft", stored.Status)
			}
		})
	}
}

// TestChangeController_GetChange tests GET /api/v1/changes/:id
func TestChangeController_GetChange(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	for _, test := range []struct {
		id   string
		code int
	}{{fmt.Sprint(f.record.ID), 200}, {"invalid", 400}, {"0", 400}, {"99999", 404}} {
		w := governedHTTP(r, "GET", "/api/v1/changes/"+test.id, "", nil)
		require.Equal(t, test.code, w.Code, w.Body.String())
	}

}

func TestChangeController_GetChangeIncludesDetailActionsOnly(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	w := governedHTTP(r, "GET", fmt.Sprintf("/api/v1/changes/%d", f.record.ID), "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var body struct {
		Data dto.ChangeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, 1, body.Data.Version)
	require.True(t, body.Data.Actions["submit"].Allowed)
	require.False(t, body.Data.Actions["approve"].Allowed)
	require.Empty(t, body.Data.CurrentTasks)
	w = governedHTTP(r, "GET", "/api/v1/changes", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), `"actions"`)

}

func TestChangeController_GetChangeUsesResolvedMSPTenant(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	headers := governedMSPHeaders(t, f)
	w := governedHTTP(r, "GET", fmt.Sprintf("/api/v1/changes/%d", f.record.ID), "", headers)
	require.Equal(t, 404, w.Code, w.Body.String(), "allocation does not grant row scope for an unassigned request")
	actorID, parseErr := strconv.Atoi(headers["X-User-ID"])
	require.NoError(t, parseErr)
	f.client.User.UpdateOneID(actorID).SetRole("super_admin").ClearMspRole().ExecX(f.ctx)
	w = governedHTTP(r, "GET", fmt.Sprintf("/api/v1/changes/%d", f.record.ID), "", headers)
	require.Equal(t, 200, w.Code, w.Body.String())
	var body struct {
		Data dto.ChangeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, f.tenant, body.Data.TenantID)

}

func TestChangeController_GetChangeDeniesUnauthorizedMSPTenant(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	headers := governedMSPHeaders(t, f)
	f.client.MSPAllocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
	w := governedHTTP(r, "GET", fmt.Sprintf("/api/v1/changes/%d", f.record.ID), "", headers)
	require.Equal(t, 403, w.Code, w.Body.String())

}

func TestChangeControllerMutationsUseResolvedMSPTenant(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	headers := governedMSPHeaders(t, f)
	base := fmt.Sprintf("/api/v1/changes/%d", f.record.ID)
	w := governedHTTP(r, "PUT", base, `{"expectedVersion":1,"operationId":"msp-edit","title":"MSP title"}`, headers)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "MSP title", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Title)
	w = governedHTTP(r, "POST", base+"/assign", fmt.Sprintf(`{"expectedVersion":2,"operationId":"msp-assign","assigneeId":%d}`, f.requester), headers)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, f.requester, f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).AssigneeID)

}

func TestChangeController_GetChangeRejectsInvalidActionActorContext(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	base := fmt.Sprintf("/api/v1/changes/%d", f.record.ID)
	for _, headers := range []map[string]string{{"X-User-ID": "0"}, {"X-User-ID": "invalid"}, {"X-Tenant-ID": "bad"}} {
		w := governedHTTP(r, "GET", base, "", headers)
		require.Equal(t, 401, w.Code, w.Body.String())
	}
	w := governedHTTP(r, "GET", base, "", map[string]string{"X-User-Role": "stale-role"})
	require.Equal(t, 200, w.Code, w.Body.String(), "current persisted role owns authorization")

}

// TestChangeController_UpdateChange tests PUT /api/v1/changes/:id
func TestChangeController_UpdateChange(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	base := fmt.Sprintf("/api/v1/changes/%d", f.record.ID)
	w := governedHTTP(r, "PUT", base, `{"expectedVersion":1,"operationId":"edit","title":"Updated"}`, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "Updated", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Title)
	replay := governedHTTP(r, "PUT", base, `{"expectedVersion":1,"operationId":"edit","title":"Updated"}`, nil)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.Contains(t, replay.Body.String(), `"replayed":true`)
	w = governedHTTP(r, "PUT", base, `{"expectedVersion":1,"operationId":"stale","title":"Other"}`, nil)
	require.Equal(t, 409, w.Code, w.Body.String())
	w = governedHTTP(r, "PUT", base, `{"title":"No key"}`, nil)
	require.Equal(t, 400, w.Code, w.Body.String())

}

// TestChangeController_DeleteChange tests DELETE /api/v1/changes/:id
func TestChangeController_DeleteChange(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	for _, tc := range []struct {
		id     string
		status int
	}{{"invalid", 400}, {"99999", 404}, {fmt.Sprint(f.record.ID), 200}} {
		w := governedHTTP(r, "DELETE", "/api/v1/changes/"+tc.id, "", nil)
		require.Equal(t, tc.status, w.Code, w.Body.String())
		var response common.Response
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		expected := map[int]int{200: common.SuccessCode, 400: common.ParamErrorCode, 404: common.NotFoundErrorCode}
		require.Equal(t, expected[tc.status], response.Code)
	}
	require.NotNil(t, f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).DeletedAt)
}

func TestChangeController_GetStats(t *testing.T) {
	r, _, repo := setupTestHandler(t)

	// Create test data with different statuses
	for i, status := range []string{"draft", "pending", "approved", "in_progress", "completed"} {
		c := &Change{
			ID:        i + 1,
			Title:     "Change " + strconv.Itoa(i),
			Status:    status,
			TenantID:  1,
			CreatedBy: 1,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		repo.changes[c.ID] = c
	}

	req, _ := http.NewRequest("GET", "/api/v1/changes/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response common.Response
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, common.SuccessCode, response.Code)

	data := response.Data.(map[string]interface{})
	// Stats struct uses camelCase/lowercase JSON tags
	assert.Contains(t, data, "total")
}

// TestChangeController_SubmitChange tests POST /api/v1/changes/:id/submit
func TestChangeController_SubmitChange(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	w := governedHTTP(r, "POST", fmt.Sprintf("/api/v1/changes/%d/submit", f.record.ID), `{"expectedVersion":1,"operationId":"submit"}`, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"submitted"`)
	require.Equal(t, "Activity_Assessment", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)

}

// TestChangeController_AssignChange tests POST /api/v1/changes/:id/assign
func TestChangeController_AssignChange(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)
	base := fmt.Sprintf("/api/v1/changes/%d/assign", f.record.ID)
	w := governedHTTP(r, "POST", base, fmt.Sprintf(`{"expectedVersion":1,"operationId":"assign","assigneeId":%d}`, f.approver), nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, f.approver, f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).AssigneeID)
	w = governedHTTP(r, "POST", base, `{"expectedVersion":2,"operationId":"invalid","assigneeId":999}`, nil)
	require.Equal(t, 400, w.Code, w.Body.String())

}

func TestSubmitChangeAtomicFailureLeavesDraftUnchanged(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, ok := m.(*ent.AuditLogMutation); ok {
				return nil, errors.New("receipt unavailable")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err := f.svc.ApplyCommand(f.ctx, f.command("submit", f.requester))
	require.ErrorContains(t, err, "receipt unavailable")
	require.Equal(t, "draft", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))

}

// TestChangeController_GetRiskAssessment tests GET /api/v1/changes/:id/risk-assessment
func TestChangeController_GetRiskAssessment(t *testing.T) {
	f, r := newGovernedHandlerFixture(t)

	_, err := f.client.ExecContext(f.ctx, `INSERT INTO change_risk_assessments(id,change_id,tenant_id,risk_description,created_at,updated_at) VALUES(1,$1,$2,'observed',$3,$3)`, f.record.ID, f.tenant, time.Now())
	require.NoError(t, err)
	w := governedHTTP(r, "GET", fmt.Sprintf("/api/v1/changes/%d/risk-assessment", f.record.ID), "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"riskDescription":"observed"`)
	require.Contains(t, w.Body.String(), `"riskLevel":"medium"`)

}

// Helper functions
func strPtr(s string) *string {
	return &s
}

func ptrChangePriority(p dto.ChangePriority) *dto.ChangePriority {
	return &p
}

// ===================== CMDB Impact Summary Helper Tests =====================

func TestRecommendRiskLevel(t *testing.T) {
	cases := []struct {
		name          string
		totalCIs      int
		criticalCIs   int
		highRiskDeps  int
		openIncidents int
		changeType    string
		want          string
	}{
		{"emergency overrides everything", 1, 0, 0, 0, "emergency", "high"},
		{"critical CI wins", 1, 1, 0, 0, "normal", "high"},
		{"high risk deps >= 4", 3, 0, 4, 0, "normal", "high"},
		{"open incidents >= 2", 3, 0, 0, 2, "normal", "high"},
		{"medium: 5+ CIs", 5, 0, 0, 0, "normal", "medium"},
		{"medium: 1 high risk dep", 2, 0, 1, 0, "normal", "medium"},
		{"medium: 1 open incident", 2, 0, 0, 1, "normal", "medium"},
		{"low: nothing matches", 2, 0, 0, 0, "normal", "low"},
		{"low: 0 CI", 0, 0, 0, 0, "normal", "low"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recommendRiskLevel(tc.totalCIs, tc.criticalCIs, tc.highRiskDeps, tc.openIncidents, tc.changeType)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRecommendImpactScope(t *testing.T) {
	cases := []struct {
		name         string
		totalCIs     int
		criticalCIs  int
		highRiskDeps int
		want         string
	}{
		{"critical CI triggers high", 1, 1, 0, "high"},
		{"5+ CIs triggers high", 5, 0, 0, "high"},
		{"3+ high risk deps triggers high", 2, 0, 3, "high"},
		{"2 CIs triggers medium", 2, 0, 0, "medium"},
		{"1 high risk dep triggers medium", 1, 0, 1, "medium"},
		{"nothing triggers low", 1, 0, 0, "low"},
		{"empty triggers low", 0, 0, 0, "low"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recommendImpactScope(tc.totalCIs, tc.criticalCIs, tc.highRiskDeps)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBuildWorkflowHints(t *testing.T) {
	t.Run("no CIs, not emergency", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{TotalAffectedCIs: 0}
		hints := buildWorkflowHints(summary, "normal")
		assert.Contains(t, hints, "补充受影响 CI 后再发起审批，以便自动执行风险分流。")
		assert.NotContains(t, hints, "紧急变更建议启用快速审批路径，并在实施后自动创建 PIR 任务。")
	})

	t.Run("critical CI triggers CAB hint", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{TotalAffectedCIs: 1, CriticalCICount: 1}
		hints := buildWorkflowHints(summary, "normal")
		assert.Contains(t, hints, "命中关键 CI，建议走 CAB 审批并校验变更窗口。")
	})

	t.Run("open incidents trigger conflict check hint", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{TotalAffectedCIs: 1, OpenIncidentCount: 1}
		hints := buildWorkflowHints(summary, "normal")
		assert.Contains(t, hints, "受影响 CI 当前存在未关闭事件，建议先做冲突检查和实施前健康确认。")
	})

	t.Run("high risk deps trigger rollback drill hint", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{TotalAffectedCIs: 1, HighRiskDependencyCount: 1}
		hints := buildWorkflowHints(summary, "normal")
		assert.Contains(t, hints, "存在高风险依赖，建议在工作流中增加影响确认和回滚演练节点。")
	})

	t.Run("emergency triggers fast track hint", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{TotalAffectedCIs: 1}
		hints := buildWorkflowHints(summary, "emergency")
		assert.Contains(t, hints, "紧急变更建议启用快速审批路径，并在实施后自动创建 PIR 任务。")
	})

	t.Run("requires backout plan triggers integrity hint", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{TotalAffectedCIs: 1, RequiresBackoutPlan: true}
		hints := buildWorkflowHints(summary, "normal")
		assert.Contains(t, hints, "建议在提交流程前强制校验回滚计划与实施计划完整性。")
	})

	t.Run("combined all triggers all hints", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{
			TotalAffectedCIs:        2,
			CriticalCICount:         1,
			HighRiskDependencyCount: 2,
			OpenIncidentCount:       1,
			RequiresBackoutPlan:     true,
		}
		hints := buildWorkflowHints(summary, "emergency")
		// 5 个触发条件（除 TotalAffectedCIs==0 分支）：critical + open incident + high risk dep + emergency + backout plan
		assert.Len(t, hints, 5)
	})
}

func TestInferITILPractices(t *testing.T) {
	t.Run("all triggers all 4 practices", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{
			CriticalCICount:         1,
			HighRiskDependencyCount: 1,
			OpenIncidentCount:       1,
			RequiresCAB:             true,
		}
		got := inferITILPractices(summary)
		assert.ElementsMatch(t, []string{
			"incident_management",
			"risk_management",
			"change_enablement",
			"monitoring_and_event_management",
		}, got)
	})

	t.Run("only incident triggers 1 practice", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{OpenIncidentCount: 1}
		got := inferITILPractices(summary)
		assert.Equal(t, []string{"incident_management"}, got)
	})

	t.Run("nothing triggers empty", func(t *testing.T) {
		summary := &dto.ChangeCMDBImpactSummary{}
		got := inferITILPractices(summary)
		assert.Empty(t, got)
	})
}
