package problem

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/dto"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/known_error"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestProblemKnownErrorPublishingDirectService(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:ke-publish-%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenant := createProblemHandlerTenant(t, ctx, client, "ke-srv")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "ke-srv")

	// Create Problem
	probRepo := newTestProblemRepository(client)
	probHandlerSvc := NewService(probRepo, logger)
	problem, err := probHandlerSvc.Create(ctx, tenant.ID, &Problem{
		Title:       "KEDB Source Problem",
		Description: "Problem description for KEDB",
		Priority:    "high",
		Category:    "network",
		RootCause:   "BGP flapping on edge router",
		Workaround:  "Route via backup ISP",
		CreatedBy:   user.ID,
	})
	require.NoError(t, err)

	// Setup ProblemService with KnownErrorService
	probSvc := service.NewProblemService(client, logger)
	keSvc := service.NewKnownErrorService(client, logger)
	probSvc.SetKnownErrorService(keSvc)

	// Publish Known Error from Problem
	req := dto.KEDBCreateRequest{
		Title:       "Published Known Error",
		Description: "Published Description",
		RootCause:   "BGP flapping on edge router",
		Workaround:  "Route via backup ISP",
		Category:    "network",
		Severity:    "high",
	}

	keResp, err := probSvc.CreateKnownErrorFromProblem(ctx, problem.ID, user.ID, &req)
	require.NoError(t, err)
	require.NotNil(t, keResp)
	assert.Equal(t, req.Title, keResp.Title)
	assert.Equal(t, "BGP flapping on edge router", keResp.RootCause)
	assert.Equal(t, tenant.ID, keResp.TenantID)

	// Verify database record
	storedKE, err := client.KnownError.Get(ctx, keResp.ID)
	require.NoError(t, err)
	assert.Equal(t, req.Title, storedKE.Title)
	assert.Equal(t, tenant.ID, storedKE.TenantID)
}

func TestProblemKnownErrorPublishingHTTPEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:ke-http-%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer client.Close()
	ctx := context.Background()
	logger := zaptest.NewLogger(t).Sugar()

	tenantA := createProblemHandlerTenant(t, ctx, client, "ke-http-a")
	tenantB := createProblemHandlerTenant(t, ctx, client, "ke-http-b")
	userA := createProblemHandlerUser(t, ctx, client, tenantA.ID, "ke-http-a")
	userB := createProblemHandlerUser(t, ctx, client, tenantB.ID, "ke-http-b")

	probRepo := newTestProblemRepository(client)
	probHandlerSvc := NewService(probRepo, logger)
	problemA, err := probHandlerSvc.Create(ctx, tenantA.ID, &Problem{
		Title:       "HTTP Source Problem Tenant A",
		Description: "Description A",
		Priority:    "critical",
		RootCause:   "DNS timeout",
		CreatedBy:   userA.ID,
	})
	require.NoError(t, err)

	keHandler := known_error.NewHandler(client, logger)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		if tid := c.GetHeader("X-Tenant-ID"); tid != "" {
			id, _ := strconv.Atoi(tid)
			c.Set("tenant_id", id)
		}
		if uid := c.GetHeader("X-User-ID"); uid != "" {
			id, _ := strconv.Atoi(uid)
			c.Set("user_id", id)
		}
		c.Next()
	})
	r.POST("/api/v1/problems/:id/known-error", keHandler.CreateFromProblem)

	// 1. Tenant A publishes Known Error for Problem A (Success)
	reqBody, _ := json.Marshal(dto.KEDBCreateRequest{Title: "KE Title A"})
	httpReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/problems/%d/known-error", problemA.ID), bytes.NewBuffer(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Tenant-ID", strconv.Itoa(tenantA.ID))
	httpReq.Header.Set("X-User-ID", strconv.Itoa(userA.ID))

	wA := httptest.NewRecorder()
	r.ServeHTTP(wA, httpReq)
	require.Equal(t, http.StatusOK, wA.Code)

	// AuditMiddleware is defined in itsm-backend/middleware/audit.go but is never registered
	// via r.Use(...) anywhere in router.go/main.go (verified by repo-wide search), so this
	// endpoint writes its own AuditLog row explicitly (known_error.Handler.
	// recordKnownErrorFromProblemAudit) instead of relying on that unwired global middleware.
	auditCount, err := client.AuditLog.Query().
		Where(
			auditlog.TenantIDEQ(tenantA.ID),
			auditlog.UserIDEQ(userA.ID),
			auditlog.ResourceEQ("known_error"),
			auditlog.ActionEQ("create_from_problem"),
		).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, auditCount, "publishing a known error from a problem must produce exactly one audit log entry")

	// 2. Tenant B attempts to publish Known Error for Tenant A's Problem A (Cross-tenant Isolation check)
	httpReqB := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/problems/%d/known-error", problemA.ID), bytes.NewBuffer(reqBody))
	httpReqB.Header.Set("Content-Type", "application/json")
	httpReqB.Header.Set("X-Tenant-ID", strconv.Itoa(tenantB.ID))
	httpReqB.Header.Set("X-User-ID", strconv.Itoa(userB.ID))

	wB := httptest.NewRecorder()
	r.ServeHTTP(wB, httpReqB)
	require.Equal(t, http.StatusInternalServerError, wB.Code)

	// 3. Non-existent Problem ID
	httpReq404 := httptest.NewRequest("POST", "/api/v1/problems/99999/known-error", bytes.NewBuffer(reqBody))
	httpReq404.Header.Set("Content-Type", "application/json")
	httpReq404.Header.Set("X-Tenant-ID", strconv.Itoa(tenantA.ID))
	httpReq404.Header.Set("X-User-ID", strconv.Itoa(userA.ID))

	w404 := httptest.NewRecorder()
	r.ServeHTTP(w404, httpReq404)
	require.Equal(t, http.StatusInternalServerError, w404.Code)
}
