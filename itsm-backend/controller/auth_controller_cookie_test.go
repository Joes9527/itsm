package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"itsm-backend/ent/enttest"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSwitchTenantDeliversJWTsOnlyThroughHttpOnlyCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:switch-tenant-cookie-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { _ = client.Close() })
	ctx := t.Context()
	tenant := client.Tenant.Create().
		SetName("switch-native").SetCode("switch-native-" + strconv.FormatInt(time.Now().UnixNano(), 10)).
		SetType("standard").SetStatus("active").SaveX(ctx)
	user := client.User.Create().
		SetUsername("switch-cookie-user").SetEmail("switch-cookie@example.test").SetName("Switch Cookie User").
		SetPasswordHash("not-used").SetTenantID(tenant.ID).SetRole("end_user").SetActive(true).SaveX(ctx)

	controller := NewAuthController(service.NewAuthService(client, client, "switch-cookie-secret", zap.NewNop().Sugar()))
	router := gin.New()
	router.POST("/api/v1/auth/switch-tenant", func(c *gin.Context) {
		c.Set("user_id", user.ID)
		controller.SwitchTenant(c)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/switch-tenant", strings.NewReader(`{"tenantId":`+strconv.Itoa(tenant.ID)+`}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	var accessToken, refreshToken string
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case "access_token":
			accessToken = cookie.Value
			require.True(t, cookie.HttpOnly)
		case "refresh_token":
			refreshToken = cookie.Value
			require.True(t, cookie.HttpOnly)
		}
	}
	require.NotEmpty(t, accessToken)
	require.NotEmpty(t, refreshToken)
	require.NotContains(t, response.Body.String(), "accessToken")
	require.NotContains(t, response.Body.String(), "refreshToken")
	require.NotContains(t, response.Body.String(), accessToken)
	require.NotContains(t, response.Body.String(), refreshToken)
}
