//go:build candidate_scope

package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	webhookconnector "itsm-backend/connector/builtin/webhook"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	feishu "itsm-backend/connector/builtin/feishu"
	"itsm-backend/connector/marketplace"
	"itsm-backend/controller"
	marketplacecontroller "itsm-backend/controller/marketplace"
	"itsm-backend/database"
	"itsm-backend/ent/auditlog"
	aidomain "itsm-backend/handlers/ai"
	changedomain "itsm-backend/handlers/change"
	srdomain "itsm-backend/handlers/service_request"
	"itsm-backend/handlers/shared/workflowcallback"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/connectorconfig"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/kaftaskcompletionreceipt"
	"itsm-backend/ent/marketplaceitem"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/servicerequestaccesssnapshot"
	"itsm-backend/ent/tenantinstallation"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketcc"
	"itsm-backend/ent/ticketnotification"
	"itsm-backend/handlers/common/accessgrant"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	problemdomain "itsm-backend/handlers/problem"
	catalogdomain "itsm-backend/handlers/service_catalog"
	"itsm-backend/handlers/shared"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/middleware"
	"itsm-backend/migration"
	"itsm-backend/pkg/eventbus"
	ticketrepo "itsm-backend/repository/ticket"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	cloudrunner "itsm-backend/service/cloud"
	marketplaceservice "itsm-backend/service/marketplace"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

// This uses the real intake service, resolver, base writer and professional
// owners, enforced tenant driver and restricted production directory snapshot.
// No application runtime or external delivery is started.
func TestCandidateIntakeCreationBoundary(t *testing.T) {
	socket, dsn := candidatePrivatePostgresDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name := "intake_scope_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := sql.Open("postgres", dsn("postgres", "candidate_test_owner"))
	require.NoError(t, err)
	defer admin.Close()
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	runtimeRole := name + "_run"
	roleCreated := false
	systemRole := name + "_system"
	systemCreated := false
	standardRole := name + "_standard"
	standardCreated := false
	var standardFixtureClient *ent.Client
	defer func() {
		if standardFixtureClient != nil {
			require.NoError(t, standardFixtureClient.Close())
		}
		_, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		require.NoError(t, err)
		if systemCreated {
			_, err = admin.Exec("DROP ROLE " + systemRole)
			require.NoError(t, err)
		}
		if standardCreated {
			_, err = admin.Exec("DROP ROLE " + standardRole)
			require.NoError(t, err)
		}
		if roleCreated {
			_, err = admin.Exec("DROP ROLE " + runtimeRole)
			require.NoError(t, err)
		}
	}()
	_, err = admin.ExecContext(ctx, "CREATE ROLE "+runtimeRole+" LOGIN")
	require.NoError(t, err)
	roleCreated = true
	_, err = admin.ExecContext(ctx, "CREATE ROLE "+systemRole+" LOGIN BYPASSRLS NOINHERIT")
	require.NoError(t, err)
	systemCreated = true
	ownerDB, err := sql.Open("postgres", dsn(name, "candidate_test_owner"))
	require.NoError(t, err)
	defer ownerDB.Close()
	owner := ent.NewClient(ent.Driver(entsql.OpenDB("postgres", ownerDB)))
	require.NoError(t, owner.Schema.Create(ctx))
	tenant := owner.Tenant.Create().SetName("Candidate intake").SetCode("candidate-intake").SaveX(ctx)
	actor := owner.User.Create().SetTenantID(tenant.ID).SetUsername("candidate").SetName("Candidate").SetEmail("candidate@example.invalid").SetPasswordHash("test-only").SetRole("requester").SaveX(ctx)
	role := owner.Role.Create().SetTenantID(tenant.ID).SetName("Requester").SetCode("requester").SaveX(ctx)
	permission := owner.Permission.Create().SetTenantID(tenant.ID).SetCode("create-work").SetName("Create work").SetResource("*").SetAction("*").SaveX(ctx)
	roleLink := owner.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
	for _, class := range []string{"generic", "incident", "problem", "change_request", "service_request_item"} {
		owner.ProcessBinding.Create().SetTenantID(tenant.ID).SetBusinessType(class).SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	}
	identity := creation.Identity{TenantID: tenant.ID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "http"}
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)
	catalog := owner.ServiceCatalog.Create().SetTenantID(tenant.ID).SetName("Candidate request").SetCategory("general").SetDescription("candidate").SetTargetClass("service_request_item").SetRequiresApproval(false).SetStatus("enabled").SaveX(ctx)
	catalogTx, err := owner.Tx(ctx)
	require.NoError(t, err)
	resolvedCatalog, _, err := catalogdomain.NewService(nil, owner, zap.NewNop().Sugar(), nil).ResolveCreationCatalog(ctx, catalogTx, identity, catalog.ID)
	require.NoError(t, err)
	require.NoError(t, catalogTx.Rollback())
	command := func(key, class string) creation.CreateWorkItemCommand {
		cmd := creation.CreateWorkItemCommand{RecordClass: class, IntakeKind: class, Confirmation: "confirmed", Title: "scope " + key, IdempotencyKey: key}
		if class == "change_request" {
			cmd.Change = &creation.ChangeInput{Type: "normal", Justification: "candidate", ImpactScope: "low", RiskLevel: "low", ImplementationPlan: "candidate implementation", RollbackPlan: "candidate rollback"}
		}
		if class == "service_request_item" {
			cmd.IntakeKind = "catalog_item"
			cmd.CatalogItemID, cmd.CatalogVersion, cmd.FormSchemaVersion = &catalog.ID, resolvedCatalog.Version, resolvedCatalog.FormSchemaVersion
		}
		return cmd
	}
	application := func(client *ent.Client, policy *database.ExecutionPolicy, directory database.DirectorySnapshot) *intake.Service {
		logger := zap.NewNop().Sugar()
		registry := intake.NewCreatorRegistry()
		for _, creator := range []creation.ProfessionalCreator{&service.TicketService{}, service.NewIncidentService(client, logger, policy), problemdomain.NewService(nil, logger, policy), changedomain.NewService(nil, client, logger, policy), srdomain.NewService(nil, client, logger, service.NewApprovalChainResolver(client, logger), policy)} {
			require.NoError(t, registry.Register(creator))
		}
		resolver := intake.NewResolver(catalogdomain.NewService(nil, client, logger, nil), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
		return intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), directory, policy)
	}
	historicalApp := application(owner, executionfixture.Standard(), sameTransactionDirectory{})
	historicalTool := owner.ToolInvocation.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetToolName("create_ticket").SetArguments(`{"title":"Historical approved tool"}`).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(actor.ID).SetApprovedAt(time.Now().Add(-time.Hour)).SetStatus("pending").SaveX(ctx)
	oldCommand := command("historical", "incident")
	historical, err := historicalApp.Create(ctx, identity, oldCommand)
	require.NoError(t, err)
	historicalProblem, err := historicalApp.Create(ctx, identity, command("historical-problem", "problem"))
	require.NoError(t, err)
	legacyProblemOwner := problemdomain.NewService(problemdomain.NewEntRepository(owner), zap.NewNop().Sugar(), executionfixture.Standard())
	legacyProblemOwner.SetDirectorySnapshot(sameTransactionDirectory{})
	legacyProblemTitle := "historical problem title"
	legacyProblemCommand := problemdomain.MetadataCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: 1, Source: "http", OperationID: "legacy-problem-metadata"}, ProblemID: historicalProblem.ProfessionalReference.ID, Patch: dto.UpdateProblemRequest{Title: &legacyProblemTitle}}
	legacyProblemResult, err := legacyProblemOwner.ApplyMetadata(ctx, legacyProblemCommand)
	require.NoError(t, err)
	legacyCommandProblem, err := historicalApp.Create(ctx, identity, command("historical-command-problem", "problem"))
	require.NoError(t, err)
	legacyProblemLifecycle := problemdomain.Command{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: 1, Source: "http", OperationID: "legacy-problem-investigate"}, ProblemID: legacyCommandProblem.ProfessionalReference.ID, Action: "investigate"}
	legacyProblemLifecycleResult, err := legacyProblemOwner.ApplyCommand(ctx, legacyProblemLifecycle)
	require.NoError(t, err)
	historicalChange, err := historicalApp.Create(ctx, identity, command("historical-change", "change_request"))
	require.NoError(t, err)
	legacyPIRService := service.NewChangePIRService(owner, zap.NewNop().Sugar(), executionfixture.Standard())
	legacyPIRService.SetDirectorySnapshot(sameTransactionDirectory{})
	legacyPIRMeta := workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: 1, Source: "http", OperationID: "legacy-pir"}
	legacyPIR, err := legacyPIRService.CreatePIR(ctx, &dto.CreateChangePIRRequest{ChangeID: historicalChange.ProfessionalReference.ID, OverallResult: "successful"}, legacyPIRMeta)
	require.NoError(t, err)
	historicalRequest, err := historicalApp.Create(ctx, identity, command("historical-request", "service_request_item"))
	require.NoError(t, err)
	historicalKafRequest, err := historicalApp.Create(ctx, identity, command("historical-kaf-request", "service_request_item"))
	require.NoError(t, err)
	legacyCommand := dto.IncidentCommand{IncidentID: historical.ProfessionalReference.ID, Action: "acknowledge", Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: 1, Source: "http", OperationID: "legacy-command"}}
	legacyOwner := service.NewIncidentService(owner, zap.NewNop().Sugar(), executionfixture.Standard())
	legacyOwner.SetDirectorySnapshot(sameTransactionDirectory{})
	legacyResult, err := legacyOwner.ApplyIncidentCommand(ctx, legacyCommand)
	require.NoError(t, err)
	legacyAlert, err := service.NewIncidentAlertingService(owner, zap.NewNop().Sugar(), executionfixture.Standard()).CreateIncidentAlert(service.WithIncidentAlertActor(ctx, actor.ID, "user", "legacy-alert-fixture"), &dto.CreateIncidentAlertRequest{IncidentID: historical.ProfessionalReference.ID, AlertType: "legacy", AlertName: "legacy alert", Message: "historical fixture", Channels: []string{"in_app"}, Recipients: []string{actor.Email}}, tenant.ID)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, "UPDATE outbox_events SET execution_work_item_id=NULL")
	require.NoError(t, err) // pre-039 historical fixture only
	oldRow := owner.Ticket.GetX(ctx, historical.WorkItemID)
	oldReceipt := owner.IntakeRequest.Query().Where(intakerequest.WorkItemIDEQ(historical.WorkItemID)).OnlyX(ctx)
	// Restored queue fixtures predate execution scope migration and keep NULL refs.

	legacyOrdered := owner.OutboxEvent.Create().SetEventID("legacy-ordered-barrier").SetEventType("candidate-ordered-barrier").SetTenantID(tenant.ID).SetAggregateType("test_target").SetAggregateID("legacy-target").SetPayload(json.RawMessage(`{}`)).SaveX(ctx)
	var historicalOutbox []*ent.OutboxEvent
	for _, state := range []string{"pending", "unknown", "expired", "ambiguous"} {
		kind := "candidate-test-delivery"
		if state == "unknown" {
			kind = "candidate-test-unregistered"
		}
		create := owner.OutboxEvent.Create().SetEventID("historical-queue-" + state).SetEventType(kind).SetTenantID(tenant.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(historical.WorkItemID)).SetPayload(json.RawMessage(`{}`)).SetNextAttemptAt(time.Now().Add(-time.Hour))
		if state == "expired" || state == "ambiguous" {
			create.SetStatus("publishing").SetClaimToken("old-" + state).SetClaimExpiresAt(time.Now().Add(-time.Minute))
		}
		if state == "ambiguous" {
			create.SetLastError("delivery_attempt_started:old-attempt")
		}
		historicalOutbox = append(historicalOutbox, create.SaveX(ctx))
	}

	legacySLADefinition := owner.SLADefinition.Create().SetName("Candidate scope SLA").SetResponseTime(60).SetResolutionTime(240).SetTenantID(tenant.ID).SaveX(ctx)
	// Historical SLA deadlines are restored facts, established before scope migration.
	owner.Ticket.UpdateOneID(historical.WorkItemID).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(-time.Hour)).SetSLAResolutionDeadline(time.Now().Add(-time.Hour)).SaveX(ctx)
	historicalAlertItems := make([]int, 0, 2)
	for _, key := range []string{"alert-scan", "alert-warning"} {
		item := owner.Ticket.Create().SetTitle("historical " + key).SetTicketNumber("OLD-" + key).SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetCreatedAt(time.Now().Add(-time.Hour)).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(5 * time.Minute)).SaveX(ctx)
		historicalAlertItems = append(historicalAlertItems, item.ID)
	}
	legacySLAAlertRule := owner.SLAAlertRule.Create().SetName("Critical SLA admission").SetTenantID(tenant.ID).SetSLADefinitionID(legacySLADefinition.ID).SetAlertLevel("critical").SetThresholdPercentage(20).SetNotificationChannels([]string{"in_app", "email"}).SaveX(ctx)

	legacyManualItem, err := historicalApp.Create(ctx, identity, command("legacy-manual-receipt", "generic"))
	require.NoError(t, err)
	legacyManualCommand := dto.TicketEscalationCommand{WorkItemID: legacyManualItem.WorkItemID, Reason: "legacy confirmed upgrade", Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: 1, Source: "http", OperationID: "legacy-manual-command"}}
	// Historical command receipts use an explicit in-app preference. Independent
	// NULL-target email rows below model the old transport protocol for migrations.
	legacyCommandPreference := owner.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetEventType("ticket_updated").SetInAppEnabled(true).SetEmailEnabled(false).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
	legacyNotifier := service.NewTicketNotificationService(owner, zap.NewNop().Sugar(), executionfixture.Standard())
	legacyNotifier.SetNotificationPreferenceService(service.NewNotificationPreferenceService(owner, zap.NewNop().Sugar()))
	legacyManualOwner := service.NewTicketService(&service.TicketServiceConfig{Execution: executionfixture.Standard(), Client: owner, Repository: ticketrepo.NewEntRepository(owner, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), NotificationService: legacyNotifier})
	legacyManualResult, err := legacyManualOwner.EscalateTicket(ctx, legacyManualCommand)
	require.NoError(t, err)
	legacyEditItem, err := historicalApp.Create(ctx, identity, command("legacy-edit-receipt", "generic"))
	require.NoError(t, err)
	legacyEditCommand := dto.TicketEditCommand{WorkItemID: legacyEditItem.WorkItemID, Fields: dto.TicketEditFields{Title: "Historical confirmed edit", Status: "open"}, Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: 1, Source: "http", OperationID: "legacy-edit-command"}}
	legacyEditResult, err := legacyManualOwner.UpdateTicket(ctx, legacyEditCommand)
	require.NoError(t, err)
	laterLegacyEdit := legacyEditCommand
	laterLegacyEdit.Meta.ExpectedVersion = legacyEditResult.Version
	laterLegacyEdit.Meta.OperationID = "legacy-edit-later"
	laterLegacyEdit.Fields.Status = "in_progress"
	_, err = legacyManualOwner.UpdateTicket(ctx, laterLegacyEdit)
	require.NoError(t, err)
	require.NoError(t, owner.NotificationPreference.DeleteOne(legacyCommandPreference).Exec(ctx))
	legacySLAHistory := owner.SLAAlertHistory.Create().SetTicketID(historicalAlertItems[0]).SetTicketNumber("OLD-alert-scan").SetTicketTitle("historical alert").SetAlertRuleID(legacySLAAlertRule.ID).SetAlertRuleName(legacySLAAlertRule.Name).SetTenantID(tenant.ID).SetNotificationSent(true).SaveX(ctx)
	var historicalNotifications []*ent.TicketNotification
	for _, state := range []string{"pending", "processing"} {
		create := owner.TicketNotification.Create().SetTenantID(tenant.ID).SetTicketID(historical.WorkItemID).SetUserID(actor.ID).SetType("created").SetChannel("email").SetContent("Historical notification").SetDeliveryKey("legacy-notify-" + state).SetStatus(state).SetNextAttemptAt(time.Now().Add(-time.Minute))
		if state == "processing" {
			create.SetLeaseOwner("legacy-notification-worker").SetLeaseExpiresAt(time.Now().Add(-time.Minute)).SetAttemptCount(2)
		}
		historicalNotifications = append(historicalNotifications, create.SaveX(ctx))
	}
	legacyDeployment := owner.ProcessDeployment.Create().SetDeploymentID("legacy-callback").SetDeploymentName("Legacy callback").SetTenantID(tenant.ID).SaveX(ctx)
	legacyDefinition := owner.ProcessDefinition.Create().SetKey("legacy-callback").SetName("Legacy callback").SetVersion("1").SetIsLatest(true).SetBpmnXML([]byte(`<definitions/>`)).SetDeploymentID(legacyDeployment.ID).SetTenantID(tenant.ID).SaveX(ctx)
	legacyInstance := owner.ProcessInstance.Create().SetProcessInstanceID("legacy-callback").SetProcessDefinitionKey("legacy-callback").SetProcessDefinitionID(legacyDefinition.ID).SetBusinessKey(fmt.Sprintf("incident:%d", historical.WorkItemID)).SetBusinessType("incident").SetBusinessID(historical.WorkItemID).SetStatus("running").SetTenantID(tenant.ID).SaveX(ctx)
	var historicalCallbacks []*ent.ProcessCallbackOutbox
	for _, state := range []string{"pending", "processing"} {
		create := owner.ProcessCallbackOutbox.Create().SetExecutionKey("legacy-callback-" + state).SetTenantID(tenant.ID).SetProcessInstanceID(legacyInstance.ID).SetCallbackKind("service_task").SetHandlerID("unregistered-historical-handler").SetTaskType("historical-task").SetElementID("Historical").SetStatus(state).SetNextAttemptAt(time.Now().Add(-time.Minute))
		if state == "processing" {
			create.SetLeaseOwner("historical-worker").SetLeaseExpiresAt(time.Now().Add(-time.Minute)).SetAttemptCount(2)
		}
		historicalCallbacks = append(historicalCallbacks, create.SaveX(ctx))
	}
	_, err = ownerDB.ExecContext(ctx, fmt.Sprintf("GRANT USAGE ON SCHEMA public TO %s; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO %s; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO %s", runtimeRole, runtimeRole, runtimeRole))
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, migration.GetMigrationSQL("039_candidate_execution_scope"))
	require.NoError(t, err)
	// Exercise a real upgrade from absent provenance columns; Ent's generated
	// schema must not hide missing ALTER statements in migration 040.
	_, err = ownerDB.ExecContext(ctx, `ALTER TABLE ticket_notifications DROP COLUMN sla_alert_history_id; ALTER TABLE sla_alert_histories DROP COLUMN notification_tracking_version; DROP INDEX slaalerthistory_id_tenant_id_ticket_id`)
	require.NoError(t, err)
	var oldAlertsBefore, oldNotificationsBefore string
	require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(h) ORDER BY id)::text,'[]') FROM sla_alert_histories h`).Scan(&oldAlertsBefore))
	require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(n) ORDER BY id)::text,'[]') FROM ticket_notifications n`).Scan(&oldNotificationsBefore))
	_, err = ownerDB.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO "+runtimeRole)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, migration.GetMigrationSQL(migration.SLAAlertNotificationVersion))
	require.NoError(t, err)
	var oldAlertsAfter, oldNotificationsAfter string
	require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(h)-'notification_tracking_version' ORDER BY id)::text,'[]') FROM sla_alert_histories h`).Scan(&oldAlertsAfter))
	require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(n)-'sla_alert_history_id' ORDER BY id)::text,'[]') FROM ticket_notifications n`).Scan(&oldNotificationsAfter))
	require.JSONEq(t, oldAlertsBefore, oldAlertsAfter)
	require.JSONEq(t, oldNotificationsBefore, oldNotificationsAfter)
	var linked int
	require.NoError(t, ownerDB.QueryRow(`SELECT (SELECT count(*) FROM sla_alert_histories WHERE notification_tracking_version IS NOT NULL)+(SELECT count(*) FROM ticket_notifications WHERE sla_alert_history_id IS NOT NULL)`).Scan(&linked))
	require.Zero(t, linked, "040 must not backfill existing rows")
	for _, fn := range []string{"preserve_sla_alert_delivery_reference", "preserve_sla_alert_tracking"} {
		var executable bool
		require.NoError(t, ownerDB.QueryRow(`SELECT has_function_privilege($1,$2,'EXECUTE')`, runtimeRole, "public."+fn+"()").Scan(&executable))
		require.False(t, executable, "040 removes role-specific default function ACLs")
	}
	scopeID := uuid.NewString()
	_, err = ownerDB.ExecContext(ctx, `INSERT INTO execution_scopes(id,deployment_id,tenant_id,status,created_by) VALUES($1,'intake-test',$2,'active',$3)`, scopeID, tenant.ID, actor.ID)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `INSERT INTO execution_runtime_bindings VALUES($1,'intake-test','candidate')`, runtimeRole)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_scopes,execution_scope_members,execution_runtime_bindings TO "+runtimeRole)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s;
GRANT SELECT ON users,tenants,msp_allocations,process_callback_outboxes,external_identities,connector_configs TO %s;
GRANT SELECT,UPDATE ON outbox_events,ticket_notifications TO %s;
GRANT INSERT,SELECT(id) ON audit_logs TO %s;
GRANT USAGE ON SEQUENCE audit_logs_id_seq TO %s`, systemRole, systemRole, systemRole, systemRole, systemRole))
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_scopes,execution_scope_members,execution_runtime_bindings TO "+systemRole)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, "GRANT SELECT(id,tenant_id,execution_work_item_id) ON process_instances TO "+systemRole)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, `INSERT INTO execution_runtime_bindings VALUES($1,'intake-test','candidate')`, systemRole)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				upstream, e := net.Dial("unix", filepath.Join(socket, ".s.PGSQL.25439"))
				if e != nil {
					return
				}
				defer upstream.Close()
				go func() { _, _ = io.Copy(upstream, c) }()
				_, _ = io.Copy(c, upstream)
			}()
		}
	}()
	previousRaw := database.GetRawDB()
	defer database.SetRawDBForTest(previousRaw)
	clients, err := database.InitRuntimeDatabases(&config.DatabaseConfig{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, DBName: name, User: runtimeRole, SystemRoleUser: systemRole, SSLMode: "disable"}, &config.RLSConfig{Mode: "enforce"}, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer clients.Close()
	runtime := clients.Tenant
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}, Capabilities: map[string]string{"outbox": "scoped"}})
	require.NoError(t, err)
	app := application(runtime, policy, clients.IntakeDirectorySnapshot())
	// Private component fixture role, not target runtime/RLS admission evidence.
	standardWebhookClient := func() *ent.Client {
		if standardFixtureClient != nil {
			return standardFixtureClient
		}
		_, err := admin.ExecContext(ctx, "CREATE ROLE "+standardRole+" LOGIN BYPASSRLS NOINHERIT")
		require.NoError(t, err)
		standardCreated = true
		for _, statement := range []string{"GRANT USAGE ON SCHEMA public TO " + standardRole, "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + standardRole, "GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO " + standardRole} {
			_, err = ownerDB.ExecContext(ctx, statement)
			require.NoError(t, err)
		}
		_, err = ownerDB.ExecContext(ctx, `INSERT INTO execution_runtime_bindings VALUES($1,'standard-webhook-test','standard')`, standardRole)
		require.NoError(t, err)
		db, err := sql.Open("postgres", dsn(name, standardRole))
		require.NoError(t, err)
		standardFixtureClient = ent.NewClient(ent.Driver(entsql.OpenDB("postgres", db)))
		return standardFixtureClient
	}

	t.Run("Incident email uses restricted candidate roles", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("candidate-incident-mail", "incident"))
		require.NoError(t, err)
		verifyCandidateIncidentEmailRoles(t, ctx, owner, runtime, clients.System, database.GetRawDB(), clients.SystemDB, scopeID, tenant.ID, actor.ID, fresh.ProfessionalReference.ID)
	})

	t.Run("notification target migration preserves legacy intents", func(t *testing.T) {
		migrationSQL := migration.GetMigrationSQL("044_notification_connector_target")
		require.NotEmpty(t, migrationSQL, "target protocol requires a registered migration")
		// Simulate a real pre-upgrade table, even after Ent gains the new fields.
		_, err := ownerDB.ExecContext(ctx, `ALTER TABLE ticket_notifications DROP COLUMN IF EXISTS target_protocol_version, DROP COLUMN IF EXISTS target_connector_name, DROP COLUMN IF EXISTS target_connector_provider, DROP COLUMN IF EXISTS target_destination_digest`)
		require.NoError(t, err)
		var legacyConnectorID int
		require.NoError(t, ownerDB.QueryRow(`INSERT INTO ticket_notifications(tenant_id,ticket_id,user_id,type,channel,content,status,created_at,next_attempt_at,attempt_count) VALUES($1,$2,$3,'created','sms','legacy unbound connector','pending',now(),now(),0) RETURNING id`, tenant.ID, historicalNotifications[0].TicketID, actor.ID).Scan(&legacyConnectorID))
		defer func() {
			_, e := ownerDB.ExecContext(ctx, `DELETE FROM ticket_notifications WHERE id=$1`, legacyConnectorID)
			require.NoError(t, e)
		}()
		_, err = ownerDB.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO "+runtimeRole)
		require.NoError(t, err)
		constraintError := func(err error) *pq.Error {
			t.Helper()
			var pg *pq.Error
			require.ErrorAs(t, err, &pg)
			require.Equal(t, pq.ErrorCode("23514"), pg.Code)
			return pg
		}
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT jsonb_agg(to_jsonb(n) ORDER BY id)::text FROM ticket_notifications n`).Scan(&before))
		_, err = ownerDB.ExecContext(ctx, migrationSQL)
		require.NoError(t, err)
		require.NoError(t, ownerDB.QueryRow(`SELECT jsonb_agg(to_jsonb(n)-'target_protocol_version'-'target_connector_name'-'target_connector_provider'-'target_destination_digest' ORDER BY id)::text FROM ticket_notifications n`).Scan(&after))
		require.JSONEq(t, before, after)
		var bound int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE target_protocol_version IS NOT NULL OR target_connector_name IS NOT NULL OR target_connector_provider IS NOT NULL OR target_destination_digest IS NOT NULL`).Scan(&bound))
		require.Zero(t, bound)
		_, err = ownerDB.ExecContext(ctx, `UPDATE ticket_notifications SET target_protocol_version=1,target_connector_name=channel,target_connector_provider='local',target_destination_digest=repeat('a',64) WHERE id=$1`, legacyConnectorID)
		require.Equal(t, "notification target and delivery identity are immutable", constraintError(err).Message)
		var canExecute bool
		require.NoError(t, ownerDB.QueryRow(`SELECT has_function_privilege($1,'public.preserve_notification_connector_target()','EXECUTE')`, runtimeRole).Scan(&canExecute))
		require.False(t, canExecute)
		insert := `INSERT INTO ticket_notifications(tenant_id,ticket_id,user_id,type,channel,content,status,created_at,next_attempt_at,attempt_count,target_protocol_version,target_connector_name,target_connector_provider,target_destination_digest) VALUES($1,$2,$3,'created','sms','private target migration','pending',now(),now(),0,$4,$5,$6,$7) RETURNING id`
		for _, invalid := range []struct {
			version                any
			name, provider, digest any
		}{{1, "sms", nil, nil}, {2, "sms", "local", strings.Repeat("a", 64)}, {1, "webhook", "local", strings.Repeat("a", 64)}, {1, "sms", " ", strings.Repeat("a", 64)}, {1, "sms", "local", "invalid"}, {nil, "sms", "local", strings.Repeat("a", 64)}} {
			_, err = ownerDB.ExecContext(ctx, insert, tenant.ID, historicalNotifications[0].TicketID, actor.ID, invalid.version, invalid.name, invalid.provider, invalid.digest)
			constraintError(err)
		}
		var id int
		require.NoError(t, ownerDB.QueryRow(insert, tenant.ID, historicalNotifications[0].TicketID, actor.ID, 1, "sms", "local", strings.Repeat("a", 64)).Scan(&id))
		defer func() {
			_, e := ownerDB.ExecContext(ctx, `DELETE FROM ticket_notifications WHERE id=$1`, id)
			require.NoError(t, e)
		}()
		for _, assignment := range []string{"target_connector_provider='changed'", "target_destination_digest=repeat('b',64)", "channel='webhook'", "content='changed'", "target_protocol_version=NULL", "user_id=user_id+1"} {
			_, err = ownerDB.ExecContext(ctx, "UPDATE ticket_notifications SET "+assignment+" WHERE id=$1", id)
			constraintError(err)
		}
		_, err = ownerDB.ExecContext(ctx, `UPDATE ticket_notifications SET status='processing',attempt_count=1 WHERE id=$1`, id)
		require.NoError(t, err)
		verifyNotificationEmailTargetMigration(t, ctx, ownerDB, tenant.ID, historicalNotifications[0].TicketID, actor.ID, runtimeRole)
	})

	t.Run("candidate cloud discovery direct entry is disabled", func(t *testing.T) {
		discovery := service.NewCloudDiscoveryService(runtime, zap.NewNop().Sugar(), policy)
		require.ErrorIs(t, discovery.DiscoverAll(ctx, tenant.ID), executionscope.ErrDenied)
	})
	t.Run("candidate cloud runner direct entry is disabled", func(t *testing.T) {
		runner := cloudrunner.NewRunner(runtime, zap.NewNop().Sugar(), policy)
		require.ErrorIs(t, runner.RunAll(ctx, tenant.ID), executionscope.ErrDenied)
	})
	t.Run("candidate cloud account and missing policy reject before IO", func(t *testing.T) {
		discovery := service.NewCloudDiscoveryService(nil, zap.NewNop().Sugar(), policy)
		require.ErrorIs(t, discovery.DiscoverAccount(ctx, &ent.CloudAccount{ID: 1, TenantID: tenant.ID, Provider: "aws"}), executionscope.ErrDenied)
		require.ErrorIs(t, discovery.DiscoverAccount(ctx, nil), executionscope.ErrDenied)
		absent := service.NewCloudDiscoveryService(nil, zap.NewNop().Sugar(), nil)
		require.ErrorIs(t, absent.DiscoverAll(ctx, tenant.ID), executionscope.ErrDenied)
		require.ErrorIs(t, absent.DiscoverAccount(ctx, &ent.CloudAccount{ID: 1, TenantID: tenant.ID, Provider: "aws"}), executionscope.ErrDenied)
		require.ErrorIs(t, cloudrunner.NewRunner(nil, zap.NewNop().Sugar(), nil).RunAll(ctx, tenant.ID), executionscope.ErrDenied)
	})
	t.Run("standard explicit cloud capability permits empty account scan", func(t *testing.T) {
		standard, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "cloud-standard-test", Capabilities: map[string]string{"cloud_discovery": "enabled"}})
		require.NoError(t, err)
		require.NoError(t, service.NewCloudDiscoveryService(owner, zap.NewNop().Sugar(), standard).DiscoverAll(ctx, tenant.ID))
		require.NoError(t, cloudrunner.NewRunner(owner, zap.NewNop().Sugar(), standard).RunAll(ctx, tenant.ID))
		disabled, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "cloud-standard-test"})
		require.NoError(t, err)
		require.ErrorIs(t, service.NewCloudDiscoveryService(nil, zap.NewNop().Sugar(), disabled).DiscoverAll(ctx, tenant.ID), executionscope.ErrDenied)
	})

	t.Run("candidate connector restore rejects persisted activation", func(t *testing.T) {
		saved := owner.ConnectorConfig.Create().SetTenantID(tenant.ID).SetName("webhook").SetProvider("local-restore-probe").SetEnabled(true).SetSettings(`{}`).SaveX(ctx)
		defer owner.ConnectorConfig.DeleteOneID(saved.ID).ExecX(ctx)
		var inits atomic.Int32
		reg := connector.NewRegistry()
		reg.Register(func() connector.Connector { return &candidateRestoreProbe{inits: &inits} })
		manager := connector.NewManager(reg, zap.NewNop().Sugar(), policy)
		defer manager.CloseAll()
		ctrl := controller.NewConnectorController(manager, reg, marketplace.New(), zap.NewNop().Sugar(), runtime, clients.System)
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(c)::text FROM connector_configs c WHERE id=$1`, saved.ID).Scan(&before))
		err := ctrl.LoadAll(tenantctx.SystemContext(ctx, "test:candidate-restore", "verify frozen restore boundary"))
		assert.ErrorIs(t, err, executionscope.ErrDenied)
		assert.Zero(t, inits.Load(), "candidate restore must not initialize persisted connectors")
		assert.Empty(t, manager.ListByTenant(tenant.ID))
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(c)::text FROM connector_configs c WHERE id=$1`, saved.ID).Scan(&after))
		require.JSONEq(t, before, after)
		standard, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "restore-test", Capabilities: map[string]string{"connector_poll": "enabled"}})
		require.NoError(t, err)
		allowed := connector.NewManager(reg, zap.NewNop().Sugar(), standard)
		defer allowed.CloseAll()
		standardController := controller.NewConnectorController(allowed, reg, nil, zap.NewNop().Sugar(), runtime, clients.System)
		require.ErrorIs(t, standardController.LoadAll(ctx), executionscope.ErrDenied, "ordinary tenant context is not runtime startup authority")
		require.Zero(t, inits.Load())
		require.NoError(t, standardController.LoadAll(tenantctx.SystemContext(ctx, "test:standard-restore", "verify explicit startup permission")))
		require.EqualValues(t, 1, inits.Load())
		require.Len(t, allowed.ListByTenant(tenant.ID), 1)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(c)::text FROM connector_configs c WHERE id=$1`, saved.ID).Scan(&after))
		require.JSONEq(t, before, after)
		absent := controller.NewConnectorController(connector.NewManager(reg, nil, nil), reg, nil, zap.NewNop().Sugar(), nil, nil)
		require.ErrorIs(t, absent.LoadAll(tenantctx.SystemContext(ctx, "test:absent-restore", "reject before query")), executionscope.ErrDenied)
		noClient := controller.NewConnectorController(manager, reg, nil, zap.NewNop().Sugar(), nil, nil)
		require.ErrorIs(t, noClient.LoadAll(tenantctx.SystemContext(ctx, "test:candidate-no-client", "reject before query")), executionscope.ErrDenied)

	})
	t.Run("candidate request cannot activate an undeclared delivery target", func(t *testing.T) {
		var sends atomic.Int32
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sends.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer receiver.Close()
		for _, entry := range []string{"manager", "controller"} {
			t.Run(entry, func(t *testing.T) {
				saved := owner.ConnectorConfig.Create().SetTenantID(tenant.ID).SetName("webhook").SetProvider("historical-preserved").SetEnabled(false).SetSettings(`{"history":"preserved"}`).SaveX(ctx)
				defer owner.ConnectorConfig.DeleteOneID(saved.ID).ExecX(ctx)
				var before, after string
				var countBefore, countAfter int
				require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM connector_configs WHERE tenant_id=$1`, tenant.ID).Scan(&countBefore))
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(c)::text FROM connector_configs c WHERE id=$1`, saved.ID).Scan(&before))
				reg := connector.NewRegistry()
				reg.Register(func() connector.Connector { return webhookconnector.New() })
				manager := connector.NewManager(reg, zap.NewNop().Sugar(), policy)
				defer manager.CloseAll()
				cfg := connector.Config{TenantID: tenant.ID, Name: "webhook", Provider: "request-target", Enabled: true, Settings: map[string]interface{}{"url": receiver.URL}}
				beforeSends := sends.Load()
				if entry == "manager" {
					assert.ErrorIs(t, manager.Provision(ctx, cfg), executionscope.ErrDenied)
				} else {
					ctrl := controller.NewConnectorController(manager, reg, marketplace.New(), zap.NewNop().Sugar(), runtime, clients.System)
					router := gin.New()
					router.Use(func(c *gin.Context) { c.Set("tenant_id", tenant.ID); c.Next() })
					router.POST("/connectors/configs", ctrl.Provision)
					body, err := json.Marshal(dto.ProvisionConnectorRequest{Name: cfg.Name, Provider: cfg.Provider, Enabled: true, Settings: cfg.Settings})
					require.NoError(t, err)
					request := httptest.NewRequest(http.MethodPost, "/connectors/configs", strings.NewReader(string(body))).WithContext(ctx)
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					assert.Equal(t, http.StatusForbidden, response.Code)
				}
				assert.Empty(t, manager.ListByTenant(tenant.ID), "request configuration is not trusted startup authority")
				if _, exists := manager.Get(tenant.ID, "webhook"); exists {
					// Exercise the real built-in connector, with only a loopback receiver.
					require.NoError(t, manager.Send(ctx, tenant.ID, "webhook", &connector.Message{Type: "text", Content: "unadmitted target probe"}))
				}
				assert.Equal(t, beforeSends, sends.Load(), "an undeclared target must not become usable for outbound delivery")
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(c)::text FROM connector_configs c WHERE id=$1`, saved.ID).Scan(&after))
				assert.JSONEq(t, before, after, "rejected activation must preserve historical configuration")
				require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM connector_configs WHERE tenant_id=$1`, tenant.ID).Scan(&countAfter))
				assert.Equal(t, countBefore, countAfter, "rejected activation must not insert another configuration")
			})
		}
	})
	t.Run("candidate connector delete preserves stored configuration", func(t *testing.T) {
		saved := owner.ConnectorConfig.Create().SetTenantID(tenant.ID).SetName("webhook").SetProvider("protected-delete").SetEnabled(true).SetCredentials("{}").SetSettings("{}").SetLabels("{}").SaveX(ctx)
		defer owner.ConnectorConfig.DeleteOneID(saved.ID).Exec(ctx)
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(c)::text FROM connector_configs c WHERE id=$1`, saved.ID).Scan(&before))
		manager := connector.NewManager(nil, zap.NewNop().Sugar(), policy)
		defer manager.CloseAll()
		ctrl := controller.NewConnectorController(manager, nil, marketplace.New(), zap.NewNop().Sugar(), runtime, clients.System)
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("tenant_id", tenant.ID); c.Next() })
		router.DELETE("/connectors/configs/:name", ctrl.Revoke)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/connectors/configs/webhook", nil).WithContext(ctx))
		assert.Equal(t, http.StatusForbidden, response.Code)
		err := ownerDB.QueryRow(`SELECT row_to_json(c)::text FROM connector_configs c WHERE id=$1`, saved.ID).Scan(&after)
		assert.NoError(t, err, "denied configuration deletion must retain the original row")
		if err == nil {
			assert.JSONEq(t, before, after)
		}

		standard, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "connector-management-standard"})
		require.NoError(t, err)
		registry := connector.NewRegistry()
		registry.Register(func() connector.Connector { return webhookconnector.New() })
		standardManager := connector.NewManager(registry, zap.NewNop().Sugar(), standard)
		defer standardManager.CloseAll()
		standardCtrl := controller.NewConnectorController(standardManager, registry, marketplace.New(), zap.NewNop().Sugar(), runtime, clients.System)
		standardRouter := gin.New()
		standardRouter.Use(func(c *gin.Context) { c.Set("tenant_id", tenant.ID); c.Next() })
		standardRouter.POST("/configs", standardCtrl.Provision)
		standardRouter.DELETE("/configs/:name", standardCtrl.Revoke)
		var requests atomic.Int32
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusNoContent) }))
		defer receiver.Close()
		body, err := json.Marshal(dto.ProvisionConnectorRequest{Name: "webhook", Provider: "local-test", Enabled: true, Settings: map[string]interface{}{"url": receiver.URL}})
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, "/configs", strings.NewReader(string(body))).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		response = httptest.NewRecorder()
		standardRouter.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		require.Len(t, standardManager.ListByTenant(tenant.ID), 1)
		updated := owner.ConnectorConfig.GetX(ctx, saved.ID)
		require.True(t, updated.Enabled)
		require.Contains(t, updated.Settings, receiver.URL)
		require.Zero(t, requests.Load(), "configuration Init must not send a webhook")
		response = httptest.NewRecorder()
		standardRouter.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/configs/webhook", nil).WithContext(ctx))
		require.Equal(t, http.StatusOK, response.Code)
		require.Empty(t, standardManager.ListByTenant(tenant.ID))
		require.False(t, owner.ConnectorConfig.Query().Where(connectorconfig.IDEQ(saved.ID)).ExistX(ctx))
	})
	t.Run("candidate marketplace management preserves existing configuration", func(t *testing.T) {
		for _, kind := range []marketplaceitem.Type{marketplaceitem.TypeConnector, marketplaceitem.TypeSkill, marketplaceitem.TypePlugin} {
			for _, action := range []string{"install", "reactivate", "update", "uninstall", "merge"} {
				if action == "merge" && kind != marketplaceitem.TypeConnector {
					continue
				}
				t.Run(string(kind)+"/"+action, func(t *testing.T) {
					item := owner.MarketplaceItem.Create().SetName("candidate-" + string(kind) + "-" + action).SetType(kind).SetTitle("Private fixture").SetProvider("local-test").SetLatestVersion("1").SetStatus(marketplaceitem.StatusPublished).SaveX(ctx)
					defer owner.MarketplaceItem.DeleteOneID(item.ID).ExecX(ctx)
					defer owner.TenantInstallation.Delete().Where(tenantinstallation.ItemIDEQ(item.ID)).ExecX(ctx)
					if action != "install" {
						state := tenantinstallation.StatusActive
						if action == "reactivate" {
							state = tenantinstallation.StatusUninstalled
						}
						owner.TenantInstallation.Create().SetTenantID(tenant.ID).SetItemID(item.ID).SetInstalledVersion("0").SetStatus(state).SetInstalledBy("fixture").SetConfig(map[string]interface{}{"history": "preserved"}).SaveX(ctx)
						if state == tenantinstallation.StatusActive {
							owner.MarketplaceItem.UpdateOneID(item.ID).SetInstallCount(1).SaveX(ctx)
						}
					}
					snapshot := func() string {
						var raw string
						require.NoError(t, ownerDB.QueryRow(`SELECT json_build_object('item',(SELECT row_to_json(i) FROM marketplace_items i WHERE id=$1),'installations',(SELECT coalesce(json_agg(n ORDER BY id),'[]') FROM tenant_installations n WHERE item_id=$1))::text`, item.ID).Scan(&raw))
						return raw
					}
					before := snapshot()
					market := marketplaceservice.NewService(runtime, zap.NewNop().Sugar(), policy)
					var err error
					switch action {
					case "install", "reactivate":
						_, err = market.InstallItem(ctx, tenant.ID, item.ID, fmt.Sprint(actor.ID))
					case "update":
						_, err = market.UpdateInstallationConfig(ctx, tenant.ID, item.ID, map[string]interface{}{"history": "overwritten"})
					case "merge":
						_, err = market.MergeConnectorInstallationConfig(ctx, tenant.ID, item.Name, map[string]interface{}{"oauth": "unadmitted"})
					case "uninstall":
						err = market.UninstallItem(ctx, tenant.ID, item.ID)
					}
					assert.ErrorIs(t, err, executionscope.ErrDenied)
					assert.JSONEq(t, before, snapshot(), "unadmitted marketplace mutation must preserve rows and counts")
					standardPolicy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "marketplace-standard-test"})
					require.NoError(t, err)
					standardMarket := marketplaceservice.NewService(runtime, zap.NewNop().Sugar(), standardPolicy)
					switch action {
					case "install", "reactivate":
						installed, err := standardMarket.InstallItem(ctx, tenant.ID, item.ID, fmt.Sprint(actor.ID))
						require.NoError(t, err)
						require.Equal(t, tenantinstallation.StatusActive, installed.Status)
						require.Equal(t, "1", installed.InstalledVersion)
					case "update":
						updated, err := standardMarket.UpdateInstallationConfig(ctx, tenant.ID, item.ID, map[string]interface{}{"history": "standard-update"})
						require.NoError(t, err)
						require.Equal(t, "standard-update", updated.Config["history"])
					case "merge":
						updated, err := standardMarket.MergeConnectorInstallationConfig(ctx, tenant.ID, item.Name, map[string]interface{}{"oauth": "standard-merge"})
						require.NoError(t, err)
						require.Equal(t, "preserved", updated.Config["history"])
						require.Equal(t, "standard-merge", updated.Config["oauth"])
					case "uninstall":
						require.NoError(t, standardMarket.UninstallItem(ctx, tenant.ID, item.ID))
						require.Equal(t, tenantinstallation.StatusUninstalled, owner.TenantInstallation.Query().Where(tenantinstallation.ItemIDEQ(item.ID)).OnlyX(ctx).Status)
					}
					wantCount := 1
					if action == "uninstall" {
						wantCount = 0
					}
					require.Equal(t, wantCount, owner.MarketplaceItem.GetX(ctx, item.ID).InstallCount)
					require.Equal(t, 1, owner.TenantInstallation.Query().Where(tenantinstallation.ItemIDEQ(item.ID)).CountX(ctx))
				})
			}
		}
	})
	t.Run("standard marketplace HTTP retains request tenant context", func(t *testing.T) {
		item := owner.MarketplaceItem.Create().SetName("standard-http-plugin").SetType(marketplaceitem.TypePlugin).SetTitle("Private HTTP fixture").SetProvider("local-test").SetLatestVersion("1").SetStatus(marketplaceitem.StatusPublished).SaveX(ctx)
		defer owner.MarketplaceItem.DeleteOneID(item.ID).ExecX(ctx)
		defer owner.TenantInstallation.Delete().Where(tenantinstallation.ItemIDEQ(item.ID)).ExecX(ctx)
		standard, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "marketplace-http-test"})
		require.NoError(t, err)
		ctrl := marketplacecontroller.NewController(marketplaceservice.NewService(runtime, zap.NewNop().Sugar(), standard))
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tenant.ID})
			c.Set("user_id", actor.ID)
			c.Next()
		})
		router.POST("/items/:id/install", ctrl.InstallItem)
		router.POST("/items/:id/uninstall", ctrl.UninstallItem)
		router.PUT("/installations/:id/config", ctrl.UpdateInstallationConfig)
		call := func(method, route string) {
			req := httptest.NewRequest(method, fmt.Sprintf(route, item.ID), strings.NewReader(`{"configured":"standard-http"}`)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Equal(t, http.StatusOK, response.Code)
		}
		call(http.MethodPost, "/items/%d/install")
		installed := owner.TenantInstallation.Query().Where(tenantinstallation.ItemIDEQ(item.ID)).OnlyX(ctx)
		require.Equal(t, tenant.ID, installed.TenantID)
		require.Equal(t, tenantinstallation.StatusActive, installed.Status)
		call(http.MethodPut, "/installations/%d/config")
		require.Equal(t, "standard-http", owner.TenantInstallation.GetX(ctx, installed.ID).Config["configured"])
		call(http.MethodPost, "/items/%d/uninstall")
		require.Equal(t, tenantinstallation.StatusUninstalled, owner.TenantInstallation.GetX(ctx, installed.ID).Status)
		require.Zero(t, owner.MarketplaceItem.GetX(ctx, item.ID).InstallCount)
	})
	t.Run("candidate connector read routes do not probe unscoped instances", func(t *testing.T) {
		var calls, foreignCalls atomic.Int32
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == fmt.Sprintf("/%d", tenant.ID+1) {
				foreignCalls.Add(1)
			} else {
				calls.Add(1)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer receiver.Close()
		reg := connector.NewRegistry()
		reg.Register(func() connector.Connector { return &candidateHealthProbe{target: receiver.URL} })
		manager := connector.NewManager(reg, zap.NewNop().Sugar(), candidateTestStandardManagement(t))
		defer manager.CloseAll()
		// Read-only defense fixture built using standard management, not candidate activation.
		// Existing runtime instances have no candidate enrollment or WorkItem authority.
		for _, id := range []int{tenant.ID, tenant.ID + 1} {
			require.NoError(t, manager.Provision(tenantctx.WithTenantID(ctx, id), connector.Config{TenantID: id, Name: "webhook", Provider: "local-health-probe", Enabled: true}))
		}
		ctrl := controller.NewConnectorController(manager, reg, marketplace.New(), zap.NewNop().Sugar(), runtime, owner)
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set("tenant_id", tenant.ID); c.Next() })
		router.GET("/connectors", ctrl.ListMarket)
		router.GET("/connectors/configs", ctrl.ListConfigs)
		router.GET("/connectors/health", ctrl.Health)
		router.POST("/connectors/health", ctrl.RefreshHealth)
		router.GET("/connectors/lifecycle", ctrl.Lifecycle)
		for _, route := range []string{"/connectors", "/connectors/configs", "/connectors/health", "/connectors/lifecycle"} {
			t.Run(route, func(t *testing.T) {
				before, beforeForeign := calls.Load(), foreignCalls.Load()
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil).WithContext(ctx))
				if route != "/connectors/health" {
					require.Equal(t, http.StatusOK, response.Code)
				}
				// Explicit health may deny diagnostics; it must never probe either instance.
				assert.Equal(t, before, calls.Load(), "candidate GET must not execute unscoped health probes")
				assert.Equal(t, beforeForeign, foreignCalls.Load(), "tenant GET must never probe another tenant")
			})
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/connectors/health", nil).WithContext(ctx))
		require.Equal(t, http.StatusForbidden, response.Code)
		require.Zero(t, calls.Load())
		require.Zero(t, foreignCalls.Load())
	})
	memberCount := func() int {
		var n int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_scope_members`).Scan(&n))
		return n
	}
	ensureToolAuthorityLock := func() {
		var exists bool
		require.NoError(t, ownerDB.QueryRow(`SELECT to_regprocedure('public.lock_candidate_tool_authorization(uuid,text,bigint,bigint,bigint)') IS NOT NULL`).Scan(&exists))
		if !exists {
			_, err := ownerDB.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO "+runtimeRole)
			require.NoError(t, err)
			_, err = ownerDB.ExecContext(ctx, migration.GetMigrationSQL(migration.ToolExecutionAuthorityLockVersion)+migration.GetMigrationSQL(migration.ToolExecutionAuthorizationLockVersion))
			require.NoError(t, err)
			var allowed bool
			require.NoError(t, ownerDB.QueryRow(`SELECT has_function_privilege($1,'public.lock_candidate_tool_authorization(uuid,text,bigint,bigint,bigint)','EXECUTE')`, runtimeRole).Scan(&allowed))
			require.False(t, allowed, "default EXECUTE must be stripped")
		}
		_, err := ownerDB.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION public.lock_candidate_tool_authorization(uuid,text,bigint,bigint,bigint) TO "+runtimeRole)
		require.NoError(t, err)
	}
	t.Run("tool invocation enrollment is atomic and history preserving", func(t *testing.T) {
		var before string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, historicalTool.ID).Scan(&before))
		_, err := ownerDB.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES GRANT ALL ON TABLES TO "+runtimeRole)
		require.NoError(t, err)
		sqlText := migration.GetMigrationSQL("041_tool_invocation_execution_scope")
		require.NotEmpty(t, sqlText, "tool provenance must use a registered migration")
		_, err = ownerDB.ExecContext(ctx, sqlText)
		require.NoError(t, err)
		var after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, historicalTool.ID).Scan(&after))
		require.JSONEq(t, before, after)
		var count int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations`).Scan(&count))
		require.Zero(t, count, "migration must not enroll historical invocations")
		var allowed bool
		require.NoError(t, ownerDB.QueryRow(`SELECT has_function_privilege($1,'public.register_new_execution_tool_invocation()','EXECUTE')`, runtimeRole).Scan(&allowed))
		require.False(t, allowed, "default function grants must be removed")
		require.NoError(t, ownerDB.QueryRow(`SELECT has_table_privilege($1,'execution_tool_invocations','SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')`, runtimeRole).Scan(&allowed))
		require.False(t, allowed, "default table grants must be removed")
		_, err = ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_tool_invocations TO "+runtimeRole)
		require.NoError(t, err)
		ensureToolAuthorityLock()
		create := func(c *ent.Client) *ent.ToolInvocationCreate {
			return c.ToolInvocation.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetToolName("create_ticket").SetArguments(`{"title":"New scoped source"}`).SetStatus("pending")
		}
		_, err = create(runtime).Save(ctx)
		require.Error(t, err, "unbound insert must fail")
		tx, err := runtime.Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
		added, err := create(tx.Client()).Save(ctx)
		require.NoError(t, err)
		var enrolled string
		rows, err := tx.Client().QueryContext(ctx, `SELECT scope_id::text FROM execution_tool_invocations WHERE invocation_id=$1`, added.ID)
		require.NoError(t, err)
		require.True(t, rows.Next())
		require.NoError(t, rows.Scan(&enrolled))
		require.NoError(t, rows.Close())
		require.Equal(t, scopeID, enrolled)
		require.NoError(t, policy.RequireEntToolInvocation(ctx, tx, tenant.ID, added.ID))
		require.ErrorIs(t, policy.RequireEntToolInvocation(ctx, tx, tenant.ID, historicalTool.ID), executionscope.ErrDenied)
		require.NoError(t, tx.Rollback())
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM tool_invocations WHERE id=$1`, added.ID).Scan(&count))
		require.Zero(t, count)
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations`).Scan(&count))
		require.Zero(t, count)
		tx, err = runtime.Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
		added, err = create(tx.Client()).Save(ctx)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations WHERE invocation_id=$1 AND scope_id=$2`, added.ID, scopeID).Scan(&count))
		require.Equal(t, 1, count)
		t.Run("SQL permission fault is not a scope denial", func(t *testing.T) {
			_, err := ownerDB.ExecContext(ctx, "REVOKE SELECT ON execution_tool_invocations FROM "+runtimeRole)
			require.NoError(t, err)
			defer func() {
				_, err := ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_tool_invocations TO "+runtimeRole)
				require.NoError(t, err)
			}()
			check, err := runtime.Tx(ctx)
			require.NoError(t, err)
			defer check.Rollback()
			require.NoError(t, policy.BindEnt(ctx, check, tenant.ID))
			err = policy.RequireEntToolInvocation(ctx, check, tenant.ID, added.ID)
			require.Error(t, err)
			require.False(t, errors.Is(err, executionscope.ErrDenied))
			var pgError *pq.Error
			require.ErrorAs(t, err, &pgError)
			require.Equal(t, "42501", string(pgError.Code))
		})
		foreignTenant := owner.Tenant.Create().SetName("Tool scope foreign").SetCode("tool-scope-foreign").SaveX(ctx)
		for _, kind := range []string{"foreign tenant", "closed scope", "revoked binding"} {
			t.Run(kind, func(t *testing.T) {
				if kind == "closed scope" {
					_, e := ownerDB.ExecContext(ctx, `UPDATE execution_scopes SET status='closed' WHERE id=$1`, scopeID)
					require.NoError(t, e)
					defer func() {
						_, e := ownerDB.ExecContext(ctx, `UPDATE execution_scopes SET status='active' WHERE id=$1`, scopeID)
						require.NoError(t, e)
					}()
				}
				if kind == "revoked binding" {
					_, e := ownerDB.ExecContext(ctx, `DELETE FROM execution_runtime_bindings WHERE runtime_role=$1`, runtimeRole)
					require.NoError(t, e)
					defer func() {
						_, e := ownerDB.ExecContext(ctx, `INSERT INTO execution_runtime_bindings VALUES($1,'intake-test','candidate')`, runtimeRole)
						require.NoError(t, e)
					}()
				}
				beforeCount := owner.ToolInvocation.Query().CountX(ctx)
				var beforeEnroll int
				require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations`).Scan(&beforeEnroll))
				attempt, e := runtime.Tx(ctx)
				require.NoError(t, e)
				defer attempt.Rollback()
				// Exercise the trigger independently of the application's BindEnt precheck.
				_, e = attempt.Client().ExecContext(ctx, `SELECT set_config('app.execution_scope_id',$1,true)`, scopeID)
				require.NoError(t, e)
				statement := create(attempt.Client())
				if kind == "foreign tenant" {
					statement.SetTenantID(foreignTenant.ID)
				}
				_, e = statement.Save(ctx)
				require.Error(t, e)
				require.NoError(t, attempt.Rollback())
				require.Equal(t, beforeCount, owner.ToolInvocation.Query().CountX(ctx))
				var afterEnroll int
				require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations`).Scan(&afterEnroll))
				require.Equal(t, beforeEnroll, afterEnroll)
			})
		}
		_, err = runtime.ExecContext(ctx, `INSERT INTO execution_tool_invocations(scope_id,tenant_id,invocation_id) VALUES($1,$2,$3)`, scopeID, tenant.ID, historicalTool.ID)
		require.Error(t, err, "runtime cannot enroll history directly")
		_, err = runtime.ExecContext(ctx, `DELETE FROM execution_tool_invocations WHERE invocation_id=$1`, added.ID)
		require.Error(t, err)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, historicalTool.ID).Scan(&after))
		require.JSONEq(t, before, after)
	})
	t.Run("AI repository creates scoped invocation atomically", func(t *testing.T) {
		var installed bool
		require.NoError(t, ownerDB.QueryRow(`SELECT to_regclass('public.execution_tool_invocations') IS NOT NULL`).Scan(&installed))
		if !installed {
			_, err := ownerDB.ExecContext(ctx, migration.GetMigrationSQL(migration.ToolInvocationExecutionScopeVersion))
			require.NoError(t, err)
		}
		_, err := ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_tool_invocations TO "+runtimeRole)
		require.NoError(t, err)
		ensureToolAuthorityLock()
		repository := aidomain.NewEntRepository(runtime, policy)
		for _, approval := range []bool{true, false} {
			state := "auto"
			if approval {
				state = "pending"
			}
			created, err := repository.CreateToolInvocation(ctx, &aidomain.ToolInvocation{TenantID: tenant.ID, UserID: actor.ID, ToolName: "create_ticket", Arguments: `{"title":"Repository scoped source"}`, Status: "pending", NeedsApproval: approval, ApprovalState: state})
			require.NoError(t, err)
			var assigned string
			require.NoError(t, ownerDB.QueryRow(`SELECT scope_id::text FROM execution_tool_invocations WHERE invocation_id=$1`, created.ID).Scan(&assigned))
			require.Equal(t, scopeID, assigned)
		}
		input := func() *aidomain.ToolInvocation {
			return &aidomain.ToolInvocation{TenantID: tenant.ID, UserID: actor.ID, ToolName: "create_ticket", Arguments: `{"title":"Atomic source"}`, Status: "pending", NeedsApproval: true, ApprovalState: "pending"}
		}
		counts := func() (int, int) {
			n := owner.ToolInvocation.Query().CountX(ctx)
			var m int
			require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations`).Scan(&m))
			return n, m
		}
		var armed atomic.Bool
		injected := errors.New("injected invocation post-insert failure")
		runtime.ToolInvocation.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(c, m)
				if err == nil && m.Op() == ent.OpCreate && armed.CompareAndSwap(true, false) {
					return nil, injected
				}
				return value, err
			})
		})
		beforeCalls, beforeOrigins := counts()
		armed.Store(true)
		_, err = repository.CreateToolInvocation(ctx, input())
		require.ErrorIs(t, err, injected)
		require.False(t, armed.Load())
		afterCalls, afterOrigins := counts()
		require.Equal(t, beforeCalls, afterCalls)
		require.Equal(t, beforeOrigins, afterOrigins)
		for _, invalidCtx := range []context.Context{nil, tenantctx.WithTenantID(ctx, tenant.ID+100000)} {
			_, err := repository.CreateToolInvocation(invalidCtx, input())
			require.Error(t, err)
		}
		_, err = ownerDB.ExecContext(ctx, `UPDATE execution_scopes SET status='closed' WHERE id=$1`, scopeID)
		require.NoError(t, err)
		defer func() {
			_, err := ownerDB.ExecContext(ctx, `UPDATE execution_scopes SET status='active' WHERE id=$1`, scopeID)
			require.NoError(t, err)
		}()
		_, err = repository.CreateToolInvocation(ctx, input())
		require.ErrorIs(t, err, executionscope.ErrDenied)
		afterCalls, afterOrigins = counts()
		require.Equal(t, beforeCalls, afterCalls)
		require.Equal(t, beforeOrigins, afterOrigins)
		_, err = ownerDB.ExecContext(ctx, `UPDATE execution_scopes SET status='active' WHERE id=$1`, scopeID)
		require.NoError(t, err)
		t.Run("standard insert preserves mode contract", func(t *testing.T) {
			_, err := ownerDB.ExecContext(ctx, `UPDATE execution_runtime_bindings SET mode='standard' WHERE runtime_role=$1`, runtimeRole)
			require.NoError(t, err)
			defer func() {
				_, err := ownerDB.ExecContext(ctx, `UPDATE execution_runtime_bindings SET mode='candidate' WHERE runtime_role=$1`, runtimeRole)
				require.NoError(t, err)
			}()
			standard := aidomain.NewEntRepository(runtime, executionfixture.Standard())
			created, err := standard.CreateToolInvocation(ctx, input())
			require.NoError(t, err)
			var n int
			require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations WHERE invocation_id=$1`, created.ID).Scan(&n))
			require.Zero(t, n)
		})
	})
	t.Run("AI readonly tool requires durable audit", func(t *testing.T) {
		var installed bool
		require.NoError(t, ownerDB.QueryRow(`SELECT to_regclass('public.execution_tool_invocations') IS NOT NULL`).Scan(&installed))
		if !installed {
			_, err := ownerDB.ExecContext(ctx, migration.GetMigrationSQL(migration.ToolInvocationExecutionScopeVersion))
			require.NoError(t, err)
		}
		_, err := ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_tool_invocations TO "+runtimeRole)
		require.NoError(t, err)
		ensureToolAuthorityLock()
		repository := aidomain.NewEntRepository(runtime, policy)
		tools := service.NewToolRegistry(nil, service.NewIncidentService(runtime, zap.NewNop().Sugar(), policy), nil, runtime)
		svc := aidomain.NewService(repository, zap.NewNop().Sugar(), nil, tools, nil, nil, nil, nil, nil, nil, nil)
		result, _, err := svc.ExecuteTool(ctx, actor.ID, tenant.ID, actor.Role, "get_incident_stats", map[string]interface{}{})
		require.NoError(t, err)
		require.NotNil(t, result)
		beforeCalls := owner.ToolInvocation.Query().CountX(ctx)
		var beforeOrigins int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations`).Scan(&beforeOrigins))
		var armed atomic.Bool
		armed.Store(true)
		injected := errors.New("private audit post-insert failure")
		runtime.ToolInvocation.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(c, m)
				if err == nil && m.Op() == ent.OpCreate && armed.CompareAndSwap(true, false) {
					return nil, injected
				}
				return value, err
			})
		})
		result, _, err = svc.ExecuteTool(ctx, actor.ID, tenant.ID, actor.Role, "get_incident_stats", map[string]interface{}{})
		require.ErrorIs(t, err, aidomain.ErrToolAuditUnavailable)
		require.ErrorIs(t, err, injected)
		require.Nil(t, result)
		require.False(t, armed.Load())
		require.Equal(t, beforeCalls, owner.ToolInvocation.Query().CountX(ctx))
		var afterOrigins int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations`).Scan(&afterOrigins))
		require.Equal(t, beforeOrigins, afterOrigins)
		// A failed business read must still commit its failed audit record.
		_, err = ownerDB.ExecContext(ctx, "REVOKE SELECT ON incidents FROM "+runtimeRole)
		require.NoError(t, err)
		defer func() {
			_, err := ownerDB.ExecContext(ctx, "GRANT SELECT ON incidents TO "+runtimeRole)
			require.NoError(t, err)
		}()
		result, _, err = svc.ExecuteTool(ctx, actor.ID, tenant.ID, actor.Role, "get_incident_stats", map[string]interface{}{})
		require.Error(t, err)
		require.NotErrorIs(t, err, aidomain.ErrToolAuditUnavailable)
		require.Nil(t, result)
		require.Equal(t, beforeCalls+1, owner.ToolInvocation.Query().CountX(ctx))
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_tool_invocations`).Scan(&afterOrigins))
		require.Equal(t, beforeOrigins+1, afterOrigins)
		var auditStatus string
		require.NoError(t, ownerDB.QueryRow(`SELECT status FROM tool_invocations ORDER BY id DESC LIMIT 1`).Scan(&auditStatus))
		require.Equal(t, "failed", auditStatus)
	})
	t.Run("AI approval preserves historical invocation", func(t *testing.T) {
		var installed bool
		require.NoError(t, ownerDB.QueryRow(`SELECT to_regclass('public.execution_tool_invocations') IS NOT NULL`).Scan(&installed))
		if !installed {
			_, err := ownerDB.ExecContext(ctx, migration.GetMigrationSQL(migration.ToolInvocationExecutionScopeVersion))
			require.NoError(t, err)
		}
		_, err := ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_tool_invocations TO "+runtimeRole)
		require.NoError(t, err)
		ensureToolAuthorityLock()

		repository := aidomain.NewEntRepository(runtime, policy)
		svc := aidomain.NewService(repository, zap.NewNop().Sugar(), nil, nil, nil, nil, nil, nil, nil, nil, nil)
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, historicalTool.ID).Scan(&before))
		_, err = svc.ApproveTool(ctx, historicalTool.ID, tenant.ID, actor.ID, false, "candidate rejection")
		assert.ErrorIs(t, err, executionscope.ErrDenied)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, historicalTool.ID).Scan(&after))
		assert.JSONEq(t, before, after)
		newPending := func() *aidomain.ToolInvocation {
			item, err := repository.CreateToolInvocation(ctx, &aidomain.ToolInvocation{TenantID: tenant.ID, UserID: actor.ID, ToolName: "create_ticket", Arguments: `{"title":"Approval candidate"}`, Status: "pending", NeedsApproval: true, ApprovalState: "pending"})
			require.NoError(t, err)
			return item
		}
		snapshot := func(id int) string {
			var raw string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, id).Scan(&raw))
			return raw
		}
		fresh := newPending()
		_, err = svc.ApproveTool(ctx, fresh.ID, tenant.ID, actor.ID, true, "reviewed")
		require.ErrorIs(t, err, aidomain.ErrToolExecutionPending)
		first := snapshot(fresh.ID)
		_, err = svc.ApproveTool(ctx, fresh.ID, tenant.ID, actor.ID, true, "reviewed")
		require.ErrorIs(t, err, aidomain.ErrToolExecutionPending)
		require.JSONEq(t, first, snapshot(fresh.ID))
		_, err = svc.ApproveTool(ctx, fresh.ID, tenant.ID, actor.ID, false, "opposite")
		require.ErrorIs(t, err, aidomain.ErrToolApprovalConflict)
		require.JSONEq(t, first, snapshot(fresh.ID))
		owner.User.UpdateOneID(actor.ID).SetRole("no_tool_permission").ExecX(ctx)
		_, err = svc.ApproveTool(ctx, fresh.ID, tenant.ID, actor.ID, true, "reviewed")
		owner.User.UpdateOneID(actor.ID).SetRole(actor.Role).ExecX(ctx)
		require.Error(t, err)
		require.JSONEq(t, first, snapshot(fresh.ID))
		rejected := newPending()
		state, err := svc.ApproveTool(ctx, rejected.ID, tenant.ID, actor.ID, false, "declined")
		require.NoError(t, err)
		require.Equal(t, "rejected", state)
		recorded := owner.ToolInvocation.GetX(ctx, rejected.ID)
		require.Equal(t, actor.ID, recorded.ApprovedBy)
		require.False(t, recorded.ApprovedAt.IsZero())
		var failUpdate atomic.Bool
		var gateUpdate atomic.Bool
		entered := make(chan int, 2)
		release := make(chan struct{})
		var releaseOnce sync.Once
		injected := errors.New("injected approval after UPDATE")
		runtime.ToolInvocation.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
				if m.Op() == ent.OpUpdate && gateUpdate.Load() {
					mutationTx, err := m.(*ent.ToolInvocationMutation).Tx()
					if err != nil {
						return nil, err
					}
					rows, err := mutationTx.Client().QueryContext(c, `SELECT pg_backend_pid()`)
					if err != nil {
						return nil, err
					}
					var pid int
					if !rows.Next() {
						rows.Close()
						return nil, errors.New("approval backend missing")
					}
					err = rows.Scan(&pid)
					rows.Close()
					if err != nil {
						return nil, err
					}
					entered <- pid
					select {
					case <-release:
					case <-c.Done():
						return nil, c.Err()
					}
				}
				value, err := next.Mutate(c, m)
				if err == nil && m.Op() == ent.OpUpdate && failUpdate.CompareAndSwap(true, false) {
					return nil, injected
				}
				return value, err
			})
		})
		failed := newPending()
		failedBefore := snapshot(failed.ID)
		failUpdate.Store(true)
		_, err = repository.DecideToolInvocation(ctx, failed.ID, tenant.ID, actor.ID, true, "fault")
		require.ErrorIs(t, err, injected)
		require.False(t, failUpdate.Load())
		require.JSONEq(t, failedBefore, snapshot(failed.ID))
		for _, sameDecision := range []bool{false, true} {
			concurrent := newPending()
			entered = make(chan int, 2)
			release = make(chan struct{})
			releaseOnce = sync.Once{}
			type outcome struct {
				approve bool
				err     error
			}
			results := make(chan outcome, 2)
			gateUpdate.Store(true)
			defer func() { gateUpdate.Store(false); releaseOnce.Do(func() { close(release) }) }()
			secondDecision := false
			if sameDecision {
				secondDecision = true
			}
			for _, approve := range []bool{true, secondDecision} {
				go func(approve bool) {
					_, err := repository.DecideToolInvocation(ctx, concurrent.ID, tenant.ID, actor.ID, approve, "concurrent")
					results <- outcome{approve, err}
				}(approve)
			}
			var approvalPID int
			select {
			case approvalPID = <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("approval UPDATE barrier timed out")
			}
			require.Eventually(t, func() bool {
				var waiting bool
				err := ownerDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND $2::integer=ANY(pg_blocking_pids(pid)))`, runtimeRole, approvalPID).Scan(&waiting)
				return err == nil && waiting
			}, 5*time.Second, 10*time.Millisecond, "second approval must wait on first invocation lock")
			releaseOnce.Do(func() { close(release) })
			var winner bool
			successes := 0
			for n := 0; n < 2; n++ {
				select {
				case result := <-results:
					if result.err == nil {
						successes++
						winner = result.approve
					} else {
						var pgError *pq.Error
						require.ErrorAs(t, result.err, &pgError)
						require.Equal(t, pq.ErrorCode("40001"), pgError.Code, "concurrent approval must retry with a fresh transaction")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("approval did not finish")
				}
			}
			gateUpdate.Store(false)
			require.Equal(t, 1, successes)
			won := snapshot(concurrent.ID)
			_, err = repository.DecideToolInvocation(ctx, concurrent.ID, tenant.ID, actor.ID, winner, "concurrent")
			require.NoError(t, err)
			require.JSONEq(t, won, snapshot(concurrent.ID))
		}
	})
	t.Run("historical approved tool cannot authorize candidate execution", func(t *testing.T) {
		var installed bool
		require.NoError(t, ownerDB.QueryRow(`SELECT to_regclass('public.execution_tool_invocations') IS NOT NULL`).Scan(&installed))
		if !installed {
			_, err := ownerDB.ExecContext(ctx, migration.GetMigrationSQL(migration.ToolInvocationExecutionScopeVersion))
			require.NoError(t, err)
		}
		_, err := ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_tool_invocations TO "+runtimeRole)
		require.NoError(t, err)
		ensureToolAuthorityLock()

		snapshot := func() string {
			var raw string
			require.NoError(t, ownerDB.QueryRowContext(ctx, `SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, historicalTool.ID).Scan(&raw))
			return raw
		}
		before := snapshot()
		beforeItems := owner.Ticket.Query().CountX(ctx)
		beforeMembers := memberCount()
		t.Run("direct intake rejects historical tool source", func(t *testing.T) {
			identity := identity
			identity.Channel, identity.Provider = "ai_tool", "tool_queue"
			input := creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", Title: "Historical approved tool", IdempotencyKey: fmt.Sprintf("tool-invocation:%d", historicalTool.ID), Generic: &creation.GenericInput{Source: "ai"}, SourceReference: &creation.SourceReference{Provider: "tool_queue", EventID: fmt.Sprint(historicalTool.ID)}}
			receipts := owner.IntakeRequest.Query().CountX(ctx)
			_, err := app.Create(ctx, identity, input)
			assert.ErrorIs(t, err, creation.ErrPermissionDenied, "business transaction must independently reject unregistered historical tool source")
			assert.Equal(t, beforeItems, owner.Ticket.Query().CountX(ctx))
			assert.Equal(t, beforeMembers, memberCount())
			assert.Equal(t, receipts, owner.IntakeRequest.Query().CountX(ctx))
			assert.JSONEq(t, before, snapshot())
		})
		queue := service.NewToolQueue(runtime, nil, app, nil, 1, zap.NewNop().Sugar(), policy)
		defer queue.Close()
		require.NoError(t, queue.Start(ctx))
		assert.ErrorIs(t, queue.Enqueue(service.ToolJob{InvocationID: historicalTool.ID, TenantID: tenant.ID}), executionscope.ErrDenied, "history must be rejected before enqueue")
		err = queue.ProcessJob(ctx, service.ToolJob{InvocationID: historicalTool.ID, TenantID: tenant.ID})
		assert.ErrorIs(t, err, executionscope.ErrDenied, "copied approval must not authorize new candidate work")
		assert.JSONEq(t, before, snapshot(), "historical invocation must remain unchanged")
		assert.Equal(t, beforeItems, owner.Ticket.Query().CountX(ctx), "historical approval must not create a WorkItem")
		assert.Equal(t, beforeMembers, memberCount(), "historical approval must not enroll a candidate member")
		tx, err := runtime.Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
		fresh, err := tx.ToolInvocation.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetToolName("create_ticket").SetArguments(`{"title":"Fresh scoped tool"}`).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(actor.ID).SetApprovedAt(time.Now()).SetStatus("pending").Save(ctx)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
		freshJob := service.ToolJob{InvocationID: fresh.ID, TenantID: tenant.ID}
		t.Run("tool actor SQL fault retains infrastructure cause", func(t *testing.T) {
			_, err := ownerDB.ExecContext(ctx, "REVOKE SELECT ON users FROM "+runtimeRole)
			require.NoError(t, err)
			defer func() {
				_, err := ownerDB.ExecContext(ctx, "GRANT SELECT ON users TO "+runtimeRole)
				require.NoError(t, err)
			}()
			err = queue.ProcessJob(ctx, freshJob)
			require.Error(t, err)
			require.NotErrorIs(t, err, creation.ErrPermissionDenied)
			var pgError *pq.Error
			require.ErrorAs(t, err, &pgError)
			require.Equal(t, "42501", string(pgError.Code))
			require.Equal(t, beforeItems, owner.Ticket.Query().CountX(ctx))
		})
		toolIdentity := identity
		toolIdentity.Channel, toolIdentity.Provider = "ai_tool", "tool_queue"
		toolInput := func() creation.CreateWorkItemCommand {
			return creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", Title: "Fresh scoped tool", IdempotencyKey: fmt.Sprintf("tool-invocation:%d", fresh.ID), Generic: &creation.GenericInput{Source: "ai"}, SourceReference: &creation.SourceReference{Provider: "tool_queue", EventID: fmt.Sprint(fresh.ID)}}
		}
		for _, mutation := range []string{"title", "operation", "missing source", "missing capability"} {
			t.Run("direct creation rejects "+mutation, func(t *testing.T) {
				input := toolInput()
				switch mutation {
				case "title":
					input.Title = "Unapproved title"
				case "operation":
					input.IdempotencyKey = "unapproved-operation"
				case "missing source":
					input.SourceReference = nil
				case "missing capability":
					_, err := ownerDB.ExecContext(ctx, "REVOKE EXECUTE ON FUNCTION public.lock_candidate_tool_authorization(uuid,text,bigint,bigint,bigint) FROM "+runtimeRole)
					require.NoError(t, err)
					defer func() {
						_, err := ownerDB.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION public.lock_candidate_tool_authorization(uuid,text,bigint,bigint,bigint) TO "+runtimeRole)
						require.NoError(t, err)
					}()
				}
				requests := owner.IntakeRequest.Query().CountX(ctx)
				_, err := app.Create(ctx, toolIdentity, input)
				if mutation == "missing capability" {
					require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
					require.NotErrorIs(t, err, creation.ErrPermissionDenied)
				} else {
					require.ErrorIs(t, err, creation.ErrPermissionDenied)
				}
				require.Equal(t, beforeItems, owner.Ticket.Query().CountX(ctx))
				require.Equal(t, requests, owner.IntakeRequest.Query().CountX(ctx))
			})
		}

		for _, state := range []string{"pending", "rejected", "dry_run", "inactive_actor"} {
			t.Run("enqueue rejects "+state, func(t *testing.T) {
				if state == "inactive_actor" {
					owner.User.UpdateOneID(actor.ID).SetActive(false).ExecX(ctx)
				} else if state == "dry_run" {
					owner.ToolInvocation.UpdateOneID(fresh.ID).SetDryRun(true).ExecX(ctx)
				} else {
					owner.ToolInvocation.UpdateOneID(fresh.ID).SetApprovalState(state).ExecX(ctx)
				}
				defer func() {
					owner.User.UpdateOneID(actor.ID).SetActive(true).ExecX(ctx)
					owner.ToolInvocation.UpdateOneID(fresh.ID).SetApprovalState("approved").SetDryRun(false).ExecX(ctx)
				}()
				var beforeCall, afterCall string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, fresh.ID).Scan(&beforeCall))
				require.Error(t, queue.Enqueue(freshJob))
				requests := owner.IntakeRequest.Query().CountX(ctx)
				_, err := app.Create(ctx, toolIdentity, toolInput())
				if state == "inactive_actor" {
					require.ErrorIs(t, err, creation.ErrAuthenticationRequired)
				} else {
					require.ErrorIs(t, err, creation.ErrPermissionDenied)
				}
				require.Equal(t, requests, owner.IntakeRequest.Query().CountX(ctx))
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, fresh.ID).Scan(&afterCall))
				require.JSONEq(t, beforeCall, afterCall)
				require.Equal(t, beforeItems, owner.Ticket.Query().CountX(ctx))
			})
		}
		require.NoError(t, queue.Enqueue(freshJob))
		require.Eventually(t, func() bool {
			row, err := owner.ToolInvocation.Get(ctx, fresh.ID)
			return err == nil && row.Status == "done"
		}, 3*time.Second, 20*time.Millisecond)

		var completedBefore, completedAfter string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, fresh.ID).Scan(&completedBefore))
		require.NoError(t, queue.ProcessJob(ctx, freshJob))
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, fresh.ID).Scan(&completedAfter))
		require.JSONEq(t, completedBefore, completedAfter, "completed tool receipt must be immutable on replay")
		require.Equal(t, beforeItems+1, owner.Ticket.Query().CountX(ctx))
		require.Equal(t, beforeMembers+1, memberCount())
		require.JSONEq(t, before, snapshot())

		for _, first := range []string{"done", "failed"} {
			t.Run("concurrent tool outcomes first="+first, func(t *testing.T) {
				tx, err := runtime.Tx(ctx)
				require.NoError(t, err)
				require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
				call, err := tx.ToolInvocation.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetToolName("create_ticket").SetArguments(`{"title":"Concurrent tool result"}`).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(actor.ID).SetApprovedAt(time.Now()).SetStatus("pending").Save(ctx)
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
				items, receipts := owner.Ticket.Query().CountX(ctx), owner.IntakeRequest.Query().CountX(ctx)
				operationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				type attemptKey struct{}
				injected := errors.New("competing tool creation failed after INSERT")
				var faultArmed atomic.Bool
				faultArmed.Store(true)
				defer faultArmed.Store(false)
				runtime.Ticket.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(c, m)
						if err == nil && m.Op() == ent.OpCreate && c.Value(attemptKey{}) == "failed" && faultArmed.CompareAndSwap(true, false) {
							return nil, injected
						}
						return value, err
					})
				})
				businessDone := make(chan string, 2)
				release := map[string]chan struct{}{"done": make(chan struct{}), "failed": make(chan struct{})}
				wrapped := candidateCreationFunc(func(c context.Context, who creation.Identity, input creation.CreateWorkItemCommand) (*creation.CreateWorkItemResult, error) {
					result, err := app.Create(c, who, input)
					name := c.Value(attemptKey{}).(string)
					businessDone <- name
					select {
					case <-release[name]:
						return result, err
					case <-c.Done():
						return result, errors.Join(err, c.Err())
					}
				})
				competing := service.NewToolQueue(runtime, nil, wrapped, nil, 10, zap.NewNop().Sugar(), policy)
				job := service.ToolJob{InvocationID: call.ID, TenantID: tenant.ID}
				results := map[string]chan error{"done": make(chan error, 1), "failed": make(chan error, 1)}
				var workers sync.WaitGroup
				defer func() { cancel(); workers.Wait() }()
				launch := func(name string) {
					workers.Add(1)
					go func() {
						defer workers.Done()
						results[name] <- competing.ProcessJob(context.WithValue(operationCtx, attemptKey{}, name), job)
					}()
					select {
					case actual := <-businessDone:
						require.Equal(t, name, actual)
					case <-operationCtx.Done():
						t.Fatal(operationCtx.Err())
					}
				}
				// Both real business attempts finish before either outcome may commit.
				launch("failed")
				require.False(t, faultArmed.Load())
				require.Equal(t, items, owner.Ticket.Query().CountX(ctx))
				require.Equal(t, receipts, owner.IntakeRequest.Query().CountX(ctx))
				launch("done")
				require.Equal(t, items+1, owner.Ticket.Query().CountX(ctx))
				require.Equal(t, receipts+1, owner.IntakeRequest.Query().CountX(ctx))
				committedReceipt := owner.IntakeRequest.Query().Where(intakerequest.TenantIDEQ(tenant.ID), intakerequest.IdempotencyKeyEQ(fmt.Sprintf("tool-invocation:%d", call.ID))).OnlyX(ctx)
				require.NotNil(t, committedReceipt.WorkItemID)
				work := owner.Ticket.GetX(ctx, *committedReceipt.WorkItemID)
				var businessBefore string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, work.ID).Scan(&businessBefore))
				outcomeEntered := make(chan int, 1)
				releaseUpdate := make(chan struct{})
				var gateArmed atomic.Bool
				gateArmed.Store(true)
				defer gateArmed.Store(false)
				runtime.ToolInvocation.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
						if m.Op().Is(ent.OpUpdate|ent.OpUpdateOne) && c.Value(attemptKey{}) == first && gateArmed.CompareAndSwap(true, false) {
							mutationTx, err := m.(*ent.ToolInvocationMutation).Tx()
							if err != nil {
								return nil, err
							}
							rows, err := mutationTx.Client().QueryContext(c, `SELECT pg_backend_pid()`)
							if err != nil {
								return nil, err
							}
							var pid int
							if !rows.Next() {
								rows.Close()
								return nil, errors.New("outcome backend missing")
							}
							err = rows.Scan(&pid)
							rows.Close()
							if err != nil {
								return nil, err
							}
							outcomeEntered <- pid
							select {
							case <-releaseUpdate:
							case <-c.Done():
								return nil, c.Err()
							}
						}
						return next.Mutate(c, m)
					})
				})
				close(release[first])
				var firstPID int
				select {
				case firstPID = <-outcomeEntered:
				case <-operationCtx.Done():
					t.Fatal(operationCtx.Err())
				}
				second := "done"
				if first == "done" {
					second = "failed"
				}
				close(release[second])
				require.Eventually(t, func() bool {
					var waiting bool
					err := ownerDB.QueryRowContext(operationCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND $2::integer=ANY(pg_blocking_pids(pid)))`, runtimeRole, firstPID).Scan(&waiting)
					return err == nil && waiting
				}, 5*time.Second, 10*time.Millisecond, "second outcome must wait on the first outcome transaction")
				close(releaseUpdate)
				var firstErr, secondErr error
				select {
				case firstErr = <-results[first]:
				case <-operationCtx.Done():
					t.Fatal(operationCtx.Err())
				}
				select {
				case secondErr = <-results[second]:
				case <-operationCtx.Done():
					t.Fatal(operationCtx.Err())
				}
				if first == "done" {
					require.NoError(t, firstErr)
				} else {
					require.ErrorIs(t, firstErr, injected)
				}
				var pgError *pq.Error
				require.ErrorAs(t, secondErr, &pgError)
				require.Equal(t, pq.ErrorCode("40001"), pgError.Code)
				if second == "failed" {
					require.ErrorIs(t, secondErr, injected)
				}
				var beforeRetry, afterRetry string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, call.ID).Scan(&beforeRetry))
				require.Equal(t, first, owner.ToolInvocation.GetX(ctx, call.ID).Status)
				require.NoError(t, queue.ProcessJob(ctx, job))
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, call.ID).Scan(&afterRetry))
				if first == "done" {
					require.JSONEq(t, beforeRetry, afterRetry, "late failure and retry cannot replace first completion")
				}
				require.Equal(t, "done", owner.ToolInvocation.GetX(ctx, call.ID).Status)
				require.NoError(t, queue.ProcessJob(ctx, job))
				var replay string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, call.ID).Scan(&replay))
				require.JSONEq(t, afterRetry, replay)
				completed := owner.ToolInvocation.GetX(ctx, call.ID)
				require.Nil(t, completed.Error, "successful recovery clears the failed receipt error")
				require.NotNil(t, completed.Result)
				var result creation.CreateWorkItemResult
				require.NoError(t, json.Unmarshal([]byte(*completed.Result), &result))
				require.Equal(t, work.ID, result.WorkItemID)
				require.Equal(t, work.TicketNumber, result.Number)
				require.Equal(t, work.RecordClass, result.RecordClass)
				var businessAfter string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, work.ID).Scan(&businessAfter))
				require.JSONEq(t, businessBefore, businessAfter, "outcome retries preserve business row including version and status")
				require.Equal(t, items+1, owner.Ticket.Query().CountX(ctx))
				require.Equal(t, receipts+1, owner.IntakeRequest.Query().CountX(ctx))
			})
		}

		for _, toolName := range []string{"create_ticket", "missing-tool"} {
			t.Run("outcome rollback and retry "+toolName, func(t *testing.T) {
				tx, err := runtime.Tx(ctx)
				require.NoError(t, err)
				require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
				call, err := tx.ToolInvocation.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetToolName(toolName).SetArguments(`{"title":"Receipt recovery"}`).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(actor.ID).SetApprovedAt(time.Now()).SetStatus("pending").Save(ctx)
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
				var before, after string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, call.ID).Scan(&before))
				items := owner.Ticket.Query().CountX(ctx)
				var armed atomic.Bool
				armed.Store(true)
				injected := errors.New("private outcome post-update failure")
				runtime.ToolInvocation.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(c, m)
						if err == nil && m.Op().Is(ent.OpUpdate|ent.OpUpdateOne) && armed.CompareAndSwap(true, false) {
							return nil, injected
						}
						return value, err
					})
				})
				job := service.ToolJob{InvocationID: call.ID, TenantID: tenant.ID}
				err = queue.ProcessJob(ctx, job)
				require.ErrorIs(t, err, injected)
				require.False(t, armed.Load())
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, call.ID).Scan(&after))
				require.JSONEq(t, before, after)
				if toolName == "create_ticket" {
					require.Equal(t, items+1, owner.Ticket.Query().CountX(ctx))
				} else {
					require.Equal(t, items, owner.Ticket.Query().CountX(ctx))
				}
				err = queue.ProcessJob(ctx, job)
				row := owner.ToolInvocation.GetX(ctx, call.ID)
				if toolName == "create_ticket" {
					require.NoError(t, err)
					require.Equal(t, "done", row.Status)
					require.Equal(t, items+1, owner.Ticket.Query().CountX(ctx))
				} else {
					require.Error(t, err)
					require.Equal(t, "failed", row.Status)
					require.NotNil(t, row.Error)
					require.Equal(t, "tool execution failed", *row.Error)
					require.Equal(t, items, owner.Ticket.Query().CountX(ctx))
				}
			})
		}

		for _, revocation := range []struct{ name, revoke, restore, key string }{
			{"scope", `UPDATE execution_scopes SET status='closed' WHERE id=$1`, `UPDATE execution_scopes SET status='active' WHERE id=$1`, scopeID},
			{"binding deployment", `UPDATE execution_runtime_bindings SET deployment_id='revoked-deployment' WHERE runtime_role=$1`, `UPDATE execution_runtime_bindings SET deployment_id='intake-test' WHERE runtime_role=$1`, runtimeRole},
			{"binding mode", `UPDATE execution_runtime_bindings SET mode='standard' WHERE runtime_role=$1`, `UPDATE execution_runtime_bindings SET mode='candidate' WHERE runtime_role=$1`, runtimeRole},
			{"binding deletion", `DELETE FROM execution_runtime_bindings WHERE runtime_role=$1`, `INSERT INTO execution_runtime_bindings(runtime_role,deployment_id,mode) VALUES($1,'intake-test','candidate') ON CONFLICT(runtime_role) DO UPDATE SET deployment_id='intake-test',mode='candidate'`, runtimeRole},
		} {
			for _, finish := range []string{"commit", "rollback"} {
				t.Run(revocation.name+" revocation waits for outcome "+finish, func(t *testing.T) {
					tx, err := runtime.Tx(ctx)
					require.NoError(t, err)
					require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
					call, err := tx.ToolInvocation.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetToolName("create_ticket").SetArguments(`{"title":"Outcome scope revocation"}`).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(actor.ID).SetApprovedAt(time.Now()).SetStatus("pending").Save(ctx)
					require.NoError(t, err)
					require.NoError(t, tx.Commit())
					var before, after string
					require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, call.ID).Scan(&before))
					items := owner.Ticket.Query().CountX(ctx)
					var armed atomic.Bool
					revoked := make(chan error, 1)
					finished := make(chan struct{})
					var started atomic.Bool
					operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()
					connection, err := ownerDB.Conn(operationCtx)
					require.NoError(t, err)
					defer connection.Close()
					var pid int
					require.NoError(t, connection.QueryRowContext(operationCtx, `SELECT pg_backend_pid()`).Scan(&pid))
					injected := errors.New("rollback locked outcome")
					armed.Store(true)
					defer func() {
						armed.Store(false)
						cancel()
						if started.Load() {
							select {
							case <-finished:
							case <-time.After(5 * time.Second):
								t.Fatal("revoker cleanup did not finish")
							}
						}
						restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer restoreCancel()
						_, err := ownerDB.ExecContext(restoreCtx, revocation.restore, revocation.key)
						require.NoError(t, err)
					}()
					runtime.ToolInvocation.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
							fired := m.Op().Is(ent.OpUpdate|ent.OpUpdateOne) && armed.CompareAndSwap(true, false)
							if fired {
								mutationTx, err := m.(*ent.ToolInvocationMutation).Tx()
								require.NoError(t, err)
								rows, err := mutationTx.Client().QueryContext(c, `SELECT pg_backend_pid()`)
								require.NoError(t, err)
								require.True(t, rows.Next())
								var outcomePID int
								require.NoError(t, rows.Scan(&outcomePID))
								require.NoError(t, rows.Close())
								started.Store(true)
								go func() {
									defer close(finished)
									_, err := connection.ExecContext(operationCtx, revocation.revoke, revocation.key)
									revoked <- err
								}()
								require.Eventually(t, func() bool {
									var waiting bool
									err := ownerDB.QueryRowContext(operationCtx, `SELECT $1::integer=ANY(pg_blocking_pids($2))`, outcomePID, pid).Scan(&waiting)
									return err == nil && waiting
								}, 5*time.Second, 10*time.Millisecond, "revocation must wait on the outcome transaction")
							}
							value, err := next.Mutate(c, m)
							if err == nil && fired && finish == "rollback" {
								return nil, injected
							}
							return value, err
						})
					})
					err = queue.ProcessJob(ctx, service.ToolJob{InvocationID: call.ID, TenantID: tenant.ID})
					if finish == "commit" {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, injected)
					}
					require.False(t, armed.Load())
					select {
					case err := <-revoked:
						require.NoError(t, err)
					case <-operationCtx.Done():
						t.Fatal("revocation did not finish after outcome transaction")
					}
					require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, call.ID).Scan(&after))
					if finish == "rollback" {
						require.JSONEq(t, before, after)
					} else {
						require.Equal(t, "done", owner.ToolInvocation.GetX(ctx, call.ID).Status)
					}
					err = queue.ProcessJob(ctx, service.ToolJob{InvocationID: call.ID, TenantID: tenant.ID})
					require.ErrorIs(t, err, executionscope.ErrDenied, "committed revocation blocks the next write")
					var afterRetry string
					require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tool_invocations t WHERE id=$1`, call.ID).Scan(&afterRetry))
					require.JSONEq(t, after, afterRetry)
					assert.Equal(t, items+1, owner.Ticket.Query().CountX(ctx), "business committed before this revocation")
				})

			}

		}
		for _, kind := range []string{"actor", "approver", "requester", "actor_role", "role_active", "role_permission", "permission", "approval", "arguments"} {
			for _, rollback := range []bool{false, true} {
				t.Run(fmt.Sprintf("tool authorization revocation %s rollback=%t", kind, rollback), func(t *testing.T) {
					suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
					approver := owner.User.Create().SetTenantID(tenant.ID).SetUsername("approver-" + suffix).SetName("Approver").SetEmail("approver-" + suffix + "@example.invalid").SetPasswordHash("fixture").SetRole("requester").SaveX(ctx)
					requester := owner.User.Create().SetTenantID(tenant.ID).SetUsername("requester-" + suffix).SetName("Requester").SetEmail("requester-" + suffix + "@example.invalid").SetPasswordHash("fixture").SetRole("requester").SaveX(ctx)
					// Preserve referenced identities without adding recipients to later SLA fixtures.
					defer func() {
						cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cleanupCancel()
						_, err := ownerDB.ExecContext(cleanupCtx, `UPDATE users SET active=false WHERE id IN ($1,$2)`, approver.ID, requester.ID)
						require.NoError(t, err)
					}()
					tx, err := runtime.Tx(ctx)
					require.NoError(t, err)
					require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
					call, err := tx.ToolInvocation.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetToolName("create_ticket").SetArguments(fmt.Sprintf(`{"title":"Actor revocation","requester_id":%d}`, requester.ID)).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(approver.ID).SetApprovedAt(time.Now()).SetStatus("pending").Save(ctx)
					require.NoError(t, err)
					require.NoError(t, tx.Commit())
					input := creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", Title: "Actor revocation", IdempotencyKey: fmt.Sprintf("tool-invocation:%d", call.ID), Generic: &creation.GenericInput{Source: "ai"}, SourceReference: &creation.SourceReference{Provider: "tool_queue", EventID: fmt.Sprint(call.ID)}}
					who := identity
					who.Channel, who.Provider = "ai_tool", "tool_queue"
					who.RequesterID = requester.ID
					var revokeSQL, restoreSQL string
					var key int
					switch kind {
					case "actor", "approver", "requester":
						key = actor.ID
						if kind == "approver" {
							key = approver.ID
						}
						if kind == "requester" {
							key = requester.ID
						}
						revokeSQL, restoreSQL = `UPDATE users SET active=false WHERE id=$1`, `UPDATE users SET active=true WHERE id=$1`
					case "actor_role":
						key = actor.ID
						revokeSQL, restoreSQL = `UPDATE users SET role='no_tool_permission' WHERE id=$1`, `UPDATE users SET role='requester' WHERE id=$1`
					case "role_active":
						key = role.ID
						revokeSQL, restoreSQL = `UPDATE roles SET is_active=false WHERE id=$1`, `UPDATE roles SET is_active=true WHERE id=$1`
					case "role_permission":
						key = roleLink.ID
						revokeSQL = `DELETE FROM role_permissions WHERE id=$1`
						restoreSQL = fmt.Sprintf(`INSERT INTO role_permissions(id,role_id,permission_id,tenant_id) VALUES ($1,%d,%d,%d) ON CONFLICT(id) DO NOTHING`, role.ID, permission.ID, tenant.ID)
					case "permission":
						key = permission.ID
						revokeSQL, restoreSQL = `UPDATE permissions SET action='no_tool_permission' WHERE id=$1`, `UPDATE permissions SET action='*' WHERE id=$1`
					case "approval":
						key = call.ID
						revokeSQL, restoreSQL = `UPDATE tool_invocations SET approval_state='rejected' WHERE id=$1`, `UPDATE tool_invocations SET approval_state='approved' WHERE id=$1`
					case "arguments":
						key = call.ID
						revokeSQL = `UPDATE tool_invocations SET arguments='{"title":"Changed approval"}' WHERE id=$1`
						restoreSQL = fmt.Sprintf(`UPDATE tool_invocations SET arguments='{"title":"Actor revocation","requester_id":%d}' WHERE id=$1`, requester.ID)
					}
					injected := errors.New("tool business rollback after INSERT")
					items, requests := owner.Ticket.Query().CountX(ctx), owner.IntakeRequest.Query().CountX(ctx)
					var armed, started atomic.Bool
					revoked, finished := make(chan error, 1), make(chan struct{})
					operationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
					defer cancel()
					connection, err := ownerDB.Conn(operationCtx)
					require.NoError(t, err)
					defer connection.Close()
					var revokerPID int
					require.NoError(t, connection.QueryRowContext(operationCtx, `SELECT pg_backend_pid()`).Scan(&revokerPID))
					armed.Store(true)
					defer func() {
						armed.Store(false)
						cancel()
						if started.Load() {
							select {
							case <-finished:
							case <-time.After(5 * time.Second):
								t.Fatal("actor revoker did not stop")
							}
						}
						restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer restoreCancel()
						_, err := ownerDB.ExecContext(restoreCtx, restoreSQL, key)
						require.NoError(t, err)
					}()
					runtime.Ticket.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
							fired := m.Op() == ent.OpCreate && armed.CompareAndSwap(true, false)
							if fired {
								mutationTx, err := m.(*ent.TicketMutation).Tx()
								require.NoError(t, err)
								rows, err := mutationTx.Client().QueryContext(c, `SELECT pg_backend_pid()`)
								require.NoError(t, err)
								require.True(t, rows.Next())
								var businessPID int
								require.NoError(t, rows.Scan(&businessPID))
								require.NoError(t, rows.Close())
								started.Store(true)
								go func() {
									defer close(finished)
									_, err := connection.ExecContext(operationCtx, revokeSQL, key)
									revoked <- err
								}()
								require.Eventually(t, func() bool {
									var waiting bool
									err := ownerDB.QueryRowContext(operationCtx, `SELECT $1::integer=ANY(pg_blocking_pids($2))`, businessPID, revokerPID).Scan(&waiting)
									return err == nil && waiting
								}, 5*time.Second, 10*time.Millisecond, "authorization revocation must wait on the actual business transaction")
							}
							value, err := next.Mutate(c, m)
							if err == nil && fired && rollback {
								return nil, injected
							}
							return value, err
						})
					})
					_, err = app.Create(operationCtx, who, input)
					delta := 1
					if rollback {
						require.ErrorIs(t, err, injected)
						delta = 0
					} else {
						require.NoError(t, err)
					}
					require.True(t, started.Load())
					select {
					case err := <-revoked:
						require.NoError(t, err)
					case <-operationCtx.Done():
						t.Fatal(operationCtx.Err())
					}
					assert.Equal(t, items+delta, owner.Ticket.Query().CountX(ctx))
					assert.Equal(t, requests+delta, owner.IntakeRequest.Query().CountX(ctx))
					_, err = app.Create(ctx, who, input)
					expectedError := creation.ErrPermissionDenied
					if kind == "actor" || kind == "actor_role" {
						expectedError = creation.ErrAuthenticationRequired
					}
					require.ErrorIs(t, err, expectedError, "committed authorization revocation must deny creation or receipt replay")
					assert.Equal(t, items+delta, owner.Ticket.Query().CountX(ctx))
					assert.Equal(t, requests+delta, owner.IntakeRequest.Query().CountX(ctx))
				})
			}
		}

	})
	t.Run("new base extension and member commit together", func(t *testing.T) {
		created, err := app.Create(ctx, identity, command("new", "incident"))
		require.NoError(t, err)
		require.Positive(t, created.ProfessionalReference.ID)
		event := owner.OutboxEvent.Query().Where(outboxevent.EventIDEQ(fmt.Sprintf("incident-created:%d", created.WorkItemID))).OnlyX(ctx)
		require.NotNil(t, event.ExecutionWorkItemID)
		require.Equal(t, created.WorkItemID, *event.ExecutionWorkItemID)
		require.Equal(t, created.WorkItemID, owner.Incident.GetX(ctx, created.ProfessionalReference.ID).WorkItemID)
		var scope string
		require.NoError(t, ownerDB.QueryRow(`SELECT scope_id::text FROM execution_scope_members WHERE work_item_id=$1`, created.WorkItemID).Scan(&scope))
		require.Equal(t, scopeID, scope)
		parent := command("child", "generic")
		parent.ParentTicketID = &created.WorkItemID
		_, err = app.Create(ctx, identity, parent)
		require.NoError(t, err)
		related := command("new-source", "problem")
		related.SourceRelations = []creation.SourceRelationInput{{SourceWorkItemID: created.WorkItemID, ExpectedVersion: 1, RelationType: "investigated_by"}}
		problem, err := app.Create(ctx, identity, related)
		require.NoError(t, err)
		require.Equal(t, problem.WorkItemID, owner.Problem.GetX(ctx, problem.ProfessionalReference.ID).WorkItemID)
		relationEvent := owner.OutboxEvent.Query().Where(outboxevent.EventTypeEQ(service.RelationCreatedEventType)).OnlyX(ctx)
		require.NotNil(t, relationEvent.ExecutionWorkItemID)
		require.Equal(t, created.WorkItemID, *relationEvent.ExecutionWorkItemID)
	})

	t.Run("KAF delegation generation preserves historical work", func(t *testing.T) {
		creator := service.NewKafDelegationService(runtime, policy)
		actionCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
		actionCtx = context.WithValue(actionCtx, bpmn.BPMNUserIDContextKey, actor.ID)
		node := &service.BPMNServiceTask{ID: "CandidateDelegate", Name: "Candidate delegation", ExtensionElements: &service.BPMNExtensionElements{MetaData: []service.BPMNMetaData{{Name: "service_task_type", Value: bpmn.KafDelegateTaskType}, {Name: "allowed_actions", Value: "complete_bpmn_task"}}}}
		fail := false
		injected := errors.New("injected delegation outbox post-write failure")
		runtime.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, m)
				if err == nil && fail {
					return nil, injected
				}
				return value, err
			})
		})
		defer func() { fail = false }()
		for _, entry := range []string{"owned", "joined", "fault", "joined_fault"} {
			fresh, err := app.Create(ctx, identity, command("generation-"+entry, "incident"))
			require.NoError(t, err)
			for _, itemID := range []int{historical.WorkItemID, fresh.WorkItemID} {
				key := fmt.Sprintf("generation-%s-%d", entry, itemID)
				deployment := owner.ProcessDeployment.Create().SetDeploymentID(key).SetDeploymentName(key).SetTenantID(tenant.ID).SaveX(ctx)
				definition := owner.ProcessDefinition.Create().SetKey(key).SetName(key).SetVersion("1").SetDeploymentID(deployment.ID).SetBpmnXML([]byte("fixture-not-executed")).SetTenantID(tenant.ID).SaveX(ctx)
				var reference *int
				if itemID == fresh.WorkItemID {
					reference = &itemID
				}
				instance := owner.ProcessInstance.Create().SetProcessInstanceID(key).SetProcessDefinitionID(definition.ID).SetProcessDefinitionKey(key).SetBusinessKey(fmt.Sprintf("incident:%d", itemID)).SetBusinessType("incident").SetBusinessID(itemID).SetNillableExecutionWorkItemID(reference).SetTenantID(tenant.ID).SetStatus("running").SaveX(ctx)
				snapshot := func() []byte {
					v, err := json.Marshal([]interface{}{owner.ProcessInstance.GetX(ctx, instance.ID), owner.ProcessTask.Query().CountX(ctx), owner.AuditLog.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx)})
					require.NoError(t, err)
					return v
				}
				before := snapshot()
				var task *ent.ProcessTask
				fail = strings.Contains(entry, "fault")
				if !strings.HasPrefix(entry, "joined") {
					task, err = creator.CreateDelegatedTask(actionCtx, instance.ID, node)
				} else {
					tx, beginErr := runtime.Tx(actionCtx)
					require.NoError(t, beginErr)
					task, err = creator.CreateDelegatedTaskTx(actionCtx, tx, instance.ID, node)
					if err == nil {
						require.Positive(t, task.ID)
						require.Equal(t, "CandidateDelegate", tx.ProcessInstance.GetX(actionCtx, instance.ID).CurrentActivityID)
					}
					require.NoError(t, tx.Rollback())
				}
				fail = false
				if itemID == historical.WorkItemID {
					require.ErrorIs(t, err, executionscope.ErrDenied)
					require.JSONEq(t, string(before), string(snapshot()))
				} else if strings.Contains(entry, "fault") {
					require.ErrorIs(t, err, injected)
					require.JSONEq(t, string(before), string(snapshot()))
				} else {
					require.NoError(t, err)
					if entry == "joined" {
						require.JSONEq(t, string(before), string(snapshot()))
					} else {
						event := owner.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(task.TaskID)).OnlyX(ctx)
						require.NotNil(t, event.ExecutionWorkItemID)
						require.Equal(t, itemID, *event.ExecutionWorkItemID)
					}
				}
			}
		}
	})

	t.Run("KAF access completion uses candidate transaction", func(t *testing.T) {
		automation := owner.User.Create().SetTenantID(tenant.ID).SetUsername("scope-kaf").SetEmail("scope-kaf@example.invalid").SetName("Candidate automation").SetPasswordHash("test-only").SetRole("kaf_automation").SaveX(ctx)
		owner.ExternalIdentity.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetProvider("graph").SetWorkspace("directory").SetSubject("approved-subject").SaveX(ctx)
		items := map[string]int{"historical": historicalKafRequest.WorkItemID}
		for _, kind := range []string{"rollback", "success", "success_round_up", "success_even_half", "success_odd_half"} {
			fresh, err := app.Create(ctx, identity, command("kaf-"+kind, "service_request_item"))
			require.NoError(t, err)
			items[kind] = fresh.WorkItemID
		}
		accessPolicy := owner.CatalogAccessPolicy.Create().SetCatalogID(catalog.ID).SetProvider("graph").SetExternalSystem("directory").SetGroupID("approved-group").SetDurationField("duration").SetDurationOptions([]accessgrant.DurationOption{{Key: "month", Label: "Month", Seconds: 2592000}}).SaveX(ctx)
		// Approved snapshots and delegated workflow state are fixture prerequisites.
		// This test exercises completion, not the preceding approval/provider journey.
		defer func() {
			for _, itemID := range items {
				_, err := owner.ServiceRequestAccessSnapshot.Delete().Where(servicerequestaccesssnapshot.WorkItemIDEQ(itemID)).Exec(ctx)
				require.NoError(t, err)
			}
			require.NoError(t, owner.CatalogAccessPolicy.DeleteOne(accessPolicy).Exec(ctx))
		}()
		completionOwner := srdomain.NewService(srdomain.NewEntRepository(runtime, policy), runtime, zap.NewNop().Sugar(), nil, policy)
		engine := service.NewCustomProcessEngine(runtime, zap.NewNop().Sugar(), policy).(*service.CustomProcessEngine)
		engine.SetAccessCompletionContributor(completionOwner)
		engine.CallbackRegistry().RegisterHandler(bpmn.NewKafDelegateServiceTaskHandler(runtime, zap.NewNop().Sugar()))
		injected := errors.New("injected KAF final lease fence failure")
		fail := false
		runtime.KafTaskActionLedger.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				v, err := next.Mutate(ctx, m)
				if err == nil && fail {
					return nil, injected
				}
				return v, err
			})
		})
		defer func() { fail = false }()
		for _, kind := range []string{"historical", "rollback", "success", "success_round_up", "success_even_half", "success_odd_half"} {
			itemID := items[kind]
			key := "candidate-kaf-" + kind
			deployment := owner.ProcessDeployment.Create().SetDeploymentID(key).SetDeploymentName(key).SetTenantID(tenant.ID).SaveX(ctx)
			xml := `<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="https://example.invalid"><bpmn:process id="candidate-kaf" isExecutable="true"><bpmn:startEvent id="Start"/><bpmn:serviceTask id="Current"/><bpmn:endEvent id="End"/><bpmn:sequenceFlow id="S1" sourceRef="Start" targetRef="Current"/><bpmn:sequenceFlow id="S2" sourceRef="Current" targetRef="End"/></bpmn:process></bpmn:definitions>`
			definition := owner.ProcessDefinition.Create().SetKey(key).SetName(key).SetVersion("1").SetIsLatest(true).SetBpmnXML([]byte(xml)).SetDeploymentID(deployment.ID).SetTenantID(tenant.ID).SaveX(ctx)
			var executionID *int
			if kind != "historical" {
				executionID = &itemID
			}
			instance := owner.ProcessInstance.Create().SetNillableExecutionWorkItemID(executionID).SetProcessInstanceID(key).SetProcessDefinitionKey(key).SetProcessDefinitionID(definition.ID).SetBusinessKey(fmt.Sprintf("service_request_item:%d", itemID)).SetBusinessType("service_request_item").SetBusinessID(itemID).SetStatus("running").SetCurrentActivityID("Current").SetVersion(1).SetTenantID(tenant.ID).SaveX(ctx)
			task := owner.ProcessTask.Create().SetTaskID(key).SetProcessInstanceID(instance.ID).SetProcessDefinitionKey(key).SetTaskDefinitionKey("Current").SetTaskName(key).SetCreatedTime(time.Now().Add(-5 * time.Second)).SetTaskType(bpmn.KafDelegateTaskType).SetStatus("delegated").SetTaskVariables(map[string]interface{}{"allowed_actions": "complete_bpmn_task"}).SetCallbackHandlerID("kaf_delegate_handler").SetCallbackTaskType(bpmn.KafDelegateTaskType).SetCallbackAction(accessgrant.Capability).SetCallbackConfigRef(fmt.Sprint(accessPolicy.ID)).SetTenantID(tenant.ID).SaveX(ctx)
			owner.ProcessApprovalDecision.Create().SetProcessInstanceID(instance.ID).SetProcessTaskID(task.ID).SetProcessInstanceKey(key).SetTaskID(key + "-approval").SetProcessDefinitionKey(key).SetNodeKey("Approval").SetActorID(actor.ID).SetAction("approve").SetDecision("approved").SetTenantID(tenant.ID).SaveX(ctx)
			owner.ServiceRequestAccessSnapshot.Create().SetWorkItemID(itemID).SetPolicyID(accessPolicy.ID).SetPolicyVersion(1).SetProvider("graph").SetExternalSystem("directory").SetSubjectID("approved-subject").SetGroupID("approved-group").SetDurationKey("month").SetDurationSeconds(2592000).SaveX(ctx)
			if kind == "historical" {
				claim := service.KafActionRequest{Action: "complete_bpmn_task", ExpectedVersion: instance.Version,
					Execution: service.KafActionExecution{RunID: "historical-claim", StepID: "finish", CorrelationID: key, ProcedureRef: "access", ProcedureVersion: "1"}}
				claim.Execution.IdempotencyKey = fmt.Sprintf("%d:%s:%s:%s", tenant.ID, task.TaskID, claim.Execution.RunID, claim.Execution.StepID)
				beforeClaims := owner.KafTaskActionLedger.Query().CountX(ctx)
				_, claimed, claimErr := service.NewKafDelegationService(runtime, policy).ClaimKafAction(ctx, task, claim)
				t.Logf("historical claim: claimed=%t, ledger count before=%d after=%d", claimed, beforeClaims, owner.KafTaskActionLedger.Query().CountX(ctx))
				require.Error(t, claimErr, "historical task must be rejected before ledger INSERT or lease UPDATE")
				require.False(t, claimed)
				require.Equal(t, beforeClaims, owner.KafTaskActionLedger.Query().CountX(ctx))
			}
			ledger := owner.KafTaskActionLedger.Create().SetTenantID(tenant.ID).SetTaskID(key).SetRunID(key).SetStepID("finish").SetAction("complete_bpmn_task").SetIdempotencyKey(key).SetRequestDigest("fixture-preclaimed-digest").SetCorrelationID(key).SetProcedureRef("access").SetProcedureVersion("1").SetResultStatus("executing").SetLeaseOwner(key).SetLeaseExpiresAt(time.Now().Add(time.Minute)).SaveX(ctx)
			actionCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
			actionCtx = context.WithValue(actionCtx, bpmn.BPMNUserIDContextKey, automation.ID)
			offset := 123 * time.Nanosecond
			if kind == "success_round_up" {
				offset = 789 * time.Nanosecond
			}
			verifiedAt := time.Now().UTC().Truncate(time.Microsecond).Add(offset)
			if kind == "success_even_half" {
				verifiedAt = time.Now().UTC().Add(-time.Second).Truncate(time.Second).Add(time.Millisecond + 500*time.Nanosecond)
			}
			if kind == "success_odd_half" {
				verifiedAt = time.Now().UTC().Truncate(time.Millisecond).Add(1500 * time.Nanosecond)
			}
			variables := map[string]interface{}{"kaf_access_result": map[string]interface{}{"outcome": "granted", "provider": "graph", "subjectId": "approved-subject", "groupId": "approved-group", "baseline": "not_member", "verifiedAt": verifiedAt.Format(time.RFC3339Nano), "evidenceRef": key}}
			if kind != "historical" {
				claim := service.KafActionRequest{Action: "complete_bpmn_task", ExpectedVersion: instance.Version,
					Execution: service.KafActionExecution{RunID: "candidate-claim", StepID: "finish", CorrelationID: key, ProcedureRef: "access", ProcedureVersion: "1"}}
				claim.Execution.IdempotencyKey = fmt.Sprintf("%d:%s:%s:%s", tenant.ID, task.TaskID, claim.Execution.RunID, claim.Execution.StepID)
				claim.Payload.AccessResult, err = json.Marshal(variables["kaf_access_result"])
				require.NoError(t, err)
				claimOwner := service.NewKafDelegationService(runtime, policy)
				claimedLedger, claimed, claimErr := claimOwner.ClaimKafAction(ctx, task, claim)
				require.NoError(t, claimErr)
				require.True(t, claimed)
				require.Equal(t, "executing", claimedLedger.ResultStatus)
				require.NotEmpty(t, claimedLedger.RequestDigest)
				_, claimed, claimErr = claimOwner.ClaimKafAction(ctx, task, claim)
				require.ErrorIs(t, claimErr, service.ErrKafActionInProgress)
				require.False(t, claimed)
				if kind == "rollback" {
					owner.KafTaskActionLedger.UpdateOneID(claimedLedger.ID).SetLeaseExpiresAt(time.Now().Add(-time.Minute)).ExecX(ctx)
					beforeClaim, _ := json.Marshal(owner.KafTaskActionLedger.GetX(ctx, claimedLedger.ID))
					_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
					require.NoError(t, err)
					_, claimed, claimErr = claimOwner.ClaimKafAction(ctx, task, claim)
					_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
					require.NoError(t, err)
					require.ErrorIs(t, claimErr, executionscope.ErrDenied)
					require.False(t, claimed)
					afterClaim, _ := json.Marshal(owner.KafTaskActionLedger.GetX(ctx, claimedLedger.ID))
					require.JSONEq(t, string(beforeClaim), string(afterClaim))
				}
			}
			snapshot := func() []byte {
				v, err := json.Marshal([]interface{}{owner.Ticket.GetX(ctx, itemID), owner.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(itemID)).OnlyX(ctx), owner.ProcessTask.GetX(ctx, task.ID), owner.ProcessInstance.GetX(ctx, instance.ID), owner.KafTaskActionLedger.GetX(ctx, ledger.ID), owner.KafTaskCompletionReceipt.Query().Where(kaftaskcompletionreceipt.LedgerIDEQ(ledger.ID)).AllX(ctx), owner.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessTaskIDEQ(task.ID)).AllX(ctx), owner.ServiceRequestAccessResult.Query().CountX(ctx), owner.AuditLog.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx)})
				require.NoError(t, err)
				return v
			}
			before := snapshot()
			fail = kind == "rollback"
			err = engine.CompleteKafDelegatedTask(actionCtx, ledger.ID, key, key, variables)
			fail = false
			switch kind {
			case "historical":
				require.ErrorIs(t, err, executionscope.ErrDenied)
				require.JSONEq(t, string(before), string(snapshot()))
			case "rollback":
				require.ErrorIs(t, err, injected)
				require.JSONEq(t, string(before), string(snapshot()))
			case "success", "success_round_up", "success_even_half", "success_odd_half":
				require.NoError(t, err)
				require.Equal(t, "resolved", owner.Ticket.GetX(ctx, itemID).Status)
				require.Equal(t, "completed", owner.ProcessTask.GetX(ctx, task.ID).Status)
				require.Equal(t, "completed", owner.ProcessInstance.GetX(ctx, instance.ID).Status)
				replayed := snapshot()
				require.NoError(t, engine.CompleteKafDelegatedTask(actionCtx, ledger.ID, key, key, variables))
				require.JSONEq(t, string(replayed), string(snapshot()))
				if kind == "success" {
					owner.KafTaskCompletionReceipt.Update().Where(kaftaskcompletionreceipt.LedgerIDEQ(ledger.ID)).SetStatus("callback_pending").SaveX(ctx)
					beforeRecovery := snapshot()
					_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
					require.NoError(t, err)
					recoveryErr := engine.CompleteKafDelegatedTask(actionCtx, ledger.ID, key, key, variables)
					_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
					require.NoError(t, err)
					require.ErrorIs(t, recoveryErr, executionscope.ErrDenied)
					require.JSONEq(t, string(beforeRecovery), string(snapshot()))
					require.NoError(t, engine.CompleteKafDelegatedTask(actionCtx, ledger.ID, key, key, variables))
				}

			}
		}
	})

	t.Run("Requested Item writes preserve history", func(t *testing.T) {
		repo := srdomain.NewEntRepository(runtime, policy)
		svc := srdomain.NewService(repo, runtime, zap.NewNop().Sugar(), nil, policy)
		svc.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
		for _, kind := range []string{"update", "delete", "update_request", "assign_request", "provision_resource", "approve_request", "complete_request"} {
			fresh, err := app.Create(ctx, identity, command("request-"+kind, "service_request_item"))
			require.NoError(t, err)
			for _, target := range []struct{ id, workID int }{{historicalRequest.ProfessionalReference.ID, historicalRequest.WorkItemID}, {fresh.ProfessionalReference.ID, fresh.WorkItemID}} {
				before := owner.Ticket.GetX(ctx, target.workID)
				extensionBefore, _ := json.Marshal(owner.ServiceRequest.GetX(ctx, target.id))
				audits, outboxes := owner.AuditLog.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx)
				switch kind {
				case "update":
					_, err = svc.Update(ctx, target.id, tenant.ID, actor.ID, actor.Role, &srdomain.ServiceRequest{CostCenter: "candidate cost center"})
				case "delete":
					err = svc.Delete(ctx, target.id, workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "http", OperationID: fmt.Sprintf("delete-request-%d", target.id)})
				default:
					cost := "candidate callback cost"
					_, err = svc.ApplyServiceRequestWorkflowCallback(ctx, workflowcallback.ServiceRequestCommand{RequestID: target.id, TenantID: tenant.ID, Action: kind, AssigneeID: actor.ID, CostCenter: &cost, CompletionNote: "candidate completion"})
				}
				if target.workID == historicalRequest.WorkItemID {
					require.ErrorContains(t, err, "execution scope denied")
					beforeJSON, _ := json.Marshal(before)
					afterJSON, _ := json.Marshal(owner.Ticket.GetX(ctx, target.workID))
					require.JSONEq(t, string(beforeJSON), string(afterJSON))
					extensionAfter, _ := json.Marshal(owner.ServiceRequest.GetX(ctx, target.id))
					require.JSONEq(t, string(extensionBefore), string(extensionAfter))
					require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
					require.Equal(t, outboxes, owner.OutboxEvent.Query().CountX(ctx))
				} else {
					require.NoError(t, err)
					require.Greater(t, owner.Ticket.GetX(ctx, target.workID).Version, before.Version)
				}
			}
		}
	})

	t.Run("Requested Item extension failure rolls back base", func(t *testing.T) {
		svc := srdomain.NewService(srdomain.NewEntRepository(runtime, policy), runtime, zap.NewNop().Sugar(), nil, policy)
		svc.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
		injected := errors.New("injected Requested Item extension failure")
		fail := false
		runtime.ServiceRequest.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				v, err := next.Mutate(ctx, m)
				if err == nil && fail {
					return nil, injected
				}
				return v, err
			})
		})
		defer func() { fail = false }()
		for _, kind := range []string{"update", "complete_request"} {
			fresh, err := app.Create(ctx, identity, command("request-rollback-"+kind, "service_request_item"))
			require.NoError(t, err)
			before, _ := json.Marshal(owner.Ticket.GetX(ctx, fresh.WorkItemID))
			extensionBefore, _ := json.Marshal(owner.ServiceRequest.GetX(ctx, fresh.ProfessionalReference.ID))
			audits, outboxes := owner.AuditLog.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx)
			fail = true
			if kind == "update" {
				_, err = svc.Update(ctx, fresh.ProfessionalReference.ID, tenant.ID, actor.ID, actor.Role, &srdomain.ServiceRequest{CostCenter: "rollback cost"})
			} else {
				_, err = svc.ApplyServiceRequestWorkflowCallback(ctx, workflowcallback.ServiceRequestCommand{RequestID: fresh.ProfessionalReference.ID, TenantID: tenant.ID, Action: kind, CompletionNote: "rollback completion"})
			}
			fail = false
			require.Error(t, err)
			require.ErrorContains(t, err, injected.Error())
			after, _ := json.Marshal(owner.Ticket.GetX(ctx, fresh.WorkItemID))
			extensionAfter, _ := json.Marshal(owner.ServiceRequest.GetX(ctx, fresh.ProfessionalReference.ID))
			require.JSONEq(t, string(before), string(after))
			require.JSONEq(t, string(extensionBefore), string(extensionAfter))
			require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
			require.Equal(t, outboxes, owner.OutboxEvent.Query().CountX(ctx))
		}
	})

	t.Run("Change and PIR writes preserve history", func(t *testing.T) {
		svc := changedomain.NewService(changedomain.NewEntRepository(runtime, nil), runtime, zap.NewNop().Sugar(), policy)
		svc.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
		pir := service.NewChangePIRService(runtime, zap.NewNop().Sugar(), policy)
		pir.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
		for _, kind := range []string{"metadata", "cancel", "delete", "pir"} {
			fresh, err := app.Create(ctx, identity, command("change-"+kind, "change_request"))
			require.NoError(t, err)
			for _, target := range []struct{ id, workID int }{{historicalChange.ProfessionalReference.ID, historicalChange.WorkItemID}, {fresh.ProfessionalReference.ID, fresh.WorkItemID}} {
				before := owner.Ticket.GetX(ctx, target.workID)
				extensionBefore, _ := json.Marshal(owner.Change.GetX(ctx, target.id))
				audits, pirs := owner.AuditLog.Query().CountX(ctx), owner.ChangePIR.Query().CountX(ctx)
				meta := workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "http", OperationID: fmt.Sprintf("change-%s-%d", kind, target.id)}
				switch kind {
				case "metadata":
					title := "candidate Change"
					_, err = svc.ApplyMetadata(ctx, changedomain.MetadataCommand{Meta: meta, ChangeID: target.id, Patch: dto.UpdateChangeRequest{Title: &title}})
				case "cancel":
					_, err = svc.ApplyCommand(ctx, changedomain.Command{Meta: meta, ChangeID: target.id, Action: "cancel", Evidence: "candidate test"})
				case "delete":
					err = svc.DeleteChange(ctx, target.id, meta)
				case "pir":
					_, err = pir.CreatePIR(ctx, &dto.CreateChangePIRRequest{ChangeID: target.id, OverallResult: "successful"}, meta)
				}
				if target.workID == historicalChange.WorkItemID {
					require.ErrorContains(t, err, "execution scope denied")
					beforeJSON, _ := json.Marshal(before)
					afterJSON, _ := json.Marshal(owner.Ticket.GetX(ctx, target.workID))
					require.JSONEq(t, string(beforeJSON), string(afterJSON))
					extensionAfter, _ := json.Marshal(owner.Change.GetX(ctx, target.id))
					require.JSONEq(t, string(extensionBefore), string(extensionAfter))
					require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
					require.Equal(t, pirs, owner.ChangePIR.Query().CountX(ctx))
				} else {
					require.NoError(t, err)
					require.Equal(t, before.Version+1, owner.Ticket.GetX(ctx, target.workID).Version)
				}
			}
		}
		audits := owner.AuditLog.Query().CountX(ctx)
		replay, err := pir.CreatePIR(ctx, &dto.CreateChangePIRRequest{ChangeID: historicalChange.ProfessionalReference.ID, OverallResult: "successful"}, legacyPIRMeta)
		require.NoError(t, err)
		require.Equal(t, legacyPIR.PIRID, replay.PIRID)
		require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
		for _, target := range []struct{ changeID, workID, pirID int }{{historicalChange.ProfessionalReference.ID, historicalChange.WorkItemID, legacyPIR.PIRID}} {
			meta := workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: owner.Ticket.GetX(ctx, target.workID).Version, Source: "http", OperationID: "historical-pir-update"}
			summary := "new facts"
			_, err = pir.UpdatePIR(ctx, target.pirID, &dto.UpdateChangePIRRequest{ChangeID: target.changeID, SuccessSummary: &summary}, meta)
			require.ErrorContains(t, err, "execution scope denied")
			meta.OperationID = "historical-pir-delete"
			_, err = pir.DeletePIR(ctx, target.pirID, &dto.DeleteChangePIRRequest{ChangeID: target.changeID}, meta)
			require.ErrorContains(t, err, "execution scope denied")
		}
		fresh, err := app.Create(ctx, identity, command("pir-edit", "change_request"))
		require.NoError(t, err)
		meta := func(key string) workitemmutation.Meta {
			return workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: owner.Ticket.GetX(ctx, fresh.WorkItemID).Version, Source: "http", OperationID: key}
		}
		created, err := pir.CreatePIR(ctx, &dto.CreateChangePIRRequest{ChangeID: fresh.ProfessionalReference.ID, OverallResult: "successful"}, meta("pir-new"))
		require.NoError(t, err)
		summary := "candidate revised PIR"
		_, err = pir.UpdatePIR(ctx, created.PIRID, &dto.UpdateChangePIRRequest{ChangeID: fresh.ProfessionalReference.ID, SuccessSummary: &summary}, meta("pir-edit"))
		require.NoError(t, err)
		_, err = pir.DeletePIR(ctx, created.PIRID, &dto.DeleteChangePIRRequest{ChangeID: fresh.ProfessionalReference.ID}, meta("pir-delete"))
		require.NoError(t, err)
		injected := errors.New("injected Change audit failure")
		failAudit := false
		runtime.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil && failAudit {
					failAudit = false
					return nil, injected
				}
				return value, err
			})
		})
		for _, kind := range []string{"metadata", "pir"} {
			before, _ := json.Marshal(owner.Ticket.GetX(ctx, fresh.WorkItemID))
			extensionBefore, _ := json.Marshal(owner.Change.GetX(ctx, fresh.ProfessionalReference.ID))
			audits, pirs := owner.AuditLog.Query().CountX(ctx), owner.ChangePIR.Query().CountX(ctx)
			failAudit = true
			if kind == "metadata" {
				title := "rollback change title"
				_, err = svc.ApplyMetadata(ctx, changedomain.MetadataCommand{Meta: meta("fault-change-metadata"), ChangeID: fresh.ProfessionalReference.ID, Patch: dto.UpdateChangeRequest{Title: &title}})
			} else {
				_, err = pir.CreatePIR(ctx, &dto.CreateChangePIRRequest{ChangeID: fresh.ProfessionalReference.ID, OverallResult: "successful"}, meta("fault-pir"))
			}
			require.ErrorIs(t, err, injected)
			require.False(t, failAudit)
			after, _ := json.Marshal(owner.Ticket.GetX(ctx, fresh.WorkItemID))
			extensionAfter, _ := json.Marshal(owner.Change.GetX(ctx, fresh.ProfessionalReference.ID))
			require.JSONEq(t, string(before), string(after))
			require.JSONEq(t, string(extensionBefore), string(extensionAfter))
			require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
			require.Equal(t, pirs, owner.ChangePIR.Query().CountX(ctx))
		}
	})

	t.Run("Problem writes preserve historical work", func(t *testing.T) {
		svc := problemdomain.NewService(problemdomain.NewEntRepository(runtime), zap.NewNop().Sugar(), policy)
		svc.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
		for _, kind := range []string{"command", "metadata", "delete"} {
			fresh, err := app.Create(ctx, identity, command("problem-"+kind, "problem"))
			require.NoError(t, err)
			for _, target := range []struct{ id, workID int }{{historicalProblem.ProfessionalReference.ID, historicalProblem.WorkItemID}, {fresh.ProfessionalReference.ID, fresh.WorkItemID}} {
				before := owner.Ticket.GetX(ctx, target.workID)
				beforeExtension, _ := json.Marshal(owner.Problem.GetX(ctx, target.id))
				audits, outboxes := owner.AuditLog.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx)
				meta := workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "http", OperationID: fmt.Sprintf("problem-%s-%d", kind, target.id)}
				switch kind {
				case "command":
					_, err = svc.ApplyCommand(ctx, problemdomain.Command{Meta: meta, ProblemID: target.id, Action: "investigate"})
				case "metadata":
					title := "candidate problem metadata"
					_, err = svc.ApplyMetadata(ctx, problemdomain.MetadataCommand{Meta: meta, ProblemID: target.id, Patch: dto.UpdateProblemRequest{Title: &title}})
				case "delete":
					err = svc.Delete(ctx, target.id, meta)
				}
				if target.workID == historicalProblem.WorkItemID {
					require.ErrorContains(t, err, "execution scope denied")
					beforeJSON, _ := json.Marshal(before)
					afterJSON, _ := json.Marshal(owner.Ticket.GetX(ctx, target.workID))
					require.JSONEq(t, string(beforeJSON), string(afterJSON))
					afterExtension, _ := json.Marshal(owner.Problem.GetX(ctx, target.id))
					require.JSONEq(t, string(beforeExtension), string(afterExtension))
					require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
					require.Equal(t, outboxes, owner.OutboxEvent.Query().CountX(ctx))
				} else {
					require.NoError(t, err)
					require.Equal(t, before.Version+1, owner.Ticket.GetX(ctx, target.workID).Version)
				}
			}
		}
		audits := owner.AuditLog.Query().CountX(ctx)
		replayed, err := svc.ApplyMetadata(ctx, legacyProblemCommand)
		require.NoError(t, err)
		require.Equal(t, legacyProblemResult.Version, replayed.Version)
		require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
		replayed, err = svc.ApplyCommand(ctx, legacyProblemLifecycle)
		require.NoError(t, err)
		require.Equal(t, legacyProblemLifecycleResult.Version, replayed.Version)
		require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
		fresh, err := app.Create(ctx, identity, command("problem-lifecycle", "problem"))
		require.NoError(t, err)
		meta := func(key string) workitemmutation.Meta {
			return workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: owner.Ticket.GetX(ctx, fresh.WorkItemID).Version, Source: "http", OperationID: key}
		}
		cause, resolution := "verified candidate root cause", "permanent candidate fix"
		_, err = svc.ApplyMetadata(ctx, problemdomain.MetadataCommand{Meta: meta("problem-evidence"), ProblemID: fresh.ProfessionalReference.ID, Patch: dto.UpdateProblemRequest{RootCause: &cause, Resolution: &resolution}})
		require.NoError(t, err)
		for _, action := range []string{"investigate", "verify_resolution"} {
			_, err = svc.ApplyCommand(ctx, problemdomain.Command{Meta: meta("problem-" + action), ProblemID: fresh.ProfessionalReference.ID, Action: action, VerificationNote: "candidate verification"})
			require.NoError(t, err)
		}
		before, _ := json.Marshal(owner.Ticket.GetX(ctx, fresh.WorkItemID))
		beforeExtension, _ := json.Marshal(owner.Problem.GetX(ctx, fresh.ProfessionalReference.ID))
		audits, outboxes := owner.AuditLog.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx)
		injected := errors.New("injected problem resolve outbox failure")
		failOutbox := true
		runtime.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil && failOutbox {
					failOutbox = false
					return nil, injected
				}
				return value, err
			})
		})
		resolve := problemdomain.Command{Meta: meta("problem-resolve"), ProblemID: fresh.ProfessionalReference.ID, Action: "resolve"}
		_, err = svc.ApplyCommand(ctx, resolve)
		require.ErrorIs(t, err, injected)
		require.False(t, failOutbox)
		after, _ := json.Marshal(owner.Ticket.GetX(ctx, fresh.WorkItemID))
		afterExtension, _ := json.Marshal(owner.Problem.GetX(ctx, fresh.ProfessionalReference.ID))
		require.JSONEq(t, string(before), string(after))
		require.JSONEq(t, string(beforeExtension), string(afterExtension))
		require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
		require.Equal(t, outboxes, owner.OutboxEvent.Query().CountX(ctx))
		resolved, err := svc.ApplyCommand(ctx, resolve)
		require.NoError(t, err)
		replayed, err = svc.ApplyCommand(ctx, resolve)
		require.NoError(t, err)
		require.Equal(t, resolved.Version, replayed.Version)
		for _, action := range []string{"close", "reopen"} {
			_, err = svc.ApplyCommand(ctx, problemdomain.Command{Meta: meta("problem-" + action), ProblemID: fresh.ProfessionalReference.ID, Action: action, Reason: "candidate regression"})
			require.NoError(t, err)
		}
		require.Equal(t, "investigating", owner.Ticket.GetX(ctx, fresh.WorkItemID).Status)

	})

	t.Run("Incident CI and alert writes require membership", func(t *testing.T) {
		ctx := service.WithIncidentAlertActor(ctx, actor.ID, "user", "candidate-alert-fixture")
		svc := service.NewIncidentService(runtime, zap.NewNop().Sugar(), policy)
		alerts := service.NewIncidentAlertingService(runtime, zap.NewNop().Sugar(), policy)
		alertMailer := service.NewEmailService(candidateNotificationEmailConfig(t), zap.NewNop().Sugar())
		alertMailer.SetDeliveryTargetDependencies(nil, policy)
		alerts.SetEmailService(alertMailer)
		fresh, err := app.Create(ctx, identity, command("incident-ci-alert", "incident"))
		require.NoError(t, err)
		ciType := owner.CIType.Create().SetName("candidate CI type").SetTenantID(tenant.ID).SaveX(ctx)
		ci := owner.ConfigurationItem.Create().SetName("candidate CI").SetCiTypeID(ciType.ID).SetTenantID(tenant.ID).SaveX(ctx)
		err = svc.LinkIncidentCIs(ctx, historical.ProfessionalReference.ID, []int{ci.ID}, tenant.ID)
		require.ErrorContains(t, err, "execution scope denied")
		require.Zero(t, owner.Incident.Query().Where(incident.IDEQ(historical.ProfessionalReference.ID)).QueryConfigurationItems().CountX(ctx))
		require.NoError(t, svc.LinkIncidentCIs(ctx, fresh.ProfessionalReference.ID, []int{ci.ID}, tenant.ID))
		require.Equal(t, 1, owner.Incident.Query().Where(incident.IDEQ(fresh.ProfessionalReference.ID)).QueryConfigurationItems().CountX(ctx))
		request := func(id int) *dto.CreateIncidentAlertRequest {
			return &dto.CreateIncidentAlertRequest{IncidentID: id, AlertType: "scope", AlertName: "candidate alert", Message: "scope test", Severity: "high", Channels: []string{"email", "in_app"}, Recipients: []string{actor.Email}}
		}
		count, outboxes, notifications := owner.IncidentAlert.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx), owner.Notification.Query().CountX(ctx)
		_, err = alerts.CreateIncidentAlert(ctx, request(historical.ProfessionalReference.ID), tenant.ID)
		require.ErrorContains(t, err, "execution scope denied")
		require.Equal(t, count, owner.IncidentAlert.Query().CountX(ctx))
		require.Equal(t, outboxes, owner.OutboxEvent.Query().CountX(ctx))
		require.Equal(t, notifications, owner.Notification.Query().CountX(ctx))
		created, err := alerts.CreateIncidentAlert(ctx, request(fresh.ProfessionalReference.ID), tenant.ID)
		require.NoError(t, err)
		require.Equal(t, count+1, owner.IncidentAlert.Query().CountX(ctx))
		require.Equal(t, outboxes+1, owner.OutboxEvent.Query().CountX(ctx))
		require.NoError(t, alerts.AcknowledgeAlert(ctx, created.ID, actor.ID, tenant.ID))
		require.NoError(t, alerts.ResolveAlert(ctx, created.ID, actor.ID, tenant.ID))
		beforeLegacy, _ := json.Marshal(owner.IncidentAlert.GetX(ctx, legacyAlert.ID))
		events := owner.IncidentEvent.Query().CountX(ctx)
		require.ErrorContains(t, alerts.AcknowledgeAlert(ctx, legacyAlert.ID, actor.ID, tenant.ID), "execution scope denied")
		require.ErrorContains(t, alerts.ResolveAlert(ctx, legacyAlert.ID, actor.ID, tenant.ID), "execution scope denied")
		afterLegacy, _ := json.Marshal(owner.IncidentAlert.GetX(ctx, legacyAlert.ID))
		require.JSONEq(t, string(beforeLegacy), string(afterLegacy))
		require.Equal(t, events, owner.IncidentEvent.Query().CountX(ctx))
		faultAlert, err := alerts.CreateIncidentAlert(ctx, request(fresh.ProfessionalReference.ID), tenant.ID)
		require.NoError(t, err)
		beforeAlert, _ := json.Marshal(owner.IncidentAlert.GetX(ctx, faultAlert.ID))
		injected := errors.New("injected alert timeline failure")
		failEvent := true
		runtime.IncidentEvent.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil && failEvent {
					failEvent = false
					return nil, injected
				}
				return value, err
			})
		})
		require.ErrorIs(t, alerts.AcknowledgeAlert(ctx, faultAlert.ID, actor.ID, tenant.ID), injected)
		require.False(t, failEvent)
		afterAlert, _ := json.Marshal(owner.IncidentAlert.GetX(ctx, faultAlert.ID))
		require.JSONEq(t, string(beforeAlert), string(afterAlert))
		require.Equal(t, events, owner.IncidentEvent.Query().CountX(ctx))

		failEvent = true
		require.ErrorIs(t, alerts.ResolveAlert(ctx, faultAlert.ID, actor.ID, tenant.ID), injected)
		require.False(t, failEvent)
		afterAlert, _ = json.Marshal(owner.IncidentAlert.GetX(ctx, faultAlert.ID))
		require.JSONEq(t, string(beforeAlert), string(afterAlert))
		require.Equal(t, events, owner.IncidentEvent.Query().CountX(ctx))

		svc.SetAlertCreator(alerts)
		rule := owner.IncidentRule.Create().SetName("notify candidate").SetRuleType("notification").SetTenantID(tenant.ID).SetIsActive(true).
			SetConditions(map[string]interface{}{}).SetActions([]map[string]interface{}{{"type": "notify", "channels": []string{"email", "in_app"}, "recipients": []string{actor.Email}, "message": "candidate notification"}}).SaveX(ctx)
		current := owner.Incident.Query().Where(incident.IDEQ(fresh.ProfessionalReference.ID)).WithWorkItem().OnlyX(ctx)
		require.NoError(t, svc.RuleEngine().ExecuteRule(ctx, rule, current, tenant.ID))
		count, outboxes, notifications = owner.IncidentAlert.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx), owner.Notification.Query().CountX(ctx)
		audits := owner.AuditLog.Query().CountX(ctx)
		failOutbox := true
		runtime.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil && failOutbox {
					failOutbox = false
					return nil, injected
				}
				return value, err
			})
		})
		require.ErrorIs(t, svc.RuleEngine().ExecuteRule(ctx, rule, current, tenant.ID), injected)
		require.False(t, failOutbox)
		require.Equal(t, count, owner.IncidentAlert.Query().CountX(ctx))
		require.Equal(t, outboxes, owner.OutboxEvent.Query().CountX(ctx))
		require.Equal(t, notifications, owner.Notification.Query().CountX(ctx))
		require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))

	})

	t.Run("Incident event metric and major escalation reject history", func(t *testing.T) {
		svc := service.NewIncidentService(runtime, zap.NewNop().Sugar(), policy)
		fresh, err := app.Create(ctx, identity, command("incident-supplemental", "incident"))
		require.NoError(t, err)
		for _, kind := range []string{"event", "metric", "major"} {
			for _, id := range []int{historical.ProfessionalReference.ID, fresh.ProfessionalReference.ID} {
				extension := owner.Incident.GetX(ctx, id)
				before, _ := json.Marshal(owner.Ticket.GetX(ctx, extension.WorkItemID))
				beforeExtension, _ := json.Marshal(extension)
				events, metrics := owner.IncidentEvent.Query().CountX(ctx), owner.IncidentMetric.Query().CountX(ctx)
				switch kind {
				case "event":
					_, err = svc.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{IncidentID: id, EventType: "note", EventName: "scope note", Status: "active", Severity: "info", Source: "test"}, tenant.ID)
				case "metric":
					_, err = svc.CreateIncidentMetric(ctx, &dto.CreateIncidentMetricRequest{IncidentID: id, MetricType: "scope", MetricName: "scope metric", MetricValue: 1}, tenant.ID)
				case "major":
					err = svc.EscalateToMajorIncident(ctx, id, actor.ID, tenant.ID, &dto.EscalateMajorIncidentRequest{ImpactScope: "high", BusinessImpact: "candidate test"})
				}
				if id == historical.ProfessionalReference.ID {
					require.ErrorContains(t, err, "execution scope denied")
					after, _ := json.Marshal(owner.Ticket.GetX(ctx, extension.WorkItemID))
					afterExtension, _ := json.Marshal(owner.Incident.GetX(ctx, id))
					require.JSONEq(t, string(before), string(after))
					require.JSONEq(t, string(beforeExtension), string(afterExtension))
					require.Equal(t, events, owner.IncidentEvent.Query().CountX(ctx))
					require.Equal(t, metrics, owner.IncidentMetric.Query().CountX(ctx))
				} else {
					require.NoError(t, err)
					if kind == "metric" {
						require.Equal(t, metrics+1, owner.IncidentMetric.Query().CountX(ctx))
					} else {
						require.Equal(t, events+1, owner.IncidentEvent.Query().CountX(ctx))
					}
				}
			}
		}
		require.True(t, owner.Incident.GetX(ctx, fresh.ProfessionalReference.ID).IsMajorIncident)
		for _, kind := range []string{"event", "metric"} {
			tx, err := runtime.Tx(ctx)
			require.NoError(t, err)
			func() {
				defer tx.Rollback()
				events, metrics := owner.IncidentEvent.Query().CountX(ctx), owner.IncidentMetric.Query().CountX(ctx)
				if kind == "event" {
					_, err = svc.CreateIncidentEventTx(ctx, tx, &dto.CreateIncidentEventRequest{IncidentID: fresh.ProfessionalReference.ID, EventType: "note", EventName: "rollback", Status: "active", Severity: "info", Source: "test"}, tenant.ID)
					require.NoError(t, err)
					require.Equal(t, events+1, tx.IncidentEvent.Query().CountX(ctx))
				} else {
					_, err = svc.CreateIncidentMetricTx(ctx, tx, &dto.CreateIncidentMetricRequest{IncidentID: fresh.ProfessionalReference.ID, MetricType: "scope", MetricName: "rollback", MetricValue: 1}, tenant.ID)
					require.NoError(t, err)
					require.Equal(t, metrics+1, tx.IncidentMetric.Query().CountX(ctx))
				}
				require.Equal(t, events, owner.IncidentEvent.Query().CountX(ctx))
				require.Equal(t, metrics, owner.IncidentMetric.Query().CountX(ctx))
				require.NoError(t, tx.Rollback())
				require.Equal(t, events, owner.IncidentEvent.Query().CountX(ctx))
				require.Equal(t, metrics, owner.IncidentMetric.Query().CountX(ctx))
			}()
		}
		faultTarget, err := app.Create(ctx, identity, command("major-rollback", "incident"))
		require.NoError(t, err)
		before, _ := json.Marshal(owner.Ticket.GetX(ctx, faultTarget.WorkItemID))
		beforeExtension, _ := json.Marshal(owner.Incident.GetX(ctx, faultTarget.ProfessionalReference.ID))
		events := owner.IncidentEvent.Query().CountX(ctx)
		injected := errors.New("injected major escalation timeline failure")
		failEvent := true
		runtime.IncidentEvent.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil && failEvent {
					failEvent = false
					return nil, injected
				}
				return value, err
			})
		})
		err = svc.EscalateToMajorIncident(ctx, faultTarget.ProfessionalReference.ID, actor.ID, tenant.ID, &dto.EscalateMajorIncidentRequest{ImpactScope: "high", BusinessImpact: "rollback"})
		require.ErrorIs(t, err, injected)
		require.False(t, failEvent)
		after, _ := json.Marshal(owner.Ticket.GetX(ctx, faultTarget.WorkItemID))
		afterExtension, _ := json.Marshal(owner.Incident.GetX(ctx, faultTarget.ProfessionalReference.ID))
		require.JSONEq(t, string(before), string(after))
		require.JSONEq(t, string(beforeExtension), string(afterExtension))
		require.Equal(t, events, owner.IncidentEvent.Query().CountX(ctx))

		engine := service.NewIncidentRuleEngine(runtime, zap.NewNop().Sugar(), policy)
		rule := owner.IncidentRule.Create().SetName("metric transaction").SetRuleType("monitoring").SetTenantID(tenant.ID).SetIsActive(true).
			SetConditions(map[string]interface{}{}).SetActions([]map[string]interface{}{{"type": "collect_metric", "metric_type": "scope", "metric_name": "rule metric", "metric_value": float64(1)}}).SaveX(ctx)
		current := owner.Incident.Query().Where(incident.IDEQ(fresh.ProfessionalReference.ID)).WithWorkItem().OnlyX(ctx)
		metrics := owner.IncidentMetric.Query().CountX(ctx)
		require.NoError(t, engine.ExecuteRule(ctx, rule, current, tenant.ID))
		require.Equal(t, metrics+1, owner.IncidentMetric.Query().CountX(ctx))
		failMetric := true
		runtime.IncidentMetric.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil && failMetric {
					failMetric = false
					return nil, injected
				}
				return value, err
			})
		})
		require.ErrorIs(t, engine.ExecuteRule(ctx, rule, current, tenant.ID), injected)
		require.False(t, failMetric)
		require.Equal(t, metrics+1, owner.IncidentMetric.Query().CountX(ctx), "metric action must roll back its caller transaction")

	})

	t.Run("rule execution bookkeeping preserves historical incidents", func(t *testing.T) {
		engine := service.NewIncidentRuleEngine(runtime, zap.NewNop().Sugar(), policy)
		fresh, err := app.Create(ctx, identity, command("rule-bookkeeping", "incident"))
		require.NoError(t, err)
		rule := owner.IncidentRule.Create().SetName("candidate rule").SetRuleType("assignment").
			SetConditions(map[string]interface{}{}).
			SetActions([]map[string]interface{}{{"type": "unsupported-candidate-test"}}).
			SetIsActive(true).SetTenantID(tenant.ID).SaveX(ctx)
		beforeRule, _ := json.Marshal(rule)
		beforeExecutions := owner.IncidentRuleExecution.Query().CountX(ctx)
		history := owner.Incident.Query().Where(incident.IDEQ(historical.ProfessionalReference.ID)).WithWorkItem().OnlyX(ctx)
		err = engine.ExecuteRule(ctx, rule, history, tenant.ID)
		require.ErrorContains(t, err, "execution scope denied")
		require.Equal(t, beforeExecutions, owner.IncidentRuleExecution.Query().CountX(ctx))
		afterRule, _ := json.Marshal(owner.IncidentRule.GetX(ctx, rule.ID))
		require.JSONEq(t, string(beforeRule), string(afterRule))
		current := owner.Incident.Query().Where(incident.IDEQ(fresh.ProfessionalReference.ID)).WithWorkItem().OnlyX(ctx)
		err = engine.ExecuteRule(ctx, rule, current, tenant.ID)
		require.Error(t, err, "unknown dispatch must fail closed")
		require.Equal(t, beforeExecutions+1, owner.IncidentRuleExecution.Query().CountX(ctx))
		// The failed record is persisted for the new member only.
		executions := owner.IncidentRuleExecution.Query().AllX(ctx)
		for _, execution := range executions {
			if execution.RuleID == rule.ID {
				require.Equal(t, fresh.ProfessionalReference.ID, execution.IncidentID)
				require.Equal(t, "failed", execution.Status)
			}
		}
		// A recognized action failure persists its result and statistics atomically.
		rule = owner.IncidentRule.UpdateOneID(rule.ID).SetActions([]map[string]interface{}{{"type": "assign", "assignee_id": actor.ID}}).SaveX(ctx)
		err = engine.ExecuteRule(ctx, rule, current, tenant.ID)
		require.ErrorContains(t, err, "rule action failed") // no trusted action actor
		require.Equal(t, 1, owner.IncidentRule.GetX(ctx, rule.ID).ExecutionCount)
		injected := errors.New("injected rule statistics failure")
		failStatistics := true
		runtime.IncidentRule.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil && failStatistics {
					failStatistics = false
					return nil, injected
				}
				return value, err
			})
		})
		err = engine.ExecuteRule(ctx, rule, current, tenant.ID)
		require.ErrorIs(t, err, injected)
		require.False(t, failStatistics)
		require.Equal(t, 1, owner.IncidentRule.GetX(ctx, rule.ID).ExecutionCount)
		latest := owner.IncidentRuleExecution.Query().Order(ent.Desc("id")).FirstX(ctx)
		require.Equal(t, "running", latest.Status, "completion must roll back with failed statistics")

		// Skipped conditions persist a result, but do not count an action execution.
		rule = owner.IncidentRule.UpdateOneID(rule.ID).SetConditions(map[string]interface{}{"priority": []string{"no-match"}}).SaveX(ctx)
		require.NoError(t, engine.ExecuteRule(ctx, rule, current, tenant.ID))
		latest = owner.IncidentRuleExecution.Query().Order(ent.Desc("id")).FirstX(ctx)
		require.Equal(t, "skipped", latest.Status)
		require.Equal(t, 1, owner.IncidentRule.GetX(ctx, rule.ID).ExecutionCount)
		failResult := true
		runtime.IncidentRuleExecution.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil && failResult && mutation.Op().Is(ent.OpUpdateOne) {
					failResult = false
					return nil, injected
				}
				return value, err
			})
		})
		require.ErrorIs(t, engine.ExecuteRule(ctx, rule, current, tenant.ID), injected)
		require.False(t, failResult)
		latest = owner.IncidentRuleExecution.Query().Order(ent.Desc("id")).FirstX(ctx)
		require.Equal(t, "running", latest.Status)
		// A real escalation action commits before the completed result and count.
		rule = owner.IncidentRule.UpdateOneID(rule.ID).SetConditions(map[string]interface{}{}).
			SetActions([]map[string]interface{}{{"type": "escalate", "level": 1, "reason": "candidate rule"}}).SaveX(ctx)
		require.NoError(t, engine.ExecuteRule(ctx, rule, current, tenant.ID))
		latest = owner.IncidentRuleExecution.Query().Order(ent.Desc("id")).FirstX(ctx)
		require.Equal(t, "completed", latest.Status)
		require.Equal(t, 1, owner.Incident.GetX(ctx, current.ID).EscalationLevel)
		require.Equal(t, 2, owner.IncidentRule.GetX(ctx, rule.ID).ExecutionCount)

		// Closing the private fixture scope after start must prevent result writes.
		closeScope := true
		runtime.IncidentRuleExecution.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, mutation)
				if err == nil && closeScope && mutation.Op().Is(ent.OpCreate) {
					closeScope = false
					_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
				}
				return value, err
			})
		})
		defer func() {
			_, err := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
			require.NoError(t, err)
		}()
		rule = owner.IncidentRule.UpdateOneID(rule.ID).SetActions([]map[string]interface{}{{"type": "unsupported-candidate-test"}}).SaveX(ctx)
		err = engine.ExecuteRule(ctx, rule, current, tenant.ID)
		require.ErrorContains(t, err, "execution scope denied")
		latest = owner.IncidentRuleExecution.Query().Order(ent.Desc("id")).FirstX(ctx)
		require.Equal(t, "running", latest.Status)
		require.Equal(t, 2, owner.IncidentRule.GetX(ctx, rule.ID).ExecutionCount)

	})

	t.Run("Incident metadata and direct escalation require original transaction membership", func(t *testing.T) {
		svc := service.NewIncidentService(runtime, zap.NewNop().Sugar(), policy)
		fresh, err := app.Create(ctx, identity, command("metadata-incident", "incident"))
		require.NoError(t, err)
		for _, kind := range []string{"metadata", "escalation"} {
			for _, target := range []struct{ incidentID, workItemID int }{{historical.ProfessionalReference.ID, historical.WorkItemID}, {fresh.ProfessionalReference.ID, fresh.WorkItemID}} {
				before := owner.Ticket.GetX(ctx, target.workItemID)
				extension := owner.Incident.GetX(ctx, target.incidentID)
				events := owner.IncidentEvent.Query().CountX(ctx)
				tx, err := runtime.Tx(ctx)
				require.NoError(t, err)
				func() {
					defer tx.Rollback()
					if kind == "metadata" {
						title := "candidate metadata"
						_, err = svc.UpdateIncidentTx(ctx, tx, target.incidentID, &dto.UpdateIncidentRequest{Version: before.Version, Title: &title}, tenant.ID)
					} else {
						_, err = svc.EscalateIncidentTx(ctx, tx, &dto.IncidentEscalationRequest{IncidentID: target.incidentID, EscalationLevel: 1, Reason: "candidate scope test"}, tenant.ID)
					}
					if target.workItemID == historical.WorkItemID {
						require.ErrorContains(t, err, "execution scope denied")
					} else {
						require.NoError(t, err)
						require.Equal(t, before.Version+1, tx.Ticket.GetX(ctx, target.workItemID).Version)
						require.Equal(t, events+1, tx.IncidentEvent.Query().CountX(ctx))
					}
					require.Equal(t, before.Version, owner.Ticket.GetX(ctx, target.workItemID).Version)
				}()
				afterJSON, _ := json.Marshal(owner.Ticket.GetX(ctx, target.workItemID))
				beforeJSON, _ := json.Marshal(before)
				require.JSONEq(t, string(beforeJSON), string(afterJSON))
				extensionAfter, _ := json.Marshal(owner.Incident.GetX(ctx, target.incidentID))
				extensionBefore, _ := json.Marshal(extension)
				require.JSONEq(t, string(extensionBefore), string(extensionAfter))
				require.Equal(t, events, owner.IncidentEvent.Query().CountX(ctx))
			}
		}
		title := "committed candidate metadata"
		_, err = svc.UpdateIncident(ctx, fresh.ProfessionalReference.ID, &dto.UpdateIncidentRequest{Version: 1, Title: &title}, tenant.ID)
		require.NoError(t, err)
		_, err = svc.EscalateIncident(ctx, &dto.IncidentEscalationRequest{IncidentID: fresh.ProfessionalReference.ID, EscalationLevel: 1, Reason: "candidate commit"}, tenant.ID)
		require.NoError(t, err)
		require.Equal(t, title, owner.Ticket.GetX(ctx, fresh.WorkItemID).Title)
		require.Equal(t, 3, owner.Ticket.GetX(ctx, fresh.WorkItemID).Version)
		require.Equal(t, 1, owner.Incident.GetX(ctx, fresh.ProfessionalReference.ID).EscalationLevel)
	})

	t.Run("Incident commands and rule core preserve history", func(t *testing.T) {
		svc := service.NewIncidentService(runtime, zap.NewNop().Sugar(), policy)
		svc.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
		fresh, err := app.Create(ctx, identity, command("command-incident", "incident"))
		require.NoError(t, err)
		makeCommand := func(id, version int, key string) dto.IncidentCommand {
			return dto.IncidentCommand{IncidentID: id, Action: "acknowledge", Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: version, Source: "http", OperationID: key}}
		}
		beforeEvents, beforeAudits := owner.OutboxEvent.Query().CountX(ctx), owner.AuditLog.Query().CountX(ctx)
		historicalWrite := makeCommand(historical.ProfessionalReference.ID, oldRow.Version, "history-command")
		historicalWrite.Action = "start"
		_, err = svc.ApplyIncidentCommand(ctx, historicalWrite)
		require.ErrorContains(t, err, "execution scope denied")
		require.Equal(t, oldRow.Version, owner.Ticket.GetX(ctx, oldRow.ID).Version)
		require.Equal(t, beforeEvents, owner.OutboxEvent.Query().CountX(ctx))
		require.Equal(t, beforeAudits, owner.AuditLog.Query().CountX(ctx))
		legacyReplay, err := svc.ApplyIncidentCommand(ctx, legacyCommand)
		require.NoError(t, err)
		require.Equal(t, legacyResult.Version, legacyReplay.Version)
		require.Equal(t, beforeEvents, owner.OutboxEvent.Query().CountX(ctx))
		require.Equal(t, beforeAudits, owner.AuditLog.Query().CountX(ctx))
		current := owner.Ticket.GetX(ctx, fresh.WorkItemID)
		cmd := makeCommand(fresh.ProfessionalReference.ID, current.Version, "new-command")
		result, err := svc.ApplyIncidentCommand(ctx, cmd)
		require.NoError(t, err)
		require.Equal(t, current.Version+1, result.Version)
		afterEvents := owner.OutboxEvent.Query().CountX(ctx)
		replay, err := svc.ApplyIncidentCommand(ctx, cmd)
		require.NoError(t, err)
		require.Equal(t, result.Version, replay.Version)
		require.Equal(t, afterEvents, owner.OutboxEvent.Query().CountX(ctx))
		beforeRule := owner.Ticket.GetX(ctx, fresh.WorkItemID)
		beforeRuleEvents, beforeRuleAudits := owner.OutboxEvent.Query().CountX(ctx), owner.AuditLog.Query().CountX(ctx)
		for _, actionKind := range []string{"status", "assignment"} {
			for _, id := range []int{historical.ProfessionalReference.ID, fresh.ProfessionalReference.ID} {
				tx, err := runtime.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				require.NoError(t, err)
				func() {
					defer tx.Rollback()
					incident := tx.Incident.Query().Where(incident.IDEQ(id)).WithWorkItem().OnlyX(ctx)
					var action interface {
						SetExecutionPolicy(*database.ExecutionPolicy)
						SetDirectorySnapshot(database.DirectorySnapshot)
						ExecuteTx(context.Context, *ent.Tx, *ent.Incident, int) error
					}
					if actionKind == "status" {
						action = &service.StatusChangeAction{Status: "in_progress"}
					} else {
						action = &service.AssignmentAction{AssigneeID: actor.ID, Reason: "candidate assignment"}
					}
					action.SetExecutionPolicy(policy)
					action.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
					err = action.ExecuteTx(service.WithIncidentAlertActor(ctx, actor.ID, "incident_rule", fmt.Sprintf("scope-rule-%s-%d", actionKind, id)), tx, incident, tenant.ID)
					if id == historical.ProfessionalReference.ID {
						require.ErrorContains(t, err, "execution scope denied")
					} else {
						require.NoError(t, err)
						if actionKind == "status" {
							require.Equal(t, "in_progress", tx.Ticket.GetX(ctx, fresh.WorkItemID).Status)
						} else {
							require.Equal(t, actor.ID, tx.Ticket.GetX(ctx, fresh.WorkItemID).AssigneeID)
						}
						require.Equal(t, beforeRule.Status, owner.Ticket.GetX(ctx, fresh.WorkItemID).Status, "rule action must not commit caller transaction")
					}
					require.NoError(t, tx.Rollback())
				}()
			}
		}

		require.Equal(t, beforeRule.Version, owner.Ticket.GetX(ctx, fresh.WorkItemID).Version)
		require.Equal(t, beforeRuleEvents, owner.OutboxEvent.Query().CountX(ctx))
		require.Equal(t, beforeRuleAudits, owner.AuditLog.Query().CountX(ctx))
		beforeTimeline := owner.IncidentEvent.Query().CountX(ctx)
		injected := errors.New("injected command outbox failure")
		failNext := true
		runtime.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, m)
				if err == nil && failNext {
					failNext = false
					return nil, injected
				}
				return value, err
			})
		})
		failing := makeCommand(fresh.ProfessionalReference.ID, beforeRule.Version, "rollback-command")
		failing.Action = "start"
		_, err = svc.ApplyIncidentCommand(ctx, failing)
		require.ErrorIs(t, err, injected)
		require.False(t, failNext)
		beforeJSON, err := json.Marshal(beforeRule)
		require.NoError(t, err)
		afterJSON, err := json.Marshal(owner.Ticket.GetX(ctx, fresh.WorkItemID))
		require.NoError(t, err)
		require.JSONEq(t, string(beforeJSON), string(afterJSON))
		require.Equal(t, beforeRuleEvents, owner.OutboxEvent.Query().CountX(ctx))
		require.Equal(t, beforeRuleAudits, owner.AuditLog.Query().CountX(ctx))
		require.Equal(t, beforeTimeline, owner.IncidentEvent.Query().CountX(ctx))
	})
	t.Run("historical parent and relation reject without writes", func(t *testing.T) {
		tickets, receipts, members := owner.Ticket.Query().CountX(ctx), owner.IntakeRequest.Query().CountX(ctx), memberCount()
		relationsBefore := owner.WorkItemRelation.Query().CountX(ctx)
		parent := command("old-parent", "generic")
		parent.ParentTicketID = &historical.WorkItemID
		t.Run("parent", func(t *testing.T) {
			_, err := app.Create(ctx, identity, parent)
			require.ErrorIs(t, err, creation.ErrPermissionDenied)
		})
		related := command("old-source", "problem")
		related.SourceRelations = []creation.SourceRelationInput{{SourceWorkItemID: historical.WorkItemID, ExpectedVersion: oldRow.Version, RelationType: "investigated_by"}}
		t.Run("relation", func(t *testing.T) {
			_, err := app.Create(ctx, identity, related)
			require.ErrorIs(t, err, creation.ErrPermissionDenied)
		})
		require.Equal(t, tickets, owner.Ticket.Query().CountX(ctx))
		require.Equal(t, receipts, owner.IntakeRequest.Query().CountX(ctx))
		require.Equal(t, members, memberCount())
		require.Equal(t, oldRow.Version, owner.Ticket.GetX(ctx, historical.WorkItemID).Version)
		require.Equal(t, relationsBefore, owner.WorkItemRelation.Query().CountX(ctx))
	})
	t.Run("historical completed receipt remains read only", func(t *testing.T) {
		replay, err := app.Create(ctx, identity, oldCommand)
		require.NoError(t, err)
		require.True(t, replay.Replayed)
		require.Equal(t, historical.WorkItemID, replay.WorkItemID)
		after := owner.IntakeRequest.GetX(ctx, oldReceipt.ID)
		beforeJSON, err := json.Marshal(oldReceipt)
		require.NoError(t, err)
		afterJSON, err := json.Marshal(after)
		require.NoError(t, err)
		require.JSONEq(t, string(beforeJSON), string(afterJSON))
		require.Equal(t, oldReceipt.Status, after.Status)
		var n int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_scope_members WHERE work_item_id=$1`, historical.WorkItemID).Scan(&n))
		require.Zero(t, n)
	})
	t.Run("outbox foreign key failure is not a duplicate", func(t *testing.T) {
		repo := service.NewOutboxEventRepository(runtime, policy)
		existing := owner.OutboxEvent.Query().FirstX(ctx)
		input := service.NewOutboxEvent{EventID: "invalid-ref", EventType: "test", TenantID: tenant.ID, AggregateType: "work_item", AggregateID: "invalid", ExecutionWorkItemID: 999999, Payload: json.RawMessage(`{}`)}
		_, err := repo.Enqueue(ctx, nil, input)
		require.Error(t, err)
		require.NotErrorIs(t, err, service.ErrDuplicateOutboxEvent)
		input.EventID = existing.EventID
		input.ExecutionWorkItemID = 0
		_, err = repo.Enqueue(ctx, nil, input)
		require.ErrorIs(t, err, service.ErrDuplicateOutboxEvent)
		require.Nil(t, owner.OutboxEvent.GetX(ctx, existing.ID).ExecutionWorkItemID, "historical event reference must remain NULL")
	})
	t.Run("unadmitted tenant and missing policy reject", func(t *testing.T) {
		otherPolicy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID + 1, ScopeID: uuid.NewString()}}})
		require.NoError(t, err)
		before := owner.IntakeRequest.Query().CountX(ctx)
		_, err = application(runtime, otherPolicy, clients.IntakeDirectorySnapshot()).Create(ctx, identity, command("unadmitted", "generic"))
		require.Error(t, err)
		_, err = application(runtime, nil, clients.IntakeDirectorySnapshot()).Create(ctx, identity, command("missing", "generic"))
		require.Error(t, err)
		require.Equal(t, before, owner.IntakeRequest.Query().CountX(ctx))
	})
	t.Run("extension failure rolls back base receipt and membership", func(t *testing.T) {
		tickets, receipts, members := owner.Ticket.Query().CountX(ctx), owner.IntakeRequest.Query().CountX(ctx), memberCount()
		injected := errors.New("injected extension persistence failure")
		activeExtensionFault := true
		defer func() { activeExtensionFault = false }()
		runtime.Incident.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(ctx, m)
				if err != nil || !activeExtensionFault {
					return value, err
				}
				return nil, injected
			})
		})
		_, err := app.Create(ctx, identity, command("fail-extension", "incident"))
		require.ErrorIs(t, err, injected)
		require.Equal(t, tickets, owner.Ticket.Query().CountX(ctx))
		require.Equal(t, receipts, owner.IntakeRequest.Query().CountX(ctx))
		require.Equal(t, members, memberCount())
		require.False(t, owner.Ticket.Query().Where(ticket.TitleEQ("scope fail-extension")).ExistX(ctx))
	})
	t.Run("worker manifest validates before SQL membership filtering", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("predicate-member", "generic"))
		require.NoError(t, err)
		event := owner.OutboxEvent.Create().SetEventID("predicate-member-event").SetEventType("predicate-only").SetTenantID(tenant.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(fresh.WorkItemID)).SetExecutionWorkItemID(fresh.WorkItemID).SetPayload(json.RawMessage(`{}`)).SaveX(ctx)
		workerCtx := tenantctx.SystemContext(ctx, "outbox:poll", "candidate test")
		selectMembers := func() ([]int, error) {
			tx, e := clients.System.Tx(workerCtx)
			if e != nil {
				return nil, e
			}
			defer tx.Rollback()
			predicate, e := policy.WorkerPredicate(workerCtx, tx, outboxevent.FieldTenantID, outboxevent.FieldExecutionWorkItemID)
			if e != nil {
				return nil, e
			}
			ids, e := tx.OutboxEvent.Query().Where(predicate).IDs(workerCtx)
			if e != nil {
				return nil, e
			}
			updated, e := tx.OutboxEvent.Update().Where(predicate).SetLastError("predicate transaction probe").Save(workerCtx)
			if e != nil {
				return nil, e
			}
			require.Equal(t, len(ids), updated)
			for _, old := range historicalOutbox {
				require.NotEqual(t, "predicate transaction probe", tx.OutboxEvent.GetX(workerCtx, old.ID).LastError)
			}
			return ids, nil
		}
		ids, err := selectMembers()
		require.NoError(t, err)
		require.Contains(t, ids, event.ID)
		for _, old := range historicalOutbox {
			require.NotContains(t, ids, old.ID)
		}
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
		require.NoError(t, err)
		_, scopeErr := selectMembers()
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
		require.NoError(t, err)
		require.ErrorIs(t, scopeErr, executionscope.ErrDenied)
		_, err = ownerDB.ExecContext(ctx, "DELETE FROM execution_runtime_bindings WHERE runtime_role=$1", systemRole)
		require.NoError(t, err)
		_, bindingErr := selectMembers()
		_, err = ownerDB.ExecContext(ctx, `INSERT INTO execution_runtime_bindings VALUES($1,'intake-test','candidate')`, systemRole)
		require.NoError(t, err)
		require.ErrorIs(t, bindingErr, executionscope.ErrDenied)
		_, err = ownerDB.ExecContext(ctx, "REVOKE SELECT ON execution_scopes FROM "+systemRole)
		require.NoError(t, err)
		_, permissionErr := selectMembers()
		_, err = ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_scopes TO "+systemRole)
		require.NoError(t, err)
		require.Error(t, permissionErr)
		require.NotErrorIs(t, permissionErr, executionscope.ErrDenied)

	})
	t.Run("real outbox worker preserves historical states", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("worker-member", "generic"))
		require.NoError(t, err)
		current := owner.OutboxEvent.Create().SetEventID("candidate-queue-event").SetEventType("candidate-test-delivery").SetTenantID(tenant.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(fresh.WorkItemID)).SetExecutionWorkItemID(fresh.WorkItemID).SetPayload(json.RawMessage(`{}`)).SetNextAttemptAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		foreign := owner.Tenant.Create().SetName("Unadmitted queue tenant").SetCode("unadmitted-queue").SaveX(ctx)
		foreignEvent := owner.OutboxEvent.Create().SetEventID("foreign-queue-event").SetEventType("candidate-test-delivery").SetTenantID(foreign.ID).SetAggregateType("work_item").SetAggregateID("unresolved").SetPayload(json.RawMessage(`{}`)).SetNextAttemptAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		historicalOutbox = append(historicalOutbox, foreignEvent)
		foreignActor := owner.User.Create().SetTenantID(foreign.ID).SetUsername("foreign-worker").SetName("Foreign worker").SetEmail("foreign@example.invalid").SetPasswordHash("test-only").SetRole("requester").SaveX(ctx)
		foreignScope := uuid.NewString()
		_, err = ownerDB.ExecContext(ctx, `INSERT INTO execution_scopes(id,deployment_id,tenant_id,status,created_by) VALUES($1,'intake-test',$2,'active',$3)`, foreignScope, foreign.ID, foreignActor.ID)
		require.NoError(t, err)
		foreignPolicy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: foreign.ID, ScopeID: foreignScope}}})
		require.NoError(t, err)
		foreignCtx := tenantctx.WithTenantID(ctx, foreign.ID)
		foreignTx, err := runtime.Tx(foreignCtx)
		require.NoError(t, err)
		defer foreignTx.Rollback()
		err = foreignPolicy.BindEnt(foreignCtx, foreignTx, foreign.ID)
		require.NoError(t, err)
		foreignItem, err := foreignTx.Ticket.Create().SetTenantID(foreign.ID).SetRequesterID(foreignActor.ID).SetTicketNumber("FOREIGN-WORKER-1").SetTitle("Foreign member").Save(foreignCtx)
		require.NoError(t, err)
		require.NoError(t, foreignTx.Commit())
		var foreignMembers int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_scope_members WHERE scope_id=$1 AND work_item_id=$2`, foreignScope, foreignItem.ID).Scan(&foreignMembers))
		require.Equal(t, 1, foreignMembers)
		foreignMemberEvent := owner.OutboxEvent.Create().SetEventID("foreign-member-event").SetEventType("candidate-test-delivery").SetTenantID(foreign.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(foreignItem.ID)).SetExecutionWorkItemID(foreignItem.ID).SetPayload(json.RawMessage(`{}`)).SetNextAttemptAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		historicalOutbox = append(historicalOutbox, foreignMemberEvent)
		auditsBefore, err := json.Marshal(owner.AuditLog.Query().Order(ent.Asc("id")).AllX(ctx))
		require.NoError(t, err)

		before := make(map[int][]byte)
		for _, row := range historicalOutbox {
			before[row.ID], err = json.Marshal(owner.OutboxEvent.GetX(ctx, row.ID))
			require.NoError(t, err)
		}
		// Reserve unrelated fixture event types; the test receiver makes no external calls.
		reserved := []string{}
		seen := map[string]bool{}
		for _, row := range owner.OutboxEvent.Query().AllX(ctx) {
			if row.EventType != "candidate-test-delivery" && row.EventType != "candidate-test-unregistered" && !seen[row.EventType] {
				reserved = append(reserved, row.EventType)
				seen[row.EventType] = true
			}
		}
		receiver := &candidateOutboxTestReceiver{}
		registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{receiver}, reserved...)
		require.NoError(t, err)
		worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(clients.System, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 1000, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
		require.NoError(t, err)
		require.NoError(t, worker.DispatchOnce(ctx))
		for _, row := range historicalOutbox {
			after, err := json.Marshal(owner.OutboxEvent.GetX(ctx, row.ID))
			require.NoError(t, err)
			t.Logf("queue row %s: status=%s attempts=%d", row.EventID, owner.OutboxEvent.GetX(ctx, row.ID).Status, owner.OutboxEvent.GetX(ctx, row.ID).AttemptCount)
			assert.JSONEq(t, string(before[row.ID]), string(after), "historical row %s changed", row.EventID)
		}
		auditsAfter, err := json.Marshal(owner.AuditLog.Query().Order(ent.Asc("id")).AllX(ctx))
		require.NoError(t, err)
		require.JSONEq(t, string(auditsBefore), string(auditsAfter), "filtered historical events must not create audits")
		require.Equal(t, []int{current.ID}, receiver.delivered)
		require.Equal(t, "published", owner.OutboxEvent.GetX(ctx, current.ID).Status)
	})

	t.Run("worker transitions revalidate scope and binding after claim", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("worker-transitions", "generic"))
		require.NoError(t, err)
		workerCtx := tenantctx.SystemContext(ctx, "outbox:poll", "candidate transition test")
		repo := service.NewOutboxEventRepository(clients.System, policy)
		actions := []struct {
			name, status string
			run          func(*ent.OutboxEvent) error
		}{
			{"attempt", "publishing", func(e *ent.OutboxEvent) error {
				return repo.MarkDeliveryAttemptStarted(workerCtx, e.ID, e.ClaimToken, "local-test")
			}},
			{"retry", "pending", func(e *ent.OutboxEvent) error {
				return repo.MarkRetry(workerCtx, e.ID, e.ClaimToken, "local-test", time.Now())
			}},
			{"retry-audit", "pending", func(e *ent.OutboxEvent) error {
				return repo.MarkRetryWithAudit(workerCtx, e.ID, e.ClaimToken, "local-test", time.Now(), service.OutboxRetryAudit{TenantID: tenant.ID, RequestID: e.EventID, Resource: "outbox_event", Action: "test.retry", Path: "outbox/events", Method: "WORKER", StatusCode: 503})
			}},
			{"published", "published", func(e *ent.OutboxEvent) error { return repo.MarkPublished(workerCtx, e.ID, e.ClaimToken, time.Now()) }},
			{"unknown", "blocked", func(e *ent.OutboxEvent) error {
				return repo.MarkDeliveryUnknown(workerCtx, e, e.ClaimToken, "local-test")
			}},
			{"blocked", "blocked", func(e *ent.OutboxEvent) error { return repo.MarkBlocked(workerCtx, e.ID, e.ClaimToken, "local-test") }},
			{"dead-letter", "dead_letter", func(e *ent.OutboxEvent) error {
				return repo.MarkDeadLetter(workerCtx, e.ID, e.ClaimToken, "local-test")
			}},
		}
		for _, action := range actions {
			t.Run(action.name, func(t *testing.T) {
				eventType := "transition-" + action.name
				row := owner.OutboxEvent.Create().SetEventID(eventType).SetEventType(eventType).SetTenantID(tenant.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(fresh.WorkItemID)).SetExecutionWorkItemID(fresh.WorkItemID).SetPayload(json.RawMessage(`{}`)).SetNextAttemptAt(time.Now().Add(-time.Minute)).SaveX(ctx)
				claimed, err := repo.ClaimDueByEventType(workerCtx, time.Now(), 10, eventType, false)
				require.NoError(t, err)
				require.Len(t, claimed, 1)
				require.Equal(t, row.ID, claimed[0].ID)
				before, err := json.Marshal(owner.OutboxEvent.GetX(ctx, row.ID))
				require.NoError(t, err)
				audits, err := json.Marshal(owner.AuditLog.Query().Order(ent.Asc("id")).AllX(ctx))
				require.NoError(t, err)
				for _, invalidation := range []string{"scope", "binding"} {
					if invalidation == "scope" {
						_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
					} else {
						_, err = ownerDB.ExecContext(ctx, "DELETE FROM execution_runtime_bindings WHERE runtime_role=$1", systemRole)
					}
					require.NoError(t, err)
					rejected := action.run(claimed[0])
					if invalidation == "scope" {
						_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
					} else {
						_, err = ownerDB.ExecContext(ctx, `INSERT INTO execution_runtime_bindings VALUES($1,'intake-test','candidate')`, systemRole)
					}
					require.NoError(t, err)
					require.ErrorIs(t, rejected, executionscope.ErrDenied, invalidation)
					after, err := json.Marshal(owner.OutboxEvent.GetX(ctx, row.ID))
					require.NoError(t, err)
					require.JSONEq(t, string(before), string(after))
					auditAfter, err := json.Marshal(owner.AuditLog.Query().Order(ent.Asc("id")).AllX(ctx))
					require.NoError(t, err)
					require.JSONEq(t, string(audits), string(auditAfter))
				}
				require.NoError(t, action.run(claimed[0]))
				require.Equal(t, action.status, owner.OutboxEvent.GetX(ctx, row.ID).Status)
			})
		}
	})
	t.Run("worker concurrent claims recover leases without redelivery", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("worker-concurrency", "generic"))
		require.NoError(t, err)
		workerCtx := tenantctx.SystemContext(ctx, "outbox:poll", "candidate concurrency test")
		repo := service.NewOutboxEventRepository(clients.System, policy)
		const kind = "candidate-concurrent"
		ids := map[int]bool{}
		for i := 0; i < 12; i++ {
			row := owner.OutboxEvent.Create().SetEventID(fmt.Sprintf("concurrent-%d", i)).SetEventType(kind).SetTenantID(tenant.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(fresh.WorkItemID)).SetExecutionWorkItemID(fresh.WorkItemID).SetPayload(json.RawMessage(`{}`)).SetNextAttemptAt(time.Now().Add(-time.Minute)).SaveX(ctx)
			ids[row.ID] = true
		}
		type result struct {
			rows []*ent.OutboxEvent
			err  error
		}
		results := make(chan result, 2)
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				rows, e := repo.ClaimDueByEventType(workerCtx, time.Now(), 100, kind, false)
				results <- result{rows, e}
			}()
		}
		close(start)
		claimed := map[int]*ent.OutboxEvent{}
		for i := 0; i < 2; i++ {
			r := <-results
			require.NoError(t, r.err)
			for _, row := range r.rows {
				require.True(t, ids[row.ID])
				require.NotContains(t, claimed, row.ID, "concurrent dispatchers returned same claim")
				claimed[row.ID] = row
			}
		}
		require.Len(t, claimed, len(ids))
		var expired, ambiguous *ent.OutboxEvent
		for _, row := range claimed {
			if expired == nil {
				expired = row
			} else {
				ambiguous = row
				break
			}
		}
		require.NoError(t, repo.MarkDeliveryAttemptStarted(workerCtx, ambiguous.ID, ambiguous.ClaimToken, "local-attempt"))
		owner.OutboxEvent.UpdateOneID(expired.ID).SetClaimExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		owner.OutboxEvent.UpdateOneID(ambiguous.ID).SetClaimExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		recovered, err := repo.ClaimDueByEventType(workerCtx, time.Now(), 100, kind, false)
		require.NoError(t, err)
		require.Len(t, recovered, 1)
		require.Equal(t, expired.ID, recovered[0].ID)
		require.NotEqual(t, expired.ClaimToken, recovered[0].ClaimToken)
		require.ErrorIs(t, repo.MarkPublished(workerCtx, expired.ID, expired.ClaimToken, time.Now()), service.ErrOutboxEventClaimLost)
		require.NoError(t, repo.MarkPublished(workerCtx, recovered[0].ID, recovered[0].ClaimToken, time.Now()))
		require.Equal(t, "blocked", owner.OutboxEvent.GetX(ctx, ambiguous.ID).Status)
		require.Equal(t, 1, owner.OutboxEvent.GetX(ctx, ambiguous.ID).AttemptCount)
		var audits int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM audit_logs WHERE request_id=$1 AND action='outbox.delivery_unknown'`, ambiguous.EventID).Scan(&audits))
		require.Equal(t, 1, audits)
	})
	t.Run("worker audit write faults roll back event transitions", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("worker-audit-fault", "generic"))
		require.NoError(t, err)
		workerCtx := tenantctx.SystemContext(ctx, "outbox:poll", "candidate audit rollback test")
		repo := service.NewOutboxEventRepository(clients.System, policy)
		fault := errors.New("injected after actual worker audit write")
		inject := false
		writes := 0
		clients.System.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				v, e := next.Mutate(ctx, m)
				if e == nil && inject {
					writes++
					return nil, fault
				}
				return v, e
			})
		})
		for _, branch := range []string{"retry", "unknown", "recovery", "unregistered"} {
			t.Run(branch, func(t *testing.T) {
				kind := "audit-fault-" + branch
				row := owner.OutboxEvent.Create().SetEventID(kind).SetEventType(kind).SetTenantID(tenant.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(fresh.WorkItemID)).SetExecutionWorkItemID(fresh.WorkItemID).SetPayload(json.RawMessage(`{}`)).SetNextAttemptAt(time.Now().Add(-time.Minute)).SaveX(ctx)
				if branch != "unregistered" {
					claimed, e := repo.ClaimDueByEventType(workerCtx, time.Now(), 10, kind, false)
					require.NoError(t, e)
					require.Len(t, claimed, 1)
					row = claimed[0]
				}
				if branch == "recovery" {
					require.NoError(t, repo.MarkDeliveryAttemptStarted(workerCtx, row.ID, row.ClaimToken, "local-test"))
					owner.OutboxEvent.UpdateOneID(row.ID).SetClaimExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
				}
				before, e := json.Marshal(owner.OutboxEvent.GetX(ctx, row.ID))
				require.NoError(t, e)
				audits, e := json.Marshal(owner.AuditLog.Query().Order(ent.Asc("id")).AllX(ctx))
				require.NoError(t, e)
				run := func() error {
					switch branch {
					case "retry":
						return repo.MarkRetryWithAudit(workerCtx, row.ID, row.ClaimToken, "test", time.Now(), service.OutboxRetryAudit{TenantID: tenant.ID, RequestID: row.EventID, Resource: "outbox_event", Action: "test.retry", Path: "outbox/events", Method: "WORKER", StatusCode: 503})
					case "unknown":
						return repo.MarkDeliveryUnknown(workerCtx, row, row.ClaimToken, "test")
					case "recovery":
						_, e := repo.ClaimDueByEventType(workerCtx, time.Now(), 10, kind, false)
						return e
					default:
						known := []string{}
						for _, ev := range owner.OutboxEvent.Query().AllX(ctx) {
							if ev.EventType != kind {
								known = append(known, ev.EventType)
							}
						}
						_, e := repo.BlockUnknownPendingEventTypes(workerCtx, time.Now(), 1000, known)
						return e
					}
				}
				writesBefore := writes
				inject = true
				rejected := run()
				inject = false
				require.ErrorIs(t, rejected, fault)
				require.Equal(t, writesBefore+1, writes)
				after, e := json.Marshal(owner.OutboxEvent.GetX(ctx, row.ID))
				require.NoError(t, e)
				require.JSONEq(t, string(before), string(after))
				auditAfter, e := json.Marshal(owner.AuditLog.Query().Order(ent.Asc("id")).AllX(ctx))
				require.NoError(t, e)
				require.JSONEq(t, string(audits), string(auditAfter))
				auditCount := owner.AuditLog.Query().CountX(ctx)
				require.NoError(t, run())
				expectedStatus := "blocked"
				if branch == "retry" {
					expectedStatus = "pending"
				}
				require.Equal(t, expectedStatus, owner.OutboxEvent.GetX(ctx, row.ID).Status)
				require.Equal(t, auditCount+1, owner.AuditLog.Query().CountX(ctx))
			})
		}
	})

	t.Run("real callback worker preserves historical states", func(t *testing.T) {
		before := map[int][]byte{}
		for _, row := range historicalCallbacks {
			before[row.ID], err = json.Marshal(owner.ProcessCallbackOutbox.GetX(ctx, row.ID))
			require.NoError(t, err)
		}
		engine := service.NewCustomProcessEngine(runtime, zap.NewNop().Sugar(), policy).(*service.CustomProcessEngine)
		engine.SetCallbackCandidateClient(clients.System)

		fresh, err := app.Create(ctx, identity, command("callback-member", "generic"))
		require.NoError(t, err)
		dep := owner.ProcessDeployment.Create().SetDeploymentID("candidate-callback").SetDeploymentName("Candidate callback").SetTenantID(tenant.ID).SaveX(ctx)
		xml := `<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="https://example.invalid"><bpmn:process id="candidate-callback" isExecutable="true"><bpmn:startEvent id="Start"/><bpmn:serviceTask id="Current"/><bpmn:endEvent id="End"/><bpmn:sequenceFlow id="S1" sourceRef="Start" targetRef="Current"/><bpmn:sequenceFlow id="S2" sourceRef="Current" targetRef="End"/></bpmn:process></bpmn:definitions>`
		def := owner.ProcessDefinition.Create().SetKey("candidate-callback").SetName("Candidate callback").SetVersion("1").SetIsLatest(true).SetBpmnXML([]byte(xml)).SetDeploymentID(dep.ID).SetTenantID(tenant.ID).SaveX(ctx)
		instance := owner.ProcessInstance.Create().SetProcessInstanceID("candidate-callback").SetProcessDefinitionKey(def.Key).SetProcessDefinitionID(def.ID).SetBusinessKey(fmt.Sprintf("generic:%d", fresh.WorkItemID)).SetBusinessType("generic").SetBusinessID(fresh.WorkItemID).SetExecutionWorkItemID(fresh.WorkItemID).SetStatus("running").SetCurrentActivityID("Current").SetTenantID(tenant.ID).SaveX(ctx)
		current := owner.ProcessCallbackOutbox.Create().SetExecutionKey("candidate-callback-key").SetTenantID(tenant.ID).SetProcessInstanceID(instance.ID).SetCallbackKind("service_task").SetHandlerID("candidate-local-callback").SetTaskType("candidate-local-task").SetElementID("Current").SetNextAttemptAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		handler := &candidateCallbackHandler{}
		engine.CallbackRegistry().RegisterHandler(handler)

		callbacksBefore, err := json.Marshal(owner.ProcessCallbackOutbox.Query().Order(ent.Asc("id")).AllX(ctx))
		require.NoError(t, err)
		for _, invalidation := range []string{"scope", "system-binding", "tenant-binding"} {
			revokedRole := systemRole
			if invalidation == "tenant-binding" {
				revokedRole = runtimeRole
			}
			if invalidation == "scope" {
				_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
			} else {
				_, err = ownerDB.ExecContext(ctx, "DELETE FROM execution_runtime_bindings WHERE runtime_role=$1", revokedRole)
			}
			require.NoError(t, err)
			n, rejected := engine.ProcessPendingCallbacks(context.Background(), "candidate-callback-denied", 1000)
			if invalidation == "scope" {
				_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
			} else {
				_, err = ownerDB.ExecContext(ctx, `INSERT INTO execution_runtime_bindings VALUES($1,'intake-test','candidate')`, revokedRole)
			}
			require.NoError(t, err)
			require.Error(t, rejected, invalidation)
			require.Zero(t, n)
			require.Zero(t, handler.calls)
			after, err := json.Marshal(owner.ProcessCallbackOutbox.Query().Order(ent.Asc("id")).AllX(ctx))
			require.NoError(t, err)
			require.JSONEq(t, string(callbacksBefore), string(after))
		}
		completed, scanErr := engine.ProcessPendingCallbacks(context.Background(), "candidate-callback-worker", 1000)
		t.Logf("real callback sweep completed=%d error=%v", completed, scanErr)
		require.NoError(t, scanErr)
		require.Equal(t, 1, completed)
		require.Equal(t, 1, handler.calls)
		require.Equal(t, "completed", owner.ProcessCallbackOutbox.GetX(ctx, current.ID).Status)
		require.Equal(t, "completed", owner.ProcessInstance.GetX(ctx, instance.ID).Status)

		for _, row := range historicalCallbacks {
			after, e := json.Marshal(owner.ProcessCallbackOutbox.GetX(ctx, row.ID))
			require.NoError(t, e)
			assert.JSONEq(t, string(before[row.ID]), string(after), "historical callback %s changed", row.ExecutionKey)
		}
	})

	for _, entry := range []string{"workflow", "bpmn"} {
		t.Run("CC_target_owner_"+entry, func(t *testing.T) {
			fresh, e := app.Create(ctx, identity, command("cc-target-"+entry, "generic"))
			require.NoError(t, e)
			p, e := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}, Capabilities: map[string]string{"notification": "scoped"}})
			require.NoError(t, e)
			notifications := service.NewTicketNotificationService(runtime, zap.NewNop().Sugar(), p)
			workflow := service.NewTicketWorkflowService(runtime, zap.NewNop().Sugar())
			workflow.SetNotificationService(notifications)
			handler := bpmn.NewCCTaskHandler(runtime, zap.NewNop().Sugar())
			handler.SetNotificationTargetBinder(notifications)
			call := func() error {
				if entry == "workflow" {
					return workflow.CCTicket(ctx, &dto.CCTicketRequest{TicketID: fresh.WorkItemID, CCUsers: []int{actor.ID}, NotifyChannels: []string{"sms"}}, actor.ID, tenant.ID)
				}
				callbackCtx := bpmn.WithBPMNCallbackExecutionKey(context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID), "cc-target-"+entry)
				_, e := handler.Execute(callbackCtx, nil, map[string]interface{}{"ticket_id": fresh.WorkItemID, "ccType": "variable", "ccResolvedUserIds": []int{actor.ID}, "ccNotify": true, "notifyChannels": "sms", "addedBy": actor.ID})
				return e
			}
			snapshotCC := func() map[string]string {
				result := map[string]string{}
				for _, table := range []string{"ticket_ccs", "ticket_notifications", "notifications", "audit_logs", "ticket_workflow_records"} {
					var raw string
					require.NoError(t, ownerDB.QueryRow("SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY id)::text,'[]') FROM "+table+" r").Scan(&raw))
					result[table] = raw
				}
				return result
			}
			beforeCC := snapshotCC()
			unchanged := func() {
				for table, raw := range snapshotCC() {
					require.JSONEq(t, beforeCC[table], raw, table)
				}
			}
			require.ErrorIs(t, call(), executionscope.ErrDenied)
			unchanged()
			require.Zero(t, owner.TicketCC.Query().Where(ticketcc.TicketIDEQ(fresh.WorkItemID)).CountX(ctx))
			require.Zero(t, owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID)).CountX(ctx))
			probe := &candidateSMSConnector{}
			reg := connector.NewRegistry()
			reg.Register(func() connector.Connector { return probe })
			target := config.ConnectorTargetConfig{Name: "sms", Provider: "cc-local", DestinationDigest: probe.DeliveryDestinationIdentity(), Capabilities: []string{"notification"}}
			wrong := candidateDeclaredManager(t, ctx, tenant.ID, "149ff1af-a27c-47c7-827f-103271130bb9", reg, target)
			notifications.SetConnectorManager(wrong)
			require.ErrorIs(t, call(), executionscope.ErrDenied)
			unchanged()
			require.Zero(t, owner.TicketCC.Query().Where(ticketcc.TicketIDEQ(fresh.WorkItemID)).CountX(ctx))
			require.Zero(t, owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID)).CountX(ctx))
			manager := candidateDeclaredManager(t, ctx, tenant.ID, scopeID, reg, target)
			notifications.SetConnectorManager(manager)
			require.NoError(t, call())
			row := owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID)).OnlyX(ctx)
			require.NotNil(t, row.TargetProtocolVersion)
			require.Equal(t, 1, *row.TargetProtocolVersion)
			require.NotNil(t, row.TargetConnectorName)
			require.Equal(t, "sms", *row.TargetConnectorName)
			require.NotNil(t, row.TargetConnectorProvider)
			require.Equal(t, "cc-local", *row.TargetConnectorProvider)
			require.NotNil(t, row.TargetDestinationDigest)
			require.Equal(t, probe.DeliveryDestinationIdentity(), *row.TargetDestinationDigest)
			notifications.SetConnectorManager(nil)
			require.NoError(t, call(), "existing CC must not rediscover or expand a target")
			require.Equal(t, 1, owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID)).CountX(ctx))
			require.Empty(t, probe.ids)
			// Queue cleanup after producer assertions, so unrelated worker tests do not consume this intent.
			require.NoError(t, owner.TicketNotification.DeleteOne(row).Exec(ctx))
		})
	}

	t.Run("direct notification enqueues without provider calls", func(t *testing.T) {
		fresh, e := app.Create(ctx, identity, command("direct-notification-queue", "generic"))
		require.NoError(t, e)
		pref := owner.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetEventType("direct_queue_test").SetEmailEnabled(true).SetInAppEnabled(false).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
		defer func() { require.NoError(t, owner.NotificationPreference.DeleteOne(pref).Exec(ctx)) }()
		probe := &candidateNotificationMailProbe{}
		email := service.NewEmailService(candidateNotificationEmailConfig(t), zap.NewNop().Sugar())
		email.SetGraphProvider(func(int) (service.GraphMailSender, string, bool) { return probe, "local-sender@example.invalid", true })
		svc := service.NewTicketNotificationService(runtime, zap.NewNop().Sugar(), policy)
		svc.SetNotificationPreferenceService(service.NewNotificationPreferenceService(runtime, zap.NewNop().Sugar()))
		email.SetDeliveryTargetDependencies(nil, policy)
		svc.SetEmailService(email)
		req := dto.SendTicketNotificationRequest{UserIDs: []int{actor.ID}, EventType: "direct_queue_test", Content: "private queued notification", DeliveryKey: "direct-queue-test"}
		result, e := svc.SendNotification(ctx, fresh.WorkItemID, &req, tenant.ID)
		require.NoError(t, e)
		assert.Empty(t, probe.recipients, "request owner must not call transport")
		require.NotNil(t, result)
		require.Equal(t, dto.TicketNotificationEffectQueued, result.Effect)
		require.Equal(t, 1, result.QueuedCount)
		require.Equal(t, 1, result.ExternalIntentCount)
		require.Equal(t, 1, result.DeliveryCount)
		require.Zero(t, result.AppliedCount)
		rows := owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID), ticketnotification.DeliveryKeyEQ(req.DeliveryKey)).AllX(ctx)
		assert.Len(t, rows, 1, "external intent must be durable before response")
		for _, row := range rows {
			assert.Equal(t, "email", row.Channel)
			assert.Equal(t, "pending", row.Status)
			assert.True(t, row.SentAt.IsZero())
		}
		result, e = svc.SendNotification(ctx, fresh.WorkItemID, &req, tenant.ID)
		require.NotNil(t, result)
		require.Equal(t, dto.TicketNotificationEffectIdempotent, result.Effect)
		require.Equal(t, 1, result.IdempotentCount)
		require.Equal(t, 1, result.ExternalIntentCount)
		require.Equal(t, 1, result.DeliveryCount)
		require.Zero(t, result.QueuedCount)
		require.NoError(t, e)
		assert.Empty(t, probe.recipients, "same request must not resend outside the worker")
		assert.Equal(t, 1, owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID), ticketnotification.DeliveryKeyEQ(req.DeliveryKey)).CountX(ctx))
		// Test-only queue cleanup, after assertions; not delivery evidence.
		_, e = owner.TicketNotification.Delete().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID)).Exec(ctx)
		require.NoError(t, e)
	})

	t.Run("disabled notification worker preserves pending and recovery", func(t *testing.T) {
		disabled, e := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}})
		require.NoError(t, e)
		fresh, e := app.Create(ctx, identity, command("disabled-notification-worker", "generic"))
		require.NoError(t, e)
		svc := service.NewTicketNotificationService(runtime, zap.NewNop().Sugar(), disabled)
		svc.SetNotificationPreferenceService(service.NewNotificationPreferenceService(runtime, zap.NewNop().Sugar()))
		svc.SetDeliveryQueueClient(clients.System)
		probe := &candidateNotificationMailProbe{}
		email := service.NewEmailService(candidateNotificationEmailConfig(t), zap.NewNop().Sugar())
		email.SetGraphProvider(func(int) (service.GraphMailSender, string, bool) { return probe, "local@example.invalid", true })
		email.SetDeliveryTargetDependencies(nil, disabled)
		svc.SetEmailService(email)
		for _, channel := range []string{"email", "push"} {
			for _, state := range []string{"pending", "processing"} {
				t.Run(channel+"/"+state, func(t *testing.T) {
					eventType := "disabled_" + channel + "_" + state
					pref := owner.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetEventType(eventType).SetInAppEnabled(false).SetEmailEnabled(channel == "email").SetSmsEnabled(false).SetPushEnabled(channel == "push").SaveX(ctx)
					defer func() { require.NoError(t, owner.NotificationPreference.DeleteOne(pref).Exec(ctx)) }()
					result, e := svc.SendNotification(ctx, fresh.WorkItemID, &dto.SendTicketNotificationRequest{UserIDs: []int{actor.ID}, EventType: eventType, Content: "disabled execution", DeliveryKey: eventType}, tenant.ID)
					require.NoError(t, e)
					require.Equal(t, dto.TicketNotificationEffectQueued, result.Effect)
					row := owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID), ticketnotification.DeliveryKeyEQ(eventType)).OnlyX(ctx)
					defer func() { require.NoError(t, owner.TicketNotification.DeleteOne(row).Exec(ctx)) }()
					if state == "processing" {
						row.Update().SetStatus("processing").SetAttemptCount(1).SetLeaseOwner("old-worker").SetLeaseExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
					}
					var before, after string
					require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(n)::text FROM ticket_notifications n WHERE id=$1`, row.ID).Scan(&before))
					n, e := svc.ProcessPendingDeliveries(context.Background(), "disabled-worker", 1000)
					require.ErrorIs(t, e, executionscope.ErrDenied)
					require.Zero(t, n)
					require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(n)::text FROM ticket_notifications n WHERE id=$1`, row.ID).Scan(&after))
					require.JSONEq(t, before, after)
					require.Empty(t, probe.recipients)
				})
			}
		}
	})

	t.Run("notification producer freezes connector target", func(t *testing.T) {
		policy, e := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}, Capabilities: map[string]string{"notification": "scoped"}})
		require.NoError(t, e)
		fresh, e := app.Create(ctx, identity, command("notification-target-producer", "generic"))
		require.NoError(t, e)
		pref := owner.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetEventType("target_freeze_test").SetSmsEnabled(true).SetInAppEnabled(false).SetEmailEnabled(false).SetPushEnabled(false).SaveX(ctx)
		defer owner.NotificationPreference.DeleteOne(pref).Exec(ctx)
		probe := &candidateSMSConnector{}
		reg := connector.NewRegistry()
		reg.Register(func() connector.Connector { return probe })
		manager := candidateDeclaredManager(t, ctx, tenant.ID, scopeID, reg, config.ConnectorTargetConfig{Name: "sms", Provider: "local", DestinationDigest: probe.DeliveryDestinationIdentity(), Capabilities: []string{"notification"}})
		svc := service.NewTicketNotificationService(runtime, zap.NewNop().Sugar(), policy)
		svc.SetConnectorManager(manager)
		svc.SetNotificationPreferenceService(service.NewNotificationPreferenceService(runtime, zap.NewNop().Sugar()))
		req := dto.SendTicketNotificationRequest{UserIDs: []int{actor.ID}, EventType: "target_freeze_test", Content: "frozen target", DeliveryKey: "target-freeze"}
		tx, e := runtime.Tx(ctx)
		require.NoError(t, e)
		defer tx.Rollback()
		require.NoError(t, svc.EnqueueNotificationTx(ctx, tx, fresh.WorkItemID, tenant.ID, &req))
		row := tx.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID), ticketnotification.DeliveryKeyEQ(req.DeliveryKey)).OnlyX(ctx)
		require.NotNil(t, row.TargetProtocolVersion)
		require.Equal(t, 1, *row.TargetProtocolVersion)
		require.NotNil(t, row.TargetConnectorName)
		require.Equal(t, "sms", *row.TargetConnectorName)
		require.NotNil(t, row.TargetConnectorProvider)
		require.Equal(t, "local", *row.TargetConnectorProvider)
		require.NotNil(t, row.TargetDestinationDigest)
		require.Equal(t, probe.DeliveryDestinationIdentity(), *row.TargetDestinationDigest)
		require.Empty(t, probe.ids, "producer must not send")
		require.NoError(t, tx.Rollback())
		require.Zero(t, owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID)).CountX(ctx))
		run := func(request dto.SendTicketNotificationRequest) error {
			tx, e := runtime.Tx(ctx)
			if e != nil {
				return e
			}
			defer tx.Rollback()
			if e = svc.EnqueueNotificationTx(ctx, tx, fresh.WorkItemID, tenant.ID, &request); e != nil {
				return e
			}
			return tx.Commit()
		}
		require.NoError(t, run(req))
		stored := owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID), ticketnotification.DeliveryKeyEQ(req.DeliveryKey)).OnlyX(ctx)
		defer func() { require.NoError(t, owner.TicketNotification.DeleteOneID(stored.ID).Exec(ctx)) }()
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(n)::text FROM ticket_notifications n WHERE id=$1`, stored.ID).Scan(&before))
		svc.SetConnectorManager(nil)
		require.NoError(t, run(req), "replay must use original target rather than current discovery")
		conflict := req
		conflict.Content = "changed"
		require.Error(t, run(conflict))
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(n)::text FROM ticket_notifications n WHERE id=$1`, stored.ID).Scan(&after))
		require.JSONEq(t, before, after)
		ambiguous := candidateDeclaredManager(t, ctx, tenant.ID, scopeID, reg,
			config.ConnectorTargetConfig{Name: "sms", Provider: "first", DestinationDigest: probe.DeliveryDestinationIdentity(), Capabilities: []string{"notification"}},
			config.ConnectorTargetConfig{Name: "sms", Provider: "second", DestinationDigest: probe.DeliveryDestinationIdentity(), Capabilities: []string{"notification"}})
		svc.SetConnectorManager(ambiguous)
		req.DeliveryKey = "target-ambiguous"
		require.ErrorIs(t, run(req), executionscope.ErrDenied)
		require.Zero(t, owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID), ticketnotification.DeliveryKeyEQ(req.DeliveryKey)).CountX(ctx))
		unknown := owner.TicketNotification.Create().SetTenantID(tenant.ID).SetTicketID(fresh.WorkItemID).SetUserID(actor.ID).SetType(req.EventType).SetChannel("unknown").SetContent(req.Content).SetDeliveryKey("target-unknown").SaveX(ctx)
		defer func() { require.NoError(t, owner.TicketNotification.DeleteOne(unknown).Exec(ctx)) }()
		req.DeliveryKey = "target-unknown"
		require.ErrorIs(t, run(req), executionscope.ErrDenied)
		require.Empty(t, probe.ids)

	})

	t.Run("real notification worker preserves historical rows", func(t *testing.T) {
		policy, e := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}, Capabilities: map[string]string{"notification": "scoped"}})
		require.NoError(t, e)
		snapshot := func(id int) string {
			var raw string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(n)::text FROM ticket_notifications n WHERE id=$1`, id).Scan(&raw))
			return raw
		}
		before := map[int]string{}
		for _, row := range historicalNotifications {
			before[row.ID] = snapshot(row.ID)
		}
		fresh, err := app.Create(ctx, identity, command("notification-member", "generic"))
		require.NoError(t, err)
		current := owner.TicketNotification.Create().SetTenantID(tenant.ID).SetTicketID(fresh.WorkItemID).SetUserID(actor.ID).SetType("created").SetChannel("email").SetContent("Candidate notification without external transport").SetDeliveryKey("candidate-notification").SetNextAttemptAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		notifications := service.NewTicketNotificationService(runtime, zap.NewNop().Sugar(), policy)
		notifications.SetDeliveryQueueClient(clients.System)
		n, err := notifications.ProcessPendingDeliveries(context.Background(), "candidate-notify-worker", 1000)
		require.Error(t, err, "missing transport must remain a visible failure")
		require.Zero(t, n)
		for _, row := range historicalNotifications {
			assert.JSONEq(t, before[row.ID], snapshot(row.ID), "historical notification changed")
		}
		actual := owner.TicketNotification.GetX(ctx, current.ID)
		require.Equal(t, "failed", actual.Status)
		require.Equal(t, 1, actual.AttemptCount)
		require.Equal(t, "delivery_target_invalid", actual.LastErrorClass)
		receiver := &candidateSMSConnector{}
		registry := connector.NewRegistry()
		registry.Register(func() connector.Connector { return receiver })
		manager := candidateDeclaredManager(t, ctx, tenant.ID, scopeID, registry, config.ConnectorTargetConfig{Name: "sms", Provider: "local-candidate-test", DestinationDigest: receiver.DeliveryDestinationIdentity(), Capabilities: []string{"notification"}})
		notifications.SetConnectorManager(manager)
		owner.User.UpdateOneID(actor.ID).SetPhone("private-local-recipient").SaveX(ctx)
		defer owner.User.UpdateOneID(actor.ID).SetPhone(actor.Phone).Exec(ctx)
		pref := owner.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetEventType("notification_worker_target").SetSmsEnabled(true).SetInAppEnabled(false).SetEmailEnabled(false).SetPushEnabled(false).SaveX(ctx)
		defer owner.NotificationPreference.DeleteOne(pref).Exec(ctx)
		producer := service.NewTicketNotificationService(runtime, zap.NewNop().Sugar(), policy)
		producer.SetConnectorManager(manager)
		producer.SetNotificationPreferenceService(service.NewNotificationPreferenceService(runtime, zap.NewNop().Sugar()))
		makeDelivery := func(key string) *ent.TicketNotification {
			tx, e := runtime.Tx(ctx)
			require.NoError(t, e)
			defer tx.Rollback()
			req := dto.SendTicketNotificationRequest{UserIDs: []int{actor.ID}, EventType: "notification_worker_target", Content: "Local notification", DeliveryKey: key}
			require.NoError(t, producer.EnqueueNotificationTx(ctx, tx, fresh.WorkItemID, tenant.ID, &req))
			require.NoError(t, tx.Commit())
			row := owner.TicketNotification.Query().Where(ticketnotification.TicketIDEQ(fresh.WorkItemID), ticketnotification.DeliveryKeyEQ(key)).OnlyX(ctx)
			require.NotNil(t, row.TargetProtocolVersion)
			require.Equal(t, 1, *row.TargetProtocolVersion)
			require.NotNil(t, row.TargetConnectorName)
			require.Equal(t, "sms", *row.TargetConnectorName)
			require.NotNil(t, row.TargetConnectorProvider)
			require.Equal(t, "local-candidate-test", *row.TargetConnectorProvider)
			require.NotNil(t, row.TargetDestinationDigest)
			require.Equal(t, receiver.DeliveryDestinationIdentity(), *row.TargetDestinationDigest)
			return row
		}
		success := makeDelivery("candidate-notification-success")
		baseline := snapshot(success.ID)
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
		require.NoError(t, err)
		n, rejected := notifications.ProcessPendingDeliveries(context.Background(), "notification-closed", 1000)
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
		require.NoError(t, err)
		require.ErrorIs(t, rejected, executionscope.ErrDenied)
		require.Zero(t, n)
		require.Empty(t, receiver.ids)
		require.JSONEq(t, baseline, snapshot(success.ID))
		n, err = notifications.ProcessPendingDeliveries(context.Background(), "notification-success", 1000)
		require.NoError(t, err)
		require.Equal(t, 1, n)
		require.Equal(t, []string{"candidate-notification-success"}, receiver.ids)
		require.Equal(t, "sent", owner.TicketNotification.GetX(ctx, success.ID).Status)
		inFlight := makeDelivery("candidate-notification-inflight")
		receiver.afterSend = func() {
			_, e := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
			require.NoError(t, e)
		}
		n, rejected = notifications.ProcessPendingDeliveries(context.Background(), "notification-inflight", 1000)
		receiver.afterSend = nil
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
		require.NoError(t, err)
		require.Error(t, rejected)
		require.Zero(t, n)
		inflightState := owner.TicketNotification.GetX(ctx, inFlight.ID)
		require.Equal(t, "processing", inflightState.Status)
		require.True(t, inflightState.SentAt.IsZero())
		require.Equal(t, 1, inflightState.AttemptCount)
		require.Equal(t, []string{"candidate-notification-success", "candidate-notification-inflight"}, receiver.ids)
		owner.TicketNotification.UpdateOneID(inFlight.ID).SetLeaseExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		n, err = notifications.ProcessPendingDeliveries(context.Background(), "notification-recovery", 1000)
		require.Error(t, err)
		require.Zero(t, n)
		recovered := owner.TicketNotification.GetX(ctx, inFlight.ID)
		require.Equal(t, "failed", recovered.Status)
		require.Equal(t, "delivery_unknown", recovered.LastErrorClass)
		require.Len(t, receiver.ids, 2)
		for _, row := range historicalNotifications {
			require.JSONEq(t, before[row.ID], snapshot(row.ID))
		}

		for _, scenario := range []string{"scope", "deployment", "webhook-only", "outbox-only"} {
			t.Run("target_authority_"+scenario, func(t *testing.T) {
				probe := &candidateSMSConnector{}
				reg := connector.NewRegistry()
				reg.Register(func() connector.Connector { return probe })
				targetScope, deployment, capability := scopeID, "intake-test", "notification"
				switch scenario {
				case "scope":
					targetScope = "149ff1af-a27c-47c7-827f-103271130bb9"
				case "deployment":
					deployment = "other-deployment"
				case "webhook-only":
					capability = "webhook"
				case "outbox-only":
					capability = "outbox"
				}
				wrongPolicy, e := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: deployment, Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: targetScope}}, Capabilities: map[string]string{capability: "scoped"}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: tenant.ID, ScopeID: targetScope, Name: "sms", Provider: "local-candidate-test", DestinationDigest: probe.DeliveryDestinationIdentity(), Capabilities: []string{capability}}}})
				require.NoError(t, e)
				wrongManager := connector.NewManager(reg, zap.NewNop().Sugar(), wrongPolicy)
				defer wrongManager.CloseAll()
				require.NoError(t, wrongManager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:notification-target", "activate local probe with unrelated delivery authority")))
				notifications.SetConnectorManager(wrongManager)
				defer notifications.SetConnectorManager(manager)
				row := makeDelivery("notification-wrong-target-" + scenario)
				n, e := notifications.ProcessPendingDeliveries(context.Background(), "notification-target-"+scenario, 1000)
				assert.Error(t, e, "unrelated declaration must reject before Send")
				assert.Zero(t, n)
				assert.Empty(t, probe.ids, "local transport must not receive an unauthorized notification")
				stored := owner.TicketNotification.GetX(ctx, row.ID)
				assert.Equal(t, "failed", stored.Status)
				assert.Equal(t, "delivery_target_invalid", stored.LastErrorClass)
				assert.True(t, stored.SentAt.IsZero(), "rejection must not record successful delivery")
				assert.Equal(t, 1, stored.AttemptCount)
				sentBeforeReplay := append([]string(nil), probe.ids...)
				_, _ = notifications.ProcessPendingDeliveries(context.Background(), "notification-target-replay-"+scenario, 1000)
				assert.Equal(t, sentBeforeReplay, probe.ids, "repeated poll must not add another external effect")
				for _, historical := range historicalNotifications {
					assert.JSONEq(t, before[historical.ID], snapshot(historical.ID))
				}
			})
		}

		for _, cause := range []error{errors.New("temporary notification resolver failure"), context.Canceled, context.DeadlineExceeded} {
			t.Run("resolver_"+cause.Error(), func(t *testing.T) {
				probe := &candidateSMSConnector{}
				reg := connector.NewRegistry()
				reg.Register(func() connector.Connector { return probe })
				p, e := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}, Capabilities: map[string]string{"notification": "scoped"}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: tenant.ID, ScopeID: scopeID, Name: "sms", Provider: "local-candidate-test", DestinationDigest: probe.DeliveryDestinationIdentity(), Capabilities: []string{"notification"}}}})
				require.NoError(t, e)
				managerWithFailure := connector.NewManager(reg, zap.NewNop().Sugar(), &webhookResolverFailureGate{ExecutionPolicy: p, err: cause})
				defer managerWithFailure.CloseAll()
				require.NoError(t, managerWithFailure.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:notification-cause", "activate local target before resolver failure")))
				row := makeDelivery("notification-resolver-" + cause.Error())
				notifications.SetConnectorManager(managerWithFailure)
				defer notifications.SetConnectorManager(manager)
				n, e := notifications.ProcessPendingDeliveries(context.Background(), "notification-resolver", 1000)
				assert.ErrorIs(t, e, cause)
				assert.Zero(t, n)
				assert.Empty(t, probe.ids)
				stored := owner.TicketNotification.GetX(ctx, row.ID)
				assert.True(t, stored.SentAt.IsZero())
				assert.Equal(t, "pending", stored.Status)
				assert.Equal(t, "connector_unavailable", stored.LastErrorClass)
				// Test cleanup only, after assertions on the actual worker retry state.
				owner.TicketNotification.UpdateOneID(row.ID).SetStatus("failed").SaveX(ctx)
			})
		}

		for _, state := range []string{"pending", "failed", "sent"} {
			t.Run("writeback_"+state, func(t *testing.T) {
				row := makeDelivery("notification-writeback-" + state)
				if state != "sent" {
					cause := errors.New("resolver unavailable")
					if state == "failed" {
						cause = executionscope.ErrDenied
					}
					failedManager := connector.NewManager(registry, zap.NewNop().Sugar(), &webhookResolverFailureGate{ExecutionPolicy: policy, err: cause})
					defer failedManager.CloseAll()
					notifications.SetConnectorManager(failedManager)
				} else {
					notifications.SetConnectorManager(manager)
				}
				defer notifications.SetConnectorManager(manager)
				injected := errors.New("injected notification " + state + " write failure")
				active := true
				defer func() { active = false }()
				clients.System.TicketNotification.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
						if typed, ok := m.(*ent.TicketNotificationMutation); ok && active {
							status, _ := typed.Status()
							if status == state {
								return nil, injected
							}
						}
						return next.Mutate(c, m)
					})
				})
				beforeSend := len(receiver.ids)
				n, e := notifications.ProcessPendingDeliveries(context.Background(), "notification-writeback", 1000)
				active = false
				assert.ErrorIs(t, e, injected)
				assert.Zero(t, n)
				if state == "sent" {
					assert.Equal(t, beforeSend+1, len(receiver.ids))
				} else {
					assert.Equal(t, beforeSend, len(receiver.ids))
				}
				stored := owner.TicketNotification.GetX(ctx, row.ID)
				require.Equal(t, "processing", stored.Status)
				require.True(t, stored.SentAt.IsZero())
				owner.TicketNotification.UpdateOneID(row.ID).SetStatus("failed").SaveX(ctx)
			})
		}

	})

	t.Run("notification intents share the owning transaction", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("notification-tx-member", "generic"))
		require.NoError(t, err)
		svc := newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy)
		request := dto.SendTicketNotificationRequest{UserIDs: []int{actor.ID, actor.ID}, EventType: "sla_violated", Content: "Transactional SLA notification", DeliveryKey: "candidate-notify-tx"}
		run := func(itemID int, req *dto.SendTicketNotificationRequest, commit bool) error {
			tx, e := runtime.Tx(ctx)
			if e != nil {
				return e
			}
			defer tx.Rollback()
			if e = svc.EnqueueNotificationTx(ctx, tx, itemID, tenant.ID, req); e != nil {
				return e
			}
			if commit {
				return tx.Commit()
			}
			return nil
		}
		counts := func() []int {
			return []int{owner.TicketNotification.Query().CountX(ctx), owner.Notification.Query().CountX(ctx)}
		}
		before := counts()
		require.ErrorIs(t, run(historical.WorkItemID, &request, true), executionscope.ErrDenied)
		require.Equal(t, before, counts())
		require.NoError(t, run(fresh.WorkItemID, &request, false))
		require.Equal(t, before, counts())
		require.NoError(t, run(fresh.WorkItemID, &request, true))
		require.Equal(t, []int{before[0] + 2, before[1] + 1}, counts())
		var pending int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE delivery_key=$1 AND channel='email' AND status='pending' AND sent_at IS NULL`, request.DeliveryKey).Scan(&pending))
		require.Equal(t, 1, pending)
		require.NoError(t, run(fresh.WorkItemID, &request, true))
		require.Equal(t, []int{before[0] + 2, before[1] + 1}, counts())
		conflict := request
		conflict.Content = "Conflicting content"
		require.Error(t, run(fresh.WorkItemID, &conflict, true))
		require.Equal(t, []int{before[0] + 2, before[1] + 1}, counts())
		injected := errors.New("injected unified notification write failure")
		inject := false
		writes := 0
		runtime.Notification.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				v, e := next.Mutate(ctx, m)
				if e == nil && inject {
					writes++
					return nil, injected
				}
				return v, e
			})
		})
		snapshot := owner.Ticket.GetX(ctx, fresh.WorkItemID)
		tx, err := runtime.Tx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
		_, err = tx.Ticket.UpdateOneID(fresh.WorkItemID).SetTitle("owning write must roll back").Save(ctx)
		require.NoError(t, err)
		faultRequest := request
		faultRequest.DeliveryKey = "candidate-notify-tx-fault"
		inject = true
		rejected := svc.EnqueueNotificationTx(ctx, tx, fresh.WorkItemID, tenant.ID, &faultRequest)
		inject = false
		require.ErrorIs(t, rejected, injected)
		require.Equal(t, 1, writes)
		require.NoError(t, tx.Rollback())
		require.Equal(t, snapshot.Title, owner.Ticket.GetX(ctx, fresh.WorkItemID).Title)
		require.Equal(t, []int{before[0] + 2, before[1] + 1}, counts())
		svc.SetNotificationPreferenceService(service.NewNotificationPreferenceService(runtime, zap.NewNop().Sugar()))
		prefTx, err := runtime.Tx(ctx)
		require.NoError(t, err)
		defer prefTx.Rollback()
		pref := prefTx.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetEventType("sla_warning").SetEmailEnabled(true).SetInAppEnabled(false).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
		changed := request
		changed.EventType = "sla_warning"
		changed.DeliveryKey = "notify-pref-snapshot"
		require.NoError(t, svc.EnqueueNotificationTx(ctx, prefTx, fresh.WorkItemID, tenant.ID, &changed))
		rows, err := prefTx.TicketNotification.Query().Where(func(sel *entsql.Selector) { sel.Where(entsql.EQ(sel.C("delivery_key"), changed.DeliveryKey)) }).All(ctx)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, "email", rows[0].Channel)
		prefTx.NotificationPreference.UpdateOneID(pref.ID).SetEmailEnabled(false).SetInAppEnabled(true).SaveX(ctx)
		mismatch := changed
		mismatch.Content = "Changed after preference switch"
		require.Error(t, svc.EnqueueNotificationTx(ctx, prefTx, fresh.WorkItemID, tenant.ID, &mismatch))
		require.NoError(t, svc.EnqueueNotificationTx(ctx, prefTx, fresh.WorkItemID, tenant.ID, &changed))
		replayRows, err := prefTx.TicketNotification.Query().Where(func(sel *entsql.Selector) { sel.Where(entsql.EQ(sel.C("delivery_key"), changed.DeliveryKey)) }).All(ctx)
		require.NoError(t, err)
		require.Len(t, replayRows, 1)
		require.Equal(t, rows[0].ID, replayRows[0].ID)
		require.Equal(t, "email", replayRows[0].Channel)
		require.Equal(t, rows[0].Content, replayRows[0].Content)

		prefTx.NotificationPreference.UpdateOneID(pref.ID).SetInAppEnabled(false).SaveX(ctx)
		require.Error(t, svc.EnqueueNotificationTx(ctx, prefTx, fresh.WorkItemID, tenant.ID, &mismatch))
		disabled := changed
		disabled.DeliveryKey = "notify-explicitly-disabled"
		require.NoError(t, svc.EnqueueNotificationTx(ctx, prefTx, fresh.WorkItemID, tenant.ID, &disabled))
		var extra int
		extra, err = prefTx.TicketNotification.Query().Where(func(sel *entsql.Selector) { sel.Where(entsql.EQ(sel.C("delivery_key"), disabled.DeliveryKey)) }).Count(ctx)
		require.NoError(t, err)
		require.Zero(t, extra)
		require.NoError(t, prefTx.Rollback())
		require.Equal(t, []int{before[0] + 2, before[1] + 1}, counts())

	})
	t.Run("stream source requires current persistent authority", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("stream-source-member", "generic"))
		require.NoError(t, err)
		other, err := app.Create(ctx, identity, command("stream-other-member", "generic"))
		require.NoError(t, err)
		owner.Ticket.UpdateOneID(fresh.WorkItemID).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(-time.Hour)).SetSLAResolutionDeadline(time.Now().Add(time.Hour)).SaveX(ctx)
		monitor := service.NewSLAMonitorService(runtime, zap.NewNop().Sugar(), policy)
		monitor.SetNotificationService(newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy))
		_, err = monitor.CheckSLAViolations(ctx, tenant.ID)
		require.NoError(t, err)
		row := owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(fresh.WorkItemID), outboxevent.EventTypeEQ("sla.breached")).OnlyX(ctx)
		capture := &candidateSourceCaptureBus{}
		previous := eventbus.GetGlobalEventBus()
		eventbus.SetGlobalEventBus(capture)
		defer eventbus.SetGlobalEventBus(previous)
		require.NoError(t, service.NewSLABreachDeliveryHandler().Deliver(ctx, row))
		require.Len(t, capture.events, 1)
		source := capture.events[0].(interface {
			EventType() string
			TenantID() string
			OccurredAt() time.Time
			ExecutionWorkItemID() int
			PersistentEventID() string
		})
		payload, err := json.Marshal(source)
		require.NoError(t, err)
		envelope := eventbus.Envelope{EventType: source.EventType(), TenantID: source.TenantID(), OccurredAt: source.OccurredAt(), EventID: source.PersistentEventID(), Payload: payload, Execution: &eventbus.ExecutionIdentity{DeploymentID: "intake-test", ScopeID: scopeID, WorkItemID: source.ExecutionWorkItemID()}}
		ref := executionscope.Ref{DeploymentID: "intake-test", ScopeID: scopeID, TenantID: tenant.ID}
		authority := service.NewExecutionEventAuthority(runtime, policy)
		var before string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(e)::text FROM outbox_events e WHERE id=$1`, row.ID).Scan(&before))
		audits := owner.AuditLog.Query().CountX(ctx)
		require.NoError(t, authority.ValidateEvent(ctx, ref, envelope))
		for _, mutate := range []func(*eventbus.Envelope){
			func(e *eventbus.Envelope) { e.EventID = "unknown-persistent-event" },
			func(e *eventbus.Envelope) { e.Execution.WorkItemID = other.WorkItemID },
			func(e *eventbus.Envelope) { e.Execution.WorkItemID = historical.WorkItemID },
			func(e *eventbus.Envelope) { e.Execution.ScopeID = uuid.NewString() },
			func(e *eventbus.Envelope) { e.TenantID = fmt.Sprint(tenant.ID + 1) },
			func(e *eventbus.Envelope) { e.EventType = "unregistered.event" },
			func(e *eventbus.Envelope) {
				e.Payload = json.RawMessage(strings.Replace(string(e.Payload), "response", "resolve", 1))
			},
			func(e *eventbus.Envelope) { e.OccurredAt = e.OccurredAt.Add(time.Second) },
		} {
			changed := envelope
			execution := *envelope.Execution
			changed.Execution = &execution
			mutate(&changed)
			require.Error(t, authority.ValidateEvent(ctx, ref, changed))
		}
		t.Cleanup(func() {
			_, e := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
			require.NoError(t, e)
			_, e = ownerDB.ExecContext(ctx, "UPDATE execution_runtime_bindings SET deployment_id='intake-test' WHERE runtime_role=$1", runtimeRole)
			require.NoError(t, e)
		})
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
		require.NoError(t, err)
		require.Error(t, authority.ValidateEvent(ctx, ref, envelope))
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
		require.NoError(t, err)
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_runtime_bindings SET deployment_id='other-stream-test' WHERE runtime_role=$1", runtimeRole)
		require.NoError(t, err)
		require.Error(t, authority.ValidateEvent(ctx, ref, envelope))
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_runtime_bindings SET deployment_id='intake-test' WHERE runtime_role=$1", runtimeRole)
		require.NoError(t, err)
		require.NoError(t, authority.ValidateEvent(ctx, ref, envelope))
		var after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(e)::text FROM outbox_events e WHERE id=$1`, row.ID).Scan(&after))
		require.JSONEq(t, before, after)
		require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx), "source validation must not write audit")
		t.Run("standard persistent authority retains source boundaries", func(t *testing.T) {
			// Owner connection is an explicit private source-validation fixture, not
			// evidence that a standard application role passed startup admission.
			standard, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "standard-source-test"})
			require.NoError(t, err)
			validator := service.NewExecutionEventAuthority(owner, standard)
			standardRef := executionscope.Ref{DeploymentID: "standard-source-test", TenantID: tenant.ID}
			standardEnv := envelope
			standardEnv.Execution = &eventbus.ExecutionIdentity{DeploymentID: standardRef.DeploymentID, WorkItemID: fresh.WorkItemID}
			require.NoError(t, validator.ValidateEvent(ctx, standardRef, standardEnv))
			for _, mutate := range []func(*eventbus.Envelope){
				func(e *eventbus.Envelope) { e.Execution.DeploymentID = "forged-deployment" },
				func(e *eventbus.Envelope) { e.Execution.ScopeID = scopeID },
				func(e *eventbus.Envelope) { e.Execution.WorkItemID = other.WorkItemID },
				func(e *eventbus.Envelope) { e.Execution.WorkItemID = 0 },
				func(e *eventbus.Envelope) { e.EventID = "missing-source" },
				func(e *eventbus.Envelope) { e.TenantID = fmt.Sprint(tenant.ID + 1) },
				func(e *eventbus.Envelope) { e.EventType = "unknown.event" },
				func(e *eventbus.Envelope) { e.Payload = json.RawMessage(`{}`) },
				func(e *eventbus.Envelope) { e.OccurredAt = e.OccurredAt.Add(time.Second) },
			} {
				changed := standardEnv
				execution := *standardEnv.Execution
				changed.Execution = &execution
				mutate(&changed)
				require.Error(t, validator.ValidateEvent(ctx, standardRef, changed))
			}
			forgedRef := standardRef
			forgedRef.DeploymentID = "forged-deployment"
			forgedEnv := standardEnv
			forgedEnv.Execution = &eventbus.ExecutionIdentity{DeploymentID: forgedRef.DeploymentID, WorkItemID: fresh.WorkItemID}
			require.Error(t, validator.ValidateEvent(ctx, forgedRef, forgedEnv), "matching caller identities cannot replace the frozen deployment")
			require.Error(t, validator.ValidateEvent(tenantctx.WithTenantID(ctx, tenant.ID+1), standardRef, standardEnv))
			require.Error(t, validator.ValidateEvent(ctx, ref, envelope), "standard mode must not accept a candidate scope")
			require.Error(t, authority.ValidateEvent(ctx, standardRef, standardEnv), "candidate mode must not downgrade into standard mode")
			var preserved string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(e)::text FROM outbox_events e WHERE id=$1`, row.ID).Scan(&preserved))
			require.JSONEq(t, before, preserved)
			require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
		})
		t.Run("standard stream preserves verified persistent source", func(t *testing.T) {
			streamCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			redisCfg, redisClient := startCandidateStreamRedis(t, streamCtx)
			cfg := config.ExecutionConfig{Mode: "standard", DeploymentID: "standard-stream-test"}
			standard, err := database.NewExecutionPolicy(cfg)
			require.NoError(t, err)
			validator := service.NewExecutionEventAuthority(owner, standard)
			bus, err := eventbus.NewWatermillEventBus(redisCfg, cfg, validator, zap.NewNop().Sugar())
			require.NoError(t, err)
			defer bus.Close()
			received := make(chan interface{}, 2)
			require.NoError(t, bus.RegisterSubscription("sla.breached", standardExecutionObserver{candidateStreamObserver{received}}))
			require.NoError(t, bus.Start(streamCtx))
			cfg.DeploymentID = "changed-after-construction"
			for i := 0; i < 2; i++ {
				require.NoError(t, bus.Publish(source))
				select {
				case value := <-received:
					delivered, ok := value.(eventbus.Envelope)
					require.True(t, ok)
					require.Equal(t, source.PersistentEventID(), delivered.EventID)
					require.Equal(t, source.ExecutionWorkItemID(), delivered.Execution.WorkItemID)
					require.Equal(t, "standard-stream-test", delivered.Execution.DeploymentID)
					require.Empty(t, delivered.Execution.ScopeID)
					require.Equal(t, envelope.Payload, delivered.Payload)
				case <-streamCtx.Done():
					t.Fatal("standard persistent source did not reach typed subscriber")
				}
			}
			entries, err := redisClient.XRange(streamCtx, "sla.breached", "-", "+").Result()
			require.NoError(t, err)
			require.Len(t, entries, 2)
			var preserved string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(e)::text FROM outbox_events e WHERE id=$1`, row.ID).Scan(&preserved))
			require.JSONEq(t, before, preserved)
			require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
		})
		t.Run("webhook consumption freezes durable target intents", func(t *testing.T) {
			var sent atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent.Add(1); w.WriteHeader(http.StatusOK) }))
			defer endpoint.Close()
			registry := connector.NewRegistry()
			registry.Register(func() connector.Connector { return webhookconnector.New() })
			newManager := func(providers ...string) *connector.Manager {
				targets := make([]config.ConnectorTargetConfig, 0, len(providers))
				for _, provider := range providers {
					targets = append(targets, candidateWebhookTarget(provider, endpoint.URL))
				}
				return candidateDeclaredManager(t, ctx, tenant.ID, scopeID, registry, targets...)
			}
			manager := newManager("first", "second")

			t.Run("webhook producer rejects unrelated declared authority", func(t *testing.T) {
				for _, mismatch := range []string{"scope", "notification-only", "outbox-only", "deployment"} {
					t.Run(mismatch, func(t *testing.T) {
						fresh, err := app.Create(ctx, identity, command("webhook-target-authority-"+mismatch, "generic"))
						require.NoError(t, err)
						owner.Ticket.UpdateOneID(fresh.WorkItemID).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(-time.Hour)).SetSLAResolutionDeadline(time.Now().Add(time.Hour)).SaveX(ctx)
						_, err = monitor.CheckSLAViolations(ctx, tenant.ID)
						require.NoError(t, err)
						source := owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(fresh.WorkItemID), outboxevent.EventTypeEQ("sla.breached")).OnlyX(ctx)
						require.NoError(t, service.NewSLABreachDeliveryHandler().Deliver(ctx, source))
						captured := capture.events[len(capture.events)-1]
						wire, err := json.Marshal(captured)
						require.NoError(t, err)
						sourceEnvelope := envelope
						sourceIdentity := *envelope.Execution
						sourceIdentity.WorkItemID = fresh.WorkItemID
						sourceEnvelope.Execution = &sourceIdentity
						sourceEnvelope.EventID = source.EventID
						sourceEnvelope.Payload = wire
						sourceEnvelope.OccurredAt = captured.(interface{ OccurredAt() time.Time }).OccurredAt()
						targetScope, targetDeployment, capability := scopeID, "intake-test", "webhook"
						switch mismatch {
						case "scope":
							targetScope = "149ff1af-a27c-47c7-827f-103271130bb9"
						case "deployment":
							targetDeployment = "other-deployment"
						case "notification-only":
							capability = "notification"
						case "outbox-only":
							capability = "outbox"
						}
						declared := candidateWebhookTarget("authority-test", endpoint.URL)
						declared.TenantID, declared.ScopeID = tenant.ID, targetScope
						declared.Capabilities = []string{capability}
						targetPolicy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: targetDeployment, Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: targetScope}}, Capabilities: map[string]string{capability: "scoped"}, ConnectorTargets: []config.ConnectorTargetConfig{declared}})
						require.NoError(t, err)
						wrongManager := connector.NewManager(registry, zap.NewNop().Sugar(), targetPolicy)
						defer wrongManager.CloseAll()
						require.NoError(t, wrongManager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:unrelated-target", "declare a local target without authority for this source")))
						operationID := "webhook_consume:" + source.EventID
						// Cleanup only this fresh negative fixture's generated intents/receipt,
						// after asserting its writes; keep the source and historical records.
						defer owner.OutboxEvent.Delete().Where(outboxevent.ExecutionWorkItemIDEQ(fresh.WorkItemID), outboxevent.EventTypeEQ(service.WebhookDeliveryRequestedEventType)).ExecX(ctx)
						defer owner.AuditLog.Delete().Where(auditlog.TenantID(tenant.ID), auditlog.OperationID(operationID)).ExecX(ctx)
						beforeSends := sent.Load()
						var sourceBefore, sourceAfter string
						require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(e)::text FROM outbox_events e WHERE id=$1`, source.ID).Scan(&sourceBefore))
						err = service.NewWebhookEventSubscriber(wrongManager, zap.NewNop().Sugar(), runtime, policy).HandleContext(ctx, sourceEnvelope)
						assert.ErrorIs(t, err, executionscope.ErrDenied)
						assert.Zero(t, owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(fresh.WorkItemID), outboxevent.EventTypeEQ(service.WebhookDeliveryRequestedEventType)).CountX(ctx), "unrelated target must not acquire a durable delivery intent")
						assert.Zero(t, owner.AuditLog.Query().Where(auditlog.TenantID(tenant.ID), auditlog.OperationID(operationID)).CountX(ctx), "rejection must not commit a successful consumption receipt")
						assert.Equal(t, beforeSends, sent.Load())
						require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(e)::text FROM outbox_events e WHERE id=$1`, source.ID).Scan(&sourceAfter))
						assert.JSONEq(t, sourceBefore, sourceAfter, "rejected target must preserve original source row")
					})
				}
			})
			subscriber := service.NewWebhookEventSubscriber(manager, zap.NewNop().Sugar(), runtime, policy)
			outboxBefore, auditBefore := owner.OutboxEvent.Query().CountX(ctx), owner.AuditLog.Query().CountX(ctx)
			require.Error(t, subscriber.HandleContext(ctx, map[string]interface{}{"eventType": "sla.breached", "tenantId": fmt.Sprint(tenant.ID)}))
			empty := connector.NewManager(registry, zap.NewNop().Sugar(), nil)
			require.Error(t, service.NewWebhookEventSubscriber(empty, zap.NewNop().Sugar(), runtime, policy).HandleContext(ctx, envelope))
			injected := errors.New("webhook actual insert rollback")
			faultStage := ""
			writes := 0
			defer func() { faultStage = "" }()
			runtime.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
					value, e := next.Mutate(c, m)
					if e != nil {
						return value, e
					}
					if typed, ok := m.(*ent.OutboxEventMutation); ok {
						kind, _ := typed.EventType()
						if faultStage == "outbox" && kind == service.WebhookDeliveryRequestedEventType {
							writes++
							return nil, injected
						}
					}
					return value, nil
				})
			})
			runtime.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
					value, e := next.Mutate(c, m)
					if e != nil {
						return value, e
					}
					if typed, ok := m.(*ent.AuditLogMutation); ok {
						op, _ := typed.OperationID()
						if faultStage == "audit" && op == "webhook_consume:"+envelope.EventID {
							writes++
							return nil, injected
						}
					}
					return value, nil
				})
			})
			for _, stage := range []string{"outbox", "audit"} {
				faultStage = stage
				require.ErrorIs(t, subscriber.HandleContext(ctx, envelope), injected)
				faultStage = ""
				require.Equal(t, outboxBefore, owner.OutboxEvent.Query().CountX(ctx))
				require.Equal(t, auditBefore, owner.AuditLog.Query().CountX(ctx))
			}
			require.Equal(t, 2, writes)
			racing := true
			arrived, release := make(chan struct{}, 2), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			runtime.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
					if typed, ok := m.(*ent.OutboxEventMutation); ok && racing {
						kind, _ := typed.EventType()
						if kind == service.WebhookDeliveryRequestedEventType {
							arrived <- struct{}{}
							select {
							case <-release:
							case <-c.Done():
								return nil, c.Err()
							}
						}
					}
					return next.Mutate(c, m)
				})
			})
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				go func() { results <- subscriber.HandleContext(ctx, envelope) }()
			}
			for i := 0; i < 2; i++ {
				select {
				case <-arrived:
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent webhook transactions did not reach the insert")
				}
			}
			releaseOnce.Do(func() { close(release) })
			errs := []error{<-results, <-results}
			racing = false
			succeeded, conflicts := 0, 0
			for _, e := range errs {
				if e == nil {
					succeeded++
					continue
				}
				var pg *pq.Error
				require.ErrorAs(t, e, &pg)
				require.Equal(t, pq.ErrorCode("23505"), pg.Code)
				conflicts++
			}
			require.Equal(t, 1, succeeded)
			require.Equal(t, 1, conflicts)
			require.NoError(t, subscriber.HandleContext(ctx, envelope))
			require.Zero(t, sent.Load(), "Redis consumer must commit intent without sending")
			require.Equal(t, outboxBefore+2, owner.OutboxEvent.Query().CountX(ctx))
			require.Equal(t, auditBefore+1, owner.AuditLog.Query().CountX(ctx))
			var intentsBefore string
			require.NoError(t, ownerDB.QueryRow(`SELECT coalesce(json_agg(e ORDER BY id),'[]')::text FROM outbox_events e WHERE event_type='webhook.event.delivery.requested'`).Scan(&intentsBefore))
			// A new startup configuration adds a target; replay retains the committed target set.
			manager.CloseAll()
			manager = newManager("first", "second", "third")
			subscriber = service.NewWebhookEventSubscriber(manager, zap.NewNop().Sugar(), runtime, policy)
			require.NoError(t, subscriber.HandleContext(ctx, envelope))
			require.Equal(t, outboxBefore+2, owner.OutboxEvent.Query().CountX(ctx), "replay cannot discover new targets")
			require.Equal(t, auditBefore+1, owner.AuditLog.Query().CountX(ctx))
			var intentsAfter string
			require.NoError(t, ownerDB.QueryRow(`SELECT coalesce(json_agg(e ORDER BY id),'[]')::text FROM outbox_events e WHERE event_type='webhook.event.delivery.requested'`).Scan(&intentsAfter))
			require.JSONEq(t, intentsBefore, intentsAfter)
			var receiptBefore, receiptAfter string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE operation_id=$1 AND tenant_id=$2`, "webhook_consume:"+envelope.EventID, tenant.ID).Scan(&receiptBefore))
			defer func() {
				_, e := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
				require.NoError(t, e)
			}()
			_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
			require.NoError(t, err)
			require.Error(t, subscriber.HandleContext(ctx, envelope))
			_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
			require.NoError(t, err)
			persisted := owner.OutboxEvent.Query().Where(outboxevent.EventTypeEQ(service.WebhookDeliveryRequestedEventType)).FirstX(ctx)
			defer func() {
				_, e := owner.OutboxEvent.UpdateOneID(persisted.ID).SetPayload(persisted.Payload).Save(ctx)
				require.NoError(t, e)
			}()
			owner.OutboxEvent.UpdateOneID(persisted.ID).SetPayload(json.RawMessage(`{}`)).SaveX(ctx)
			require.Error(t, subscriber.HandleContext(ctx, envelope))
			owner.OutboxEvent.UpdateOneID(persisted.ID).SetPayload(persisted.Payload).SaveX(ctx)
			_, err = ownerDB.ExecContext(ctx, `UPDATE outbox_events SET payload=jsonb_set(payload,'{unexpected}', 'true'::jsonb) WHERE id=$1`, persisted.ID)
			require.NoError(t, err)
			require.Error(t, subscriber.HandleContext(ctx, envelope), "unknown persisted fields must not be ignored in digest verification")
			owner.OutboxEvent.UpdateOneID(persisted.ID).SetPayload(persisted.Payload).SaveX(ctx)
			require.NoError(t, subscriber.HandleContext(ctx, envelope))
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE operation_id=$1 AND tenant_id=$2`, "webhook_consume:"+envelope.EventID, tenant.ID).Scan(&receiptAfter))
			require.JSONEq(t, receiptBefore, receiptAfter)
			require.Zero(t, sent.Load())
			reservedSet := map[string]bool{}
			for _, event := range owner.OutboxEvent.Query().AllX(ctx) {
				if event.EventType != service.WebhookDeliveryRequestedEventType {
					reservedSet[event.EventType] = true
				}
			}
			reserved := []string{}
			for kind := range reservedSet {
				reserved = append(reserved, kind)
			}
			disabledReserved := []string{service.WebhookDeliveryRequestedEventType}
			for _, kind := range reserved {
				if kind != "sla.breached" {
					disabledReserved = append(disabledReserved, kind)
				}
			}
			disabledRegistry, e := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewSLABreachDeliveryHandler()}, disabledReserved...)
			require.NoError(t, e)
			disabledWorker, e := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(clients.System, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 1000, PollInterval: time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), disabledRegistry)
			require.NoError(t, e)
			var beforeDisabledPoll string
			require.NoError(t, ownerDB.QueryRow(`SELECT coalesce(json_agg(e ORDER BY id),'[]')::text FROM outbox_events e WHERE event_type='webhook.event.delivery.requested'`).Scan(&beforeDisabledPoll))
			require.NoError(t, disabledWorker.DispatchOnce(ctx))
			var disabledIntents string
			require.NoError(t, ownerDB.QueryRow(`SELECT coalesce(json_agg(e ORDER BY id),'[]')::text FROM outbox_events e WHERE event_type='webhook.event.delivery.requested'`).Scan(&disabledIntents))
			require.JSONEq(t, beforeDisabledPoll, disabledIntents, "reserved webhook type must retain original pending intents")
			require.Zero(t, sent.Load())
			deliveryRegistry, e := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewWebhookDeliveryHandler(runtime, policy, manager)}, reserved...)
			require.NoError(t, e)
			worker, e := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(clients.System, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 1000, PollInterval: time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), deliveryRegistry)
			require.NoError(t, e)
			require.NoError(t, worker.DispatchOnce(ctx))
			require.EqualValues(t, 2, sent.Load(), "real worker must dispatch each persisted target")
			for _, intent := range owner.OutboxEvent.Query().Where(outboxevent.EventTypeEQ(service.WebhookDeliveryRequestedEventType)).AllX(ctx) {
				require.Equal(t, "published", intent.Status)
			}
			require.NoError(t, worker.DispatchOnce(ctx))
			require.EqualValues(t, 2, sent.Load(), "published intents are not re-sent")
			for _, scenario := range []string{"redirect", "destination_changed", "during_send_rebind", "receipt_fault", "server_error", "authority_scope", "authority_notification", "authority_outbox", "authority_deployment", "resolver_transient", "resolver_canceled", "resolver_deadline"} {
				t.Run(scenario, func(t *testing.T) {
					ctx, tenantID, deliveryPolicy, deliveryClient, workerClient := ctx, tenant.ID, policy, runtime, clients.System
					mutable := scenario == "destination_changed" || scenario == "during_send_rebind"
					var redirected, attempted atomic.Int32
					var onSend func()
					otherEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(http.StatusOK) }))
					defer otherEndpoint.Close()
					redirectEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						attempted.Add(1)
						if onSend != nil {
							onSend()
						}
						if scenario == "redirect" {
							http.Redirect(w, r, otherEndpoint.URL, http.StatusTemporaryRedirect)
						} else if scenario == "server_error" {
							w.WriteHeader(http.StatusServiceUnavailable)
						} else {
							w.WriteHeader(http.StatusOK)
						}
					}))
					defer redirectEndpoint.Close()
					var sourceRow *ent.OutboxEvent
					var freshWorkItemID int
					if mutable {
						fixture := newStandardWebhookSourceFixture(t, ctx, standardWebhookClient(), "mutable-"+scenario)
						ctx, tenantID, deliveryPolicy, deliveryClient, workerClient = fixture.ctx, fixture.tenantID, fixture.policy, standardWebhookClient(), standardWebhookClient()
						freshWorkItemID, sourceRow = fixture.workItemID, fixture.source
					} else {
						freshRedirect, e := app.Create(ctx, identity, command("webhook-"+scenario, "generic"))
						require.NoError(t, e)
						freshWorkItemID = freshRedirect.WorkItemID
						owner.Ticket.UpdateOneID(freshWorkItemID).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(-time.Hour)).SetSLAResolutionDeadline(time.Now().Add(time.Hour)).SaveX(ctx)
						_, e = monitor.CheckSLAViolations(ctx, tenantID)
						require.NoError(t, e)
						sourceRow = owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(freshWorkItemID), outboxevent.EventTypeEQ("sla.breached")).OnlyX(ctx)
					}
					require.NoError(t, service.NewSLABreachDeliveryHandler().Deliver(ctx, sourceRow))
					captured := capture.events[len(capture.events)-1]
					raw, e := json.Marshal(captured)
					require.NoError(t, e)
					freshEnv := envelope
					execID := *envelope.Execution
					execID.WorkItemID = freshWorkItemID
					ref, e := deliveryPolicy.EventRef(tenantID)
					require.NoError(t, e)
					execID.DeploymentID, execID.ScopeID = ref.DeploymentID, ref.ScopeID
					freshEnv.TenantID = strconv.Itoa(tenantID)
					freshEnv.Execution = &execID
					freshEnv.EventID = sourceRow.EventID
					freshEnv.Payload = raw
					freshEnv.OccurredAt = captured.(interface{ OccurredAt() time.Time }).OccurredAt()
					var targetManager *connector.Manager
					if mutable {
						// Standard mutable-instance fixture preserves the original digest/generation defense tests.
						targetManager = connector.NewManager(registry, zap.NewNop().Sugar(), deliveryPolicy)
						require.NoError(t, targetManager.Provision(ctx, connector.Config{TenantID: tenantID, Name: "webhook", Provider: "redirect", Enabled: true, Settings: map[string]interface{}{"url": redirectEndpoint.URL}}))
						t.Cleanup(targetManager.CloseAll)
					} else {
						targetManager = candidateDeclaredManager(t, ctx, tenantID, scopeID, registry, candidateWebhookTarget("redirect", redirectEndpoint.URL))
					}
					require.NoError(t, service.NewWebhookEventSubscriber(targetManager, zap.NewNop().Sugar(), deliveryClient, deliveryPolicy).HandleContext(ctx, freshEnv))
					if strings.HasPrefix(scenario, "authority_") {
						// The producer committed an authorized intent. Independently replace
						// only the runtime target used by the real delivery worker.
						targetScope, targetDeployment, capability := scopeID, "intake-test", "webhook"
						switch scenario {
						case "authority_scope":
							targetScope = "149ff1af-a27c-47c7-827f-103271130bb9"
						case "authority_notification":
							capability = "notification"
						case "authority_outbox":
							capability = "outbox"
						case "authority_deployment":
							targetDeployment = "other-deployment"
						}
						declaration := candidateWebhookTarget("redirect", redirectEndpoint.URL)
						declaration.TenantID, declaration.ScopeID = tenantID, targetScope
						declaration.Capabilities = []string{capability}
						unrelatedPolicy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: targetDeployment, Scopes: []config.ExecutionScopeConfig{{TenantID: tenantID, ScopeID: targetScope}}, Capabilities: map[string]string{capability: "scoped"}, ConnectorTargets: []config.ConnectorTargetConfig{declaration}})
						require.NoError(t, err)
						targetManager = connector.NewManager(registry, zap.NewNop().Sugar(), unrelatedPolicy)
						defer targetManager.CloseAll()
						require.NoError(t, targetManager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:unrelated-worker-target", "activate only a loopback target without this intent authority")))
					}

					if strings.HasPrefix(scenario, "resolver_") {
						cause := errors.New("injected target resolver failure")
						if scenario == "resolver_canceled" {
							cause = context.Canceled
						}
						if scenario == "resolver_deadline" {
							cause = context.DeadlineExceeded
						}
						declaration := candidateWebhookTarget("redirect", redirectEndpoint.URL)
						declaration.TenantID, declaration.ScopeID = tenantID, scopeID
						p, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenantID, ScopeID: scopeID}}, Capabilities: map[string]string{"webhook": "scoped"}, ConnectorTargets: []config.ConnectorTargetConfig{declaration}})
						require.NoError(t, err)
						failingManager := connector.NewManager(registry, zap.NewNop().Sugar(), &webhookResolverFailureGate{ExecutionPolicy: p, err: cause})
						defer failingManager.CloseAll()
						require.NoError(t, failingManager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:resolver-failure", "activate a loopback target before injecting resolution failure")))
						repo := service.NewOutboxEventRepository(workerClient, deliveryPolicy)
						workerCtx := tenantctx.SystemContext(ctx, "outbox:poll", "test pre-send resolver failure")
						rows, err := repo.ClaimDueByEventType(workerCtx, time.Now(), 1000, service.WebhookDeliveryRequestedEventType, false)
						require.NoError(t, err)
						require.Len(t, rows, 1)
						intent := rows[0]
						require.Equal(t, freshWorkItemID, *intent.ExecutionWorkItemID)
						require.NoError(t, repo.MarkDeliveryAttemptStarted(workerCtx, intent.ID, intent.ClaimToken, intent.EventID))
						err = service.NewWebhookDeliveryHandler(deliveryClient, deliveryPolicy, failingManager).Deliver(ctx, intent)
						assert.ErrorIs(t, err, cause, "pre-send failure must retain its cause")
						assert.Zero(t, attempted.Load())
						assert.Zero(t, owner.AuditLog.Query().Where(auditlog.TenantID(tenantID), auditlog.OperationID("webhook_deliver:"+intent.EventID)).CountX(ctx))
						// Test-only cleanup of this directly claimed intent; no worker outcome is asserted.
						require.NoError(t, repo.MarkBlocked(workerCtx, intent.ID, intent.ClaimToken, "test-only direct delivery completed"))
						return
					}

					if scenario == "destination_changed" {
						require.NoError(t, targetManager.Provision(ctx, connector.Config{TenantID: tenantID, Name: "webhook", Provider: "redirect", Enabled: true, Settings: map[string]interface{}{"url": otherEndpoint.URL}}))
					}
					if scenario == "during_send_rebind" {
						onSend = func() {
							e := targetManager.Provision(ctx, connector.Config{TenantID: tenantID, Name: "webhook", Provider: "redirect", Enabled: true, Settings: map[string]interface{}{"url": otherEndpoint.URL}})
							if e != nil {
								panic(e)
							}
						}
					}
					receiptFaultActive := scenario == "receipt_fault"
					defer func() { receiptFaultActive = false }()
					deliveryClient.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
							value, e := next.Mutate(c, m)
							if e != nil {
								return value, e
							}
							if receiptFaultActive {
								if typed, ok := m.(*ent.AuditLogMutation); ok {
									action, _ := typed.Action()
									if action == "webhook.delivered" {
										return nil, errors.New("injected webhook delivery receipt failure")
									}
								}
							}
							return value, nil
						})
					})

					targetRegistry, e := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewWebhookDeliveryHandler(deliveryClient, deliveryPolicy, targetManager)}, reserved...)
					require.NoError(t, e)
					targetWorker, e := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(workerClient, deliveryPolicy), service.OutboxDeliveryWorkerConfig{BatchSize: 1000, PollInterval: time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), targetRegistry)
					require.NoError(t, e)
					require.NoError(t, targetWorker.DispatchOnce(ctx))
					require.Zero(t, redirected.Load(), "frozen endpoint must not redirect the payload")
					intent := owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(freshWorkItemID), outboxevent.EventTypeEQ(service.WebhookDeliveryRequestedEventType)).OnlyX(ctx)
					if strings.HasPrefix(scenario, "authority_") {
						assert.Equal(t, "blocked", intent.Status, "an unrelated declaration must not authorize a persisted intent")
						assert.Zero(t, attempted.Load(), "worker must reject before the local HTTP request")
						assert.Zero(t, owner.AuditLog.Query().Where(auditlog.TenantID(tenantID), auditlog.OperationID("webhook_deliver:"+intent.EventID)).CountX(ctx), "wrong target authority must not acquire a delivered receipt")
						attemptsBeforeReplay := attempted.Load()
						require.NoError(t, targetWorker.DispatchOnce(ctx))
						assert.Equal(t, attemptsBeforeReplay, attempted.Load(), "terminal delivery state must not cause another attempt")
						assert.Zero(t, owner.AuditLog.Query().Where(auditlog.TenantID(tenantID), auditlog.OperationID("webhook_deliver:"+intent.EventID)).CountX(ctx), "repeated rejected poll must not create a delivered receipt")
						return
					}
					require.Equal(t, "blocked", intent.Status)
					if scenario == "destination_changed" {
						require.NotContains(t, intent.LastError, "delivery_unknown:")
						require.Zero(t, attempted.Load())
					} else {
						require.Contains(t, intent.LastError, "delivery_unknown:")
						require.EqualValues(t, 1, attempted.Load())
					}
					initialAttempts := attempted.Load()
					require.NoError(t, targetWorker.DispatchOnce(ctx))
					require.Equal(t, initialAttempts, attempted.Load(), "blocked delivery must not be sent again")
					require.Zero(t, redirected.Load())
				})

			}
		})
		t.Run("stream consumer recovers committed audit before ack", func(t *testing.T) {
			streamCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			redisCfg, redisClient := startCandidateStreamRedis(t, streamCtx)
			redisCfg.EventStream = config.EventStreamConfig{ClaimIdle: 500 * time.Millisecond, ClaimInterval: 50 * time.Millisecond, NackDelay: 10 * time.Millisecond}
			_, err := redisClient.XAdd(streamCtx, &redis.XAddArgs{Stream: "sla.breached", Values: map[string]interface{}{"payload": "protected-history"}}).Result()
			require.NoError(t, err)
			require.NoError(t, redisClient.XGroupCreate(streamCtx, "sla.breached", "history", "0").Err())
			_, err = redisClient.XReadGroup(streamCtx, &redis.XReadGroupArgs{Group: "history", Consumer: "original", Streams: []string{"sla.breached", ">"}, Count: 1}).Result()
			require.NoError(t, err)
			protected := snapshotCandidateRedis(t, streamCtx, redisClient)
			owner.Ticket.UpdateOneID(fresh.WorkItemID).SetSLAResolutionDeadline(time.Now().Add(-time.Hour)).SaveX(ctx)
			_, err = monitor.CheckSLAViolations(ctx, tenant.ID)
			require.NoError(t, err)
			resolution := owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(fresh.WorkItemID), outboxevent.EventTypeEQ("sla.breached"), outboxevent.IDNEQ(row.ID)).OnlyX(ctx)
			require.NoError(t, service.NewSLABreachDeliveryHandler().Deliver(ctx, resolution))
			resolutionSource := capture.events[len(capture.events)-1]
			execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}}
			consumer, err := eventbus.NewWatermillEventBus(redisCfg, execution, authority, zap.NewNop().Sugar())
			require.NoError(t, err)
			defer consumer.Close()
			audit := service.NewEventAuditSubscriber(runtime, zap.NewNop().Sugar(), policy)
			committed := make(chan struct{}, 1)
			require.NoError(t, consumer.RegisterSubscription("sla.breached", &candidateCommitBeforeAck{audit: audit, committed: committed}))
			require.NoError(t, consumer.Start(streamCtx))
			before := owner.AuditLog.Query().CountX(ctx)
			require.NoError(t, consumer.Publish(resolutionSource))
			select {
			case <-committed:
			case <-streamCtx.Done():
				t.Fatal("audit did not commit before shutdown")
			}
			require.Equal(t, before+1, owner.AuditLog.Query().CountX(ctx))
			key := "candidate:intake-test:" + scopeID + ":sla.breached"
			pending, err := redisClient.XPendingExt(streamCtx, &redis.XPendingExtArgs{Stream: key, Group: "itsm:event_audit", Start: "-", End: "+", Count: 10}).Result()
			require.NoError(t, err)
			require.Len(t, pending, 1, "database commit has not acknowledged Redis")
			rows, err := redisClient.XRange(streamCtx, key, "-", "+").Result()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, rows[0].ID, pending[0].ID)
			var receiptBefore string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=0 AND operation_id=$2`, tenant.ID, "event_audit:"+resolution.EventID).Scan(&receiptBefore))
			require.NoError(t, consumer.Close())
			afterClose, err := redisClient.XPendingExt(streamCtx, &redis.XPendingExtArgs{Stream: key, Group: "itsm:event_audit", Start: "-", End: "+", Count: 10}).Result()
			require.NoError(t, err)
			require.Len(t, afterClose, 1, "closing the old consumer must leave the original entry unacknowledged")
			require.Equal(t, pending[0].ID, afterClose[0].ID)
			require.Equal(t, pending[0].Consumer, afterClose[0].Consumer)
			restarted, err := eventbus.NewWatermillEventBus(redisCfg, execution, authority, zap.NewNop().Sugar())
			require.NoError(t, err)
			defer restarted.Close()
			require.NoError(t, restarted.RegisterSubscription("sla.breached", audit))
			require.NoError(t, restarted.Start(streamCtx))
			require.Eventually(t, func() bool {
				p, e := redisClient.XPending(streamCtx, key, "itsm:event_audit").Result()
				return e == nil && p.Count == 0
			}, 5*time.Second, 20*time.Millisecond, "new consumer must claim and acknowledge the original entry")
			consumers, err := redisClient.XInfoConsumers(streamCtx, key, "itsm:event_audit").Result()
			require.NoError(t, err)
			require.Len(t, consumers, 2)
			require.NotEqual(t, consumers[0].Name, consumers[1].Name)
			var receiptAfter string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=0 AND operation_id=$2`, tenant.ID, "event_audit:"+resolution.EventID).Scan(&receiptAfter))
			require.JSONEq(t, receiptBefore, receiptAfter)
			require.Equal(t, before+1, owner.AuditLog.Query().CountX(ctx))
			afterRows, err := redisClient.XRange(streamCtx, key, "-", "+").Result()
			require.NoError(t, err)
			require.Equal(t, rows, afterRows, "restart must not republish or replace the original entry")
			afterRedis := snapshotCandidateRedis(t, streamCtx, redisClient)
			for key, value := range protected {
				require.Equal(t, value, afterRedis[key], "historical Redis key changed: %s", key)
			}
		})
		t.Run("webhook stream recovers committed intents before ack", func(t *testing.T) {
			streamCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			redisCfg, redisClient := startCandidateStreamRedis(t, streamCtx)
			redisCfg.EventStream = config.EventStreamConfig{ClaimIdle: 500 * time.Millisecond, ClaimInterval: 50 * time.Millisecond, NackDelay: 10 * time.Millisecond}
			_, err := redisClient.XAdd(streamCtx, &redis.XAddArgs{Stream: "sla.breached", Values: map[string]interface{}{"payload": "protected-history"}}).Result()
			require.NoError(t, err)
			require.NoError(t, redisClient.XGroupCreate(streamCtx, "sla.breached", "history", "0").Err())
			_, err = redisClient.XReadGroup(streamCtx, &redis.XReadGroupArgs{Group: "history", Consumer: "original", Streams: []string{"sla.breached", ">"}, Count: 1}).Result()
			require.NoError(t, err)
			protected := snapshotCandidateRedis(t, streamCtx, redisClient)
			freshWebhook, err := app.Create(ctx, identity, command("webhook-ack-recovery", "generic"))
			require.NoError(t, err)
			owner.Ticket.UpdateOneID(freshWebhook.WorkItemID).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(-time.Hour)).SetSLAResolutionDeadline(time.Now().Add(time.Hour)).SaveX(ctx)
			_, err = monitor.CheckSLAViolations(ctx, tenant.ID)
			require.NoError(t, err)
			resolution := owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(freshWebhook.WorkItemID), outboxevent.EventTypeEQ("sla.breached")).OnlyX(ctx)
			require.NoError(t, service.NewSLABreachDeliveryHandler().Deliver(ctx, resolution))
			resolutionSource := capture.events[len(capture.events)-1]
			var firstSent, secondSent atomic.Int32
			firstEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { firstSent.Add(1); w.WriteHeader(http.StatusNoContent) }))
			defer firstEndpoint.Close()
			secondEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { secondSent.Add(1); w.WriteHeader(http.StatusNoContent) }))
			defer secondEndpoint.Close()
			registry := connector.NewRegistry()
			registry.Register(func() connector.Connector { return webhookconnector.New() })
			targetExecution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}, Capabilities: map[string]string{"webhook": "scoped"}}
			for provider, endpoint := range map[string]string{"ack-first": firstEndpoint.URL, "ack-second": secondEndpoint.URL} {
				encoded, err := json.Marshal(endpoint)
				require.NoError(t, err)
				digest := sha256.Sum256(encoded)
				targetExecution.ConnectorTargets = append(targetExecution.ConnectorTargets, config.ConnectorTargetConfig{TenantID: tenant.ID, ScopeID: scopeID, Name: "webhook", Provider: provider,
					DestinationDigest: hex.EncodeToString(digest[:]), Capabilities: []string{"webhook"}, Settings: map[string]interface{}{"url": endpoint}})
			}
			targetPolicy, err := database.NewExecutionPolicy(targetExecution)
			require.NoError(t, err)
			manager := connector.NewManager(registry, zap.NewNop().Sugar(), targetPolicy)
			defer manager.CloseAll()
			require.NoError(t, manager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:declared-webhooks", "activate frozen loopback targets through the production owner")))
			execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}}
			consumer, err := eventbus.NewWatermillEventBus(redisCfg, execution, authority, zap.NewNop().Sugar())
			require.NoError(t, err)
			defer consumer.Close()
			audit := service.NewWebhookEventSubscriber(manager, zap.NewNop().Sugar(), runtime, policy)
			committed := make(chan struct{}, 1)
			require.NoError(t, consumer.RegisterSubscription("sla.breached", &candidateCommitBeforeAck{audit: audit, committed: committed}))
			require.NoError(t, consumer.Start(streamCtx))
			before := owner.AuditLog.Query().CountX(ctx)
			require.NoError(t, consumer.Publish(resolutionSource))
			select {
			case <-committed:
			case <-streamCtx.Done():
				t.Fatal("webhook intents did not commit before shutdown")
			}
			require.Equal(t, before+1, owner.AuditLog.Query().CountX(ctx))
			key := "candidate:intake-test:" + scopeID + ":sla.breached"
			pending, err := redisClient.XPendingExt(streamCtx, &redis.XPendingExtArgs{Stream: key, Group: "itsm:webhook", Start: "-", End: "+", Count: 10}).Result()
			require.NoError(t, err)
			require.Len(t, pending, 1, "database commit has not acknowledged Redis")
			rows, err := redisClient.XRange(streamCtx, key, "-", "+").Result()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, rows[0].ID, pending[0].ID)
			var receiptBefore string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=0 AND operation_id=$2`, tenant.ID, "webhook_consume:"+resolution.EventID).Scan(&receiptBefore))
			var intentsBefore string
			require.NoError(t, ownerDB.QueryRow(`SELECT coalesce(json_agg(e ORDER BY id),'[]')::text FROM outbox_events e WHERE execution_work_item_id=$1 AND event_type=$2`, freshWebhook.WorkItemID, service.WebhookDeliveryRequestedEventType).Scan(&intentsBefore))
			require.Equal(t, 2, owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(freshWebhook.WorkItemID), outboxevent.EventTypeEQ(service.WebhookDeliveryRequestedEventType)).CountX(ctx))
			require.Zero(t, firstSent.Load())
			require.Zero(t, secondSent.Load())
			require.NoError(t, consumer.Close())
			afterClose, err := redisClient.XPendingExt(streamCtx, &redis.XPendingExtArgs{Stream: key, Group: "itsm:webhook", Start: "-", End: "+", Count: 10}).Result()
			require.NoError(t, err)
			require.Len(t, afterClose, 1, "closing the old consumer must leave the original entry unacknowledged")
			require.Equal(t, pending[0].ID, afterClose[0].ID)
			require.Equal(t, pending[0].Consumer, afterClose[0].Consumer)
			restarted, err := eventbus.NewWatermillEventBus(redisCfg, execution, authority, zap.NewNop().Sugar())
			require.NoError(t, err)
			defer restarted.Close()
			require.NoError(t, restarted.RegisterSubscription("sla.breached", audit))
			require.NoError(t, restarted.Start(streamCtx))
			require.Eventually(t, func() bool {
				p, e := redisClient.XPending(streamCtx, key, "itsm:webhook").Result()
				return e == nil && p.Count == 0
			}, 5*time.Second, 20*time.Millisecond, "new consumer must claim and acknowledge the original entry")
			consumers, err := redisClient.XInfoConsumers(streamCtx, key, "itsm:webhook").Result()
			require.NoError(t, err)
			require.Len(t, consumers, 2)
			require.NotEqual(t, consumers[0].Name, consumers[1].Name)
			var receiptAfter string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=0 AND operation_id=$2`, tenant.ID, "webhook_consume:"+resolution.EventID).Scan(&receiptAfter))
			require.JSONEq(t, receiptBefore, receiptAfter)
			require.Equal(t, before+1, owner.AuditLog.Query().CountX(ctx))
			afterRows, err := redisClient.XRange(streamCtx, key, "-", "+").Result()
			require.NoError(t, err)
			require.Equal(t, rows, afterRows, "restart must not republish or replace the original entry")
			var intentsAfter string
			require.NoError(t, ownerDB.QueryRow(`SELECT coalesce(json_agg(e ORDER BY id),'[]')::text FROM outbox_events e WHERE execution_work_item_id=$1 AND event_type=$2`, freshWebhook.WorkItemID, service.WebhookDeliveryRequestedEventType).Scan(&intentsAfter))
			require.JSONEq(t, intentsBefore, intentsAfter, "redelivery must preserve the entire original intent set")
			require.Zero(t, firstSent.Load())
			require.Zero(t, secondSent.Load())
			reservedSet := map[string]bool{}
			for _, event := range owner.OutboxEvent.Query().AllX(ctx) {
				if event.EventType != service.WebhookDeliveryRequestedEventType {
					reservedSet[event.EventType] = true
				}
			}
			var reserved []string
			for kind := range reservedSet {
				reserved = append(reserved, kind)
			}
			deliveryRegistry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewWebhookDeliveryHandler(runtime, policy, manager)}, reserved...)
			require.NoError(t, err)
			worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(clients.System, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 1000, PollInterval: time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), deliveryRegistry)
			require.NoError(t, err)
			require.NoError(t, worker.DispatchOnce(ctx))
			require.EqualValues(t, 1, firstSent.Load())
			require.EqualValues(t, 1, secondSent.Load())
			for _, intent := range owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(freshWebhook.WorkItemID), outboxevent.EventTypeEQ(service.WebhookDeliveryRequestedEventType)).AllX(ctx) {
				require.Equal(t, "published", intent.Status)
				require.True(t, owner.AuditLog.Query().Where(auditlog.TenantIDEQ(tenant.ID), auditlog.OperationIDEQ("webhook_deliver:"+intent.EventID)).ExistX(ctx))
			}
			require.NoError(t, worker.DispatchOnce(ctx))
			require.EqualValues(t, 1, firstSent.Load())
			require.EqualValues(t, 1, secondSent.Load())
			afterRedis := snapshotCandidateRedis(t, streamCtx, redisClient)
			for key, value := range protected {
				require.Equal(t, value, afterRedis[key], "historical Redis key changed: %s", key)
			}
		})
		t.Run("standard webhook uses durable owner and recovers before ack", func(t *testing.T) {
			fixture := newStandardWebhookSourceFixture(t, ctx, standardWebhookClient(), "ack-recovery")
			ctx := fixture.ctx
			tenantID := fixture.tenantID
			standard := fixture.policy

			streamCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			redisCfg, redisClient := startCandidateStreamRedis(t, streamCtx)
			redisCfg.EventStream = config.EventStreamConfig{ClaimIdle: 500 * time.Millisecond, ClaimInterval: 50 * time.Millisecond, NackDelay: 10 * time.Millisecond}
			_, err := redisClient.XAdd(streamCtx, &redis.XAddArgs{Stream: "protected.legacy", Values: map[string]interface{}{"payload": "protected-history"}}).Result()
			require.NoError(t, err)
			require.NoError(t, redisClient.XGroupCreate(streamCtx, "protected.legacy", "history", "0").Err())
			_, err = redisClient.XReadGroup(streamCtx, &redis.XReadGroupArgs{Group: "history", Consumer: "original", Streams: []string{"protected.legacy", ">"}, Count: 1}).Result()
			require.NoError(t, err)
			protected := snapshotCandidateRedis(t, streamCtx, redisClient)
			resolution := fixture.source
			require.NoError(t, service.NewSLABreachDeliveryHandler().Deliver(ctx, resolution))
			resolutionSource := capture.events[len(capture.events)-1]
			var firstSent, secondSent atomic.Int32
			firstEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { firstSent.Add(1); w.WriteHeader(http.StatusNoContent) }))
			defer firstEndpoint.Close()
			secondEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { secondSent.Add(1); w.WriteHeader(http.StatusNoContent) }))
			defer secondEndpoint.Close()
			registry := connector.NewRegistry()
			registry.Register(func() connector.Connector { return webhookconnector.New() })
			manager := connector.NewManager(registry, zap.NewNop().Sugar(), standard)
			defer manager.CloseAll()
			for provider, endpoint := range map[string]string{"ack-first": firstEndpoint.URL, "ack-second": secondEndpoint.URL} {
				require.NoError(t, manager.Provision(ctx, connector.Config{TenantID: tenantID, Name: "webhook", Provider: provider, Enabled: true, Settings: map[string]interface{}{"url": endpoint}}))
			}
			execution := config.ExecutionConfig{Mode: "standard", DeploymentID: "standard-webhook-test", Capabilities: map[string]string{"webhook": "enabled", "outbox": "enabled"}}
			standardAuthority := service.NewExecutionEventAuthority(standardWebhookClient(), standard)
			consumer, err := eventbus.NewWatermillEventBus(redisCfg, execution, standardAuthority, zap.NewNop().Sugar())
			require.NoError(t, err)
			defer consumer.Close()
			audit := service.NewWebhookEventSubscriber(manager, zap.NewNop().Sugar(), standardWebhookClient(), standard)
			committed := make(chan struct{}, 1)
			require.NoError(t, consumer.RegisterSubscription("sla.breached", &candidateCommitBeforeAck{audit: audit, committed: committed}))
			require.NoError(t, consumer.Start(streamCtx))
			before := owner.AuditLog.Query().CountX(ctx)
			require.NoError(t, consumer.Publish(resolutionSource))
			select {
			case <-committed:
			case <-streamCtx.Done():
				t.Fatal("webhook intents did not commit before shutdown")
			}
			require.Equal(t, before+1, owner.AuditLog.Query().CountX(ctx))
			key := "sla.breached"
			pending, err := redisClient.XPendingExt(streamCtx, &redis.XPendingExtArgs{Stream: key, Group: "itsm:webhook", Start: "-", End: "+", Count: 10}).Result()
			require.NoError(t, err)
			require.Len(t, pending, 1, "database commit has not acknowledged Redis")
			rows, err := redisClient.XRange(streamCtx, key, "-", "+").Result()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, rows[0].ID, pending[0].ID)
			var receiptBefore string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=0 AND operation_id=$2`, tenantID, "webhook_consume:"+resolution.EventID).Scan(&receiptBefore))
			var intentsBefore string
			require.NoError(t, ownerDB.QueryRow(`SELECT coalesce(json_agg(e ORDER BY id),'[]')::text FROM outbox_events e WHERE execution_work_item_id=$1 AND event_type=$2`, fixture.workItemID, service.WebhookDeliveryRequestedEventType).Scan(&intentsBefore))
			require.Equal(t, 2, owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(fixture.workItemID), outboxevent.EventTypeEQ(service.WebhookDeliveryRequestedEventType)).CountX(ctx))
			require.Zero(t, firstSent.Load())
			require.Zero(t, secondSent.Load())
			require.NoError(t, consumer.Close())
			afterClose, err := redisClient.XPendingExt(streamCtx, &redis.XPendingExtArgs{Stream: key, Group: "itsm:webhook", Start: "-", End: "+", Count: 10}).Result()
			require.NoError(t, err)
			require.Len(t, afterClose, 1, "closing the old consumer must leave the original entry unacknowledged")
			require.Equal(t, pending[0].ID, afterClose[0].ID)
			require.Equal(t, pending[0].Consumer, afterClose[0].Consumer)
			restarted, err := eventbus.NewWatermillEventBus(redisCfg, execution, standardAuthority, zap.NewNop().Sugar())
			require.NoError(t, err)
			defer restarted.Close()
			require.NoError(t, restarted.RegisterSubscription("sla.breached", audit))
			require.NoError(t, restarted.Start(streamCtx))
			require.Eventually(t, func() bool {
				p, e := redisClient.XPending(streamCtx, key, "itsm:webhook").Result()
				return e == nil && p.Count == 0
			}, 5*time.Second, 20*time.Millisecond, "new consumer must claim and acknowledge the original entry")
			consumers, err := redisClient.XInfoConsumers(streamCtx, key, "itsm:webhook").Result()
			require.NoError(t, err)
			require.Len(t, consumers, 2)
			require.NotEqual(t, consumers[0].Name, consumers[1].Name)
			var receiptAfter string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=0 AND operation_id=$2`, tenantID, "webhook_consume:"+resolution.EventID).Scan(&receiptAfter))
			require.JSONEq(t, receiptBefore, receiptAfter)
			require.Equal(t, before+1, owner.AuditLog.Query().CountX(ctx))
			afterRows, err := redisClient.XRange(streamCtx, key, "-", "+").Result()
			require.NoError(t, err)
			require.Equal(t, rows, afterRows, "restart must not republish or replace the original entry")
			var intentsAfter string
			require.NoError(t, ownerDB.QueryRow(`SELECT coalesce(json_agg(e ORDER BY id),'[]')::text FROM outbox_events e WHERE execution_work_item_id=$1 AND event_type=$2`, fixture.workItemID, service.WebhookDeliveryRequestedEventType).Scan(&intentsAfter))
			require.JSONEq(t, intentsBefore, intentsAfter, "redelivery must preserve the entire original intent set")
			require.Zero(t, firstSent.Load())
			require.Zero(t, secondSent.Load())
			reservedSet := map[string]bool{}
			for _, event := range owner.OutboxEvent.Query().AllX(ctx) {
				if event.EventType != service.WebhookDeliveryRequestedEventType {
					reservedSet[event.EventType] = true
				}
			}
			var reserved []string
			for kind := range reservedSet {
				reserved = append(reserved, kind)
			}
			deliveryRegistry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewWebhookDeliveryHandler(standardWebhookClient(), standard, manager)}, reserved...)
			require.NoError(t, err)
			worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(standardWebhookClient(), standard), service.OutboxDeliveryWorkerConfig{BatchSize: 1000, PollInterval: time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), deliveryRegistry)
			require.NoError(t, err)
			require.NoError(t, worker.DispatchOnce(ctx))
			require.EqualValues(t, 1, firstSent.Load())
			require.EqualValues(t, 1, secondSent.Load())
			for _, intent := range owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(fixture.workItemID), outboxevent.EventTypeEQ(service.WebhookDeliveryRequestedEventType)).AllX(ctx) {
				require.Equal(t, "published", intent.Status)
				require.True(t, owner.AuditLog.Query().Where(auditlog.TenantIDEQ(tenantID), auditlog.OperationIDEQ("webhook_deliver:"+intent.EventID)).ExistX(ctx))
			}
			require.NoError(t, worker.DispatchOnce(ctx))
			require.EqualValues(t, 1, firstSent.Load())
			require.EqualValues(t, 1, secondSent.Load())
			afterRedis := snapshotCandidateRedis(t, streamCtx, redisClient)
			for key, value := range protected {
				require.Equal(t, value, afterRedis[key], "historical Redis key changed: %s", key)
			}
		})

		t.Run("event audit deduplicates persistent delivery", func(t *testing.T) {
			wire, err := json.Marshal(envelope)
			require.NoError(t, err)
			var delivered map[string]interface{}
			require.NoError(t, json.Unmarshal(wire, &delivered))
			audit := service.NewEventAuditSubscriber(runtime, zap.NewNop().Sugar(), policy)
			before := owner.AuditLog.Query().CountX(ctx)
			require.Error(t, audit.Handle(delivered), "raw map cannot bypass candidate envelope validation")
			fault := true
			insertedBeforeFault := false
			racing := false
			arrived := make(chan struct{}, 2)
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			runtime.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(hookCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
					m, ok := mutation.(*ent.AuditLogMutation)
					operation, has := "", false
					if ok {
						operation, has = m.OperationID()
					}
					matches := has && operation == "event_audit:"+envelope.EventID
					if matches && racing {
						arrived <- struct{}{}
						select {
						case <-release:
						case <-hookCtx.Done():
							return nil, hookCtx.Err()
						}
					}
					value, err := next.Mutate(hookCtx, mutation)
					if err == nil && matches && fault {
						insertedBeforeFault = true
						return nil, errors.New("audit receipt post-insert fault")
					}
					return value, err
				})
			})
			require.ErrorContains(t, audit.Handle(envelope), "post-insert fault")
			require.True(t, insertedBeforeFault)
			require.Equal(t, before, owner.AuditLog.Query().CountX(ctx), "failed insert transaction must leave no receipt")
			fault = false
			racing = true
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				go func() { results <- audit.HandleContext(ctx, envelope) }()
			}
			for i := 0; i < 2; i++ {
				select {
				case <-arrived:
				case <-time.After(5 * time.Second):
					t.Fatal("both deliveries must reach INSERT without a receipt")
				}
			}
			releaseOnce.Do(func() { close(release) })
			successes, conflicts := 0, 0
			for i := 0; i < 2; i++ {
				select {
				case err := <-results:
					if err == nil {
						successes++
					} else {
						var pg *pq.Error
						require.ErrorAs(t, err, &pg)
						require.Equal(t, pq.ErrorCode("23505"), pg.Code)
						conflicts++
					}
				case <-time.After(5 * time.Second):
					t.Fatal("audit delivery did not complete")
				}
			}
			racing = false
			require.Equal(t, 1, successes)
			require.Equal(t, 1, conflicts)
			require.Equal(t, before+1, owner.AuditLog.Query().CountX(ctx))
			var receiptBefore string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=0 AND operation_id=$2`, tenant.ID, "event_audit:"+envelope.EventID).Scan(&receiptBefore))
			require.NoError(t, audit.Handle(envelope))
			require.Equal(t, before+1, owner.AuditLog.Query().CountX(ctx), "duplicate event must reuse the original audit receipt")
			changed := envelope
			changed.Payload = json.RawMessage(strings.Replace(string(changed.Payload), "response", "resolve", 1))
			require.Error(t, audit.Handle(changed), "receipt cannot authorize changed event facts")
			_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
			require.NoError(t, err)
			require.Error(t, audit.Handle(envelope), "current scope is checked even for replay")
			_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
			require.NoError(t, err)
			require.NoError(t, audit.Handle(envelope))
			var receiptAfter string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=0 AND operation_id=$2`, tenant.ID, "event_audit:"+envelope.EventID).Scan(&receiptAfter))
			require.JSONEq(t, receiptBefore, receiptAfter)
			require.Equal(t, before+1, owner.AuditLog.Query().CountX(ctx))
		})
	})

	t.Run("real SLA scan preserves historical violations", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("sla-scan-member", "generic"))
		require.NoError(t, err)
		owner.Ticket.UpdateOneID(fresh.WorkItemID).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(-time.Hour)).SetSLAResolutionDeadline(time.Now().Add(-time.Hour)).SaveX(ctx)
		var before string
		require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(json_agg(v ORDER BY id)::text,'[]') FROM sla_violations v WHERE ticket_id=$1`, historical.WorkItemID).Scan(&before))
		monitor := service.NewSLAMonitorService(runtime, zap.NewNop().Sugar(), policy)
		monitor.SetNotificationService(newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy))
		stats, err := monitor.CheckSLAViolations(ctx, tenant.ID)
		require.NoError(t, err)
		require.GreaterOrEqual(t, stats.NewViolations, 2, "new member must still be monitored")
		var after string
		require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(json_agg(v ORDER BY id)::text,'[]') FROM sla_violations v WHERE ticket_id=$1`, historical.WorkItemID).Scan(&after))
		assert.JSONEq(t, before, after, "SLA scan created violations for historical work")
		var created int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM sla_violations WHERE ticket_id=$1`, fresh.WorkItemID).Scan(&created))
		require.Equal(t, 2, created)
		var queued int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM outbox_events WHERE execution_work_item_id=$1 AND event_type='sla.breached'`, fresh.WorkItemID).Scan(&queued))
		require.Equal(t, 2, queued, "every committed violation needs a durable event")
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`, fresh.WorkItemID).Scan(&queued))
		require.Equal(t, 4, queued, "in-app and deferred email for each breach")
		version := owner.Ticket.GetX(ctx, fresh.WorkItemID).Version
		replay, err := monitor.CheckSLAViolations(ctx, tenant.ID)
		require.NoError(t, err)
		require.Zero(t, replay.NewViolations)
		require.Equal(t, version, owner.Ticket.GetX(ctx, fresh.WorkItemID).Version, "duplicate scan must not mutate version")
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`, fresh.WorkItemID).Scan(&queued))
		require.Equal(t, 4, queued)
		for _, fault := range []string{"notification", "second outbox"} {
			t.Run(fault+" rolls back complete SLA transaction", func(t *testing.T) {
				target, err := app.Create(ctx, identity, command("sla-fault-"+fault, "generic"))
				require.NoError(t, err)
				original := owner.Ticket.UpdateOneID(target.WorkItemID).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(-time.Hour)).SetSLAResolutionDeadline(time.Now().Add(-time.Hour)).SaveX(ctx)
				injected := errors.New("injected SLA transaction failure")
				active := true
				writes := 0
				hook := func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						value, e := next.Mutate(ctx, m)
						if e == nil && active {
							writes++
							if fault == "notification" || writes == 2 {
								return nil, injected
							}
						}
						return value, e
					})
				}
				if fault == "notification" {
					runtime.Notification.Use(hook)
				} else {
					runtime.OutboxEvent.Use(hook)
				}
				_, err = monitor.CheckSLAViolations(ctx, tenant.ID)
				active = false
				require.ErrorIs(t, err, injected)
				if fault == "notification" {
					require.Equal(t, 1, writes)
				} else {
					require.Equal(t, 2, writes)
				}
				require.Equal(t, original.Version, owner.Ticket.GetX(ctx, target.WorkItemID).Version)
				for _, query := range []string{
					`SELECT count(*) FROM sla_violations WHERE ticket_id=$1`,
					`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`,
					`SELECT count(*) FROM outbox_events WHERE execution_work_item_id=$1 AND event_type='sla.breached'`,
				} {
					var count int
					require.NoError(t, ownerDB.QueryRow(query, target.WorkItemID).Scan(&count))
					require.Zero(t, count)
				}
				stats, err := monitor.CheckSLAViolations(ctx, tenant.ID)
				require.NoError(t, err)
				require.Equal(t, 2, stats.NewViolations)
			})
		}
		target, err := app.Create(ctx, identity, command("sla-concurrent", "generic"))
		require.NoError(t, err)
		owner.Ticket.UpdateOneID(target.WorkItemID).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(-time.Hour)).SetSLAResolutionDeadline(time.Now().Add(-time.Hour)).SaveX(ctx)
		beforeVersion := owner.Ticket.GetX(ctx, target.WorkItemID).Version
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
		require.NoError(t, err)
		_, denied := monitor.CheckSLAViolations(ctx, tenant.ID)
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
		require.NoError(t, err)
		require.ErrorIs(t, denied, executionscope.ErrDenied)
		require.Equal(t, beforeVersion, owner.Ticket.GetX(ctx, target.WorkItemID).Version)
		ready := make(chan struct{})
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() { <-ready; _, e := monitor.CheckSLAViolations(ctx, tenant.ID); results <- e }()
		}
		close(ready)
		first, second := <-results, <-results
		require.True(t, first == nil || second == nil, "at least one competing scan must commit: %v / %v", first, second)
		for _, e := range []error{first, second} {
			if e != nil {
				require.Contains(t, e.Error(), "SLA cycle changed during scan")
			}
		}
		require.Equal(t, beforeVersion+1, owner.Ticket.GetX(ctx, target.WorkItemID).Version)
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM sla_violations WHERE ticket_id=$1`, target.WorkItemID).Scan(&queued))
		require.Equal(t, 2, queued)
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM outbox_events WHERE execution_work_item_id=$1 AND event_type='sla.breached'`, target.WorkItemID).Scan(&queued))
		require.Equal(t, 2, queued)
		t.Logf("new member violations=%d; historical preservation checked independently", created)
	})
	t.Run("SLA alert direct entries preserve historical rows", func(t *testing.T) {
		alerts := service.NewSLAAlertService(runtime, zap.NewNop().Sugar(), policy)
		alerts.SetNotificationService(newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy))
		entries := []struct {
			name string
			run  func(int) (bool, error)
		}{
			{"scan", func(id int) (bool, error) { return alerts.CheckAndTriggerAlerts(ctx, id, tenant.ID) }},
			{"warning", func(id int) (bool, error) { return alerts.TriggerSLAWarning(ctx, id, "response_time", tenant.ID) }},
		}
		for i, entry := range entries {
			t.Run(entry.name, func(t *testing.T) {
				var before, after string
				require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(json_agg(h ORDER BY id)::text,'[]') FROM sla_alert_histories h WHERE ticket_id=$1`, historicalAlertItems[i]).Scan(&before))
				_, err := entry.run(historicalAlertItems[i])
				assert.ErrorIs(t, err, executionscope.ErrDenied, "historical direct entry must reject")
				require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(json_agg(h ORDER BY id)::text,'[]') FROM sla_alert_histories h WHERE ticket_id=$1`, historicalAlertItems[i]).Scan(&after))
				assert.JSONEq(t, before, after, "historical alert history must remain unchanged")
				fresh, err := app.Create(ctx, identity, command("sla-alert-"+entry.name, "generic"))
				require.NoError(t, err)
				owner.Ticket.UpdateOneID(fresh.WorkItemID).SetCreatedAt(time.Now().Add(-time.Hour)).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(5 * time.Minute)).SaveX(ctx)
				triggered, err := entry.run(fresh.WorkItemID)
				require.NoError(t, err)
				require.True(t, triggered, "new candidate must execute the alert")
				var histories, sent, pending int
				require.NoError(t, ownerDB.QueryRow(`SELECT count(*),count(*) FILTER (WHERE notification_sent) FROM sla_alert_histories WHERE ticket_id=$1`, fresh.WorkItemID).Scan(&histories, &sent))
				require.Equal(t, 1, histories)
				assert.Zero(t, sent, "unconfigured email transport must not be reported sent")
				require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1 AND channel='email' AND status='pending'`, fresh.WorkItemID).Scan(&pending))
				assert.Equal(t, 1, pending, "alert must commit a deferred email intent")
				version := owner.Ticket.GetX(ctx, fresh.WorkItemID).Version
				triggered, err = entry.run(fresh.WorkItemID)
				require.NoError(t, err)
				require.False(t, triggered)
				require.Equal(t, version, owner.Ticket.GetX(ctx, fresh.WorkItemID).Version)
				for _, fault := range []string{"history", "notification"} {
					target, err := app.Create(ctx, identity, command("sla-alert-fault-"+entry.name+fault, "generic"))
					require.NoError(t, err)
					beforeItem := owner.Ticket.UpdateOneID(target.WorkItemID).SetCreatedAt(time.Now().Add(-time.Hour)).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(5 * time.Minute)).SaveX(ctx)
					active := true
					writes := 0
					injected := errors.New("injected SLA alert write failure")
					hook := func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
							v, e := next.Mutate(ctx, m)
							if active && e == nil {
								writes++
								return nil, injected
							}
							return v, e
						})
					}
					if fault == "history" {
						runtime.SLAAlertHistory.Use(hook)
					} else {
						runtime.Notification.Use(hook)
					}
					unifiedBefore := owner.Notification.Query().CountX(ctx)
					triggered, err = entry.run(target.WorkItemID)
					active = false
					require.Equal(t, unifiedBefore, owner.Notification.Query().CountX(ctx), "unified notifications roll back with alert")
					require.ErrorIs(t, err, injected)
					require.False(t, triggered)
					require.Equal(t, 1, writes)
					require.Equal(t, beforeItem.Version, owner.Ticket.GetX(ctx, target.WorkItemID).Version)
					for _, query := range []string{`SELECT count(*) FROM sla_alert_histories WHERE ticket_id=$1`, `SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`} {
						var n int
						require.NoError(t, ownerDB.QueryRow(query, target.WorkItemID).Scan(&n))
						require.Zero(t, n)
					}
					triggered, err = entry.run(target.WorkItemID)
					require.NoError(t, err)
					require.True(t, triggered)
				}

			})
		}
	})
	t.Run("SLA alert channels and active cycle", func(t *testing.T) {
		notifications := newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy)
		notifications.SetNotificationPreferenceService(service.NewNotificationPreferenceService(runtime, zap.NewNop().Sugar()))
		pref := owner.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetEventType("sla_violated").SetEmailEnabled(false).SetInAppEnabled(true).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
		defer owner.NotificationPreference.DeleteOneID(pref.ID).Exec(ctx)
		alerts := service.NewSLAAlertService(runtime, zap.NewNop().Sugar(), policy)
		alerts.SetNotificationService(notifications)
		for _, tc := range []struct {
			name              string
			channels          []string
			elapsed, timeLeft time.Duration
			paused            int
			want              bool
			notifications     int
		}{
			{"empty", []string{}, 55 * time.Minute, 5 * time.Minute, 0, true, 0},
			{"email disabled by user", []string{"email"}, 55 * time.Minute, 5 * time.Minute, 0, true, 0},
			{"in app", []string{"in_app"}, 55 * time.Minute, 5 * time.Minute, 0, true, 1},
			{"reopened new cycle", []string{"in_app"}, 10 * time.Minute, 50 * time.Minute, 0, false, 0},
			{"paused early", []string{"in_app"}, 90 * time.Minute, 30 * time.Minute, 60, false, 0},
			{"paused warning", []string{"in_app"}, 110 * time.Minute, 10 * time.Minute, 60, true, 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				definition := owner.SLADefinition.Create().SetName(tc.name).SetTenantID(tenant.ID).SetResponseTime(60).SetResolutionTime(240).SaveX(ctx)
				owner.SLAAlertRule.Create().SetName(tc.name).SetTenantID(tenant.ID).SetSLADefinitionID(definition.ID).SetThresholdPercentage(20).SetNotificationChannels(tc.channels).SaveX(ctx)
				fresh, err := app.Create(ctx, identity, command("sla-active-"+tc.name, "generic"))
				require.NoError(t, err)
				now := time.Now()
				owner.Ticket.UpdateOneID(fresh.WorkItemID).SetCreatedAt(now.Add(-24 * time.Hour)).SetSLACycleNumber(2).SetSLACycleStartedAt(now.Add(-tc.elapsed)).SetSLAPausedMinutes(tc.paused).SetSLADefinitionID(definition.ID).SetSLAResponseDeadline(now.Add(tc.timeLeft)).SaveX(ctx)
				triggered, err := alerts.TriggerSLAWarning(ctx, fresh.WorkItemID, "response_time", tenant.ID)
				require.NoError(t, err)
				require.Equal(t, tc.want, triggered)
				var n int
				require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`, fresh.WorkItemID).Scan(&n))
				require.Equal(t, tc.notifications, n)
				if tc.want {
					id := fresh.WorkItemID
					rows, total, err := alerts.GetAlertHistory(ctx, &dto.GetSLAAlertHistoryRequest{TicketID: &id}, tenant.ID)
					require.NoError(t, err)
					require.Equal(t, 1, total)
					require.Len(t, rows, 1)
					require.Equal(t, tc.notifications > 0, rows[0].NotificationSent, "SLA status must reflect actual in-app delivery")
				}

			})
		}
	})
	t.Run("real candidate SLA monitor includes alerts", func(t *testing.T) {
		alerts := service.NewSLAAlertService(runtime, zap.NewNop().Sugar(), policy)
		notifier := newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy)
		alerts.SetNotificationService(notifier)
		monitor := service.NewSLAMonitorService(runtime, zap.NewNop().Sugar(), policy)
		monitor.SetNotificationService(notifier)
		monitor.SetAlertService(alerts)
		fresh, err := app.Create(ctx, identity, command("sla-complete-monitor", "generic"))
		require.NoError(t, err)
		owner.Ticket.UpdateOneID(fresh.WorkItemID).SetCreatedAt(time.Now().Add(-time.Hour)).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(5 * time.Minute)).SaveX(ctx)
		var historyBefore string
		require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(h) ORDER BY id)::text,'[]') FROM sla_alert_histories h WHERE ticket_id IN ($1,$2)`, historicalAlertItems[0], historicalAlertItems[1]).Scan(&historyBefore))
		stats, err := monitor.CheckSLAViolations(ctx, tenant.ID)
		require.NoError(t, err)
		require.Greater(t, stats.WarningsTriggered+stats.AlertsTriggered, 0)
		rows, _, err := alerts.GetAlertHistory(ctx, &dto.GetSLAAlertHistoryRequest{TicketID: &fresh.WorkItemID}, tenant.ID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.False(t, rows[0].NotificationSent)
		var historyAfter string
		require.NoError(t, ownerDB.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(h) ORDER BY id)::text,'[]') FROM sla_alert_histories h WHERE ticket_id IN ($1,$2)`, historicalAlertItems[0], historicalAlertItems[1]).Scan(&historyAfter))
		require.JSONEq(t, historyBefore, historyAfter)
	})
	t.Run("SLA notification provenance and projection", func(t *testing.T) {
		alerts := service.NewSLAAlertService(runtime, zap.NewNop().Sugar(), policy)
		legacyRows, _, err := alerts.GetAlertHistory(ctx, &dto.GetSLAAlertHistoryRequest{TicketID: &legacySLAHistory.TicketID}, tenant.ID)
		require.NoError(t, err)
		require.Len(t, legacyRows, 1)
		require.True(t, legacyRows[0].NotificationSent, "restored historical fact stays unchanged")
		var notificationID, historyID, workItemID int
		require.NoError(t, ownerDB.QueryRow(`SELECT id,sla_alert_history_id,ticket_id FROM ticket_notifications WHERE sla_alert_history_id IS NOT NULL AND channel='email' ORDER BY id LIMIT 1`).Scan(&notificationID, &historyID, &workItemID))
		workerPolicy, policyErr := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}, Capabilities: map[string]string{"notification": "scoped"}})
		require.NoError(t, policyErr)
		worker := service.NewTicketNotificationService(runtime, zap.NewNop().Sugar(), workerPolicy)
		worker.SetDeliveryQueueClient(clients.System)
		_, deliveryErr := worker.ProcessPendingDeliveries(context.Background(), "sla-linked-notification-worker", 1000)
		require.Error(t, deliveryErr, "missing email provider is explicit")
		require.Equal(t, "failed", owner.TicketNotification.GetX(ctx, notificationID).Status)
		projected, _, err := alerts.GetAlertHistory(ctx, &dto.GetSLAAlertHistoryRequest{TicketID: &workItemID}, tenant.ID)
		require.NoError(t, err)
		require.False(t, projected[0].NotificationSent)
		for _, state := range []string{"pending", "processing", "sent", "read", "failed"} {
			_, err = ownerDB.ExecContext(ctx, `UPDATE ticket_notifications SET status=$1 WHERE id=$2`, state, notificationID)
			require.NoError(t, err)
			rows, _, err := alerts.GetAlertHistory(ctx, &dto.GetSLAAlertHistoryRequest{TicketID: &workItemID}, tenant.ID)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, state == "sent" || state == "read", rows[0].NotificationSent)
			var raw bool
			require.NoError(t, ownerDB.QueryRow(`SELECT notification_sent FROM sla_alert_histories WHERE id=$1`, historyID).Scan(&raw))
			require.False(t, raw, "worker state is the only mutable authority")
		}
		for _, query := range []string{
			`UPDATE ticket_notifications SET sla_alert_history_id=NULL WHERE id=$1`,
			`UPDATE ticket_notifications SET sla_alert_history_id=sla_alert_history_id+1 WHERE id=$1`,
			`UPDATE ticket_notifications SET ticket_id=ticket_id+1 WHERE id=$1`,
			`UPDATE ticket_notifications SET tenant_id=tenant_id+1 WHERE id=$1`,
		} {
			_, err := ownerDB.ExecContext(ctx, query, notificationID)
			require.Error(t, err)
		}
		_, err = ownerDB.ExecContext(ctx, `UPDATE ticket_notifications SET sla_alert_history_id=$1 WHERE id=$2`, historyID, historicalNotifications[0].ID)
		require.Error(t, err)
		for _, query := range []string{
			`UPDATE sla_alert_histories SET notification_tracking_version=NULL WHERE id=$1`,
			`UPDATE sla_alert_histories SET notification_tracking_version=2 WHERE id=$1`,
			`UPDATE sla_alert_histories SET notification_sent=true WHERE id=$1`,
		} {
			_, err := ownerDB.ExecContext(ctx, query, historyID)
			require.Error(t, err)
		}
		_, err = ownerDB.ExecContext(ctx, `UPDATE sla_alert_histories SET notification_tracking_version=1 WHERE id=$1`, legacySLAHistory.ID)
		require.Error(t, err)
		_, err = ownerDB.ExecContext(ctx, `INSERT INTO ticket_notifications(sla_alert_history_id,tenant_id,ticket_id,user_id,type,channel,content,status,created_at,attempt_count,next_attempt_at) VALUES($1,$2,$3,$4,'sla_violated','email','legacy invalid link','pending',NOW(),0,NOW())`, legacySLAHistory.ID, tenant.ID, legacySLAHistory.TicketID, actor.ID)
		require.Error(t, err, "legacy history cannot own newly linked deliveries")
		_, err = ownerDB.ExecContext(ctx, `INSERT INTO sla_alert_histories(notification_tracking_version,ticket_id,ticket_number,ticket_title,alert_rule_id,alert_rule_name,alert_level,threshold_percentage,actual_percentage,notification_sent,escalation_level,tenant_id,created_at) SELECT 2,ticket_id,ticket_number,ticket_title,alert_rule_id,alert_rule_name,alert_level,threshold_percentage,actual_percentage,false,escalation_level,tenant_id,NOW() FROM sla_alert_histories WHERE id=$1`, historyID)
		var versionError *pq.Error
		require.ErrorAs(t, err, &versionError)
		require.Equal(t, "23514", string(versionError.Code))
		for _, invalid := range []struct{ history, tenant, ticket int }{{historyID, tenant.ID, historical.WorkItemID}, {historyID, tenant.ID + 1, workItemID}, {historyID + 1000000, tenant.ID, workItemID}} {
			_, err := ownerDB.ExecContext(ctx, `INSERT INTO ticket_notifications(sla_alert_history_id,tenant_id,ticket_id,user_id,type,channel,content,status,created_at,attempt_count,next_attempt_at) VALUES($1,$2,$3,$4,'sla_violated','email','invalid reference','pending',NOW(),0,NOW())`, invalid.history, invalid.tenant, invalid.ticket, actor.ID)
			require.Error(t, err, "reference guard rejects invalid owner")
			// Independently exercise the FK with only its companion user trigger
			// disabled inside this private transaction; rollback restores it.
			probe, err := ownerDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			_, err = probe.ExecContext(ctx, `ALTER TABLE ticket_notifications DISABLE TRIGGER sla_alert_delivery_reference_immutable`)
			require.NoError(t, err)
			_, fkErr := probe.ExecContext(ctx, `INSERT INTO ticket_notifications(sla_alert_history_id,tenant_id,ticket_id,user_id,type,channel,content,status,created_at,attempt_count,next_attempt_at) VALUES($1,$2,$3,$4,'sla_violated','email','invalid reference','pending',NOW(),0,NOW())`, invalid.history, invalid.tenant, invalid.ticket, actor.ID)
			require.NoError(t, probe.Rollback())
			var foreignKeyError *pq.Error
			require.ErrorAs(t, fkErr, &foreignKeyError)
			require.Equal(t, "23503", string(foreignKeyError.Code))

		}
	})
	t.Run("matrix escalation preserves historical alerts", func(t *testing.T) {
		owner.SLADefinition.UpdateOneID(legacySLADefinition.ID).SetEscalationRules(map[string]interface{}{"medium": []interface{}{map[string]interface{}{"level": 1, "afterMinutes": 0, "notifyRoles": []interface{}{"requester"}, "description": "candidate escalation"}}}).SaveX(ctx)
		owner.SLAAlertRule.UpdateOneID(legacySLAAlertRule.ID).SetEscalationEnabled(true).SaveX(ctx)
		notifier := newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy)
		alerts := service.NewSLAAlertService(runtime, zap.NewNop().Sugar(), policy)
		alerts.SetNotificationService(notifier)
		fresh, err := app.Create(ctx, identity, command("sla-matrix-member", "generic"))
		require.NoError(t, err)
		owner.Ticket.UpdateOneID(fresh.WorkItemID).SetCreatedAt(time.Now().Add(-time.Hour)).SetSLADefinitionID(legacySLADefinition.ID).SetSLAResponseDeadline(time.Now().Add(5 * time.Minute)).SaveX(ctx)
		triggered, err := alerts.TriggerSLAWarning(ctx, fresh.WorkItemID, "response_time", tenant.ID)
		require.NoError(t, err)
		require.True(t, triggered)
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(h)::text FROM sla_alert_histories h WHERE id=$1`, legacySLAHistory.ID).Scan(&before))
		escalation := service.NewEscalationService(runtime, zap.NewNop().Sugar(), policy)
		escalation.SetNotificationService(notifier)
		require.NoError(t, escalation.ProcessEscalations(ctx, tenant.ID))
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(h)::text FROM sla_alert_histories h WHERE id=$1`, legacySLAHistory.ID).Scan(&after))
		assert.JSONEq(t, before, after, "matrix scan must not advance historical alert levels")
		var level int
		require.NoError(t, ownerDB.QueryRow(`SELECT escalation_level FROM sla_alert_histories WHERE ticket_id=$1`, fresh.WorkItemID).Scan(&level))
		require.Equal(t, 1, level, "new member must actually advance")
		var pending int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1 AND channel='email' AND delivery_key LIKE 'escalation:matrix:%' AND status='pending'`, fresh.WorkItemID).Scan(&pending))
		assert.Equal(t, 1, pending, "matrix progression must commit a durable escalation notice")
		// An unresolved old alert must not advance after the WorkItem reopens.
		owner.Ticket.UpdateOneID(fresh.WorkItemID).SetSLACycleNumber(2).SetSLACycleStartedAt(time.Now().Add(time.Second)).SaveX(ctx)
		owner.SLADefinition.UpdateOneID(legacySLADefinition.ID).SetEscalationRules(map[string]interface{}{"medium": []interface{}{map[string]interface{}{"level": 1, "afterMinutes": 0, "notifyRoles": []interface{}{"requester"}}, map[string]interface{}{"level": 2, "afterMinutes": 0, "notifyRoles": []interface{}{"requester"}}}}).SaveX(ctx)
		require.NoError(t, escalation.ProcessEscalations(ctx, tenant.ID))
		require.NoError(t, ownerDB.QueryRow(`SELECT escalation_level FROM sla_alert_histories WHERE ticket_id=$1`, fresh.WorkItemID).Scan(&level))
		require.Equal(t, 1, level, "old-cycle alert must not escalate again")

	})

	t.Run("automatic reminders preserve historical WorkItems", func(t *testing.T) {
		old := time.Now().Add(-48 * time.Hour)
		owner.Ticket.UpdateOneID(historical.WorkItemID).SetCreatedAt(old).SetSLACycleStartedAt(old).ClearAssigneeID().SaveX(ctx)
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, historical.WorkItemID).Scan(&before))
		var notificationsBefore, notificationsAfter int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`, historical.WorkItemID).Scan(&notificationsBefore))
		escalation := service.NewEscalationService(runtime, zap.NewNop().Sugar(), policy)
		escalation.SetNotificationService(newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy))
		require.NoError(t, escalation.ProcessEscalations(ctx, tenant.ID))
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, historical.WorkItemID).Scan(&after))
		require.JSONEq(t, before, after)
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`, historical.WorkItemID).Scan(&notificationsAfter))
		require.Equal(t, notificationsBefore, notificationsAfter)
		var receipts int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM audit_logs WHERE path=$1 AND action IN ('work_item.escalation.long_pending','work_item.escalation.unassigned')`, fmt.Sprint(historical.WorkItemID)).Scan(&receipts))
		require.Zero(t, receipts)
	})
	t.Run("automatic reminders replay within each SLA cycle", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("automatic-reminder-member", "generic"))
		require.NoError(t, err)
		escalation := service.NewEscalationService(runtime, zap.NewNop().Sugar(), policy)
		escalation.SetNotificationService(newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy))
		old := time.Now().Add(-48 * time.Hour)
		owner.Ticket.UpdateOneID(fresh.WorkItemID).SetCreatedAt(old).SetSLACycleStartedAt(old).SetSLACycleNumber(1).SaveX(ctx)
		count := func() int {
			var n int
			require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM audit_logs WHERE path=$1 AND action IN ('work_item.escalation.long_pending','work_item.escalation.unassigned')`, fmt.Sprint(fresh.WorkItemID)).Scan(&n))
			return n
		}
		require.NoError(t, escalation.ProcessEscalations(ctx, tenant.ID))
		require.Equal(t, 2, count())
		version := owner.Ticket.GetX(ctx, fresh.WorkItemID).Version
		require.NoError(t, escalation.ProcessEscalations(ctx, tenant.ID))
		require.Equal(t, 2, count())
		require.Equal(t, version, owner.Ticket.GetX(ctx, fresh.WorkItemID).Version)
		owner.Ticket.UpdateOneID(fresh.WorkItemID).SetSLACycleNumber(2).SetSLACycleStartedAt(time.Now()).SaveX(ctx)
		require.NoError(t, escalation.ProcessEscalations(ctx, tenant.ID))
		require.Equal(t, 2, count(), "new cycle starts its own age threshold")
		owner.Ticket.UpdateOneID(fresh.WorkItemID).SetSLACycleStartedAt(old).SaveX(ctx)
		require.NoError(t, escalation.ProcessEscalations(ctx, tenant.ID))
		require.Equal(t, 4, count())
		var histories int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM sla_alert_histories WHERE ticket_id=$1`, fresh.WorkItemID).Scan(&histories))
		require.Zero(t, histories, "non-rule reminders must not create invalid SLA history")
	})

	t.Run("automatic reminder notification and audit rollback", func(t *testing.T) {
		escalation := service.NewEscalationService(runtime, zap.NewNop().Sugar(), policy)
		escalation.SetNotificationService(newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy))
		// Drain prior valid work before arming faults for the next isolated target.
		require.NoError(t, escalation.ProcessEscalations(ctx, tenant.ID))
		for _, fault := range []string{"notification", "audit"} {
			fresh, err := app.Create(ctx, identity, command("automatic-reminder-fault-"+fault, "generic"))
			require.NoError(t, err)
			old := time.Now().Add(-48 * time.Hour)
			before := owner.Ticket.UpdateOneID(fresh.WorkItemID).SetCreatedAt(old).SetSLACycleStartedAt(old).SetAssigneeID(actor.ID).SaveX(ctx)
			active := true
			writes := 0
			injected := errors.New("injected reminder " + fault)
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					v, err := next.Mutate(ctx, m)
					if active && err == nil {
						writes++
						return nil, injected
					}
					return v, err
				})
			}
			if fault == "notification" {
				runtime.Notification.Use(hook)
			} else {
				runtime.AuditLog.Use(hook)
			}
			unifiedBefore := owner.Notification.Query().CountX(ctx)
			err = escalation.ProcessEscalations(ctx, tenant.ID)
			active = false
			require.ErrorIs(t, err, injected)
			require.Equal(t, 1, writes)
			require.Equal(t, before.Version, owner.Ticket.GetX(ctx, fresh.WorkItemID).Version)
			require.Equal(t, unifiedBefore, owner.Notification.Query().CountX(ctx))
			var notifications, receipts int
			require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`, fresh.WorkItemID).Scan(&notifications))
			require.Zero(t, notifications)
			require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM audit_logs WHERE path=$1 AND action='work_item.escalation.long_pending'`, fmt.Sprint(fresh.WorkItemID)).Scan(&receipts))
			require.Zero(t, receipts)
			require.NoError(t, escalation.ProcessEscalations(ctx, tenant.ID))
			require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM audit_logs WHERE path=$1 AND action='work_item.escalation.long_pending'`, fmt.Sprint(fresh.WorkItemID)).Scan(&receipts))
			require.Equal(t, 1, receipts)
		}
	})

	t.Run("ordered outbox holds successor while predecessor runs", func(t *testing.T) {
		firstItem, err := app.Create(ctx, identity, command("ordered-first", "generic"))
		require.NoError(t, err)
		otherItem, err := app.Create(ctx, identity, command("ordered-other", "generic"))
		require.NoError(t, err)
		makeEvent := func(key string, itemID int) *ent.OutboxEvent {
			return owner.OutboxEvent.Create().SetEventID(key).SetEventType("candidate-ordered-delivery").SetTenantID(tenant.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(itemID)).SetExecutionWorkItemID(itemID).SetPayload(json.RawMessage(`{}`)).SaveX(ctx)
		}
		first := makeEvent("ordered-one", firstItem.WorkItemID)
		second := makeEvent("ordered-two", firstItem.WorkItemID)
		other := makeEvent("ordered-other", otherItem.WorkItemID)
		receiver := &candidateOrderedReceiver{firstID: first.ID, entered: make(chan int, 4), release: make(chan struct{})}
		reserved := []string{}
		seen := map[string]bool{}
		for _, row := range owner.OutboxEvent.Query().AllX(ctx) {
			if row.EventType != receiver.EventType() && !seen[row.EventType] {
				reserved = append(reserved, row.EventType)
				seen[row.EventType] = true
			}
		}
		registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{receiver}, reserved...)
		require.NoError(t, err)
		worker := func() *service.OutboxDeliveryWorker {
			w, e := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(clients.System, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 1, PollInterval: time.Second, HandlerTimeout: 10 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
			require.NoError(t, e)
			return w
		}
		completed := make(chan error, 1)
		one, two := worker(), worker()
		go func() { completed <- one.DispatchOnce(ctx) }()
		released := false
		defer func() {
			if !released {
				close(receiver.release)
			}
		}()
		select {
		case id := <-receiver.entered:
			require.Equal(t, first.ID, id)
		case <-time.After(5 * time.Second):
			t.Fatal("first worker did not reach declared receiver")
		}
		require.NoError(t, two.DispatchOnce(ctx))
		assert.Equal(t, "pending", owner.OutboxEvent.GetX(ctx, second.ID).Status, "same target successor must remain unclaimed")
		assert.Equal(t, "published", owner.OutboxEvent.GetX(ctx, other.ID).Status, "independent target should continue")
		close(receiver.release)
		released = true
		require.NoError(t, <-completed)
		require.NoError(t, two.DispatchOnce(ctx))
		require.Equal(t, "published", owner.OutboxEvent.GetX(ctx, second.ID).Status)
	})

	t.Run("ordered outbox stops after delivery acknowledgement failure", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("ordered-ack-fault", "generic"))
		require.NoError(t, err)
		makeEvent := func(key string) *ent.OutboxEvent {
			return owner.OutboxEvent.Create().SetEventID(key).SetEventType("candidate-ordered-delivery").SetTenantID(tenant.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(fresh.WorkItemID)).SetExecutionWorkItemID(fresh.WorkItemID).SetPayload(json.RawMessage(`{}`)).SaveX(ctx)
		}
		first := makeEvent("ordered-ack-first")
		second := makeEvent("ordered-ack-second")
		receiver := &candidateOrderedReceiver{entered: make(chan int, 4), release: make(chan struct{})}
		reserved := []string{}
		seen := map[string]bool{}
		for _, row := range owner.OutboxEvent.Query().AllX(ctx) {
			if row.EventType != receiver.EventType() && !seen[row.EventType] {
				reserved = append(reserved, row.EventType)
				seen[row.EventType] = true
			}
		}
		registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{receiver}, reserved...)
		require.NoError(t, err)
		worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(clients.System, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 1, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
		require.NoError(t, err)
		active := true
		writes := 0
		injected := errors.New("ordered published write failed")
		clients.System.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				v, err := next.Mutate(ctx, m)
				if mutation, ok := m.(*ent.OutboxEventMutation); ok {
					state, set := mutation.Status()
					if active && set && state == "published" && err == nil {
						writes++
						return nil, injected
					}
				}
				return v, err
			})
		})
		err = worker.DispatchOnce(ctx)
		active = false
		require.ErrorIs(t, err, injected)
		require.Equal(t, 1, writes)
		require.Equal(t, 1, len(receiver.entered), "receiver actually ran once")
		require.Equal(t, "publishing", owner.OutboxEvent.GetX(ctx, first.ID).Status)
		require.NoError(t, worker.DispatchOnce(ctx))
		require.Equal(t, "pending", owner.OutboxEvent.GetX(ctx, second.ID).Status)
		require.Equal(t, 1, len(receiver.entered), "successor must not follow an unacknowledged call")
		owner.OutboxEvent.UpdateOneID(first.ID).SetClaimExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		require.NoError(t, worker.DispatchOnce(ctx))
		require.Equal(t, "blocked", owner.OutboxEvent.GetX(ctx, first.ID).Status)
		require.Contains(t, owner.OutboxEvent.GetX(ctx, first.ID).LastError, "delivery_unknown")
		require.Equal(t, "pending", owner.OutboxEvent.GetX(ctx, second.ID).Status)
		require.Equal(t, 1, len(receiver.entered))
	})
	t.Run("ordered outbox preserves unresolved barriers", func(t *testing.T) {
		fresh, err := app.Create(ctx, identity, command("ordered-barriers", "generic"))
		require.NoError(t, err)
		repo := service.NewOutboxEventRepository(clients.System, policy)
		workerCtx := tenantctx.SystemContext(ctx, "outbox:poll", "ordered barrier verification")
		makeEvent := func(key, target string) *ent.OutboxEvent {
			return owner.OutboxEvent.Create().SetEventID(key).SetEventType("candidate-ordered-barrier").SetTenantID(tenant.ID).SetAggregateType("test_target").SetAggregateID(target).SetExecutionWorkItemID(fresh.WorkItemID).SetPayload(json.RawMessage(`{}`)).SaveX(ctx)
		}
		historicalSuccessor := makeEvent("after-legacy-ordered", "legacy-target")
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(e)::text FROM outbox_events e WHERE id=$1`, legacyOrdered.ID).Scan(&before))
		successors := []int{historicalSuccessor.ID}
		for _, state := range []string{"blocked", "dead_letter", "unrecognized", "pending"} {
			first := makeEvent("barrier-"+state, state)
			owner.OutboxEvent.UpdateOneID(first.ID).SetStatus(state).SetNextAttemptAt(time.Now().Add(time.Hour)).SaveX(ctx)
			successors = append(successors, makeEvent("successor-"+state, state).ID)
		}
		ambiguous := makeEvent("ordered-unknown-first", "unknown-target")
		later := makeEvent("ordered-unknown-second", "unknown-target")
		rows, err := repo.ClaimDueByEventType(workerCtx, time.Now(), 100, "candidate-ordered-barrier", true)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, ambiguous.ID, rows[0].ID)
		require.NoError(t, repo.MarkDeliveryAttemptStarted(workerCtx, rows[0].ID, rows[0].ClaimToken, rows[0].EventID))
		owner.OutboxEvent.UpdateOneID(ambiguous.ID).SetClaimExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
		rows, err = repo.ClaimDueByEventType(workerCtx, time.Now(), 100, "candidate-ordered-barrier", true)
		require.NoError(t, err)
		require.Empty(t, rows)
		require.Equal(t, "blocked", owner.OutboxEvent.GetX(ctx, ambiguous.ID).Status)
		require.Contains(t, owner.OutboxEvent.GetX(ctx, ambiguous.ID).LastError, "delivery_unknown")
		successors = append(successors, later.ID)
		for _, id := range successors {
			row := owner.OutboxEvent.GetX(ctx, id)
			require.Equal(t, "pending", row.Status)
			require.Zero(t, row.AttemptCount)
			require.Empty(t, row.ClaimToken)
		}
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(e)::text FROM outbox_events e WHERE id=$1`, legacyOrdered.ID).Scan(&after))
		require.JSONEq(t, before, after)
	})

	t.Run("BPMN escalation rechecks scope at mutation", func(t *testing.T) {
		for _, fault := range []string{"normal", "default", "scope", "lease", "notification", "audit", "advance", "incident", "problem", "change_request"} {
			closeBeforeWrite := fault == "scope"
			key := "bpmn-escalation-" + fault
			targetClass := "generic"
			if fault == "incident" || fault == "problem" || fault == "change_request" {
				targetClass = fault
			}
			fresh, err := app.Create(ctx, identity, command(key, targetClass))
			require.NoError(t, err)
			dep := owner.ProcessDeployment.Create().SetDeploymentID(key).SetDeploymentName(key).SetTenantID(tenant.ID).SaveX(ctx)
			xml := `<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="https://example.invalid"><bpmn:process id="escalation" isExecutable="true"><bpmn:startEvent id="Start"/><bpmn:serviceTask id="Current"/><bpmn:endEvent id="End"/><bpmn:sequenceFlow id="S1" sourceRef="Start" targetRef="Current"/><bpmn:sequenceFlow id="S2" sourceRef="Current" targetRef="End"/></bpmn:process></bpmn:definitions>`
			def := owner.ProcessDefinition.Create().SetKey(key).SetName(key).SetVersion("1").SetIsLatest(true).SetBpmnXML([]byte(xml)).SetDeploymentID(dep.ID).SetTenantID(tenant.ID).SaveX(ctx)
			instance := owner.ProcessInstance.Create().SetProcessInstanceID(key).SetProcessDefinitionKey(def.Key).SetProcessDefinitionID(def.ID).SetBusinessKey(fmt.Sprintf("generic:%d", fresh.WorkItemID)).SetBusinessType("generic").SetBusinessID(fresh.WorkItemID).SetExecutionWorkItemID(fresh.WorkItemID).SetInitiator(fmt.Sprint(actor.ID)).SetStatus("running").SetCurrentActivityID("Current").SetTenantID(tenant.ID).SaveX(ctx)
			callback := owner.ProcessCallbackOutbox.Create().SetExecutionKey(key).SetTenantID(tenant.ID).SetProcessInstanceID(instance.ID).SetCallbackKind("service_task").SetHandlerID("ticket_service_handler").SetTaskType("ticket_task").SetAction("escalate").SetElementID("Current").SetVariables(map[string]interface{}{"escalate_to": "critical", "escalation_reason": "candidate workflow", "version": owner.Ticket.GetX(ctx, fresh.WorkItemID).Version}).SetNextAttemptAt(time.Now().Add(-time.Hour)).SaveX(ctx)
			t.Cleanup(func() {
				_, e := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
				assert.NoError(t, e)
				// Quarantine the deliberately failed fixture so later sweeps cannot reuse it.
				_, e = ownerDB.ExecContext(ctx, "UPDATE process_callback_outboxes SET status='blocked' WHERE id=$1 AND status<>'completed'", callback.ID)
				assert.NoError(t, e)
			})
			if fault == "default" {
				vars := callback.Variables
				delete(vars, "escalate_to")
				callback = owner.ProcessCallbackOutbox.UpdateOneID(callback.ID).SetVariables(vars).SaveX(ctx)
			}
			var notificationRecipientID int
			if fault == "notification" || fault == "audit" || fault == "advance" {
				vars := callback.Variables
				recipient := owner.User.Create().SetTenantID(tenant.ID).SetUsername(key + "-recipient").SetName("Workflow recipient").SetEmail(key + "@example.invalid").SetPasswordHash("local-only").SetRole("requester").SetActive(true).SaveX(ctx)
				notificationRecipientID = recipient.ID
				vars["notify_admin_ids"] = []int{recipient.ID}
				callback = owner.ProcessCallbackOutbox.UpdateOneID(callback.ID).SetVariables(vars).SaveX(ctx)
			}
			beforeNotifications := owner.Notification.Query().CountX(ctx)
			beforeAudits := owner.AuditLog.Query().CountX(ctx)
			activeFault := false
			writes := 0
			if fault == "notification" || fault == "audit" || fault == "advance" {
				activeFault = true
				hook := func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						v, err := next.Mutate(ctx, m)
						if activeFault && err == nil {
							writes++
							return nil, errors.New("local workflow " + fault + " write failure")
						}
						return v, err
					})
				}
				if fault == "notification" {
					runtime.Notification.Use(hook)
				} else if fault == "audit" {
					runtime.AuditLog.Use(hook)
				} else {
					runtime.ProcessInstance.Use(hook)
				}
			}
			var before, after string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, fresh.WorkItemID).Scan(&before))
			handler := &candidateEscalationHandler{TicketServiceTaskHandler: bpmn.NewTicketServiceTaskHandler(runtime, zap.NewNop().Sugar())}
			handler.SetEscalationService(service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy, NotificationService: newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy)}))
			if closeBeforeWrite {
				handler.before = func() {
					_, e := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
					require.NoError(t, e)
				}
			}
			if fault == "lease" {
				handler.before = func() {
					owner.ProcessCallbackOutbox.UpdateOneID(callback.ID).SetLeaseOwner("different-worker").SaveX(ctx)
				}
			}
			engine := service.NewCustomProcessEngine(runtime, zap.NewNop().Sugar(), policy).(*service.CustomProcessEngine)
			engine.SetCallbackCandidateClient(clients.System)
			engine.CallbackRegistry().RegisterHandler(handler)
			_, sweepErr := engine.ProcessPendingCallbacks(context.Background(), key, 1)
			activeFault = false
			t.Logf("handler error=%v callback=%s", handler.lastError, owner.ProcessCallbackOutbox.GetX(ctx, callback.ID).LastErrorClass)
			_, restoreErr := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
			require.NoError(t, restoreErr)
			require.Equal(t, 1, handler.calls, "real engine must reach the owning handler after claim")
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, fresh.WorkItemID).Scan(&after))
			if fault != "normal" && fault != "default" {
				require.Error(t, sweepErr)
				if fault != "advance" {
					require.JSONEq(t, before, after, "failed workflow command must not mutate the target")
					require.Equal(t, beforeNotifications, owner.Notification.Query().CountX(ctx))
					require.Equal(t, beforeAudits, owner.AuditLog.Query().CountX(ctx))
				}
				if fault == "notification" || fault == "audit" || fault == "advance" {
					require.Equal(t, 1, writes)
					if fault == "advance" {
						owner.User.UpdateOneID(notificationRecipientID).SetActive(false).SaveX(ctx)
					}
					owner.ProcessCallbackOutbox.UpdateOneID(callback.ID).SetNextAttemptAt(time.Now().Add(-time.Hour)).SaveX(ctx)
					_, err := engine.ProcessPendingCallbacks(context.Background(), key+"-retry", 1)
					require.NoError(t, err)
					require.Equal(t, 2, owner.Ticket.GetX(ctx, fresh.WorkItemID).Version)
					require.Equal(t, beforeAudits+1, owner.AuditLog.Query().CountX(ctx))
					require.Equal(t, beforeNotifications+1, owner.Notification.Query().CountX(ctx))
					require.Equal(t, "completed", owner.ProcessCallbackOutbox.GetX(ctx, callback.ID).Status)
				}
			} else {
				require.NoError(t, sweepErr)
				priority := "critical"
				if fault == "default" {
					priority = "high"
				}
				require.Equal(t, priority, owner.Ticket.GetX(ctx, fresh.WorkItemID).Priority)
				require.Equal(t, "escalated", owner.Ticket.GetX(ctx, fresh.WorkItemID).Status)
				require.Equal(t, 2, owner.Ticket.GetX(ctx, fresh.WorkItemID).Version)
			}

		}
	})

	t.Run("ticket repository update joins caller transaction", func(t *testing.T) {
		repo := ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar())
		fresh, err := app.Create(ctx, identity, command("ticket-edit-repository", "generic"))
		require.NoError(t, err)
		for _, mode := range []string{"rollback", "commit", "stale"} {
			func() {
				before := owner.Ticket.GetX(ctx, fresh.WorkItemID)
				var beforeJSON string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&beforeJSON))
				beforeTags := owner.TicketTag.Query().CountX(ctx)
				beforeIDs := before.QueryTags().IDsX(ctx)
				tx, err := runtime.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				require.NoError(t, err)
				defer tx.Rollback()
				require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
				require.NoError(t, policy.RequireEntMembers(ctx, tx, tenant.ID, before.ID))
				tags, err := service.NewTicketTagService(tx.Client()).ResolveTagIDsByNames(ctx, []string{"caller-tx-" + mode}, tenant.ID, true)
				require.NoError(t, err)
				title := "caller transaction " + mode
				version := before.Version
				if mode == "stale" {
					version--
				}
				updated, err := repo.UpdateTx(ctx, tx, before.ID, &ticketrepo.UpdateParams{Title: &title, Version: version, ReplaceTags: true, TagIDs: tags}, tenant.ID)
				if mode == "stale" {
					require.ErrorContains(t, err, "version conflict")
				} else {
					require.NoError(t, err)
					require.Equal(t, before.Version+1, updated.Version)
					require.Equal(t, tags, tx.Ticket.GetX(ctx, before.ID).QueryTags().IDsX(ctx))
				}
				var uncommittedJSON string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&uncommittedJSON))
				require.JSONEq(t, beforeJSON, uncommittedJSON, "repository must not commit caller transaction")
				require.Equal(t, beforeTags, owner.TicketTag.Query().CountX(ctx))
				if mode == "commit" {
					require.NoError(t, tx.Commit())
					after := owner.Ticket.GetX(ctx, before.ID)
					require.Equal(t, title, after.Title)
					require.Equal(t, before.Version+1, after.Version)
					require.Equal(t, tags, after.QueryTags().IDsX(ctx))
					require.Equal(t, beforeTags+1, owner.TicketTag.Query().CountX(ctx))
				} else {
					require.NoError(t, tx.Rollback())
					var afterJSON string
					require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&afterJSON))
					require.JSONEq(t, beforeJSON, afterJSON)
					require.Equal(t, beforeTags, owner.TicketTag.Query().CountX(ctx))
					require.Equal(t, beforeIDs, owner.Ticket.GetX(ctx, before.ID).QueryTags().IDsX(ctx))
				}
			}()
		}
	})

	t.Run("ticket edits preserve historical records and reject orphan tag writes", func(t *testing.T) {
		svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy, NotificationService: newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy)})
		fresh, err := app.Create(ctx, identity, command("ticket-edit-member", "generic"))
		require.NoError(t, err)
		for _, target := range []struct {
			name   string
			id     int
			denied bool
			stale  bool
		}{
			{"historical", historicalAlertItems[1], true, false},
			{"member", fresh.WorkItemID, false, false},
			{"stale", fresh.WorkItemID, false, true},
		} {
			before := owner.Ticket.GetX(ctx, target.id)
			var beforeJSON, afterJSON string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, target.id).Scan(&beforeJSON))
			tagsBefore := owner.TicketTag.Query().CountX(ctx)
			version := before.Version
			if target.stale {
				version--
			}
			updated, editErr := svc.UpdateTicket(ctx, editCommandForTest(target.id, &dto.TicketEditCommand{Fields: dto.TicketEditFields{Title: "scoped edit " + target.name, Tags: []string{"edit-" + target.name}}, Meta: workitemmutation.Meta{ExpectedVersion: version, ActorID: actor.ID}}, tenant.ID))
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, target.id).Scan(&afterJSON))
			if target.denied || target.stale {
				if target.denied {
					assert.ErrorIs(t, editErr, executionscope.ErrDenied)
				} else {
					assert.Error(t, editErr)
				}
				assert.JSONEq(t, beforeJSON, afterJSON, target.name)
				assert.Equal(t, tagsBefore, owner.TicketTag.Query().CountX(ctx), "rejected edit must not create tags: %s", target.name)
			} else {
				require.NoError(t, editErr)
				require.Equal(t, "scoped edit member", owner.Ticket.GetX(ctx, updated.WorkItemID).Title)
				require.Equal(t, before.Version+1, updated.Version)
				require.Equal(t, tagsBefore+1, owner.TicketTag.Query().CountX(ctx))
			}
		}
	})

	t.Run("ticket edit rolls back directory and relations after actual writes", func(t *testing.T) {
		svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy})
		for _, fault := range []string{"tag", "ticket"} {
			func() {
				fresh, err := app.Create(ctx, identity, command("edit-write-fault-"+fault, "generic"))
				require.NoError(t, err)
				before := owner.Ticket.GetX(ctx, fresh.WorkItemID)
				var beforeJSON, afterJSON string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&beforeJSON))
				beforeTags := owner.TicketTag.Query().CountX(ctx)
				beforeRelations := before.QueryTags().IDsX(ctx)
				active, writes := true, 0
				defer func() { active = false }()
				injected := errors.New("edit actual " + fault + " write failure")
				hook := func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(ctx, m)
						if active && err == nil {
							writes++
							return nil, injected
						}
						return value, err
					})
				}
				if fault == "tag" {
					runtime.TicketTag.Use(hook)
				} else {
					runtime.Ticket.Use(hook)
				}
				req := &dto.TicketEditCommand{Fields: dto.TicketEditFields{Title: "atomic edit " + fault, Tags: []string{"edit-fault-" + fault}}, Meta: workitemmutation.Meta{ExpectedVersion: before.Version, ActorID: actor.ID}}
				_, err = svc.UpdateTicket(ctx, editCommandForTest(before.ID, req, tenant.ID))
				active = false
				require.ErrorIs(t, err, injected)
				require.Equal(t, 1, writes)
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&afterJSON))
				require.JSONEq(t, beforeJSON, afterJSON)
				require.Equal(t, beforeTags, owner.TicketTag.Query().CountX(ctx))
				require.Equal(t, beforeRelations, owner.Ticket.GetX(ctx, before.ID).QueryTags().IDsX(ctx))
				updated, err := svc.UpdateTicket(ctx, editCommandForTest(before.ID, req, tenant.ID))
				require.NoError(t, err)
				require.Equal(t, before.Version+1, updated.Version)
				require.Equal(t, beforeTags+1, owner.TicketTag.Query().CountX(ctx))
				require.Len(t, owner.Ticket.GetX(ctx, before.ID).QueryTags().IDsX(ctx), 1)
			}()
		}
	})

	t.Run("ticket edit status side effects share original transaction", func(t *testing.T) {
		notifications := newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy)
		notifications.SetNotificationPreferenceService(service.NewNotificationPreferenceService(runtime, zap.NewNop().Sugar()))
		preference := owner.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetEventType("ticket_updated").SetEmailEnabled(false).SetInAppEnabled(true).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
		defer func() { require.NoError(t, owner.NotificationPreference.DeleteOneID(preference.ID).Exec(ctx)) }()
		svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy, NotificationService: notifications})
		for _, fault := range []string{"notification", "ticket_notification", "second_sla"} {
			func() {
				fresh, err := app.Create(ctx, identity, command("edit-status-fault-"+fault, "generic"))
				require.NoError(t, err)
				owner.Ticket.UpdateOneID(fresh.WorkItemID).SetStatus("in_progress").ExecX(ctx)
				before := owner.Ticket.GetX(ctx, fresh.WorkItemID)
				violations := []*ent.SLAViolation{}
				for _, kind := range []string{"response", "resolution"} {
					violations = append(violations, owner.SLAViolation.Create().SetTicketID(before.ID).SetTenantID(tenant.ID).SetSLADefinitionID(legacySLADefinition.ID).SetViolationType(kind).SaveX(ctx))
				}
				var beforeJSON, afterJSON string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&beforeJSON))
				beforeNotifications := owner.Notification.Query().CountX(ctx)
				beforeDeliveries := owner.TicketNotification.Query().CountX(ctx)
				beforeTags := owner.TicketTag.Query().CountX(ctx)
				beforeSLA := make([]string, len(violations))
				for j, v := range violations {
					require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(v)::text FROM sla_violations v WHERE id=$1`, v.ID).Scan(&beforeSLA[j]))
				}
				active, writes := true, 0
				defer func() { active = false }()
				injected := errors.New("edit status " + fault + " after write")
				hook := func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(ctx, m)
						if active && err == nil {
							writes++
							if fault != "second_sla" || writes == 2 {
								return nil, injected
							}
						}
						return value, err
					})
				}
				switch fault {
				case "notification":
					runtime.Notification.Use(hook)
				case "ticket_notification":
					runtime.TicketNotification.Use(hook)
				case "second_sla":
					runtime.SLAViolation.Use(hook)
				}
				req := &dto.TicketEditCommand{Fields: dto.TicketEditFields{Status: "resolved", Resolution: "verified resolution", Tags: []string{"status-" + fault}}, Meta: workitemmutation.Meta{ExpectedVersion: before.Version, ActorID: actor.ID}}
				_, editErr := svc.UpdateTicket(ctx, editCommandForTest(before.ID, req, tenant.ID))
				active = false
				assert.ErrorIs(t, editErr, injected)
				if fault == "second_sla" {
					assert.Equal(t, 2, writes)
				} else {
					assert.GreaterOrEqual(t, writes, 1)
				}
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&afterJSON))
				assert.JSONEq(t, beforeJSON, afterJSON)
				assert.Equal(t, beforeNotifications, owner.Notification.Query().CountX(ctx))
				assert.Equal(t, beforeDeliveries, owner.TicketNotification.Query().CountX(ctx))
				assert.Equal(t, beforeTags, owner.TicketTag.Query().CountX(ctx))
				for j, v := range violations {
					var after string
					require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(v)::text FROM sla_violations v WHERE id=$1`, v.ID).Scan(&after))
					assert.JSONEq(t, beforeSLA[j], after)
				}
				if errors.Is(editErr, injected) {
					updated, err := svc.UpdateTicket(ctx, editCommandForTest(before.ID, req, tenant.ID))
					require.NoError(t, err)
					require.Equal(t, before.Version+1, updated.Version)
					require.Equal(t, "resolved", string(updated.Status))
					require.Equal(t, beforeNotifications+1, owner.Notification.Query().CountX(ctx))
					require.Equal(t, beforeDeliveries+1, owner.TicketNotification.Query().CountX(ctx))
					var recipient int
					var deliveryKey string
					require.NoError(t, ownerDB.QueryRow(`SELECT user_id,delivery_key FROM ticket_notifications WHERE ticket_id=$1`, before.ID).Scan(&recipient, &deliveryKey))
					require.Equal(t, actor.ID, recipient)
					require.Equal(t, fmt.Sprintf("ticket:edit:%d:version:%d", before.ID, before.Version+1), deliveryKey)
					for _, v := range violations {
						require.True(t, owner.SLAViolation.GetX(ctx, v.ID).IsResolved)
					}
				}
			}()
		}
		for _, channel := range []string{"email", "disabled"} {
			owner.NotificationPreference.UpdateOneID(preference.ID).SetInAppEnabled(false).SetEmailEnabled(channel == "email").ExecX(ctx)
			fresh, err := app.Create(ctx, identity, command("edit-status-channel-"+channel, "generic"))
			require.NoError(t, err)
			before := owner.Ticket.GetX(ctx, fresh.WorkItemID)
			_, err = svc.UpdateTicket(ctx, editCommandForTest(before.ID, &dto.TicketEditCommand{Fields: dto.TicketEditFields{Status: "in_progress", AssigneeID: actor.ID}, Meta: workitemmutation.Meta{ExpectedVersion: before.Version, ActorID: actor.ID}}, tenant.ID))
			require.NoError(t, err)
			var count int
			require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`, before.ID).Scan(&count))
			if channel == "disabled" {
				require.Zero(t, count)
			} else {
				require.Equal(t, 1, count, "requester and identical assignee must be deduplicated")
				var persistedChannel, status, key string
				require.NoError(t, ownerDB.QueryRow(`SELECT channel,status,delivery_key FROM ticket_notifications WHERE ticket_id=$1`, before.ID).Scan(&persistedChannel, &status, &key))
				require.Equal(t, "email", persistedChannel)
				require.Equal(t, "pending", status, "producer must only enqueue; transport must not execute")
				require.Equal(t, fmt.Sprintf("ticket:edit:%d:version:%d", before.ID, before.Version+1), key)
			}
		}
		fresh, err := app.Create(ctx, identity, command("edit-status-no-notifier", "generic"))
		require.NoError(t, err)
		before := owner.Ticket.GetX(ctx, fresh.WorkItemID)
		withoutNotifier := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy})
		_, err = withoutNotifier.UpdateTicket(ctx, editCommandForTest(before.ID, &dto.TicketEditCommand{Fields: dto.TicketEditFields{Status: "in_progress"}, Meta: workitemmutation.Meta{ExpectedVersion: before.Version, ActorID: actor.ID}}, tenant.ID))
		require.ErrorContains(t, err, "notification service required")
		after := owner.Ticket.GetX(ctx, before.ID)
		require.Equal(t, before.Status, after.Status)
		require.Equal(t, before.Version, after.Version)

	})

	t.Run("ticket edits recheck current actor and permissions", func(t *testing.T) {
		svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy})
		code := "editor-" + uuid.NewString()
		otherTenant := owner.Tenant.Create().SetName("Foreign editor").SetCode(code).SaveX(ctx)
		editorRole := owner.Role.Create().SetTenantID(tenant.ID).SetName("Editor").SetCode(code).SetIsActive(true).SaveX(ctx)
		editPermission := owner.Permission.Create().SetTenantID(tenant.ID).SetCode(code).SetName("Edit").SetResource("ticket").SetAction("update").SaveX(ctx)
		link := owner.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(editorRole.ID).SetPermissionID(editPermission.ID).SaveX(ctx)
		editor := owner.User.Create().SetTenantID(tenant.ID).SetUsername(code).SetEmail(code + "@example.invalid").SetName("Editor").SetPasswordHash("fixture").SetRole(code).SetActive(true).SaveX(ctx)
		fresh, err := app.Create(ctx, identity, command("edit-actor-positive", "generic"))
		require.NoError(t, err)
		before := owner.Ticket.GetX(ctx, fresh.WorkItemID)
		_, err = svc.UpdateTicket(ctx, editCommandForTest(before.ID, &dto.TicketEditCommand{Fields: dto.TicketEditFields{Title: "authorized editor"}, Meta: workitemmutation.Meta{ExpectedVersion: before.Version, ActorID: editor.ID}}, tenant.ID))
		require.NoError(t, err)
		require.True(t, authorization.HasResourcePermission(owner, code, "ticket", "update", tenant.ID))
		defer authorization.InvalidateRolePermissionCache(code, tenant.ID)
		for _, state := range []string{"missing", "inactive", "revoked", "foreign"} {
			actorID := editor.ID
			switch state {
			case "missing":
				actorID = 0
			case "inactive":
				owner.User.UpdateOneID(editor.ID).SetActive(false).ExecX(ctx)
			case "revoked":
				owner.RolePermission.DeleteOneID(link.ID).ExecX(ctx)
			case "foreign":
				actorID = editor.ID
				owner.User.UpdateOneID(editor.ID).SetTenantID(otherTenant.ID).ExecX(ctx)
			}
			current := owner.Ticket.GetX(ctx, fresh.WorkItemID)
			var original, after string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, current.ID).Scan(&original))
			tags := owner.TicketTag.Query().CountX(ctx)
			_, editErr := svc.UpdateTicket(ctx, editCommandForTest(current.ID, &dto.TicketEditCommand{Fields: dto.TicketEditFields{Title: "must reject " + state, Tags: []string{"edit-actor-" + state}}, Meta: workitemmutation.Meta{ExpectedVersion: current.Version, ActorID: actorID}}, tenant.ID))
			assert.Error(t, editErr, state)
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, current.ID).Scan(&after))
			assert.JSONEq(t, original, after, state)
			assert.Equal(t, tags, owner.TicketTag.Query().CountX(ctx), state)
			if state == "inactive" {
				owner.User.UpdateOneID(editor.ID).SetActive(true).ExecX(ctx)
			}
			if state == "revoked" {
				link = owner.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(editorRole.ID).SetPermissionID(editPermission.ID).SaveX(ctx)
			}
			if state == "foreign" {
				owner.User.UpdateOneID(editor.ID).SetTenantID(tenant.ID).ExecX(ctx)
			}
		}
		incident, err := app.Create(ctx, identity, command("edit-professional-permission", "incident"))
		require.NoError(t, err)
		item := owner.Ticket.GetX(ctx, incident.WorkItemID)
		req := &dto.TicketEditCommand{Fields: dto.TicketEditFields{Tags: []string{"shared-edit-permission"}}, Meta: workitemmutation.Meta{ExpectedVersion: item.Version, ActorID: editor.ID}}
		_, err = svc.UpdateTicket(ctx, editCommandForTest(item.ID, req, tenant.ID))
		require.Error(t, err, "ticket:update alone cannot edit Incident shared metadata")
		require.Equal(t, item.Version, owner.Ticket.GetX(ctx, item.ID).Version)
		professional := owner.Permission.Create().SetTenantID(tenant.ID).SetCode(code + "-incident").SetName("Incident write").SetResource("incident").SetAction("write").SaveX(ctx)
		owner.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(editorRole.ID).SetPermissionID(professional.ID).ExecX(ctx)
		_, err = svc.UpdateTicket(ctx, editCommandForTest(item.ID, req, tenant.ID))
		require.NoError(t, err)
		require.Equal(t, item.Version+1, owner.Ticket.GetX(ctx, item.ID).Version)

	})

	t.Run("ticket edit checks actual parent execution membership", func(t *testing.T) {
		svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy})
		parent, err := app.Create(ctx, identity, command("edit-parent-member", "generic"))
		require.NoError(t, err)
		for _, target := range []struct {
			name   string
			parent int
			denied bool
		}{
			{"historical", historicalAlertItems[0], true}, {"member", parent.WorkItemID, false},
		} {
			child, err := app.Create(ctx, identity, command("edit-parent-child-"+target.name, "generic"))
			require.NoError(t, err)
			// Owner fixture sets pre-existing linkage; this does not exercise or grant
			// permission to create a new child under a protected historical parent.
			owner.Ticket.UpdateOneID(child.WorkItemID).SetParentTicketID(target.parent).ExecX(ctx)
			before := owner.Ticket.GetX(ctx, child.WorkItemID)
			var original, after, parentBefore, parentAfter string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&original))
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, target.parent).Scan(&parentBefore))
			tags := owner.TicketTag.Query().CountX(ctx)
			_, editErr := svc.UpdateTicket(ctx, editCommandForTest(before.ID, &dto.TicketEditCommand{Fields: dto.TicketEditFields{Title: "parent scoped edit", Tags: []string{"edit-parent-" + target.name}}, Meta: workitemmutation.Meta{ExpectedVersion: before.Version, ActorID: actor.ID}}, tenant.ID))
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&after))
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, target.parent).Scan(&parentAfter))
			require.JSONEq(t, parentBefore, parentAfter)
			if target.denied {
				assert.ErrorIs(t, editErr, executionscope.ErrDenied)
				assert.JSONEq(t, original, after)
				assert.Equal(t, tags, owner.TicketTag.Query().CountX(ctx))
			} else {
				require.NoError(t, editErr)
				require.Equal(t, before.Version+1, owner.Ticket.GetX(ctx, before.ID).Version)
				require.Equal(t, tags+1, owner.TicketTag.Query().CountX(ctx))
			}
		}
	})

	t.Run("historical edit receipt preserves original status and membership", func(t *testing.T) {
		svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy})
		id := legacyEditItem.WorkItemID
		var members int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_scope_members WHERE work_item_id=$1`, id).Scan(&members))
		require.Zero(t, members)
		current := owner.Ticket.GetX(ctx, id)
		require.Equal(t, "in_progress", current.Status)
		require.Greater(t, current.Version, legacyEditResult.Version)
		var before, after, receiptBefore, receiptAfter string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, id).Scan(&before))
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, tenant.ID, actor.ID, legacyEditCommand.Meta.OperationID).Scan(&receiptBefore))
		var action, digest, status, method, path string
		var resultVersion int
		require.NoError(t, ownerDB.QueryRow(`SELECT action, request_digest, result_status, result_version, method, path FROM audit_logs WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, tenant.ID, actor.ID, legacyEditCommand.Meta.OperationID).Scan(&action, &digest, &status, &resultVersion, &method, &path))
		require.Equal(t, "work_item.edit", action)
		require.Len(t, digest, 64)
		require.Equal(t, "http", method)
		require.Equal(t, fmt.Sprint(id), path)
		require.Equal(t, legacyEditResult.Version, resultVersion)
		require.Equal(t, "open", status)
		audits, notifications, deliveries, events := owner.AuditLog.Query().CountX(ctx), owner.Notification.Query().CountX(ctx), owner.TicketNotification.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx)
		replay, err := svc.UpdateTicket(ctx, legacyEditCommand)
		require.NoError(t, err)
		expected := legacyEditResult
		expected.Replayed = true
		require.Equal(t, expected, replay)
		fresh := legacyEditCommand
		fresh.Meta.OperationID = "historical-edit-new-attempt"
		fresh.Meta.ExpectedVersion = current.Version
		_, err = svc.UpdateTicket(ctx, fresh)
		require.ErrorIs(t, err, executionscope.ErrDenied)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, id).Scan(&after))
		require.JSONEq(t, before, after)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(a)::text FROM audit_logs a WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, tenant.ID, actor.ID, legacyEditCommand.Meta.OperationID).Scan(&receiptAfter))
		require.JSONEq(t, receiptBefore, receiptAfter)
		require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
		require.Equal(t, notifications, owner.Notification.Query().CountX(ctx))
		require.Equal(t, deliveries, owner.TicketNotification.Query().CountX(ctx))
		require.Equal(t, events, owner.OutboxEvent.Query().CountX(ctx))
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_scope_members WHERE work_item_id=$1`, id).Scan(&members))
		require.Zero(t, members)
	})

	t.Run("concurrent ticket edits recover the original receipt", func(t *testing.T) {
		svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy})
		created, err := app.Create(ctx, identity, command("edit-concurrent-fixture", "generic"))
		require.NoError(t, err)
		item := owner.Ticket.GetX(ctx, created.WorkItemID)
		cmd := dto.TicketEditCommand{WorkItemID: item.ID, Fields: dto.TicketEditFields{Title: "Concurrent receipt edit"}, Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, OperationID: "edit-concurrent", Source: "http"}}
		ready := make(chan struct{}, 2)
		release := make(chan struct{})
		active := true
		defer func() { active = false }()
		runtime.Ticket.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if typed, ok := m.(*ent.TicketMutation); ok && active {
					if title, ok := typed.Title(); ok && title == cmd.Fields.Title {
						// Both transactions have passed receipt lookup and read the old version.
						// Hold them immediately before their real Ticket UPDATE, not merely before
						// goroutine startup, so the second writer has a stale RR snapshot.
						ready <- struct{}{}
						select {
						case <-release:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
				}
				return next.Mutate(ctx, m)
			})
		})
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		type outcome struct {
			result workitemmutation.Result
			err    error
		}
		outcomes := make(chan outcome, 2)
		for i := 0; i < 2; i++ {
			go func() { result, err := svc.UpdateTicket(runCtx, cmd); outcomes <- outcome{result, err} }()
		}
		arrived := 0
	wait:
		for arrived < 2 {
			select {
			case <-ready:
				arrived++
			case <-runCtx.Done():
				break wait
			}
		}
		close(release)
		results := []outcome{<-outcomes, <-outcomes}
		active = false
		require.Equal(t, 2, arrived, "both commands must reach the pre-UPDATE barrier")
		successes, serializations := 0, 0
		var winner workitemmutation.Result
		for _, out := range results {
			if out.err == nil {
				successes++
				winner = out.result
				require.False(t, out.result.Replayed)
				require.Equal(t, item.Version+1, out.result.Version)
			} else {
				var pg *pq.Error
				require.ErrorAs(t, out.err, &pg)
				require.Equal(t, pq.ErrorCode("40001"), pg.Code)
				serializations++
			}
		}
		require.Equal(t, 1, successes)
		require.Equal(t, 1, serializations)
		require.Equal(t, cmd.Fields.Title, owner.Ticket.GetX(ctx, item.ID).Title)
		var receipts int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, tenant.ID, actor.ID, cmd.Meta.OperationID).Scan(&receipts))
		require.Equal(t, 1, receipts)
		require.Equal(t, item.Version+1, owner.Ticket.GetX(ctx, item.ID).Version)
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&before))
		replay, err := svc.UpdateTicket(ctx, cmd)
		require.NoError(t, err)
		require.True(t, replay.Replayed)
		winner.Replayed = true
		require.Equal(t, winner, replay)
		require.Equal(t, item.ID, replay.WorkItemID)
		require.Equal(t, item.Version+1, replay.Version)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&after))
		require.JSONEq(t, before, after)
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, tenant.ID, actor.ID, cmd.Meta.OperationID).Scan(&receipts))
		require.Equal(t, 1, receipts)
	})

	t.Run("ticket edit retries preserve immutable operation result", func(t *testing.T) {
		svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy})
		fresh, err := app.Create(ctx, identity, command("edit-receipt-fixture", "generic"))
		require.NoError(t, err)
		before := owner.Ticket.GetX(ctx, fresh.WorkItemID)
		// Preserve the original wire intent when constructing the trusted command.
		var request dto.UpdateTicketRequest
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"title":"first receipt edit","tags":["edit-receipt-tag"],"version":%d,"operationId":"edit-receipt-original"}`, before.Version)), &request))

		first, err := svc.UpdateTicket(ctx, dto.TicketEditCommand{WorkItemID: before.ID, Fields: request.TicketEditFields, Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: request.Version, OperationID: request.OperationID, Source: "http"}})
		require.NoError(t, err)
		require.Equal(t, before.Version+1, first.Version)
		require.False(t, first.Replayed)
		require.Equal(t, before.ID, first.WorkItemID)
		var receipts int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND user_id=$2 AND operation_id=$3`, tenant.ID, actor.ID, "edit-receipt-original").Scan(&receipts))
		assert.Equal(t, 1, receipts, "first edit must persist its immutable operation receipt")
		for _, phase := range []string{"immediate", "after later edit", "closed scope"} {
			if phase == "after later edit" {
				var later dto.UpdateTicketRequest
				require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"title":"later independent edit","version":%d,"operationId":"edit-receipt-later"}`, first.Version)), &later))

				_, err := svc.UpdateTicket(ctx, dto.TicketEditCommand{WorkItemID: before.ID, Fields: later.TicketEditFields, Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: later.Version, OperationID: later.OperationID, Source: "http"}})
				require.NoError(t, err)
			}
			if phase == "closed scope" {
				_, err := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
				require.NoError(t, err)
				defer func() {
					_, err := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
					require.NoError(t, err)
				}()
			}
			var original, after string
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&original))
			tags := owner.TicketTag.Query().CountX(ctx)
			links := owner.Ticket.GetX(ctx, before.ID).QueryTags().IDsX(ctx)
			audits := owner.AuditLog.Query().CountX(ctx)
			replayed, retryErr := svc.UpdateTicket(ctx, dto.TicketEditCommand{WorkItemID: before.ID, Fields: request.TicketEditFields, Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: request.Version, OperationID: request.OperationID, Source: "http"}})
			assert.NoError(t, retryErr, phase+": same operation and original expectedVersion must replay")
			if retryErr == nil {
				assert.Equal(t, before.ID, replayed.WorkItemID)
				assert.True(t, replayed.Replayed)
				assert.Equal(t, first.Version, replayed.Version, phase+": replay returns original result version")
				assert.Equal(t, first.Status, replayed.Status)
			}
			require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&after))
			assert.JSONEq(t, original, after, phase)
			assert.Equal(t, tags, owner.TicketTag.Query().CountX(ctx), phase)
			assert.ElementsMatch(t, links, owner.Ticket.GetX(ctx, before.ID).QueryTags().IDsX(ctx), phase)
			assert.Equal(t, audits, owner.AuditLog.Query().CountX(ctx), phase)
			if phase == "closed scope" {
				_, err := ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
				require.NoError(t, err)
			}

		}
		// A caller cannot recycle a successful operationId with a new payload and
		// current version to turn a retry into a different write.
		current := owner.Ticket.GetX(ctx, before.ID)
		var changed dto.UpdateTicketRequest
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"title":"operation identity collision","version":%d,"tags":["receipt-collision-tag"],"operationId":"edit-receipt-original"}`, current.Version)), &changed))

		var original, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&original))
		tags := owner.TicketTag.Query().CountX(ctx)
		links := owner.Ticket.GetX(ctx, before.ID).QueryTags().IDsX(ctx)
		audits := owner.AuditLog.Query().CountX(ctx)
		_, conflictErr := svc.UpdateTicket(ctx, dto.TicketEditCommand{WorkItemID: before.ID, Fields: changed.TicketEditFields, Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: changed.Version, OperationID: changed.OperationID, Source: "http"}})
		var conflict *workitemmutation.OperationConflictError
		assert.ErrorAs(t, conflictErr, &conflict)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, before.ID).Scan(&after))
		assert.JSONEq(t, original, after)
		assert.Equal(t, tags, owner.TicketTag.Query().CountX(ctx))
		assert.ElementsMatch(t, links, owner.Ticket.GetX(ctx, before.ID).QueryTags().IDsX(ctx))
		assert.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
	})

	t.Run("manual escalation persists Feishu update intent", func(t *testing.T) {
		receiver := &candidateFeishuUpdater{destination: "local-test-destination"}
		registry := connector.NewRegistry()
		registry.Register(func() connector.Connector { return receiver })
		manager := candidateDeclaredManager(t, ctx, tenant.ID, scopeID, registry, config.ConnectorTargetConfig{Name: "feishu", Provider: "local-test", DestinationDigest: receiver.DeliveryDestinationIdentity(), Capabilities: []string{"outbox"}})
		fresh, err := app.Create(ctx, identity, command("manual-feishu-update", "generic"))
		require.NoError(t, err)
		owner.FeishuTicketSync.Create().SetTenantID(tenant.ID).SetTicketID(fresh.WorkItemID).SetFeishuTaskID("local-task").SetFeishuTaskGUID("local-task").SetSyncStatus("synced").SaveX(ctx)
		svc := service.NewTicketService(&service.TicketServiceConfig{Execution: policy, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Client: runtime, Logger: zap.NewNop().Sugar(), ConnectorManager: manager, NotificationService: newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy)})
		item := owner.Ticket.GetX(ctx, fresh.WorkItemID)
		cmd := dto.TicketEscalationCommand{WorkItemID: item.ID, Reason: "bounded Feishu update", Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, OperationID: "manual-feishu-update", Source: "http"}}
		_, err = svc.EscalateTicket(ctx, cmd)
		require.NoError(t, err)
		var pending int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM outbox_events WHERE execution_work_item_id=$1 AND event_type='feishu.task.update.requested' AND status='pending'`, item.ID).Scan(&pending))
		require.Equal(t, 1, pending)
		require.Empty(t, receiver.guids, "producer must not call the provider")
		second := cmd
		second.Meta.OperationID += "-second"
		second.Meta.ExpectedVersion = owner.Ticket.GetX(ctx, item.ID).Version
		_, err = svc.EscalateTicket(ctx, second)
		require.NoError(t, err)
		replay, err := svc.EscalateTicket(ctx, cmd)
		require.NoError(t, err)
		require.True(t, replay.Replayed)
		handler := service.NewFeishuUpdateDeliveryHandler(runtime, policy, clients.IntakeDirectorySnapshot(), func(id int) (service.FeishuTaskUpdater, bool) {
			return receiver, id == tenant.ID
		})
		reserved := []string{}
		seen := map[string]bool{}
		for _, row := range owner.OutboxEvent.Query().AllX(ctx) {
			if row.EventType != handler.EventType() && !seen[row.EventType] {
				reserved = append(reserved, row.EventType)
				seen[row.EventType] = true
			}
		}
		deliveryRegistry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{handler}, reserved...)
		require.NoError(t, err)
		worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(clients.System, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 100, PollInterval: time.Second, HandlerTimeout: 10 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), deliveryRegistry)
		require.NoError(t, err)
		events := owner.OutboxEvent.Query().Where(outboxevent.EventTypeEQ(handler.EventType())).Order(ent.Asc(outboxevent.FieldID)).AllX(ctx)
		require.Len(t, events, 2)
		require.Error(t, handler.Deliver(ctx, events[0]), "unclaimed delivery must fail closed")
		require.Empty(t, receiver.guids)
		require.NoError(t, worker.DispatchOnce(ctx))
		require.Equal(t, "published", owner.OutboxEvent.GetX(ctx, events[0].ID).Status)
		require.Equal(t, "pending", owner.OutboxEvent.GetX(ctx, events[1].ID).Status)
		require.Len(t, receiver.guids, 1)
		require.NoError(t, worker.DispatchOnce(ctx))
		require.Equal(t, "published", owner.OutboxEvent.GetX(ctx, events[1].ID).Status)
		require.Equal(t, []string{"local-task", "local-task"}, receiver.guids)
		require.NotEqual(t, receiver.tasks[0].Priority, receiver.tasks[1].Priority, "queued snapshots must preserve each command's priority")
		require.NoError(t, worker.DispatchOnce(ctx))
		require.Len(t, receiver.guids, 2)
		// An ambiguous provider response must block the target's successor.
		third := second
		third.Meta.OperationID += "-third"
		third.Meta.ExpectedVersion = owner.Ticket.GetX(ctx, item.ID).Version
		_, err = svc.EscalateTicket(ctx, third)
		require.NoError(t, err)
		fourth := third
		fourth.Meta.OperationID += "-fourth"
		fourth.Meta.ExpectedVersion = owner.Ticket.GetX(ctx, item.ID).Version
		_, err = svc.EscalateTicket(ctx, fourth)
		require.NoError(t, err)
		receiver.responseGUID = "unexpected-remote-task"
		require.NoError(t, worker.DispatchOnce(ctx))
		events = owner.OutboxEvent.Query().Where(outboxevent.EventTypeEQ(handler.EventType())).Order(ent.Asc(outboxevent.FieldID)).AllX(ctx)
		require.Len(t, events, 4)
		require.Equal(t, "blocked", events[2].Status)
		require.Contains(t, events[2].LastError, "delivery_unknown")
		require.Equal(t, "pending", events[3].Status)
		require.NoError(t, worker.DispatchOnce(ctx))
		require.Len(t, receiver.guids, 3, "ambiguous delivery and successor must not be retried")
		for _, fault := range []string{"destination", "mapping", "actor", "payload", "provider-error", "post-call-mapping", "mapping-receipt", "claim-lock"} {
			t.Run(fault, func(t *testing.T) {
				receiver.responseGUID = ""
				receiver.destination = "local-test-destination"
				receiver.afterUpdate = nil
				receiver.updateError = nil
				target, err := app.Create(ctx, identity, command("feishu-fault-"+fault, "generic"))
				require.NoError(t, err)
				guid := "local-task-" + fault
				mapping := owner.FeishuTicketSync.Create().SetTenantID(tenant.ID).SetTicketID(target.WorkItemID).SetFeishuTaskID(guid).SetFeishuTaskGUID(guid).SetSyncStatus("pending").SaveX(ctx)
				c := cmd
				c.WorkItemID = target.WorkItemID
				c.Meta.OperationID = "feishu-fault-" + fault
				c.Meta.ExpectedVersion = owner.Ticket.GetX(ctx, target.WorkItemID).Version
				_, err = svc.EscalateTicket(ctx, c)
				require.NoError(t, err)
				event := owner.OutboxEvent.Query().Where(outboxevent.EventTypeEQ(handler.EventType()), outboxevent.ExecutionWorkItemIDEQ(target.WorkItemID)).OnlyX(ctx)
				calls := len(receiver.guids)
				activeHook := false
				var claimRaceErr error
				switch fault {
				case "destination":
					receiver.destination = "changed-destination"
				case "mapping":
					owner.FeishuTicketSync.UpdateOneID(mapping.ID).SetFeishuTaskGUID("changed-guid").SaveX(ctx)
				case "actor":
					owner.User.UpdateOneID(actor.ID).SetActive(false).SaveX(ctx)
				case "payload":
					var payload map[string]any
					require.NoError(t, json.Unmarshal(event.Payload, &payload))
					payload["task"].(map[string]any)["summary"] = "tampered"
					data, err := json.Marshal(payload)
					require.NoError(t, err)
					owner.OutboxEvent.UpdateOneID(event.ID).SetPayload(data).SaveX(ctx)
				case "provider-error":
					receiver.updateError = errors.New("local ambiguous provider error")
				case "post-call-mapping":
					receiver.afterUpdate = func() {
						owner.FeishuTicketSync.UpdateOneID(mapping.ID).SetFeishuTaskGUID("changed-after-send").SaveX(ctx)
					}
				case "claim-lock":
					activeHook = true
					runtime.FeishuTicketSync.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
							if activeHook {
								other, e := ownerDB.BeginTx(ctx, nil)
								if e != nil {
									return nil, e
								}
								defer other.Rollback()
								if _, e = other.ExecContext(ctx, "SET LOCAL lock_timeout = '100ms'"); e != nil {
									return nil, e
								}
								_, claimRaceErr = other.ExecContext(ctx, "UPDATE outbox_events SET status='blocked' WHERE id=$1", event.ID)
							}
							return next.Mutate(ctx, m)
						})
					})
				case "mapping-receipt":
					activeHook = true
					runtime.FeishuTicketSync.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
							v, err := next.Mutate(ctx, m)
							if activeHook && err == nil {
								return nil, errors.New("local mapping receipt failure")
							}
							return v, err
						})
					})
				}
				dispatchErr := worker.DispatchOnce(ctx)
				activeHook = false
				owner.User.UpdateOneID(actor.ID).SetActive(true).SaveX(ctx)
				receiver.destination = "local-test-destination"
				receiver.afterUpdate = nil
				receiver.updateError = nil
				require.NoError(t, dispatchErr)
				result := owner.OutboxEvent.GetX(ctx, event.ID)
				if fault == "claim-lock" {
					require.Error(t, claimRaceErr, "the completion transaction must exclude concurrent claim recovery")
					require.Contains(t, claimRaceErr.Error(), "lock timeout")
					require.Equal(t, "published", result.Status)
					require.Equal(t, "synced", owner.FeishuTicketSync.GetX(ctx, mapping.ID).SyncStatus)
					return
				}
				require.Equal(t, "blocked", result.Status)
				if fault == "provider-error" || fault == "post-call-mapping" || fault == "mapping-receipt" {
					require.Len(t, receiver.guids, calls+1)
					require.Contains(t, result.LastError, "delivery_unknown")
				} else {
					require.Len(t, receiver.guids, calls)
				}
				require.Equal(t, "pending", owner.FeishuTicketSync.GetX(ctx, mapping.ID).SyncStatus, "failed delivery must not persist a successful mapping receipt")
			})
		}
		for _, fault := range []string{"outbox", "audit"} {
			target, err := app.Create(ctx, identity, command("feishu-producer-"+fault, "generic"))
			require.NoError(t, err)
			guid := "producer-task-" + fault
			owner.FeishuTicketSync.Create().SetTenantID(tenant.ID).SetTicketID(target.WorkItemID).SetFeishuTaskID(guid).SetFeishuTaskGUID(guid).SaveX(ctx)
			c := cmd
			c.WorkItemID = target.WorkItemID
			c.Meta.OperationID = "feishu-producer-" + fault
			c.Meta.ExpectedVersion = owner.Ticket.GetX(ctx, target.WorkItemID).Version
			beforeNotifications := owner.Notification.Query().CountX(ctx)
			beforeEvents := owner.OutboxEvent.Query().CountX(ctx)
			beforeAudits := owner.AuditLog.Query().CountX(ctx)
			active := true
			writes := 0
			injected := errors.New("Feishu producer " + fault + " write failure")
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					v, e := next.Mutate(ctx, m)
					if active && e == nil {
						writes++
						return nil, injected
					}
					return v, e
				})
			}
			if fault == "outbox" {
				runtime.OutboxEvent.Use(hook)
			} else {
				runtime.AuditLog.Use(hook)
			}
			_, err = svc.EscalateTicket(ctx, c)
			active = false
			require.ErrorIs(t, err, injected)
			require.Equal(t, 1, writes)
			require.Equal(t, c.Meta.ExpectedVersion, owner.Ticket.GetX(ctx, target.WorkItemID).Version)
			require.Equal(t, beforeNotifications, owner.Notification.Query().CountX(ctx))
			require.Equal(t, beforeEvents, owner.OutboxEvent.Query().CountX(ctx))
			require.Equal(t, beforeAudits, owner.AuditLog.Query().CountX(ctx))
			_, err = svc.EscalateTicket(ctx, c)
			require.NoError(t, err)
			require.Equal(t, 1, owner.OutboxEvent.Query().Where(outboxevent.EventTypeEQ(handler.EventType()), outboxevent.ExecutionWorkItemIDEQ(target.WorkItemID)).CountX(ctx))
		}
	})
	t.Run("tool edit requires approved source in business transaction", func(t *testing.T) {
		var installed bool
		require.NoError(t, ownerDB.QueryRow(`SELECT to_regclass('public.execution_tool_invocations') IS NOT NULL`).Scan(&installed))
		if !installed {
			_, err := ownerDB.ExecContext(ctx, migration.GetMigrationSQL(migration.ToolInvocationExecutionScopeVersion))
			require.NoError(t, err)
		}
		_, err := ownerDB.ExecContext(ctx, "GRANT SELECT ON execution_tool_invocations TO "+runtimeRole)
		require.NoError(t, err)
		ensureToolAuthorityLock()
		created, err := app.Create(ctx, identity, command("tool-edit-source", "generic"))
		require.NoError(t, err)
		item := owner.Ticket.GetX(ctx, created.WorkItemID)
		svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy})
		cmd := dto.TicketEditCommand{WorkItemID: item.ID, Fields: dto.TicketEditFields{AssigneeID: actor.ID}, Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, OperationID: "tool:update_ticket:999999", Source: "ai_tool"}}
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&before))
		_, err = svc.UpdateTicket(ctx, cmd)
		assert.ErrorIs(t, err, executionscope.ErrDenied, "unknown invocation must not authorize tool edit")
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&after))
		assert.JSONEq(t, before, after)
		tx, err := runtime.Tx(ctx)
		require.NoError(t, err)
		require.NoError(t, policy.BindEnt(ctx, tx, tenant.ID))
		call, err := tx.ToolInvocation.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetToolName("update_ticket").SetArguments(fmt.Sprintf(`{"ticket_id":%d,"expectedVersion":%d,"assignee_id":%d}`, item.ID, item.Version, actor.ID)).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(actor.ID).SetApprovedAt(time.Now()).SetStatus("pending").Save(ctx)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
		cmd.Meta.OperationID = fmt.Sprintf("tool:update_ticket:%d", call.ID)
		for _, field := range []string{"title", "version", "source", "operation", "pending"} {
			t.Run("reject "+field, func(t *testing.T) {
				changed := cmd
				switch field {
				case "title":
					changed.Fields.Title = "Not approved"
				case "version":
					changed.Meta.ExpectedVersion++
				case "source":
					changed.Meta.Source = "http"
				case "operation":
					changed.Meta.OperationID += "0"
				case "pending":
					owner.ToolInvocation.UpdateOneID(call.ID).SetApprovalState("pending").ExecX(ctx)
					defer owner.ToolInvocation.UpdateOneID(call.ID).SetApprovalState("approved").ExecX(ctx)
				}
				_, err := svc.UpdateTicket(ctx, changed)
				require.Error(t, err)
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&after))
				require.JSONEq(t, before, after)
			})
		}
		queue := service.NewToolQueue(runtime, nil, app, svc, 1, zap.NewNop().Sugar(), policy)
		defer queue.Close()
		var armed atomic.Bool
		armed.Store(true)
		injected := errors.New("private tool edit post-update failure")
		runtime.Ticket.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(c context.Context, m ent.Mutation) (ent.Value, error) {
				value, err := next.Mutate(c, m)
				if err == nil && m.Op().Is(ent.OpUpdate|ent.OpUpdateOne) && armed.CompareAndSwap(true, false) {
					return nil, injected
				}
				return value, err
			})
		})
		audits := owner.AuditLog.Query().CountX(ctx)
		err = queue.ProcessJob(ctx, service.ToolJob{InvocationID: call.ID, TenantID: tenant.ID})
		require.ErrorIs(t, err, injected)
		require.False(t, armed.Load())
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&after))
		require.JSONEq(t, before, after)
		require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx), "failed edit must not commit its receipt")
		require.NoError(t, queue.ProcessJob(ctx, service.ToolJob{InvocationID: call.ID, TenantID: tenant.ID}))
		require.Equal(t, item.Version+1, owner.Ticket.GetX(ctx, item.ID).Version)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&after))
		_, err = svc.UpdateTicket(ctx, cmd)
		require.NoError(t, err, "legitimate business receipt replay remains available")
		var replayed string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&replayed))
		require.JSONEq(t, after, replayed)
		owner.ToolInvocation.UpdateOneID(call.ID).SetApprovalState("rejected").ExecX(ctx)
		_, err = svc.UpdateTicket(ctx, cmd)
		require.ErrorIs(t, err, creation.ErrPermissionDenied, "revoked approval must block receipt replay")
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&replayed))
		require.JSONEq(t, after, replayed)

	})
	t.Run("ticket edit receipt and Feishu intent commit together", func(t *testing.T) {
		for _, fault := range []string{"audit", "outbox"} {
			t.Run(fault, func(t *testing.T) {
				receiver := &candidateFeishuUpdater{destination: "edit-local-" + fault}
				registry := connector.NewRegistry()
				registry.Register(func() connector.Connector { return receiver })
				manager := candidateDeclaredManager(t, ctx, tenant.ID, scopeID, registry, config.ConnectorTargetConfig{Name: "feishu", Provider: "local-test", DestinationDigest: receiver.DeliveryDestinationIdentity(), Capabilities: []string{"outbox"}})
				created, err := app.Create(ctx, identity, command("edit-atomic-"+fault, "generic"))
				require.NoError(t, err)
				item := owner.Ticket.GetX(ctx, created.WorkItemID)
				mapping := owner.FeishuTicketSync.Create().SetTenantID(tenant.ID).SetTicketID(item.ID).SetFeishuTaskID("edit-task-" + fault).SetFeishuTaskGUID("edit-task-" + fault).SetSyncStatus("synced").SaveX(ctx)
				svc := service.NewTicketService(&service.TicketServiceConfig{Client: runtime, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar(), Execution: policy, ConnectorManager: manager})
				cmd := dto.TicketEditCommand{WorkItemID: item.ID, Fields: dto.TicketEditFields{Title: "Atomic edit " + fault, Tags: []string{"atomic-edit-" + fault}}, Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, OperationID: "edit-atomic-" + fault, Source: "http"}}
				var before, after string
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&before))
				tags, audits, events := owner.TicketTag.Query().CountX(ctx), owner.AuditLog.Query().CountX(ctx), owner.OutboxEvent.Query().CountX(ctx)
				active, writes := true, 0
				defer func() { active = false }()
				injected := errors.New("edit " + fault + " write failure")
				hook := func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						v, e := next.Mutate(ctx, m)
						if active && e == nil {
							writes++
							return nil, injected
						}
						return v, e
					})
				}
				if fault == "audit" {
					runtime.AuditLog.Use(hook)
				} else {
					runtime.OutboxEvent.Use(hook)
				}
				_, err = svc.UpdateTicket(ctx, cmd)
				active = false
				require.ErrorIs(t, err, injected)
				require.Equal(t, 1, writes)
				require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, item.ID).Scan(&after))
				require.JSONEq(t, before, after)
				require.Equal(t, tags, owner.TicketTag.Query().CountX(ctx))
				require.Empty(t, owner.Ticket.GetX(ctx, item.ID).QueryTags().IDsX(ctx))
				require.Equal(t, audits, owner.AuditLog.Query().CountX(ctx))
				require.Equal(t, events, owner.OutboxEvent.Query().CountX(ctx))
				require.Empty(t, receiver.guids)
				result, err := svc.UpdateTicket(ctx, cmd)
				require.NoError(t, err)
				require.Equal(t, item.Version+1, result.Version)
				require.Equal(t, audits+1, owner.AuditLog.Query().CountX(ctx))
				require.Equal(t, events+1, owner.OutboxEvent.Query().CountX(ctx))
				require.Empty(t, receiver.guids)
				replay, err := svc.UpdateTicket(ctx, cmd)
				require.NoError(t, err)
				require.True(t, replay.Replayed)
				require.Equal(t, result.Version, replay.Version)
				require.Equal(t, events+1, owner.OutboxEvent.Query().CountX(ctx))
				event := owner.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(item.ID), outboxevent.EventTypeEQ(service.FeishuUpdateRequestedEventType)).OnlyX(ctx)
				handler := service.NewFeishuUpdateDeliveryHandler(runtime, policy, clients.IntakeDirectorySnapshot(), func(id int) (service.FeishuTaskUpdater, bool) { return receiver, id == tenant.ID })
				reserved := []string{}
				seen := map[string]bool{}
				for _, row := range owner.OutboxEvent.Query().AllX(ctx) {
					if row.EventType != handler.EventType() && !seen[row.EventType] {
						reserved = append(reserved, row.EventType)
						seen[row.EventType] = true
					}
				}
				deliveryRegistry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{handler}, reserved...)
				require.NoError(t, err)
				worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(clients.System, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 100, PollInterval: time.Second, HandlerTimeout: 10 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), deliveryRegistry)
				require.NoError(t, err)
				require.NoError(t, worker.DispatchOnce(ctx))
				require.Equal(t, "published", owner.OutboxEvent.GetX(ctx, event.ID).Status)
				require.Equal(t, []string{mapping.FeishuTaskGUID}, receiver.guids)
				require.Equal(t, item.TicketNumber+" "+cmd.Fields.Title, receiver.tasks[0].Name)
			})
		}
	})

	t.Run("manual escalation preserves historical WorkItems", func(t *testing.T) {
		svc := service.NewTicketService(&service.TicketServiceConfig{Execution: policy, Repository: ticketrepo.NewEntRepository(runtime, zap.NewNop().Sugar()), Client: runtime, Logger: zap.NewNop().Sugar(), NotificationService: newCandidateNotificationOwner(t, runtime, zap.NewNop().Sugar(), policy)})

		var legacyBefore, legacyAfter string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, legacyManualItem.WorkItemID).Scan(&legacyBefore))
		legacyReplay, err := svc.EscalateTicket(ctx, legacyManualCommand)
		require.NoError(t, err)
		require.True(t, legacyReplay.Replayed)
		require.Equal(t, legacyManualResult.Version, legacyReplay.Version)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, legacyManualItem.WorkItemID).Scan(&legacyAfter))
		require.JSONEq(t, legacyBefore, legacyAfter)
		makeCommand := func(id, version int, key string) dto.TicketEscalationCommand {
			return dto.TicketEscalationCommand{WorkItemID: id, Reason: "bounded manual escalation", Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: version, OperationID: key, Source: "http"}}
		}
		old := owner.Ticket.UpdateOneID(historicalAlertItems[1]).SetPriority("high").SaveX(ctx)
		var before, after string
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, old.ID).Scan(&before))
		_, err = svc.EscalateTicket(ctx, makeCommand(old.ID, old.Version, "historical-escalate"))
		assert.ErrorIs(t, err, executionscope.ErrDenied)
		require.NoError(t, ownerDB.QueryRow(`SELECT row_to_json(t)::text FROM tickets t WHERE id=$1`, old.ID).Scan(&after))
		assert.JSONEq(t, before, after)
		fresh, err := app.Create(ctx, identity, command("manual-escalation-member", "generic"))
		require.NoError(t, err)
		current := owner.Ticket.UpdateOneID(fresh.WorkItemID).SetPriority("high").SaveX(ctx)
		cmd := makeCommand(current.ID, current.Version, "manual-escalate-once")
		updated, err := svc.EscalateTicket(ctx, cmd)
		require.NoError(t, err)
		row := owner.Ticket.GetX(ctx, current.ID)
		require.Equal(t, "critical", row.Priority)
		require.Zero(t, row.AssigneeID, "no invented assignee")
		require.Equal(t, current.Version+1, updated.Version)
		var receipts int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM audit_logs WHERE path=$1 AND action='work_item.escalation.manual'`, fmt.Sprint(current.ID)).Scan(&receipts))
		require.Equal(t, 1, receipts)
		replay, err := svc.EscalateTicket(ctx, cmd)
		require.NoError(t, err)
		require.True(t, replay.Replayed)
		require.Equal(t, updated.Version, replay.Version)
		require.Equal(t, updated.Version, owner.Ticket.GetX(ctx, current.ID).Version)
		changed := cmd
		changed.Reason = "different intent"
		_, err = svc.EscalateTicket(ctx, changed)
		var conflict *workitemmutation.OperationConflictError
		require.ErrorAs(t, err, &conflict)
		_, err = svc.EscalateTicket(ctx, makeCommand(current.ID, updated.Version, "manual-escalate-highest"))
		require.NoError(t, err)
		require.Equal(t, "critical", owner.Ticket.GetX(ctx, current.ID).Priority)
		owner.User.UpdateOneID(actor.ID).SetActive(false).SaveX(ctx)
		_, err = svc.EscalateTicket(ctx, cmd)
		owner.User.UpdateOneID(actor.ID).SetActive(true).SaveX(ctx)
		require.Error(t, err, "replay still checks current actor")
		current = owner.Ticket.GetX(ctx, current.ID)
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='closed' WHERE id=$1", scopeID)
		require.NoError(t, err)
		_, denied := svc.EscalateTicket(ctx, makeCommand(current.ID, current.Version, "closed-manual"))
		_, err = ownerDB.ExecContext(ctx, "UPDATE execution_scopes SET status='active' WHERE id=$1", scopeID)
		require.NoError(t, err)
		require.ErrorIs(t, denied, executionscope.ErrDenied)
		for _, fault := range []string{"notification", "audit"} {
			target, err := app.Create(ctx, identity, command("manual-fault-"+fault, "generic"))
			require.NoError(t, err)
			item := owner.Ticket.GetX(ctx, target.WorkItemID)
			beforeUnified := owner.Notification.Query().CountX(ctx)
			active := true
			injected := errors.New("manual " + fault + " write failure")
			writes := 0
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					v, e := next.Mutate(ctx, m)
					if active && e == nil {
						writes++
						return nil, injected
					}
					return v, e
				})
			}
			if fault == "notification" {
				runtime.Notification.Use(hook)
			} else {
				runtime.AuditLog.Use(hook)
			}
			command := makeCommand(item.ID, item.Version, "manual-fault-"+fault)
			_, err = svc.EscalateTicket(ctx, command)
			active = false
			require.ErrorIs(t, err, injected)
			require.Equal(t, 1, writes)
			require.Equal(t, item.Version, owner.Ticket.GetX(ctx, item.ID).Version)
			require.Equal(t, beforeUnified, owner.Notification.Query().CountX(ctx))
			var count int
			require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM ticket_notifications WHERE ticket_id=$1`, item.ID).Scan(&count))
			require.Zero(t, count)
			_, err = svc.EscalateTicket(ctx, command)
			require.NoError(t, err)
		}

	})

	require.Equal(t, oldRow.Title, owner.Ticket.GetX(ctx, historical.WorkItemID).Title)
}

// candidateOutboxTestReceiver is a declared local test sink for the real worker.
// It does not send mail, call providers or replace claim/recovery logic.
type candidateOutboxTestReceiver struct{ delivered []int }

func (*candidateOutboxTestReceiver) EventType() string { return "candidate-test-delivery" }
func (r *candidateOutboxTestReceiver) Deliver(_ context.Context, event *ent.OutboxEvent) error {
	r.delivered = append(r.delivered, event.ID)
	return nil
}

// Local declared callback exercises the real engine without enterprise effects.
type candidateCallbackHandler struct{ calls int }

func (*candidateCallbackHandler) GetTaskType() string  { return "candidate-local-task" }
func (*candidateCallbackHandler) GetHandlerID() string { return "candidate-local-callback" }
func (*candidateCallbackHandler) CallbackContract(string) (bpmn.CallbackActionContract, bool) {
	return bpmn.CallbackActionContract{}, true
}
func (h *candidateCallbackHandler) Execute(context.Context, *ent.ProcessTask, map[string]interface{}) (*bpmn.CallbackEffect, error) {
	h.calls++
	return bpmn.AppliedEffect("local candidate callback", nil), nil
}

// Registered, task-local transport: records deliveries without network effects.
type candidateNotificationConnector struct {
	ids       []string
	afterSend func()
}

func (*candidateNotificationConnector) Manifest() connector.Manifest {
	return connector.Manifest{InitializationBehavior: connector.InitializationLocalOnly, Name: "webhook", Version: "1.0.0", Title: "Candidate local receiver", Type: connector.TypeEmail, Capabilities: []connector.Capability{connector.CapSendMessage}, RequiredPermissions: []string{"connector:write"}}
}
func (*candidateNotificationConnector) DeliveryDestinationIdentity() string {
	return candidateTargetDigest("local-notification-receiver")
}
func (*candidateNotificationConnector) Init(context.Context, connector.Config) error { return nil }
func (c *candidateNotificationConnector) Send(_ context.Context, m *connector.Message) error {
	c.ids = append(c.ids, m.ID)
	if c.afterSend != nil {
		c.afterSend()
	}
	return nil
}
func (*candidateNotificationConnector) HealthCheck(context.Context) connector.HealthStatus {
	return connector.HealthStatus{OK: true}
}
func (*candidateNotificationConnector) Close() error { return nil }

// This declared local receiver lets two real workers overlap deterministically.
type candidateOrderedReceiver struct {
	firstID int
	entered chan int
	release chan struct{}
}

func (*candidateOrderedReceiver) EventType() string       { return "candidate-ordered-delivery" }
func (*candidateOrderedReceiver) SerialByAggregate() bool { return true }
func (r *candidateOrderedReceiver) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	r.entered <- event.ID
	if event.ID == r.firstID {
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Explicit local Feishu test receiver; no network or enterprise credentials.
type candidateFeishuUpdater struct {
	candidateNotificationConnector
	destination  string
	guids        []string
	tasks        []feishu.FeishuTask
	afterUpdate  func()
	updateError  error
	responseGUID string
}

func (*candidateFeishuUpdater) Manifest() connector.Manifest {
	return connector.Manifest{InitializationBehavior: connector.InitializationLocalOnly, Name: "feishu", Version: "1", Title: "Local Feishu receiver", Type: connector.TypeIM, Capabilities: []connector.Capability{connector.CapUpdateTicket}, RequiredPermissions: []string{"connector:write"}}
}
func (r *candidateFeishuUpdater) DeliveryDestinationIdentity() string {
	return candidateTargetDigest(r.destination)
}
func (r *candidateFeishuUpdater) TaskDestinationIdentity() string { return r.destination }
func (r *candidateFeishuUpdater) UpdateTask(_ context.Context, guid string, task *feishu.FeishuTask) (*feishu.FeishuTask, error) {
	r.guids = append(r.guids, guid)
	r.tasks = append(r.tasks, *task)
	if r.afterUpdate != nil {
		r.afterUpdate()
	}
	if r.updateError != nil {
		return nil, r.updateError
	}
	result := *task
	result.GUID = guid
	if r.responseGUID != "" {
		result.GUID = r.responseGUID
	}
	return &result, nil
}

// Delegates to the real handler after a deterministic post-claim invalidation.
type candidateEscalationHandler struct {
	lastError error
	*bpmn.TicketServiceTaskHandler
	before func()
	calls  int
}

func (h *candidateEscalationHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*bpmn.CallbackEffect, error) {
	h.calls++
	if h.before != nil {
		h.before()
	}
	effect, err := h.TicketServiceTaskHandler.Execute(ctx, task, variables)
	h.lastError = err
	return effect, err
}

// editCommandForTest assembles a trusted fixture boundary without replacing the observed version.
func editCommandForTest(id int, input *dto.TicketEditCommand, tenantID int) dto.TicketEditCommand {
	cmd := *input
	cmd.WorkItemID = id
	cmd.Meta.TenantID = tenantID
	cmd.Meta.Source = "test"
	if cmd.Meta.OperationID == "" {
		cmd.Meta.OperationID = fmt.Sprintf("test-edit:%d:%d", id, cmd.Meta.ExpectedVersion)
	}
	return cmd
}

type candidateSourceCaptureBus struct{ events []interface{} }

func (b *candidateSourceCaptureBus) Publish(event interface{}) error {
	b.events = append(b.events, event)
	return nil
}
func (*candidateSourceCaptureBus) Subscribe(string, shared.EventHandler) error { return nil }

// Test-only ACK gap: the real owner commits before this handler waits for the
// old subscriber's context to close. Returning its cancellation leaves the PEL.
type candidateCommitBeforeAck struct {
	audit interface {
		EventConsumerID() string
		HandleContext(context.Context, interface{}) error
	}
	committed chan struct{}
}

func (h *candidateCommitBeforeAck) EventConsumerID() string { return h.audit.EventConsumerID() }
func (*candidateCommitBeforeAck) Handle(interface{}) error {
	return errors.New("consumption context required")
}
func (h *candidateCommitBeforeAck) HandleContext(ctx context.Context, event interface{}) error {
	if err := h.audit.HandleContext(ctx, event); err != nil {
		return err
	}
	h.committed <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

type standardExecutionObserver struct{ candidateStreamObserver }

func (standardExecutionObserver) ExecutionEnvelopeRequired() {}

func (*candidateCommitBeforeAck) ExecutionEnvelopeRequired() {}

// candidateCreationFunc schedules around the actual application; callers must
// retain its real result/error and use Ent hooks for transaction fault injection.
type candidateCreationFunc func(context.Context, creation.Identity, creation.CreateWorkItemCommand) (*creation.CreateWorkItemResult, error)

func (f candidateCreationFunc) Create(ctx context.Context, who creation.Identity, command creation.CreateWorkItemCommand) (*creation.CreateWorkItemResult, error) {
	return f(ctx, who, command)
}

// candidateHealthProbe performs only a request to the test-owned loopback receiver.
type candidateHealthProbe struct {
	candidateNotificationConnector
	target   string
	tenantID int
}

func (p *candidateHealthProbe) Init(_ context.Context, cfg connector.Config) error {
	p.tenantID = cfg.TenantID
	return nil
}

func (p *candidateHealthProbe) HealthCheck(ctx context.Context) connector.HealthStatus {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%d", p.target, p.tenantID), nil)
	if err != nil {
		return connector.HealthStatus{OK: false}
	}
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		return connector.HealthStatus{OK: false}
	}
	response.Body.Close()
	return connector.HealthStatus{OK: response.StatusCode == http.StatusNoContent}
}

type candidateRestoreProbe struct {
	candidateNotificationConnector
	inits *atomic.Int32
}

func (p *candidateRestoreProbe) Init(context.Context, connector.Config) error {
	p.inits.Add(1)
	return nil
}

// Real deployment policy helpers: candidate positive journeys activate frozen declarations.
func candidateTestStandardManagement(t *testing.T) *database.ExecutionPolicy {
	t.Helper()
	p, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "standard-manager-fixture"})
	require.NoError(t, err)
	return p
}
func candidateTargetDigest(value string) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
func candidateWebhookTarget(provider, endpoint string) config.ConnectorTargetConfig {
	return config.ConnectorTargetConfig{Name: "webhook", Provider: provider, DestinationDigest: candidateTargetDigest(endpoint), Capabilities: []string{"webhook"}, Settings: map[string]interface{}{"url": endpoint}}
}
func candidateDeclaredManager(t *testing.T, ctx context.Context, tenantID int, scopeID string, registry *connector.Registry, targets ...config.ConnectorTargetConfig) *connector.Manager {
	t.Helper()
	execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenantID, ScopeID: scopeID}}, Capabilities: map[string]string{}}
	for i := range targets {
		targets[i].TenantID, targets[i].ScopeID = tenantID, scopeID
		for _, capability := range targets[i].Capabilities {
			execution.Capabilities[capability] = "scoped"
		}
	}
	execution.ConnectorTargets = targets
	policy, err := database.NewExecutionPolicy(execution)
	require.NoError(t, err)
	manager := connector.NewManager(registry, zap.NewNop().Sugar(), policy)
	t.Cleanup(manager.CloseAll)
	require.NoError(t, manager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:declared-delivery", "activate local declared fixture targets")))
	return manager
}

// Seeds a fresh private tenant/WorkItem, then uses the real standard SLA source
// producer. This is delivery testing, not WorkItem creation or role admission E2E.
type standardWebhookSourceFixture struct {
	ctx                  context.Context
	tenantID, workItemID int
	policy               *database.ExecutionPolicy
	source               *ent.OutboxEvent
}

func newStandardWebhookSourceFixture(t *testing.T, parent context.Context, client *ent.Client, name string) standardWebhookSourceFixture {
	t.Helper()
	tenant := client.Tenant.Create().SetName("Standard webhook " + name).SetCode("webhook-" + name).SaveX(parent)
	ctx := tenantctx.WithTenantID(parent, tenant.ID)
	user := client.User.Create().SetTenantID(tenant.ID).SetUsername("webhook-" + name).SetName("Private webhook fixture").SetEmail(name + "@example.invalid").SetPasswordHash("test-only").SetRole("requester").SaveX(ctx)
	sla := client.SLADefinition.Create().SetTenantID(tenant.ID).SetName("Private webhook SLA").SetResponseTime(60).SetResolutionTime(240).SaveX(ctx)
	item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(user.ID).SetTitle("Private standard webhook source").SetTicketNumber("STANDARD-WEBHOOK-" + name).SetSLADefinitionID(sla.ID).SetSLAResponseDeadline(time.Now().Add(-time.Hour)).SetSLAResolutionDeadline(time.Now().Add(time.Hour)).SaveX(ctx)
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "standard-webhook-test", Capabilities: map[string]string{"webhook": "enabled", "outbox": "enabled"}})
	require.NoError(t, err)
	monitor := service.NewSLAMonitorService(client, zap.NewNop().Sugar(), policy)
	monitor.SetNotificationService(newCandidateNotificationOwner(t, client, zap.NewNop().Sugar(), policy))
	_, err = monitor.CheckSLAViolations(ctx, tenant.ID)
	require.NoError(t, err)
	source := client.OutboxEvent.Query().Where(outboxevent.ExecutionWorkItemIDEQ(item.ID), outboxevent.EventTypeEQ("sla.breached")).OnlyX(ctx)
	return standardWebhookSourceFixture{ctx, tenant.ID, item.ID, policy, source}
}

// Inject only the resolver failure; startup still uses the real frozen policy.
type webhookResolverFailureGate struct {
	*database.ExecutionPolicy
	err error
}

func (g *webhookResolverFailureGate) RequireConnectorDelivery(context.Context, executionscope.Ref, string) error {
	return g.err
}

// Actual configured notification channel, without adding a product preference.
type candidateSMSConnector struct{ candidateNotificationConnector }

func (*candidateSMSConnector) Manifest() connector.Manifest {
	m := (&candidateNotificationConnector{}).Manifest()
	m.Name = "sms"
	return m
}

type candidateNotificationMailProbe struct{ recipients []string }

func (p *candidateNotificationMailProbe) SendMail(_ context.Context, _ string, to, _, _, _ string) error {
	p.recipients = append(p.recipients, to)
	return nil
}
