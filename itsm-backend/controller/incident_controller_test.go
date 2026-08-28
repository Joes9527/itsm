package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap/zaptest"
)

type fakeProblemConversionService struct {
	result      *problemDomain.Problem
	err         error
	tenantID    int
	incidentID  int
	actorUserID int
	req         dto.ConvertIncidentToProblemRequest
}

func (f *fakeProblemConversionService) CreateFromIncident(
	_ context.Context,
	tenantID, incidentID, actorUserID int,
	req dto.ConvertIncidentToProblemRequest,
) (*problemDomain.Problem, error) {
	f.tenantID = tenantID
	f.incidentID = incidentID
	f.actorUserID = actorUserID
	f.req = req
	return f.result, f.err
}

func setupTestIncidentController(t *testing.T) (*gin.Engine, *IncidentController) {
	gin.SetMode(gin.TestMode)

	// 创建内存数据库
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")

	// 创建 logger
	logger := zaptest.NewLogger(t).Sugar()

	// 创建服务
	incidentService := service.NewIncidentService(client, logger)

	// 创建控制器
	incidentController := NewIncidentController(incidentService, nil, nil, nil, nil, &fakeProblemConversionService{}, logger)

	// 创建路由
	r := gin.New()
	r.Use(gin.Recovery())

	// 注册路由
	r.GET("/api/v1/incidents", incidentController.ListIncidents)

	return r, incidentController
}

func TestConvertToProblemControllerUsesConversionServiceAndPublicMapper(t *testing.T) {
	workItemID := 912
	fake := &fakeProblemConversionService{result: &problemDomain.Problem{
		ID:          41,
		Title:       "Mapped domain problem",
		Description: "Mapped description",
		Status:      "open",
		Priority:    "high",
		Category:    "platform",
		RootCause:   "Mapped root cause",
		Impact:      "high",
		CreatedBy:   73,
		TenantID:    19,
		CreatedAt:   time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 8, 28, 10, 1, 0, 0, time.UTC),
		WorkItemID:  &workItemID,
	}}
	controller := NewIncidentController(nil, nil, nil, nil, nil, fake, zaptest.NewLogger(t).Sugar())
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", 19)
		c.Set("user_id", 73)
		c.Next()
	})
	router.POST("/api/v1/incidents/:id/convert-to-problem", controller.ConvertToProblem)

	body := []byte(`{"title":"Custom problem","description":"Custom description","rootCause":"Hypothesis"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/incidents/27/convert-to-problem", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var response common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, 0, response.Code)
	data, ok := response.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Mapped domain problem", data["title"])
	assert.EqualValues(t, workItemID, data["workItemId"])
	assert.Equal(t, "Mapped root cause", data["rootCause"])
	assert.NotContains(t, data, "work_item_id", "controller must return the public Problem DTO, not an Ent model")
	assert.Equal(t, 19, fake.tenantID)
	assert.Equal(t, 27, fake.incidentID)
	assert.Equal(t, 73, fake.actorUserID)
	assert.Equal(t, "Custom problem", fake.req.Title)
}

func TestIncidentController_ListIncidents(t *testing.T) {
	r, _ := setupTestIncidentController(t)

	tests := []struct {
		name           string
		queryParams    string
		expectedStatus int
	}{
		{
			name:           "成功获取事件列表",
			queryParams:    "",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "带分页参数",
			queryParams:    "page=1&pageSize=10",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/api/v1/incidents"
			if tt.queryParams != "" {
				path += "?" + tt.queryParams
			}

			req, err := http.NewRequest("GET", path, nil)
			require.NoError(t, err)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req
			c.Set("tenant_id", 1)

			r.ServeHTTP(w, req)
		})
	}
}
