//go:build candidate_scope

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	problemdomain "itsm-backend/handlers/problem"
	catalogdomain "itsm-backend/handlers/service_catalog"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/migration"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

// This uses the real intake service, resolver, base writer and professional
// owners, enforced tenant driver and restricted production directory snapshot.
// No application runtime or external delivery is started.
func TestCandidateIntakeCreationBoundary(t *testing.T) {
	socket := os.Getenv("CANDIDATE_SCOPE_TEST_SOCKET")
	if socket == "" {
		t.Skip("requires an explicitly isolated PostgreSQL socket")
	}
	require.True(t, filepath.IsAbs(socket))
	marker, err := os.ReadFile(filepath.Join(socket, "candidate-test-instance"))
	require.NoError(t, err)
	require.Equal(t, "itsm-candidate-isolated-test\n", string(marker))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name := "intake_scope_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	dsn := func(db, role string) string {
		return fmt.Sprintf("host=%s port=25439 dbname=%s user=%s sslmode=disable", socket, db, role)
	}
	admin, err := sql.Open("postgres", dsn("postgres", "candidate_test_owner"))
	require.NoError(t, err)
	defer admin.Close()
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	runtimeRole := name + "_run"
	roleCreated := false
	systemRole := name + "_system"
	systemCreated := false
	defer func() {
		_, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		require.NoError(t, err)
		if systemCreated {
			_, err = admin.Exec("DROP ROLE " + systemRole)
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
	owner.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
	for _, class := range []string{"generic", "incident", "problem"} {
		owner.ProcessBinding.Create().SetTenantID(tenant.ID).SetBusinessType(class).SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	}
	identity := creation.Identity{TenantID: tenant.ID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "http"}
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)
	command := func(key, class string) creation.CreateWorkItemCommand {
		return creation.CreateWorkItemCommand{RecordClass: class, IntakeKind: class, Confirmation: "confirmed", Title: "scope " + key, IdempotencyKey: key}
	}
	application := func(client *ent.Client, policy *database.ExecutionPolicy, directory database.DirectorySnapshot) *intake.Service {
		logger := zap.NewNop().Sugar()
		registry := intake.NewCreatorRegistry()
		for _, creator := range []creation.ProfessionalCreator{&service.TicketService{}, service.NewIncidentService(client, logger, policy), problemdomain.NewService(nil, logger)} {
			require.NoError(t, registry.Register(creator))
		}
		resolver := intake.NewResolver(catalogdomain.NewService(nil, client, logger, nil), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
		return intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), directory, policy)
	}
	historicalApp := application(owner, executionfixture.Standard(), sameTransactionDirectory{})
	oldCommand := command("historical", "incident")
	historical, err := historicalApp.Create(ctx, identity, oldCommand)
	require.NoError(t, err)
	legacyCommand := dto.IncidentCommand{IncidentID: historical.ProfessionalReference.ID, Action: "acknowledge", Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: 1, Source: "http", OperationID: "legacy-command"}}
	legacyOwner := service.NewIncidentService(owner, zap.NewNop().Sugar(), executionfixture.Standard())
	legacyOwner.SetDirectorySnapshot(sameTransactionDirectory{})
	legacyResult, err := legacyOwner.ApplyIncidentCommand(ctx, legacyCommand)
	require.NoError(t, err)
	legacyAlert, err := service.NewIncidentAlertingService(owner, zap.NewNop().Sugar(), executionfixture.Standard()).CreateIncidentAlert(ctx, &dto.CreateIncidentAlertRequest{IncidentID: historical.ProfessionalReference.ID, AlertType: "legacy", AlertName: "legacy alert", Message: "historical fixture", Channels: []string{"in_app"}, Recipients: []string{actor.Email}}, tenant.ID)
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, "UPDATE outbox_events SET execution_work_item_id=NULL")
	require.NoError(t, err) // pre-039 historical fixture only
	oldRow := owner.Ticket.GetX(ctx, historical.WorkItemID)
	oldReceipt := owner.IntakeRequest.Query().Where(intakerequest.WorkItemIDEQ(historical.WorkItemID)).OnlyX(ctx)
	_, err = ownerDB.ExecContext(ctx, fmt.Sprintf("GRANT USAGE ON SCHEMA public TO %s; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO %s; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO %s", runtimeRole, runtimeRole, runtimeRole))
	require.NoError(t, err)
	_, err = ownerDB.ExecContext(ctx, migration.GetMigrationSQL("039_candidate_execution_scope"))
	require.NoError(t, err)
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
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "intake-test", Scopes: []config.ExecutionScopeConfig{{TenantID: tenant.ID, ScopeID: scopeID}}})
	require.NoError(t, err)
	app := application(runtime, policy, clients.IntakeDirectorySnapshot())
	memberCount := func() int {
		var n int
		require.NoError(t, ownerDB.QueryRow(`SELECT count(*) FROM execution_scope_members`).Scan(&n))
		return n
	}
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

	t.Run("Incident CI and alert writes require membership", func(t *testing.T) {
		svc := service.NewIncidentService(runtime, zap.NewNop().Sugar(), policy)
		alerts := service.NewIncidentAlertingService(runtime, zap.NewNop().Sugar(), policy)
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
		repo := service.NewOutboxEventRepository(runtime)
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
		runtime.Incident.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				_, err := next.Mutate(ctx, m)
				if err != nil {
					return nil, err
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
	require.Equal(t, oldRow.Title, owner.Ticket.GetX(ctx, historical.WorkItemID).Title)
}
