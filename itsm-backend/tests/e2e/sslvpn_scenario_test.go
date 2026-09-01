package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"itsm-backend/controller"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/fieldvalue"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/cmdb"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/handlers/service_request"
	"itsm-backend/middleware"
	repoTicket "itsm-backend/repository/ticket"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
	"itsm-backend/tests/fixtures"
)

const testJWTSecret = "sslvpn-e2e-test-jwt-secret"

var dbCounter int64

func testDSN() string {
	return fmt.Sprintf("file:sslvpn_e2e_%d_%d?mode=memory&cache=shared&_fk=1&_busy_timeout=5000", time.Now().UnixNano(), atomic.AddInt64(&dbCounter, 1))
}

type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type sslvpnTestHarness struct {
	router      *gin.Engine
	client      *ent.Client
	rawDB       *sql.DB
	tenant      *ent.Tenant
	fixture     *fixtures.SSLVPNFixtureResult
	userToken   string
	superToken  string
	lixinToken  string
	engine      service.ProcessEngine
	ticketSvc   *service.TicketService
	workflowSvc *service.TicketWorkflowService
	slaSvc      *service.TicketSLAService
}

func setupSSLVPNTestHarness(t *testing.T) *sslvpnTestHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := testDSN()
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1) // Single connection to avoid SQLite in-memory table locking across goroutines

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := ent.NewClient(ent.Driver(drv))
	require.NoError(t, client.Schema.Create(context.Background()))

	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("SSL-VPN E2E Tenant").
		SetCode(fmt.Sprintf("VPN-E2E-%d", time.Now().UnixNano()%100000)).
		SetDomain("vpn-e2e.test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	fixResult, err := fixtures.EnsureSSLVPNMetadata(ctx, client, tenant.ID)
	require.NoError(t, err)

	// Configure SLA definition for Request service type
	_, err = client.SLADefinition.Create().
		SetName("SSL-VPN标准SLA").
		SetServiceType("service_request").
		SetPriority("medium").
		SetResponseTime(15).
		SetResolutionTime(120).
		SetCategoryIds([]int{fixResult.Category.ID}).
		SetIsActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// Initialize BPMN Engine & Trigger Service
	engine := service.NewCustomProcessEngine(client, logger)
	triggerSvc := service.NewProcessTriggerService(client, engine)
	slaSvc := service.NewTicketSLAService(client, logger)

	numberAllocator := workitemnumber.NewPostgreSQLAllocator()
	ticketRepo := repoTicket.NewEntRepository(client, logger, numberAllocator)
	ticketSvc := service.NewTicketService(&service.TicketServiceConfig{
		Repository:            ticketRepo,
		Client:                client,
		Logger:                logger,
		SLAService:            slaSvc,
		ProcessTriggerService: triggerSvc,
	})
	ticketWorkflowSvc := service.NewTicketWorkflowService(client, logger)

	// Initialize Controllers & Handlers
	ticketController := controller.NewTicketController(ticketSvc, nil, nil, client, logger)
	versionSvc := service.NewBPMNVersionService(client, logger)
	bpmnController := controller.NewBPMNWorkflowController(engine, versionSvc)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, logger)
	scHandler := service_catalog.NewHandler(scService)

	cmdbRepo := cmdb.NewEntRepository(client)
	srRepo := service_request.NewEntRepository(client)
	srService := service_request.NewService(srRepo, scRepo, cmdbRepo, client, numberAllocator, logger, ticketSvc, nil, nil)
	srHandler := service_request.NewHandler(srService)

	// Generate JWT tokens for test roles
	userToken, err := middleware.GenerateAccessToken(fixResult.Users.EndUser.ID, fixResult.Users.EndUser.Username, fixResult.Users.EndUser.Role, tenant.ID, testJWTSecret, 24*time.Hour)
	require.NoError(t, err)
	superToken, err := middleware.GenerateAccessToken(fixResult.Users.Supervisor.ID, fixResult.Users.Supervisor.Username, fixResult.Users.Supervisor.Role, tenant.ID, testJWTSecret, 24*time.Hour)
	require.NoError(t, err)
	lixinToken, err := middleware.GenerateAccessToken(fixResult.Users.Lixin.ID, fixResult.Users.Lixin.Username, fixResult.Users.Lixin.Role, tenant.ID, testJWTSecret, 24*time.Hour)
	require.NoError(t, err)

	// Set up Gin Router
	r := gin.New()
	r.Use(gin.Recovery())

	apiV1 := r.Group("/api/v1")
	apiV1.Use(middleware.AuthMiddleware(testJWTSecret))
	{
		// Tickets
		apiV1.POST("/tickets", ticketController.CreateTicket)
		apiV1.GET("/tickets/:id", ticketController.GetTicket)
		apiV1.GET("/tickets", ticketController.ListTickets)

		// Service Catalogs & Requests
		apiV1.GET("/service-catalogs/:id", scHandler.Get)
		apiV1.POST("/service-requests", srHandler.Create)
		apiV1.GET("/service-requests/:id", srHandler.Get)

		// BPMN Routes
		bpmnController.RegisterRoutes(apiV1)
	}

	return &sslvpnTestHarness{
		router:      r,
		client:      client,
		rawDB:       db,
		tenant:      tenant,
		fixture:     fixResult,
		userToken:   userToken,
		superToken:  superToken,
		lixinToken:  lixinToken,
		engine:      engine,
		ticketSvc:   ticketSvc,
		workflowSvc: ticketWorkflowSvc,
		slaSvc:      slaSvc,
	}
}

