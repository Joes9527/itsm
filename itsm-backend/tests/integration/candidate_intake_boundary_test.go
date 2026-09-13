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

	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	problemdomain "itsm-backend/handlers/problem"
	catalogdomain "itsm-backend/handlers/service_catalog"
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
		for _, creator := range []creation.ProfessionalCreator{&service.TicketService{}, service.NewIncidentService(client, logger), problemdomain.NewService(nil, logger)} {
			require.NoError(t, registry.Register(creator))
		}
		resolver := intake.NewResolver(catalogdomain.NewService(nil, client, logger, nil), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
		return intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), directory, policy)
	}
	historicalApp := application(owner, executionfixture.Standard(), sameTransactionDirectory{})
	oldCommand := command("historical", "incident")
	historical, err := historicalApp.Create(ctx, identity, oldCommand)
	require.NoError(t, err)
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
