//go:build integration_postgres

package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/controller"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	problemdomain "itsm-backend/handlers/problem"
	catalogdomain "itsm-backend/handlers/service_catalog"
	"itsm-backend/middleware"
	"itsm-backend/migration"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const intakeMSPVersion = "026_intake_actor_provenance"

func TestPostgresIntakeMSPProvenanceMigration(t *testing.T) {
	apply := migration.GetMigrationSQL(intakeMSPVersion)
	require.NotEmpty(t, apply)
	f := newIncidentEffectsFixture(t)
	asset, err := os.ReadFile("../../migrations/" + intakeMSPVersion + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(apply), strings.TrimSpace(string(asset)))
	verify, err := os.ReadFile("../../migrations/" + intakeMSPVersion + "_verify.sql")
	require.NoError(t, err)
	orphan := f.client.IntakeRequest.Create().SetTenantID(f.tenant.ID).SetActorTenantID(f.tenant.ID).SetActorID(9999999).SetRequesterID(f.actor.ID).SetChannel("itsm_web").SetOperation("create_work_item").SetIdempotencyKey("orphan").SetRequestDigest("orphan").SetDigestVersion("v3").SaveX(f.ctx)
	_, err = f.db.ExecContext(f.ctx, apply)
	require.ErrorContains(t, err, fmt.Sprintf("receipt IDs %d", orphan.ID))
	f.client.IntakeRequest.DeleteOneID(orphan.ID).ExecX(f.ctx)
	audit := f.client.AuditLog.Create().SetTenantID(f.tenant.ID).SetUserID(f.actor.ID).SetAction("intake.created").SetResource(fmt.Sprintf("work_item:%d", f.inc.WorkItemID)).SetPath("/intake").SetMethod("POST").SetRequestBody(fmt.Sprintf(`{"actorTenantId":%d}`, f.tenant.ID+1)).SaveX(f.ctx)
	_, err = f.db.ExecContext(f.ctx, apply)
	require.ErrorContains(t, err, fmt.Sprintf("audit ID %d", audit.ID))
	f.client.AuditLog.UpdateOneID(audit.ID).SetRequestBody("{}").ExecX(f.ctx)
	// Historical native backfill is provable; orphan and conflicting histories block.
	_, err = f.db.ExecContext(f.ctx, "ALTER TABLE intake_requests DROP COLUMN actor_tenant_id")
	require.NoError(t, err)
	for range 2 {
		_, err = f.db.ExecContext(f.ctx, apply)
		require.NoError(t, err)
	}
	receipt := f.client.IntakeRequest.Query().OnlyX(f.ctx)
	require.Equal(t, f.tenant.ID, receipt.ActorTenantID)
	updatedAudit := f.client.AuditLog.GetX(f.ctx, audit.ID)
	var provenance creation.ActorProvenance
	require.NoError(t, json.Unmarshal([]byte(*updatedAudit.RequestBody), &provenance))
	require.Equal(t, receipt.ID, provenance.IntakeRequestID)
	reset, err := os.ReadFile("../../migrations/" + intakeMSPVersion + "_dev_reset.sql")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, string(reset))
	require.ErrorContains(t, err, "requires empty")
	require.NoError(t, f.client.Schema.Create(f.ctx))
	_, err = f.db.ExecContext(f.ctx, string(verify))
	require.NoError(t, err)
	for _, statement := range []string{
		"UPDATE intake_requests SET actor_tenant_id=actor_tenant_id+1", "UPDATE intake_requests SET actor_id=actor_id+1", "DELETE FROM intake_requests", "UPDATE audit_logs SET request_body='{}'", "DELETE FROM audit_logs",
	} {
		_, err = f.db.ExecContext(f.ctx, statement)
		require.ErrorContains(t, err, "immutable")
	}
	_, err = f.db.ExecContext(f.ctx, "ALTER TABLE intake_requests DISABLE TRIGGER intake_receipt_provenance; UPDATE intake_requests SET actor_tenant_id=actor_tenant_id+1")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, apply)
	require.ErrorContains(t, err, fmt.Sprintf("receipt IDs %d", receipt.ID))
	_, err = f.db.ExecContext(f.ctx, "UPDATE intake_requests SET actor_tenant_id=$1", f.tenant.ID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "ALTER TABLE intake_requests ENABLE TRIGGER intake_receipt_provenance")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, apply)
	require.NoError(t, err)
}

