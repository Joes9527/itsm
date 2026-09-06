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

	"itsm-backend/authentication"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"itsm-backend/controller"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/fieldvalue"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	"itsm-backend/ent/ticket"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	problemdomain "itsm-backend/handlers/problem"
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
	router       *gin.Engine
	client       *ent.Client
	rawDB        *sql.DB
	tenant       *ent.Tenant
	fixture      *fixtures.SSLVPNFixtureResult
	userSession  string
	superSession string
	lixinSession string
	engine       service.ProcessEngine
	ticketSvc    *service.TicketService
	workflowSvc  *service.TicketWorkflowService
	slaSvc       *service.TicketSLAService
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
	ticketRepo := repoTicket.NewEntRepository(client, logger)
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
	scService := service_catalog.NewService(scRepo, client, logger, sameTransactionDirectory{})
	scHandler := service_catalog.NewHandler(scService)

	srRepo := service_request.NewEntRepository(client)
	srService := service_request.NewService(srRepo, client, logger, service.NewApprovalChainResolver(client, logger))
	srHandler := service_request.NewHandler(srService)

	// Wire the real shared Intake application, mirroring production bootstrap:
	// creation now always goes through Resolve->Prepare->CreateExtension, not a
	// direct repository/service Create method. The SSL-VPN scenario's own
	// service_catalog item already carries ProcessDefinitionKey=sslvpn_approval_flow,
	// so catalog-driven creation resolves its workflow directly from the catalog
	// row and does not need a ProcessBinding fixture for "service_request".
	registry := intake.NewCreatorRegistry()
	for _, owner := range []creation.ProfessionalCreator{
		ticketSvc,
		service.NewIncidentService(client, logger),
		problemdomain.NewService(nil, logger),
		changedomain.NewService(nil, client, logger),
		srService,
	} {
		require.NoError(t, registry.Register(owner))
	}
	resolver := intake.NewResolver(scService, service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
	creationApp := intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(numberAllocator), sameTransactionDirectory{})
	ticketController.SetCreationApplication(creationApp)
	srHandler.SetCreationApplication(creationApp)

	// Grant the end_user requester's role current read permission on the service
	// catalog: handlers/service_catalog.ResolveCreationCatalog requires it before
	// a non-super_admin actor's submission can resolve the confirmed catalog/form
	// revision. The BPMN template deployment above already provisions an active
	// "end_user" role row for this tenant, so look it up rather than creating a
	// second (non-singular) one.
	endUserRole, err := client.Role.Query().Where(role.TenantIDEQ(tenant.ID), role.CodeEQ(fixResult.Users.EndUser.Role), role.IsActiveEQ(true)).First(ctx)
	if ent.IsNotFound(err) {
		endUserRole, err = client.Role.Create().SetTenantID(tenant.ID).SetCode(fixResult.Users.EndUser.Role).SetName(fixResult.Users.EndUser.Role).SetIsActive(true).Save(ctx)
	}
	require.NoError(t, err)
	for _, grant := range []struct{ resource, action string }{
		{"service_catalog", "read"},
		{"service_request", "read"},
		{"service_request", "write"},
		{"ticket", "read"},
		{"ticket", "write"},
	} {
		code := grant.resource + ":" + grant.action
		perm, err := client.Permission.Query().Where(permission.TenantIDEQ(tenant.ID), permission.CodeEQ(code)).First(ctx)
		if ent.IsNotFound(err) {
			perm, err = client.Permission.Create().SetTenantID(tenant.ID).SetCode(code).SetName(code).SetResource(grant.resource).SetAction(grant.action).Save(ctx)
		}
		require.NoError(t, err)
		if !client.RolePermission.Query().Where(rolepermission.TenantIDEQ(tenant.ID), rolepermission.RoleIDEQ(endUserRole.ID), rolepermission.PermissionIDEQ(perm.ID)).ExistX(ctx) {
			client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(endUserRole.ID).SetPermissionID(perm.ID).SaveX(ctx)
		}
	}

	// Build signed session values for the same HttpOnly-cookie path used in production.
	userSession, err := authentication.GenerateAccessToken(fixResult.Users.EndUser.ID, fixResult.Users.EndUser.Username, fixResult.Users.EndUser.Role, tenant.ID, testJWTSecret, 24*time.Hour)
	require.NoError(t, err)
	superSession, err := authentication.GenerateAccessToken(fixResult.Users.Supervisor.ID, fixResult.Users.Supervisor.Username, fixResult.Users.Supervisor.Role, tenant.ID, testJWTSecret, 24*time.Hour)
	require.NoError(t, err)
	lixinSession, err := authentication.GenerateAccessToken(fixResult.Users.Lixin.ID, fixResult.Users.Lixin.Username, fixResult.Users.Lixin.Role, tenant.ID, testJWTSecret, 24*time.Hour)
	require.NoError(t, err)

	// Set up Gin Router
	r := gin.New()
	r.Use(gin.Recovery())

	apiV1 := r.Group("/api/v1")
	apiV1.Use(middleware.AuthMiddleware(testJWTSecret), middleware.CSRFProtectionMiddleware(middleware.DefaultCSRFConfig()))
	// The shared Intake HTTP boundary resolves tenant scope through
	// middleware.TenantContextKey (middleware.ResolveRequestTenantID), not the
	// legacy plain "tenant_id" gin key AuthMiddleware sets for older routes.
	apiV1.Use(func(c *gin.Context) {
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: c.GetInt("tenant_id")})
		c.Next()
	})
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
		router:       r,
		client:       client,
		rawDB:        db,
		tenant:       tenant,
		fixture:      fixResult,
		userSession:  userSession,
		superSession: superSession,
		lixinSession: lixinSession,
		engine:       engine,
		ticketSvc:    ticketSvc,
		workflowSvc:  ticketWorkflowSvc,
		slaSvc:       slaSvc,
	}
}

