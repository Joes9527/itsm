package integration

import (
	"bytes"
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/controller"
	"itsm-backend/middleware"
	"itsm-backend/service"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
)

func TestRequestedItemAssignedCollaborationPersistsAndStopsAfterReassignment(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	logger := zap.NewNop().Sugar()
	result, err := f.app.Create(ctx, f.identity, entryCatalogCommand(t, f, "service_request_item", ""))
	require.NoError(t, err)
	agent := f.client.User.Create().SetTenantID(f.identity.TenantID).SetUsername("helpdesk").SetName("Helpdesk").SetEmail("helpdesk@example.test").SetPasswordHash("unused").SetRole("l1_support").SetActive(true).SaveX(ctx)
	role := f.client.Role.Create().SetTenantID(f.identity.TenantID).SetCode(agent.Role).SetName("Helpdesk").SaveX(ctx)
	for _, action := range []string{"read", "provision"} {
		p := f.client.Permission.Create().SetTenantID(f.identity.TenantID).SetCode("service_request:" + action).SetName(action).SetResource("service_request").SetAction(action).SaveX(ctx)
		f.client.RolePermission.Create().SetTenantID(f.identity.TenantID).SetRoleID(role.ID).SetPermissionID(p.ID).SaveX(ctx)
	}
	authorization.InvalidateRolePermissionCache(agent.Role, f.identity.TenantID)
	t.Cleanup(func() { authorization.InvalidateRolePermissionCache(agent.Role, f.identity.TenantID) })
	f.client.Ticket.UpdateOneID(result.WorkItemID).SetAssigneeID(agent.ID).ExecX(ctx)
	comments := controller.NewTicketCommentController(service.NewTicketCommentService(f.client, logger), logger)
	attachmentsService := service.NewTicketAttachmentService(f.client, logger)
	attachmentsService.SetStorage(service.NewLocalAttachmentStorage(t.TempDir()))
	attachments := controller.NewTicketAttachmentController(attachmentsService, logger)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("client", f.client)
		c.Set("tenant_id", f.identity.TenantID)
		c.Set("user_id", agent.ID)
		c.Set("role", agent.Role)
		c.Next()
	})
	router.POST("/tickets/:id/comments", middleware.RequireWorkItemCollaborationPermission("create"), comments.CreateTicketComment)
	router.PUT("/tickets/:id/comments/:comment_id", middleware.RequireWorkItemCollaborationPermission("update"), comments.UpdateTicketComment)
	router.POST("/tickets/:id/attachments", middleware.RequireWorkItemCollaborationPermission("create"), attachments.UploadAttachment)
	comment := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/tickets/%d/comments", result.WorkItemID), strings.NewReader(`{"content":"Assigned Helpdesk progress","isInternal":false}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	upload := func() *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="file"; filename="diagnostic.txt"`)
		header.Set("Content-Type", "text/plain")
		part, e := writer.CreatePart(header)
		require.NoError(t, e)
		_, e = part.Write([]byte("local diagnostic evidence"))
		require.NoError(t, e)
		require.NoError(t, writer.Close())
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/tickets/%d/attachments", result.WorkItemID), &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	for _, w := range []*httptest.ResponseRecorder{comment(), upload()} {
		require.Contains(t, []int{200, 201}, w.Code, w.Body.String())
	}
	persistedComment := f.client.TicketComment.Query().OnlyX(ctx)
	require.Equal(t, agent.ID, persistedComment.UserID)
	require.Equal(t, result.WorkItemID, persistedComment.TicketID)
	persistedAttachment := f.client.TicketAttachment.Query().OnlyX(ctx)
	require.Equal(t, result.WorkItemID, persistedAttachment.TicketID)
	edit := func(content string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/tickets/%d/comments/%d", result.WorkItemID, persistedComment.ID), strings.NewReader(fmt.Sprintf(`{"content":%q}`, content)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	updated := edit("Edited assigned progress")
	require.Equal(t, 200, updated.Code, updated.Body.String())
	require.Equal(t, "Edited assigned progress", f.client.TicketComment.GetX(ctx, persistedComment.ID).Content)
	f.client.Ticket.UpdateOneID(result.WorkItemID).ClearAssigneeID().ExecX(ctx)
	for _, w := range []*httptest.ResponseRecorder{comment(), upload()} {
		require.Equal(t, 403, w.Code, w.Body.String())
	}
	denied := edit("After reassignment")
	require.Equal(t, 403, denied.Code, denied.Body.String())
	require.Equal(t, "Edited assigned progress", f.client.TicketComment.GetX(ctx, persistedComment.ID).Content)
	require.Equal(t, 1, f.client.TicketComment.Query().CountX(ctx))
	require.Equal(t, 1, f.client.TicketAttachment.Query().CountX(ctx))
}
