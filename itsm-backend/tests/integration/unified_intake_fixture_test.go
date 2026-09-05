package integration

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	problemdomain "itsm-backend/handlers/problem"
	catalogdomain "itsm-backend/handlers/service_catalog"
	requestdomain "itsm-backend/handlers/service_request"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type unifiedIntakeFixture struct {
	client   *ent.Client
	app      *intake.Service
	identity creation.Identity
	command  creation.CreateWorkItemCommand
}

func newUnifiedIntakeFixture(t *testing.T, ticketOwners ...func(*ent.Client, *zap.SugaredLogger) *service.TicketService) *unifiedIntakeFixture {
	t.Helper()
	ctx := context.Background()
	logger := zap.NewNop().Sugar()
	client := enttest.Open(t, "sqlite3", "file:"+filepath.Join(t.TempDir(), "intake.db")+"?_fk=1")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	tenant := client.Tenant.Create().SetName("Tenant").SetCode("tenant").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("user").SetName("User").SetEmail("u@example.test").SetPasswordHash("unused").SetRole("requester").SaveX(ctx)
	role := client.Role.Create().SetTenantID(tenant.ID).SetCode("requester").SetName("Requester").SaveX(ctx)
	permission := client.Permission.Create().SetTenantID(tenant.ID).SetCode("create-work").SetName("Create work").SetResource("*").SetAction("*").SaveX(ctx)
	client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
	for _, business := range []string{"ticket", "incident", "problem", "change", "service_request"} {
		client.ProcessBinding.Create().SetTenantID(tenant.ID).SetBusinessType(business).SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	}
	allocator := workitemnumber.NewPostgreSQLAllocator()
	registry := intake.NewCreatorRegistry()
	genericOwner := &service.TicketService{}
	if len(ticketOwners) > 0 {
		genericOwner = ticketOwners[0](client, logger)
	}
	for _, owner := range []creation.ProfessionalCreator{genericOwner, service.NewIncidentService(client, logger), problemdomain.NewService(nil, logger), changedomain.NewService(nil, client, logger), requestdomain.NewService(nil, client, logger, service.NewApprovalChainResolver(client, logger))} {
		require.NoError(t, registry.Register(owner))
	}
	resolver := intake.NewResolver(catalogdomain.NewService(nil, client, logger), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
	app := intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(allocator))
	return &unifiedIntakeFixture{client, app, creation.Identity{TenantID: tenant.ID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "itsm_web"}, creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", Title: "VPN access", IdempotencyKey: "one"}}
}

func installEntryMutationFailure(client *ent.Client, stage string) *bool {
	reached := new(bool)
	hook := func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, m)
			if err != nil {
				return value, err
			}
			*reached = true
			return value, errors.New("injected " + stage + " failure")
		})
	}
	switch stage {
	case "workflow start":
		client.OutboxEvent.Use(hook)
	case "snapshot":
		client.IntakeResolutionSnapshot.Use(hook)
	}
	return reached
}
func assertNoEntryGraph(t *testing.T, client *ent.Client) {
	t.Helper()
	ctx := context.Background()
	require.Zero(t, client.Ticket.Query().CountX(ctx))
	require.Zero(t, client.IntakeRequest.Query().CountX(ctx))
	require.Zero(t, client.IntakeResolutionSnapshot.Query().CountX(ctx))
	require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
	require.Zero(t, client.AuditLog.Query().CountX(ctx))
	require.Zero(t, client.WorkItemNumberSequence.Query().CountX(ctx))
}