type mutateDirectorySnapshot struct {
	directory             database.DirectorySnapshot
	afterImport           func()
	failExport, failClose bool
	serialize             int
	opens                 int
}

func (p *mutateDirectorySnapshot) Open(ctx context.Context, tx *ent.Tx, tenantID int) (*ent.Client, func() error, error) {
	p.opens++
	if p.failExport {
		p.failExport = false
		_ = tx.Rollback()
	}
	client, close, err := p.directory.Open(ctx, tx, tenantID)
	if err == nil && p.serialize > 0 {
		p.serialize--
		_ = close()
		return nil, nil, &pq.Error{Code: "40001", Message: "injected directory read serialization"}
	}
	if err == nil && p.failClose {
		p.failClose = false
		actualClose := close
		close = func() error { return errors.Join(actualClose(), errors.New("injected directory close failure")) }
	}

	if err == nil && p.afterImport != nil {
		run := p.afterImport
		p.afterImport = nil
		run()
	}
	return client, close, err
}

func TestPostgresIntakeMSPSharedSnapshotAndDurableEffects(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	// Test setup/migration uses only this schema owner. Application and workers below use restricted roles.
	migrator := migration.NewMigrator(f.db, zap.NewNop().Sugar())
	require.NoError(t, migrator.EnsureMigrationsTable(f.ctx))
	applied, err := migrator.RunMigrations(f.ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
	require.Equal(t, len(migration.PostSchemaMigrations()), applied)
	require.NoError(t, f.client.Schema.Create(f.ctx))
	applied, err = migrator.RunMigrations(f.ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
	require.Zero(t, applied)
	verify, err := os.ReadFile("../../migrations/" + intakeMSPVersion + "_verify.sql")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, string(verify))
	require.NoError(t, err)

	provider := f.client.Tenant.Create().SetCode("provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("operator").SetName("Original MSP Operator").SetEmail("operator@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("Customer incident operator").SaveX(f.ctx)
	for _, action := range []string{"read", "write", "create_on_behalf"} {
		permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("incident:" + action).SetName(action).SetResource("incident").SetAction(action).SaveX(f.ctx)
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	}
	deployment := f.client.ProcessDeployment.Create().SetTenantID(f.tenant.ID).SetDeploymentID("msp").SetDeploymentName("MSP workflow").SaveX(f.ctx)
	f.client.ProcessDefinition.Create().SetTenantID(f.tenant.ID).SetDeploymentID(deployment.ID).SetKey("msp").SetName("MSP workflow").SetVersion("1").SetIsActive(true).SetIsLatest(true).SetBpmnXML([]byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn"><process id="msp" isExecutable="true"><startEvent id="start"/><userTask id="approval" name="Approval" camunda:assignee="${requester_id}"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="approval"/><sequenceFlow id="b" sourceRef="approval" targetRef="end"/></process></definitions>`)).SaveX(f.ctx)
	f.client.ProcessBinding.Create().SetTenantID(f.tenant.ID).SetBusinessType("incident").SetIsDefault(true).SetProcessDefinitionKey("msp").SaveX(f.ctx)
	f.rule(metricAction("native-msp"))
	clients, cfg := runtimeClients(t, f)
	for _, table := range []string{"intake_resolution_snapshots", "work_item_number_sequences", "process_bindings", "process_definitions", "process_deployments", "process_instances", "process_tasks", "process_audit_logs", "process_callback_outboxes", "sla_definitions", "field_definitions", "field_values", "service_catalogs", "configuration_items", "groups"} {
		_, err := f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
		var sequence *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&sequence))
		if sequence != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*sequence+" TO "+cfg.User)
			require.NoError(t, err)
		}
	}
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	_, err = clients.Tenant.User.Get(ctx, actor.ID)
	require.True(t, ent.IsNotFound(err))
	require.Equal(t, actor.ID, clients.System.User.GetX(f.ctx, actor.ID).ID)
	_, err = clients.System.Role.Query().Count(f.ctx)
	require.Error(t, err)
	_, err = clients.System.Ticket.Query().Count(f.ctx)
	require.Error(t, err)
	logger := zap.NewNop().Sugar()
	owner := service.NewIncidentService(clients.Tenant, logger)
	owner.RuleEngine().SetActorDirectory(clients.System)
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(owner))
	resolver := intake.NewResolver(catalogdomain.NewService(nil, clients.Tenant, logger), service.NewProcessBindingService(clients.Tenant), service.NewConfigurationItemService(clients.Tenant, logger, nil, nil), service.NewTicketCategoryService(clients.Tenant))
	snapshots := &mutateDirectorySnapshot{directory: clients.IntakeDirectorySnapshot()}

	app := intake.NewService(clients.Tenant, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), snapshots)
	identity := creation.Identity{TenantID: f.tenant.ID, ActorID: actor.ID, RequesterID: f.actor.ID, Role: "msp_tech", Channel: "http"}
	command := creation.CreateWorkItemCommand{RecordClass: "incident", IntakeKind: "incident", Confirmation: "confirmed", Title: "Native MSP incident", IdempotencyKey: "msp"}
	for _, stage := range []string{"export", "close", "serialize"} {
		switch stage {
		case "export":
			snapshots.failExport = true
		case "close":
			snapshots.failClose = true
		case "serialize":
			snapshots.serialize = 3
		}
		before := snapshots.opens
		_, failure := app.Create(ctx, identity, command)
		require.ErrorIs(t, failure, creation.ErrInfrastructureUnavailable)
		expected := 1
		if stage == "serialize" {
			expected = 3
		}
		require.Equal(t, expected, snapshots.opens-before)
		require.Zero(t, clients.Tenant.IntakeRequest.Query().Where(intakerequest.IdempotencyKey("msp")).CountX(ctx))
		require.Zero(t, database.GetRawDB().Stats().InUse)
		require.Zero(t, clients.SystemDB.Stats().InUse)
	}
	// PostgreSQL rejects both import-after-query and an expired exporter; both real pools are released.
	for _, expired := range []bool{false, true} {
		tx, beginErr := clients.Tenant.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		require.NoError(t, beginErr)
		rows, exportErr := tx.QueryContext(ctx, "SELECT pg_export_snapshot()")
		require.NoError(t, exportErr)
		require.True(t, rows.Next())
		var snapshot string
		require.NoError(t, rows.Scan(&snapshot))
		require.NoError(t, rows.Close())
		directory, beginErr := clients.System.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		require.NoError(t, beginErr)
		if expired {
			require.NoError(t, tx.Rollback())
		} else {
			_, queryErr := directory.ExecContext(f.ctx, "SELECT 1")
			require.NoError(t, queryErr)
		}
		_, importErr := directory.ExecContext(f.ctx, "SET TRANSACTION SNAPSHOT "+pq.QuoteLiteral(snapshot))
		require.Error(t, importErr)
		require.NoError(t, directory.Rollback())
		if !expired {
			require.NoError(t, tx.Rollback())
		}
		require.Zero(t, database.GetRawDB().Stats().InUse)
		require.Zero(t, clients.SystemDB.Stats().InUse)
	}
	snapshots.afterImport = func() {
		f.client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(f.ctx)
		f.client.RolePermission.Delete().ExecX(f.ctx)
	}
	result, err := app.Create(ctx, identity, command)
	if err != nil {
		t.Logf("creation error: %+v cause=%v", err, errors.Unwrap(err))
	}
	require.NoError(t, err)
	receipt := clients.Tenant.IntakeRequest.Query().Where(intakerequest.WorkItemID(result.WorkItemID)).OnlyX(ctx)
	require.Equal(t, provider.ID, receipt.ActorTenantID)
	require.Equal(t, actor.ID, receipt.ActorID)
	_, err = app.Create(ctx, identity, command)
	require.ErrorIs(t, err, creation.ErrPermissionDenied)
	require.Zero(t, database.GetRawDB().Stats().InUse)
	require.Zero(t, clients.SystemDB.Stats().InUse)
	// Committed policy effects still run after allocation and role revocation.
	event := clients.Tenant.OutboxEvent.Query().Where(outboxevent.EventID(fmt.Sprintf("incident-created:%d", result.WorkItemID))).OnlyX(ctx)
	require.NoError(t, owner.RuleEngine().Deliver(ctx, event))
	require.NoError(t, owner.RuleEngine().Deliver(ctx, event))
	start := clients.Tenant.OutboxEvent.Query().Where(outboxevent.EventType("workflow.start.requested")).OnlyX(ctx)
	engine := service.NewCustomProcessEngine(clients.Tenant, logger).(*service.CustomProcessEngine)
	handler := service.NewWorkflowStartOutboxHandler(clients.Tenant, engine, clients.System)
	require.NoError(t, handler.Deliver(ctx, start))
	require.NoError(t, handler.Deliver(ctx, start))
	instance := clients.Tenant.ProcessInstance.Query().OnlyX(ctx)
	require.Equal(t, fmt.Sprint(actor.ID), instance.Initiator)
	audit := clients.Tenant.ProcessAuditLog.Query().Where(processauditlog.Action(service.AuditActionProcessStarted)).OnlyX(ctx)
	require.Equal(t, actor.ID, audit.UserID)
	require.Equal(t, actor.Name, audit.UserName)
	require.Equal(t, fmt.Sprint(provider.ID), fmt.Sprint(audit.VariablesAfter["actor_tenant_id"]))
	createdAudit := clients.Tenant.AuditLog.Query().Where(auditlog.Action("intake.created")).OnlyX(ctx)
	var evidence creation.ActorProvenance
	require.NoError(t, json.Unmarshal([]byte(*createdAudit.RequestBody), &evidence))
	require.Equal(t, provider.ID, evidence.ActorTenantID)
	for _, stmt := range []string{"UPDATE audit_logs SET user_id=user_id+1 WHERE action='intake.created'", "DELETE FROM audit_logs WHERE action='intake.created'", "UPDATE incident_rule_executions SET actor_id=actor_id+1 WHERE execution_key IS NOT NULL"} {
		_, err = f.db.ExecContext(f.ctx, stmt)
		require.ErrorContains(t, err, "immutable")
	}
	f.client.User.UpdateOneID(actor.ID).SetActive(false).ExecX(f.ctx)
	require.Error(t, owner.RuleEngine().Deliver(ctx, event))
	require.Error(t, handler.Deliver(ctx, start))
	_, err = clients.Tenant.Ticket.Create().SetTenantID(provider.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetTitle("forbidden").SetTicketNumber("WRONG-TENANT").Save(ctx)
	require.Error(t, err)
	require.Zero(t, database.GetRawDB().Stats().InUse)
	require.Zero(t, clients.SystemDB.Stats().InUse)
	t.Log("native actor hidden by runtime; shared RR survives concurrent allocation/permission revocation; fresh replay denied; restricted worker start and Incident action replay preserve provenance; pools released")
}

type conversionHTTPEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type conversionHTTPGraph struct {
	workItems, problems, relations, events, receipts, audits, resolutionSnapshots, outboxes int
	sequenceState                                                                           string
}

func readConversionHTTPGraph(t *testing.T, client *ent.Client) conversionHTTPGraph {
	t.Helper()
	ctx := context.Background()
	sequences := client.WorkItemNumberSequence.Query().AllX(ctx)
	sequenceValues := make([]string, 0, len(sequences))
	for _, sequence := range sequences {
		sequenceValues = append(sequenceValues, fmt.Sprintf("%d:%s:%d", sequence.TenantID, sequence.Period, sequence.LastValue))
	}
	sort.Strings(sequenceValues)
	return conversionHTTPGraph{
		workItems:           client.Ticket.Query().CountX(ctx),
		problems:            client.Problem.Query().CountX(ctx),
		relations:           client.WorkItemRelation.Query().CountX(ctx),
		events:              client.IncidentEvent.Query().CountX(ctx),
		receipts:            client.IntakeRequest.Query().CountX(ctx),
		audits:              client.AuditLog.Query().CountX(ctx),
		resolutionSnapshots: client.IntakeResolutionSnapshot.Query().CountX(ctx),
		outboxes:            client.OutboxEvent.Query().CountX(ctx),
		sequenceState:       strings.Join(sequenceValues, ","),
	}
}

