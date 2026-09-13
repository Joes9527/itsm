//go:build integration_postgres

package integration

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	requestdomain "itsm-backend/handlers/service_request"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPostgresMSPCommentsUseCurrentActorDirectory(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("comments-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("comments-agent").SetName("Provider agent").SetEmail("comments@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	f.client.TicketComment.Create().SetTenantID(f.tenant.ID).SetTicketID(f.inc.WorkItemID).SetUserID(f.actor.ID).SetContent("public").SaveX(f.ctx)
	f.client.TicketComment.Create().SetTenantID(f.tenant.ID).SetTicketID(f.inc.WorkItemID).SetUserID(actor.ID).SetContent("internal").SetIsInternal(true).SaveX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	_, err := f.db.ExecContext(f.ctx, "GRANT SELECT ON ticket_comments TO "+cfg.User)
	require.NoError(t, err)
	comments := service.NewTicketCommentService(clients.Tenant, zap.NewNop().Sugar())
	comments.SetActorDirectory(clients.System)
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	rows, err := comments.ListTicketComments(ctx, f.inc.WorkItemID, f.tenant.ID, actor.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		if row.UserID == actor.ID {
			require.NotNil(t, row.User)
			require.Equal(t, "Provider agent", row.User.Name)
		}
	}
	// Raw provider admin does not override the canonical end-user effective role.
	f.client.User.UpdateOne(actor).SetMspRole("customer_user").ExecX(f.ctx)
	visible, err := comments.ListTicketComments(ctx, f.inc.WorkItemID, f.tenant.ID, actor.ID)
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.False(t, visible[0].IsInternal)
	f.client.User.UpdateOne(actor).SetMspRole("provider_agent").ExecX(f.ctx)
	_, err = comments.ListTicketComments(tenantctx.WithTenantID(f.ctx, provider.ID), f.inc.WorkItemID, provider.ID, actor.ID)
	require.Error(t, err, "directory capability never exposes another tenant WorkItem")

	_, err = comments.ListTicketComments(ctx, f.inc.WorkItemID, f.tenant.ID, f.actor.ID)
	require.NoError(t, err)
	f.client.MSPAllocation.UpdateOne(allocation).SetDeassignedAt(time.Now()).ExecX(f.ctx)
	_, err = comments.ListTicketComments(ctx, f.inc.WorkItemID, f.tenant.ID, actor.ID)
	require.Error(t, err, "revoked allocation cannot reuse comment access")
	f.client.MSPAllocation.UpdateOne(allocation).ClearDeassignedAt().ExecX(f.ctx)
	f.client.User.UpdateOne(actor).SetActive(false).ExecX(f.ctx)
	_, err = comments.ListTicketComments(ctx, f.inc.WorkItemID, f.tenant.ID, actor.ID)
	require.Error(t, err, "inactive provider actor must fail closed")
}

func TestPostgresServiceRequestDetailPreservesRequestScopeForCustomFields(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetTitle("Detail").SetTicketNumber("SR-DETAIL").SetRecordClass("service_request_item").SaveX(f.ctx)
	catalog := f.client.ServiceCatalog.Create().SetTenantID(f.tenant.ID).SetName("Read fixture").SaveX(f.ctx)
	sr := f.client.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(catalog.ID).SaveX(f.ctx)
	f.client.FieldValue.Create().SetTenantID(f.tenant.ID).SetEntityType("ticket").SetEntityID(item.ID).SetFieldName("duration").SetFieldLabel("Duration").SetValue(json.RawMessage(`"month"`)).SaveX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	_, err := f.db.ExecContext(f.ctx, "GRANT SELECT ON service_requests,field_values TO "+cfg.User)
	require.NoError(t, err)
	owner := requestdomain.NewService(requestdomain.NewEntRepository(clients.Tenant, executionfixture.Standard()), clients.Tenant, zap.NewNop().Sugar(), nil, executionfixture.Standard())
	handler := requestdomain.NewHandler(owner)
	router := gin.New()
	router.GET("/detail", func(c *gin.Context) {
		c.Set("tenant_id", f.tenant.ID)
		c.Set("user_id", f.actor.ID)
		c.Set("role", "agent")
		c.Request = c.Request.WithContext(tenantctx.WithTenantID(c.Request.Context(), f.tenant.ID))
		c.Params = gin.Params{{Key: "ticketId", Value: fmt.Sprint(item.ID)}}
		handler.GetByTicket(c)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/detail", nil))
	require.Equal(t, 200, response.Code, response.Body.String())
	var body struct {
		Data struct {
			CustomFields []struct {
				Name  string `json:"name"`
				Value any    `json:"value"`
			} `json:"customFields"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Data.CustomFields, 1, "stored snapshot must not disappear from a successful detail response")
	require.Equal(t, "duration", body.Data.CustomFields[0].Name)
	require.Equal(t, "month", body.Data.CustomFields[0].Value)
	require.NotZero(t, sr.ID)
}
