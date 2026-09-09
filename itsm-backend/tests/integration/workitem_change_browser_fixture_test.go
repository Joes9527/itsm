//go:build integration_postgres && browser_fixture

package integration

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processdefinition"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// Explicit fixture tag: actual Change owner and PG persistence, actor supplied by
// integration middleware. This harness does not claim login/CSRF end-to-end coverage.
func TestWorkItemChangeBrowserFixture(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	if os.Getenv("A4C3_BROWSER_MSP") == "1" {
		f.actor, _, _ = seedChangeHTTPMSP(t, f)
	}
	xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
	require.NoError(t, err)
	f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
	r, h := changeHTTPFixture(f)
	r.GET("/api/v1/changes/:id", h.GetChange)
	r.GET("/api/v1/changes/:id/approvals", h.GetApprovals)
	r.GET("/api/v1/changes/:id/task-progress", h.GetTaskProgress)
	r.POST("/api/v1/changes/:id/submit", h.ExecuteAction)
	r.POST("/api/v1/changes/:id/assess", h.ExecuteAction)
	r.GET("/api/v1/csrf-token", func(c *gin.Context) { c.JSON(200, gin.H{"code": 0, "data": gin.H{"token": "fixture"}}) })
	done := make(chan struct{})
	r.POST("/fixture/done", func(c *gin.Context) { c.Status(204); close(done) })
	listener, err := net.Listen("tcp", "127.0.0.1:36485")
	require.NoError(t, err)
	server := &http.Server{Handler: r, ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	go func() { _ = server.Serve(listener) }()
	t.Log("isolated real Change fixture ready at 127.0.0.1:36485")
	select {
	case <-done:
	case <-time.After(65 * time.Second):
		t.Fatal("browser fixture timed out")
	}
	require.Equal(t, 3, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
	require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.task_completion")).CountX(f.ctx))
}
