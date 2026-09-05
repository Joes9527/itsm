//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	catalogdomain "itsm-backend/handlers/service_catalog"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
)

// A real PostgreSQL unique violation after allocation must roll back the entire
// Intake transaction and leave its first number available to a fresh request.
func TestPostgresIntakeInsertFailureRollsBackAllocationAndReusesNumber(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	require.Equal(t, "postgres://postgres@127.0.0.1:36444/sslvpn_test?sslmode=disable", dsn, "only the dedicated disposable database is permitted")
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(ctx))
	schema := fmt.Sprintf("intake_allocation_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
		var remaining int
		require.NoError(t, db.QueryRowContext(context.Background(), "SELECT count(*) FROM pg_namespace WHERE nspname=$1", schema).Scan(&remaining))
		require.Zero(t, remaining)
		t.Logf("isolated schema %s removed; remaining=%d", schema, remaining)
	})
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	client, err := ent.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, client.Schema.Create(ctx))
	tenant := client.Tenant.Create().SetName("Intake allocation").SetCode("allocation").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("actor").SetName("Actor").SetEmail("actor@example.test").SetPasswordHash("unused").SetActive(true).SetRole("agent").SaveX(ctx)
	role := client.Role.Create().SetTenantID(tenant.ID).SetCode("agent").SetName("Agent").SaveX(ctx)
	for _, action := range []string{"read", "write"} {
		permission := client.Permission.Create().SetTenantID(tenant.ID).SetCode("ticket:" + action).SetName("Ticket " + action).SetResource("ticket").SetAction(action).SaveX(ctx)
		client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
	}
	client.ProcessBinding.Create().SetTenantID(tenant.ID).SetBusinessType("ticket").SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	logger := zap.NewNop().Sugar()
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(&service.TicketService{}))
	resolver := intake.NewResolver(catalogdomain.NewService(nil, client, logger), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
	app := intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()))
	identity := creation.Identity{TenantID: tenant.ID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "itsm_web"}
	command := creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", IdempotencyKey: "collision", Title: "Fails after number allocation", Description: "The existing first number forces a real PostgreSQL unique violation"}
	period := time.Now().UTC().Format("200601")
	firstNumber := "TKT-" + period + "-000001"
	seeded := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetTicketNumber(firstNumber).SetRecordClass("generic").SetTitle("Own conflicting fixture").SetStatus("new").SetPriority("medium").SaveX(ctx)
	result, err := app.Create(ctx, identity, command)
	require.Nil(t, result)
	require.Error(t, err)
	var pgErr *pq.Error
	require.True(t, errors.As(err, &pgErr), "expected the actual PostgreSQL insert failure, got %v", err)
	require.Equal(t, pq.ErrorCode("23505"), pgErr.Code)
	require.Equal(t, "tickets", pgErr.Table)
	require.Contains(t, pgErr.Detail, firstNumber)
	require.Contains(t, pgErr.Detail, "ticket_number")
	t.Logf("actual PostgreSQL WorkItem insert rejected: SQLSTATE=%s table=%s constraint=%s number=%s", pgErr.Code, pgErr.Table, pgErr.Constraint, firstNumber)
	require.Equal(t, []int{seeded.ID}, client.Ticket.Query().IDsX(ctx), "only the seeded conflict may remain")
	require.Zero(t, client.IntakeRequest.Query().CountX(ctx))
	require.Zero(t, client.IntakeResolutionSnapshot.Query().CountX(ctx))
	require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
	require.Zero(t, client.AuditLog.Query().CountX(ctx))
	require.Zero(t, client.WorkItemNumberSequence.Query().CountX(ctx), "the failed real insert must roll back its allocation")
	require.Zero(t, client.WorkItemRelation.Query().CountX(ctx))
	require.Zero(t, client.FieldValue.Query().CountX(ctx))
	require.Zero(t, client.Incident.Query().CountX(ctx))
	require.Zero(t, client.Change.Query().CountX(ctx))
	require.Zero(t, client.Problem.Query().CountX(ctx))
	require.Zero(t, client.ServiceRequest.Query().CountX(ctx))
	require.NoError(t, client.Ticket.DeleteOneID(seeded.ID).Exec(ctx))
	command.IdempotencyKey = "fresh-after-collision"
	command.Title = "Fresh request reuses rolled-back allocation"
	result, err = app.Create(ctx, identity, command)
	require.NoError(t, err)
	require.Equal(t, period, time.Now().UTC().Format("200601"), "fixture crossed a UTC month boundary")
	require.Equal(t, firstNumber, result.Number)
	require.False(t, result.Replayed)
	require.Equal(t, firstNumber, client.Ticket.GetX(ctx, result.WorkItemID).TicketNumber)
	require.Equal(t, 1, client.Ticket.Query().CountX(ctx))
	require.Equal(t, 1, client.IntakeRequest.Query().CountX(ctx))
	require.Equal(t, 1, client.IntakeResolutionSnapshot.Query().CountX(ctx))
	require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
	require.Equal(t, int64(1), client.WorkItemNumberSequence.Query().OnlyX(ctx).LastValue)
	t.Logf("fresh real Intake request committed exact rolled-back number %s", result.Number)
}
