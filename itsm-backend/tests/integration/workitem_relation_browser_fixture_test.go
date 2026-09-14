//go:build integration_postgres && browser_fixture

package integration

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent/workitemrelation"
	"net"
	"net/http"
	"testing"
	"time"
)

// Isolated real UI/transport/PG harness. Authentication context is injected by
// the integration fixture; this is explicitly not the full login/CSRF journey.
func TestWorkItemRelationBrowserFixture(t *testing.T) {
	f, r, _ := problemRelationHTTP(t)
	r.GET("/api/v1/csrf-token", func(c *gin.Context) { c.JSON(200, gin.H{"code": 0, "data": gin.H{"token": "fixture"}}) })
	r.GET("/fixture/context", func(c *gin.Context) {
		c.JSON(200, gin.H{"source": f.inc.WorkItemID, "target": f.problem.ID, "actor": f.actor.ID, "tenant": f.tenant.ID})
	})
	done := make(chan struct{})
	r.POST("/fixture/done", func(c *gin.Context) { c.Status(204); close(done) })
	listener, err := net.Listen("tcp", "127.0.0.1:36495")
	require.NoError(t, err)
	server := &http.Server{Handler: r, ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	go func() { _ = server.Serve(listener) }()
	t.Log("isolated WorkItem relation fixture ready on 36495")
	select {
	case <-done:
	case <-time.After(65 * time.Second):
		t.Fatal("browser fixture timed out")
	}
	require.Equal(t, 4, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
	require.Equal(t, 2, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.WorkItemRelation.Query().Where(workitemrelation.DeletedAtIsNil(), workitemrelation.RelationType("investigated_by")).CountX(f.ctx))
	require.Equal(t, 3, f.client.AuditLog.Query().CountX(f.ctx))
}
