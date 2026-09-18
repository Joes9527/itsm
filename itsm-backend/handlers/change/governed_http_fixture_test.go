package change

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newGovernedHandlerFixture(t *testing.T) (*governedChangeFixture, *gin.Engine) {
	t.Helper()
	f := newGovernedChangeFixture(t, "normal")
	r, h, _ := setupTestHandler(t)
	h.svc = f.svc
	return f, r
}

func governedHTTP(r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func governedMSPHeaders(t *testing.T, f *governedChangeFixture) map[string]string {
	t.Helper()
	f.client.Tenant.UpdateOneID(f.tenant).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("provider").SetName("Provider").SetEmail("provider@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant).SetCode("msp_tech").SetName("MSP").SetIsActive(true).SaveX(f.ctx)
	for _, action := range []string{"read", "write"} {
		p := f.client.Permission.Create().SetTenantID(f.tenant).SetCode("change:" + action).SetName(action).SetResource("change").SetAction(action).SaveX(f.ctx)
		f.client.RolePermission.Create().SetTenantID(f.tenant).SetRoleID(role.ID).SetPermissionID(p.ID).SaveX(f.ctx)
	}
	return map[string]string{"X-Tenant-ID": fmt.Sprint(provider.ID), "X-User-ID": fmt.Sprint(actor.ID), "X-MSP-Customer-ID": fmt.Sprint(f.tenant), "X-MSP-Allowed-Customer-ID": fmt.Sprint(f.tenant)}
}