func doRequest(t *testing.T, r http.Handler, token, method, path string, body interface{}) (apiEnvelope, int) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var env apiEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env), "response body=%s", w.Body.String())
	return env, w.Code
}

func TestSSLVPNScenarioE2E(t *testing.T) {
	h := setupSSLVPNTestHarness(t)
	defer func() {
		h.client.Close()
		h.rawDB.Close()
	}()
	ctx := context.Background()

	// =========================================================================
	// Step 1: User Service Request Creation (with 8 Custom Fields)
	// =========================================================================
	t.Log("== Step 1: Submitting SSL-VPN Service Request with 8 Custom Fields ==")

	srCreationBody := map[string]interface{}{
		"catalogId": h.fixture.CatalogItem.ID,
		"title":     "申请研发出差 SSL-VPN 访问权限",
		"reason":    "因出差需要远程访问研发内网与生产堡垒机",
		"formData": map[string]interface{}{
			"customFieldValues": []map[string]interface{}{
				{"name": "applicant_name", "value": "侯艾华"},
				{"name": "applicant_upn", "value": "shouah@kln.com"},
				{"name": "employee_id", "value": "EMP001"},
				{"name": "department", "value": "IT研发中心"},
				{"name": "vpn_level", "value": "Level 2 - 业务系统组 (CNDL-OKTA-SSLVPN-Level2-Users)"},
				{"name": "target_systems", "value": "10.128.35.0/24, ERP与WMS生产系统"},
				{"name": "access_duration", "value": "90天临时"},
				{"name": "access_reason", "value": "因研发排障及出差值班，需远程接入内网生产环境"},
			},
		},
	}

	env, status := doRequest(t, h.router, h.userToken, http.MethodPost, "/api/v1/service-requests", srCreationBody)
	require.Equal(t, http.StatusOK, status, "service request creation should return 200 OK: %s", env.Message)
	require.Equal(t, 0, env.Code, "service request creation code must be 0: %s", env.Message)

	var srResp dto.ServiceRequestResponse
	require.NoError(t, json.Unmarshal(env.Data, &srResp))
	ticketID := srResp.TicketID
	require.Positive(t, ticketID, "linked ticket ID must be positive")
	t.Logf("Created Service Request ID: %d, Linked Ticket ID: %d", srResp.ID, ticketID)

	// Direct DB Assertions for Step 1
	// 1. Ticket Table
	dbTicket, err := h.client.Ticket.Query().
		Where(ticket.IDEQ(ticketID), ticket.TenantIDEQ(h.tenant.ID)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, string(repoTicket.StatusNew), dbTicket.Status, "ticket initial status in ITSM core is 'new'")
	assert.Equal(t, h.tenant.ID, dbTicket.TenantID, "tenant ID must match")
	assert.NotNil(t, dbTicket.SLAResponseDeadline, "SLA response deadline should be populated")
	assert.NotNil(t, dbTicket.SLAResolutionDeadline, "SLA resolution deadline should be populated")

	// 2. Custom Field Values (8 dynamic fields in field_values table)
	dbFieldValues, err := h.client.FieldValue.Query().
		Where(
			fieldvalue.TenantIDEQ(h.tenant.ID),
			fieldvalue.EntityTypeEQ("ticket"),
			fieldvalue.EntityIDEQ(ticketID),
		).
		Order(ent.Asc(fieldvalue.FieldSortOrder)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, dbFieldValues, 8, "must persist all 8 custom fields")

	expectedFieldKeys := []string{
		"applicant_name",
		"applicant_upn",
		"employee_id",
		"department",
		"vpn_level",
		"target_systems",
		"access_duration",
		"access_reason",
	}
	expectedValues := map[string]string{
		"applicant_name":  "侯艾华",
		"applicant_upn":   "shouah@kln.com",
		"employee_id":     "EMP001",
		"department":      "IT研发中心",
		"vpn_level":       "Level 2 - 业务系统组 (CNDL-OKTA-SSLVPN-Level2-Users)",
		"target_systems":  "10.128.35.0/24, ERP与WMS生产系统",
		"access_duration": "90天临时",
		"access_reason":   "因研发排障及出差值班，需远程接入内网生产环境",
	}

	fieldMap := make(map[string]string)
	for _, fv := range dbFieldValues {
		var val string
		if err := json.Unmarshal(fv.Value, &val); err == nil {
			fieldMap[fv.FieldName] = val
		}
	}
	for _, k := range expectedFieldKeys {
		val, exists := fieldMap[k]
		assert.True(t, exists, "custom field %s must exist in DB", k)
		assert.Equal(t, expectedValues[k], val, "custom field %s value mismatch", k)
	}

	// 3. BPMN Process Instance (ACTIVE) - wait briefly for async trigger goroutine if needed
	businessKey := fmt.Sprintf("ticket:%d", ticketID)
	var processInst *ent.ProcessInstance
	require.Eventually(t, func() bool {
		pi, qErr := h.client.ProcessInstance.Query().
			Where(
				processinstance.BusinessKeyEQ(businessKey),
				processinstance.TenantIDEQ(h.tenant.ID),
			).
			First(ctx)
		if qErr == nil && pi != nil {
			processInst = pi
			return true
		}
		return false
	}, 3*time.Second, 20*time.Millisecond, "BPMN process instance must exist for ticket")

	assert.Equal(t, "running", processInst.Status, "process instance status should be running/ACTIVE")
	assert.Equal(t, "sslvpn_approval_flow", processInst.ProcessDefinitionKey)

	// 4. BPMN Tasks (UserTask_DeptManagerApproval in CREATED)
	var deptTask *ent.ProcessTask
	require.Eventually(t, func() bool {
		tasks, qErr := h.client.ProcessTask.Query().
			Where(
				processtask.ProcessInstanceIDEQ(processInst.ID),
				processtask.TenantIDEQ(h.tenant.ID),
			).
			All(ctx)
		if qErr == nil && len(tasks) == 1 {
			deptTask = tasks[0]
			return true
		}
		return false
	}, 3*time.Second, 20*time.Millisecond, "exactly 1 user task should exist at Step 1")

	assert.Equal(t, "UserTask_DeptManagerApproval", deptTask.TaskDefinitionKey)
	assert.Equal(t, "created", deptTask.Status, "supervisor task should be in created state")
	assert.Equal(t, "dept_manager", deptTask.CandidateGroups)
	t.Logf("Step 1 OK: Supervisor Task ID=%s (DB ID=%d)", deptTask.TaskID, deptTask.ID)

	// =========================================================================
	// Step 2: Supervisor Primary Approval (POST /api/v1/bpmn/tasks/:id/decisions)
	// =========================================================================
	t.Log("== Step 2: Supervisor Approval ==")

	supervisorApprovalBody := map[string]interface{}{
		"action":  "approve",
		"comment": "同意申请，出差值班需要",
	}

	// Complete supervisor approval using supervisor token via REST API
	env, status = doRequest(t, h.router, h.superToken, http.MethodPost, fmt.Sprintf("/api/v1/bpmn/tasks/%s/decisions", deptTask.TaskID), supervisorApprovalBody)
	require.Equal(t, http.StatusOK, status, "supervisor approval should succeed: %s", env.Message)
	require.Equal(t, 0, env.Code, "supervisor approval code must be 0: %s", env.Message)

	// Direct DB Assertions for Step 2
	// 1. Supervisor task status -> COMPLETED
	updatedDeptTask, err := h.client.ProcessTask.Get(ctx, deptTask.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", updatedDeptTask.Status, "supervisor task must be marked completed")

	// 2. L2 Network Ops task generated in CREATED state
	tasksStep2, err := h.client.ProcessTask.Query().
		Where(
			processtask.ProcessInstanceIDEQ(processInst.ID),
			processtask.TenantIDEQ(h.tenant.ID),
			processtask.TaskDefinitionKeyEQ("UserTask_L2NetworkOpsApproval"),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, tasksStep2, 1, "UserTask_L2NetworkOpsApproval must be created")
	l2Task := tasksStep2[0]
	assert.Equal(t, "created", l2Task.Status, "L2 ops task must be in created state")
	assert.Equal(t, "network_eng", l2Task.CandidateGroups)
	t.Logf("Step 2 OK: L2 Ops Task ID=%s (DB ID=%d)", l2Task.TaskID, l2Task.ID)

	// 3. Process Approval Decision table
	decisionsStep2, err := h.client.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.ProcessInstanceIDEQ(processInst.ID),
			processapprovaldecision.NodeKeyEQ("UserTask_DeptManagerApproval"),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, decisionsStep2, 1, "supervisor approval decision must be recorded")
	assert.Equal(t, "approve", decisionsStep2[0].Action)
	assert.Equal(t, "approved", decisionsStep2[0].Decision)
	assert.Equal(t, "同意申请，出差值班需要", decisionsStep2[0].Comment)
	assert.Equal(t, h.fixture.Users.Supervisor.ID, decisionsStep2[0].ActorID)

	// 4. Audit Log record for task completion
	auditLogsStep2, err := h.client.ProcessAuditLog.Query().
		Where(
			processauditlog.ProcessInstanceIDEQ(processInst.ID),
			processauditlog.ActivityIDEQ("UserTask_DeptManagerApproval"),
			processauditlog.ActionEQ(service.AuditActionTaskCompleted),
		).
		All(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, auditLogsStep2, "audit log for supervisor task completed must exist")

	// =========================================================================
	// Step 3: L2 Network Ops Technical Review & Transfer (Li Xin)
	// =========================================================================
	t.Log("== Step 3: L2 Network Ops Technical Review by Li Xin ==")

	lixinApprovalBody := map[string]interface{}{
		"action":  "approve",
		"comment": "网络权限核准通过，允许访问",
	}

	// Complete L2 approval using Li Xin's token via REST API
	env, status = doRequest(t, h.router, h.lixinToken, http.MethodPost, fmt.Sprintf("/api/v1/bpmn/tasks/%s/decisions", l2Task.TaskID), lixinApprovalBody)
	require.Equal(t, http.StatusOK, status, "L2 ops approval should succeed: %s", env.Message)
	require.Equal(t, 0, env.Code, "L2 ops approval code must be 0: %s", env.Message)

	// Direct DB Assertions for Step 3
	// 1. L2 Ops task status -> COMPLETED
	updatedL2Task, err := h.client.ProcessTask.Get(ctx, l2Task.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", updatedL2Task.Status, "L2 ops task must be marked completed")

	// 2. Process Instance status -> COMPLETED
	finalProcessInst, err := h.client.ProcessInstance.Get(ctx, processInst.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", finalProcessInst.Status, "BPMN process instance must be completed")
	assert.NotNil(t, finalProcessInst.EndTime, "Process end time must be set")

	// 3. Process Approval Decision for L2 review
	decisionsStep3, err := h.client.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.ProcessInstanceIDEQ(processInst.ID),
			processapprovaldecision.NodeKeyEQ("UserTask_L2NetworkOpsApproval"),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, decisionsStep3, 1, "L2 ops approval decision must be recorded")
	assert.Equal(t, "approve", decisionsStep3[0].Action)
	assert.Equal(t, "approved", decisionsStep3[0].Decision)
	assert.Equal(t, "网络权限核准通过，允许访问", decisionsStep3[0].Comment)
	assert.Equal(t, h.fixture.Users.Lixin.ID, decisionsStep3[0].ActorID)

	// 4. Audit Log record for L2 review
	auditLogsStep3, err := h.client.ProcessAuditLog.Query().
		Where(
			processauditlog.ProcessInstanceIDEQ(processInst.ID),
			processauditlog.ActivityIDEQ("UserTask_L2NetworkOpsApproval"),
			processauditlog.ActionEQ(service.AuditActionTaskCompleted),
		).
		All(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, auditLogsStep3, "audit log for L2 task completed must exist")

	// 5. Verification of all approval decisions for the ticket
	allDecisions, err := h.workflowSvc.GetApprovalDecisions(ctx, ticketID, service.ActionActor{
		TenantID: h.tenant.ID,
		UserID:   h.fixture.Users.Lixin.ID,
		Role:     "super_admin",
	})
	require.NoError(t, err)
	require.Len(t, allDecisions, 2, "ticket must have exactly 2 approval decisions in sequence")
	assert.Equal(t, "UserTask_DeptManagerApproval", allDecisions[0].NodeKey)
	assert.Equal(t, "UserTask_L2NetworkOpsApproval", allDecisions[1].NodeKey)

	t.Log("== SSL-VPN 3-Step Scenario E2E Test Completed Successfully! ==")
}