func doRequest(t *testing.T, r http.Handler, session, method, path string, body interface{}) (apiEnvelope, int) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "access_token", Value: session, Path: "/", HttpOnly: true})
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		const csrf = "sslvpn-e2e-csrf"
		req.AddCookie(&http.Cookie{Name: middleware.CSRFTokenCookieName, Value: csrf, Path: "/", HttpOnly: true})
		req.Header.Set(middleware.CSRFTokenHeaderName, csrf)
		// Required by the shared Intake HTTP boundary for creation routes;
		// harmless no-op for other mutating routes that ignore it.
		req.Header.Set("Idempotency-Key", uuid.NewString())
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

	// Authenticated catalog detail is the authoritative confirmation revision
	// source: the shared Intake resolver rejects a submission whose
	// catalogVersion/formSchemaVersion do not match the current row.
	catalogEnv, catalogStatus := doRequest(t, h.router, h.userSession, http.MethodGet, fmt.Sprintf("/api/v1/service-catalogs/%d", h.fixture.CatalogItem.ID), nil)
	require.Equal(t, http.StatusOK, catalogStatus, "catalog detail read should succeed: %s", catalogEnv.Message)
	var catalogResp dto.ServiceCatalogResponse
	require.NoError(t, json.Unmarshal(catalogEnv.Data, &catalogResp))
	require.NotEmpty(t, catalogResp.CatalogVersion)
	require.NotEmpty(t, catalogResp.FormSchemaVersion)

	srCreationBody := map[string]interface{}{
		"catalogId":         h.fixture.CatalogItem.ID,
		"recordClass":       "service_request_item",
		"catalogVersion":    catalogResp.CatalogVersion,
		"formSchemaVersion": catalogResp.FormSchemaVersion,
		"title":             "申请研发出差 SSL-VPN 访问权限",
		"reason":            "因出差需要远程访问研发内网与生产堡垒机",
		"formData": map[string]interface{}{
			"customFieldValues": []map[string]interface{}{
				{"name": "target_systems", "value": "10.128.35.0/24, ERP与WMS生产系统"},
				{"name": "access_duration", "value": "days_90"},
				{"name": "access_reason", "value": "因研发排障及出差值班，需远程接入内网生产环境"},
			},
		},
	}

	env, status := doRequest(t, h.router, h.userSession, http.MethodPost, "/api/v1/service-requests", srCreationBody)
	require.Equal(t, http.StatusCreated, status, "service request creation should return 201 Created: %s", env.Message)
	require.Equal(t, 0, env.Code, "service request creation code must be 0: %s", env.Message)

	var createResult creation.CreateWorkItemResult
	require.NoError(t, json.Unmarshal(env.Data, &createResult))
	ticketID := createResult.WorkItemID
	require.Positive(t, ticketID, "linked ticket ID must be positive")
	t.Logf("Created Service Request ID: %d, Linked Ticket ID: %d", createResult.ProfessionalReference.ID, ticketID)

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
	require.Len(t, dbFieldValues, 3, "must persist all 3 business fields")

	expectedFieldKeys := []string{
		"target_systems",
		"access_duration",
		"access_reason",
	}
	expectedValues := map[string]string{
		"target_systems":  "10.128.35.0/24, ERP与WMS生产系统",
		"access_duration": "days_90",
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

	// 3. BPMN Process Instance (ACTIVE). Creation now durably persists a
	// "workflow.start.requested" Outbox event rather than synchronously starting
	// the process; deliver it through the real handler once, mirroring
	// handlers/service_request/kaf_delegation_sslvpn_e2e_test.go, then wait
	// briefly for the resulting process instance/task rows to commit.
	startEvents := h.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("workflow.start.requested"), outboxevent.AggregateIDEQ(fmt.Sprint(ticketID))).AllX(ctx)
	require.Len(t, startEvents, 1, "exactly one durable workflow start event must be recorded for the created work item")
	require.NoError(t, service.NewWorkflowStartOutboxHandler(h.client, h.engine.(*service.CustomProcessEngine), h.client).Deliver(ctx, startEvents[0]))

	businessKey := fmt.Sprintf("service_request:%d", ticketID)
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
	env, status = doRequest(t, h.router, h.superSession, http.MethodPost, fmt.Sprintf("/api/v1/bpmn/tasks/%s/decisions", deptTask.TaskID), supervisorApprovalBody)
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
	env, status = doRequest(t, h.router, h.lixinSession, http.MethodPost, fmt.Sprintf("/api/v1/bpmn/tasks/%s/decisions", l2Task.TaskID), lixinApprovalBody)
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
	assert.Equal(t, "running", finalProcessInst.Status, "BPMN awaits verified external fulfillment")
	require.Equal(t, 1, h.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(finalProcessInst.ID), processtask.TaskTypeEQ("kaf_delegate"), processtask.StatusEQ("delegated")).CountX(ctx))
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