func conversionHTTPRouter(jwtSecret string, clients *database.RuntimeClients, incidentController *controller.IncidentController, directory *ent.Client) *gin.Engine {
	router := gin.New()
	router.Use(middleware.AuthMiddleware(jwtSecret))
	router.Use(middleware.RBACMiddleware(clients.Tenant, directory))
	api := router.Group("/api/v1")
	api.Use(middleware.TenantMiddleware(directory))
	api.POST("/incidents/:id/convert-to-problem", middleware.RequirePermission("incident", "write"), incidentController.ConvertToProblem)
	return router
}

func executeConversionHTTP(t *testing.T, router http.Handler, token string, incidentID int, key, body string) (int, conversionHTTPEnvelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/incidents/%d/convert-to-problem", incidentID), bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	var envelope conversionHTTPEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope), recorder.Body.String())
	return recorder.Code, envelope
}

func TestPostgresIncidentConversionSignedMSPHTTPAuthorizationAndReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newIncidentEffectsFixture(t)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	migrator := migration.NewMigrator(f.db, zap.NewNop().Sugar())
	require.NoError(t, migrator.EnsureMigrationsTable(f.ctx))
	_, err := migrator.RunMigrations(f.ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
	require.NoError(t, f.client.Schema.Create(f.ctx))

	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").SetStatus("active").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("conversion-provider").SetName("Conversion Provider").SetType("msp_provider").SetStatus("active").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("conversion-operator").SetName("Conversion Operator").SetEmail("conversion-operator@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	requester := f.actor
	secondRequester := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("second-requester").SetName("Second Requester").SetEmail("second-requester@example.test").SetPasswordHash("test").SetRole("requester").SetActive(true).SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("Customer conversion operator").SetIsActive(true).SaveX(f.ctx)
	rolePermissions := make(map[string]*ent.RolePermission)
	for _, grant := range []struct{ resource, action string }{
		{"incident", "read"}, {"incident", "write"},
		{"problem", "read"}, {"problem", "write"}, {"problem", "create_on_behalf"},
	} {
		permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(grant.resource + ":" + grant.action).SetName(grant.resource + ":" + grant.action).SetResource(grant.resource).SetAction(grant.action).SaveX(f.ctx)
		rolePermissions[grant.resource+":"+grant.action] = f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	}
	f.client.ProcessBinding.Create().SetTenantID(f.tenant.ID).SetBusinessType("problem").SetIsDefault(true).SetIsActive(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(f.ctx)

	clients, cfg := runtimeClients(t, f)
	for _, table := range []string{"problems", "work_item_relations", "work_item_number_sequences", "intake_resolution_snapshots", "process_bindings", "sla_definitions", "field_definitions", "field_values", "service_catalogs", "configuration_items", "groups"} {
		_, err = f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
		var sequence *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&sequence))
		if sequence != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*sequence+" TO "+cfg.User)
			require.NoError(t, err)
		}
	}

	logger := zap.NewNop().Sugar()
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(problemdomain.NewService(problemdomain.NewEntRepository(clients.Tenant), logger)))
	resolver := intake.NewResolver(catalogdomain.NewService(nil, clients.Tenant, logger), service.NewProcessBindingService(clients.Tenant), service.NewConfigurationItemService(clients.Tenant, logger, nil, nil), service.NewTicketCategoryService(clients.Tenant))
	app := intake.NewService(clients.Tenant, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), clients.IntakeDirectorySnapshot())
	incidentController := controller.NewIncidentController(nil, nil, nil, nil, nil, logger)
	incidentController.SetCreationApplication(app)
	const jwtSecret = "isolated-conversion-http-signing-key"
	authService := service.NewAuthService(clients.Tenant, clients.System, jwtSecret, logger)
	session, err := authService.SwitchTenant(f.ctx, actor.ID, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, "msp_tech", session.User.Role)
	router := conversionHTTPRouter(jwtSecret, clients, incidentController, clients.System)
	body := fmt.Sprintf(`{"requesterId":%d,"title":"SSLVPN customer outage","description":"Customer cannot connect","rootCause":"Gateway instability"}`, requester.ID)

	status, envelope := executeConversionHTTP(t, router, session.AccessToken, f.inc.ID, "signed-msp-conversion", body)
	require.Equal(t, http.StatusCreated, status, envelope.Message)
	require.Equal(t, 0, envelope.Code)
	var first creation.CreateWorkItemResult
	require.NoError(t, json.Unmarshal(envelope.Data, &first))
	require.False(t, first.Replayed)
	require.Equal(t, creation.RecordClassProblem, first.RecordClass)
	status, envelope = executeConversionHTTP(t, router, session.AccessToken, f.inc.ID, "signed-msp-conversion", body)
	require.Equal(t, http.StatusOK, status, envelope.Message)
	var replay creation.CreateWorkItemResult
	require.NoError(t, json.Unmarshal(envelope.Data, &replay))
	require.True(t, replay.Replayed)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	require.Equal(t, first.Number, replay.Number)

	created := f.client.Ticket.Query().Where(ticket.IDEQ(first.WorkItemID)).OnlyX(f.ctx)
	require.Equal(t, f.tenant.ID, created.TenantID)
	require.Equal(t, requester.ID, created.RequesterID)
	require.Equal(t, actor.ID, created.OpenedByID)
	require.Equal(t, creation.RecordClassProblem, created.RecordClass)
	require.Equal(t, 1, f.client.Problem.Query().CountX(f.ctx))
	relation := f.client.WorkItemRelation.Query().OnlyX(f.ctx)
	require.Equal(t, f.inc.WorkItemID, relation.SourceWorkItemID)
	require.Equal(t, created.ID, relation.TargetWorkItemID)
	require.Equal(t, "investigated_by", relation.RelationType)
	require.Equal(t, actor.ID, relation.CreatedByID)
	event := f.client.IncidentEvent.Query().OnlyX(f.ctx)
	require.Equal(t, f.inc.ID, event.IncidentID)
	require.Equal(t, actor.ID, event.UserID)
	require.Equal(t, "convert_to_problem", event.EventName)
	receipt := f.client.IntakeRequest.Query().Where(intakerequest.WorkItemID(first.WorkItemID)).OnlyX(f.ctx)
	require.Equal(t, provider.ID, receipt.ActorTenantID)
	require.Equal(t, actor.ID, receipt.ActorID)
	require.Equal(t, requester.ID, receipt.RequesterID)
	conversionAudit := f.client.AuditLog.Query().Where(auditlog.Action("convert_to_problem")).OnlyX(f.ctx)
	var provenance creation.ActorProvenance
	require.NoError(t, json.Unmarshal([]byte(*conversionAudit.RequestBody), &provenance))
	require.Equal(t, actor.ID, provenance.ActorUserID)
	require.Equal(t, provider.ID, provenance.ActorTenantID)
	require.Equal(t, f.tenant.ID, provenance.TargetTenantID)
	require.Equal(t, first.WorkItemID, provenance.WorkItemID)
	source := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	require.Equal(t, requester.ID, source.RequesterID)
	require.Equal(t, "new", source.Status)

	stableGraph := readConversionHTTPGraph(t, f.client)
	assertDeniedReplay := func(name string, wantStatus int, mutate func(), restore func()) {
		t.Helper()
		mutate()
		status, denied := executeConversionHTTP(t, router, session.AccessToken, f.inc.ID, "signed-msp-conversion", body)
		require.Equal(t, wantStatus, status, "%s: %s", name, denied.Message)
		require.NotEqual(t, 0, denied.Code, name)
		require.Equal(t, stableGraph, readConversionHTTPGraph(t, f.client), name)
		require.Equal(t, provider.ID, f.client.IntakeRequest.GetX(f.ctx, receipt.ID).ActorTenantID, name)
		restore()
	}

	assertDeniedReplay("deassigned allocation", http.StatusForbidden,
		func() { f.client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(f.ctx) },
		func() { f.client.MSPAllocation.UpdateOneID(allocation.ID).ClearDeassignedAt().ExecX(f.ctx) })
	assertDeniedReplay("removed allocation", http.StatusForbidden,
		func() { f.client.MSPAllocation.DeleteOneID(allocation.ID).ExecX(f.ctx) },
		func() {
			allocation = f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
		})
	problemWrite := rolePermissions["problem:write"]
	assertDeniedReplay("professional permission removed", http.StatusForbidden,
		func() { f.client.RolePermission.DeleteOneID(problemWrite.ID).ExecX(f.ctx) },
		func() {
			problemWrite = f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(problemWrite.PermissionID).SaveX(f.ctx)
		})
	assertDeniedReplay("target role inactive", http.StatusForbidden,
		func() { f.client.Role.UpdateOneID(role.ID).SetIsActive(false).ExecX(f.ctx) },
		func() { f.client.Role.UpdateOneID(role.ID).SetIsActive(true).ExecX(f.ctx) })
	assertDeniedReplay("mapped role changed", http.StatusUnauthorized,
		func() { f.client.User.UpdateOneID(actor.ID).SetMspRole("provider_admin").ExecX(f.ctx) },
		func() { f.client.User.UpdateOneID(actor.ID).SetMspRole("provider_agent").ExecX(f.ctx) })
	assertDeniedReplay("actor inactive", http.StatusUnauthorized,
		func() { f.client.User.UpdateOneID(actor.ID).SetActive(false).ExecX(f.ctx) },
		func() { f.client.User.UpdateOneID(actor.ID).SetActive(true).ExecX(f.ctx) })
	assertDeniedReplay("requester inactive", http.StatusForbidden,
		func() { f.client.User.UpdateOneID(requester.ID).SetActive(false).ExecX(f.ctx) },
		func() { f.client.User.UpdateOneID(requester.ID).SetActive(true).ExecX(f.ctx) })
	assertDeniedReplay("customer inactive", http.StatusForbidden,
		func() { f.client.Tenant.UpdateOneID(f.tenant.ID).SetStatus("suspended").ExecX(f.ctx) },
		func() { f.client.Tenant.UpdateOneID(f.tenant.ID).SetStatus("active").ExecX(f.ctx) })
	assertDeniedReplay("customer expired", http.StatusForbidden,
		func() { f.client.Tenant.UpdateOneID(f.tenant.ID).SetExpiresAt(time.Now().Add(-time.Hour)).ExecX(f.ctx) },
		func() { f.client.Tenant.UpdateOneID(f.tenant.ID).ClearExpiresAt().ExecX(f.ctx) })

	for _, invalid := range []struct {
		name, key, body string
		wantStatus      int
	}{
		{"missing cross-customer requester", "missing-requester", `{}`, http.StatusBadRequest},
		{"actor self requester", "self-requester", fmt.Sprintf(`{"requesterId":%d}`, actor.ID), http.StatusBadRequest},
	} {
		status, denied := executeConversionHTTP(t, router, session.AccessToken, f.inc.ID, invalid.key, invalid.body)
		require.Equal(t, invalid.wantStatus, status, "%s: %s", invalid.name, denied.Message)
		var detail struct {
			ErrorCode   creation.ErrorCode    `json:"errorCode"`
			FieldErrors []creation.FieldError `json:"fieldErrors"`
		}
		require.NoError(t, json.Unmarshal(denied.Data, &detail), invalid.name)
		require.Equal(t, creation.InvalidCommand, detail.ErrorCode, invalid.name)
		require.NotEmpty(t, detail.FieldErrors, invalid.name)
		require.Equal(t, "requesterId", detail.FieldErrors[0].Field, invalid.name)
		require.Equal(t, stableGraph, readConversionHTTPGraph(t, f.client), invalid.name)
	}

	changedBody := fmt.Sprintf(`{"requesterId":%d,"title":"SSLVPN customer outage","description":"Customer cannot connect","rootCause":"Gateway instability"}`, secondRequester.ID)
	status, envelope = executeConversionHTTP(t, router, session.AccessToken, f.inc.ID, "signed-msp-conversion", changedBody)
	require.Equal(t, http.StatusConflict, status, envelope.Message)
	require.Equal(t, stableGraph, readConversionHTTPGraph(t, f.client))

	missingDirectoryRouter := conversionHTTPRouter(jwtSecret, clients, incidentController, nil)
	status, envelope = executeConversionHTTP(t, missingDirectoryRouter, session.AccessToken, f.inc.ID, "missing-directory", body)
	require.Equal(t, http.StatusServiceUnavailable, status, envelope.Message)
	require.Equal(t, stableGraph, readConversionHTTPGraph(t, f.client))

	foreignTenant := f.client.Tenant.Create().SetCode("foreign-customer").SetName("Foreign Customer").SetType("msp_customer").SetStatus("active").SaveX(f.ctx)
	foreignRequester := f.client.User.Create().SetTenantID(foreignTenant.ID).SetUsername("foreign-requester").SetName("Foreign Requester").SetEmail("foreign-requester@example.test").SetPasswordHash("test").SetRole("requester").SetActive(true).SaveX(f.ctx)
	foreignItem := f.client.Ticket.Create().SetTenantID(foreignTenant.ID).SetRequesterID(foreignRequester.ID).SetOpenedByID(foreignRequester.ID).SetTitle("Foreign source").SetTicketNumber("INC-FOREIGN").SetRecordClass("incident").SetStatus("new").SetPriority("high").SaveX(f.ctx)
	foreignIncident := f.client.Incident.Create().SetWorkItemID(foreignItem.ID).SetSeverity("high").SetDetectedAt(time.Now()).SaveX(f.ctx)
	beforeForeign := readConversionHTTPGraph(t, f.client)
	status, envelope = executeConversionHTTP(t, router, session.AccessToken, foreignIncident.ID, "foreign-source", body)
	require.Equal(t, http.StatusNotFound, status, envelope.Message)
	require.Equal(t, beforeForeign, readConversionHTTPGraph(t, f.client))

	deletedItem := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(requester.ID).SetOpenedByID(requester.ID).SetTitle("Deleted source").SetTicketNumber("INC-DELETED").SetRecordClass("incident").SetStatus("new").SetPriority("high").SetDeletedAt(time.Now()).SaveX(f.ctx)
	deletedIncident := f.client.Incident.Create().SetWorkItemID(deletedItem.ID).SetSeverity("high").SetDetectedAt(time.Now()).SaveX(f.ctx)
	beforeDeleted := readConversionHTTPGraph(t, f.client)
	status, envelope = executeConversionHTTP(t, router, session.AccessToken, deletedIncident.ID, "deleted-source", body)
	require.Equal(t, http.StatusNotFound, status, envelope.Message)
	require.Equal(t, beforeDeleted, readConversionHTTPGraph(t, f.client))

	finalItem := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(requester.ID).SetOpenedByID(requester.ID).SetTitle("Closed source").SetTicketNumber("INC-CLOSED").SetRecordClass("incident").SetStatus("closed").SetPriority("high").SaveX(f.ctx)
	finalIncident := f.client.Incident.Create().SetWorkItemID(finalItem.ID).SetSeverity("high").SetDetectedAt(time.Now()).SaveX(f.ctx)
	beforeFinal := readConversionHTTPGraph(t, f.client)
	status, envelope = executeConversionHTTP(t, router, session.AccessToken, finalIncident.ID, "closed-source", body)
	require.Equal(t, http.StatusBadRequest, status, envelope.Message)
	require.Equal(t, beforeFinal, readConversionHTTPGraph(t, f.client))

	require.Zero(t, database.GetRawDB().Stats().InUse)
	require.Zero(t, clients.SystemDB.Stats().InUse)
	t.Log("actual signed SwitchTenant token passed Auth, current RBAC, Tenant, route permission and conversion; every current revocation denied replay and preserved the original graph")
}

