package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/cmdb"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/handlers/service_request"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupServiceCatalogFieldsRouter(t *testing.T) (*gin.Engine, *ent.Tenant, *ent.User) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:sc_fields_e2e?mode=memory&cache=shared&_fk=1")
	logger := zaptest.NewLogger(t).Sugar()

	ctx := t.Context()
	tenant, err := client.Tenant.Create().SetName("SC Fields Tenant").SetCode("SCFIELDS").SetDomain("scfields.test.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().
		SetUsername("scfields_user").SetEmail("scfields@test.com").SetPasswordHash("hash").
		SetName("SC Fields User").SetDepartment("IT").SetPhone("1234567890").
		SetActive(true).SetRole("admin").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, logger)
	scHandler := service_catalog.NewHandler(scService)

	srRepo := service_request.NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	srService := service_request.NewService(srRepo, scRepo, cmdbRepo, client, logger)
	srHandler := service_request.NewHandler(srService)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("tenant_id", tenant.ID)
		c.Set("user_id", user.ID)
		c.Set("role", "admin")
		c.Next()
	})
	r.POST("/api/v1/service-catalogs", scHandler.Create)
	r.GET("/api/v1/service-catalogs/:id", scHandler.Get)
	r.POST("/api/v1/service-requests", srHandler.Create)
	r.GET("/api/v1/service-requests/:id", srHandler.Get)

	return r, tenant, user
}

func doServiceCatalogFieldsRequest(t *testing.T, r http.Handler, method, path string, body interface{}) (apiEnvelope, int) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env apiEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env), "response body=%s", w.Body.String())
	return env, w.Code
}

// TestServiceCatalogFields 跑通"建服务目录（带1个自定义字段）-> 员工提交服务请求（填该字段）
// -> 请求详情 customFields 展示正确 -> 列表不带 customFields"的完整链路。
func TestServiceCatalogFields(t *testing.T) {
	r, _, _ := setupServiceCatalogFieldsRouter(t)

	createCatalogReq := map[string]interface{}{
		"name": "云主机申请", "category": "云服务", "description": "测试",
		"fields": []map[string]interface{}{
			{"name": "environment", "label": "环境", "type": "text", "required": true},
		},
	}
	env, status := doServiceCatalogFieldsRequest(t, r, http.MethodPost, "/api/v1/service-catalogs", createCatalogReq)
	require.Equal(t, http.StatusOK, status, "message=%s", env.Message)
	require.Equal(t, 0, env.Code)
	var catalogResp struct {
		ID     int                      `json:"id"`
		Fields []map[string]interface{} `json:"fields"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &catalogResp))
	require.Len(t, catalogResp.Fields, 1)
	catalogID := catalogResp.ID

	createRequestReq := map[string]interface{}{
		"catalogId": catalogID, "title": "申请一台云主机", "reason": "测试",
		"complianceAck": true,
		"formData": map[string]interface{}{
			"environment": "production",
		},
	}
	env, status = doServiceCatalogFieldsRequest(t, r, http.MethodPost, "/api/v1/service-requests", createRequestReq)
	require.Equal(t, http.StatusOK, status, "message=%s", env.Message)
	require.Equal(t, 0, env.Code)
	var createdRequest struct {
		ID int `json:"id"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &createdRequest))

	env, status = doServiceCatalogFieldsRequest(t, r, http.MethodGet, "/api/v1/service-requests/"+strconv.Itoa(createdRequest.ID), nil)
	require.Equal(t, http.StatusOK, status)
	var detail struct {
		CustomFields []struct {
			Name  string      `json:"name"`
			Label string      `json:"label"`
			Value interface{} `json:"value"`
		} `json:"customFields"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &detail))
	require.Len(t, detail.CustomFields, 1)
	assert.Equal(t, "environment", detail.CustomFields[0].Name)
	assert.Equal(t, "环境", detail.CustomFields[0].Label)
	assert.Equal(t, "production", detail.CustomFields[0].Value)
}