func TestPostgresIntakeMSPEmptyDevelopmentReset(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.client.IntakeRequest.Delete().ExecX(f.ctx)
	apply := migration.GetMigrationSQL(intakeMSPVersion)
	_, err := f.db.ExecContext(f.ctx, apply)
	require.NoError(t, err)
	reset, err := os.ReadFile("../../migrations/" + intakeMSPVersion + "_dev_reset.sql")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, string(reset))
	require.NoError(t, err)
	require.NoError(t, f.client.Schema.Create(f.ctx))
	_, err = f.db.ExecContext(f.ctx, apply)
	require.NoError(t, err)
}

func TestPostgresNativeAuthorizationUsesTargetTransactionScope(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("agent").SetName("Agent").SaveX(f.ctx)
	for _, action := range []string{"read", "write"} {
		permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("ticket:" + action).SetName(action).SetResource("ticket").SetAction(action).SaveX(f.ctx)
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	}
	clients, _ := runtimeClients(t, f)
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	tx, err := clients.Tenant.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	err = authorization.AuthorizeNativeWorkItemCreation(ctx, tx, creation.Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, RequesterID: f.actor.ID, Role: f.actor.Role, Channel: "feishu", Provider: "feishu"}, creation.CreateWorkItemCommand{RecordClass: "generic"})
	require.NoError(t, err)
}
