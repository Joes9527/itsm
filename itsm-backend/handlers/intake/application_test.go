package intake

import (
	"context"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/common/workitemcreation"
	"testing"
	"time"
)

type preparedCreator struct {
	mutate func(*workitemcreation.CreationPlan)
	fail   bool
}

func (*preparedCreator) RecordClass() string { return "generic" }
func (p *preparedCreator) Prepare(_ context.Context, _ *ent.Tx, in workitemcreation.ResolvedIntake) (*workitemcreation.CreationPlan, error) {
	plan := &workitemcreation.CreationPlan{Resolved: in, WorkItem: workitemcreation.WorkItemDraft{TenantID: in.Identity.TenantID, ActorID: in.Identity.ActorID, RequesterID: in.Identity.RequesterID, RecordClass: in.RecordClass, Title: in.Command.Title, Status: "open", Priority: "high", Source: "manual"}}
	if p.mutate != nil {
		p.mutate(plan)
	}
	return plan, nil
}
func (p *preparedCreator) CreateExtension(context.Context, *ent.Tx, *ent.Ticket, *workitemcreation.CreationPlan) (*workitemcreation.ProfessionalReference, error) {
	if p.fail {
		return nil, errors.New("extension failure")
	}
	return &workitemcreation.ProfessionalReference{}, nil
}

type preparedResolver struct{}

func (preparedResolver) Resolve(_ context.Context, _ *ent.Tx, i workitemcreation.Identity, c workitemcreation.CreateWorkItemCommand) (*workitemcreation.ResolvedIntake, error) {
	return &workitemcreation.ResolvedIntake{Identity: i, Command: c, RecordClass: c.RecordClass, Workflow: workitemcreation.ResolvedWorkflowBinding{NoProcess: true}, ResolverVersion: "test-v1"}, nil
}

type testAllocator struct{ calls int }

func (a *testAllocator) Allocate(_ context.Context, _ *ent.Client, _ int, _ time.Time) (string, error) {
	a.calls++
	return fmt.Sprintf("TKT-TEST-%06d", a.calls), nil
}
func intakeFixture(t *testing.T) (*ent.Client, *Service, workitemcreation.Identity, workitemcreation.CreateWorkItemCommand, *testAllocator, *preparedCreator) {
	t.Helper()
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { client.Close() })
	tenant := client.Tenant.Create().SetName("Tenant").SetCode("tenant").SaveX(ctx)
	user := client.User.Create().SetUsername("user").SetName("User").SetEmail("u@example.test").SetPasswordHash("test").SetTenantID(tenant.ID).SetRole("requester").SaveX(ctx)
	seedCreationPermission(t, client, tenant.ID, "requester")
	identity := workitemcreation.Identity{TenantID: tenant.ID, ActorTenantID: tenant.ID, ActorID: user.ID, RequesterID: user.ID, Channel: "itsm_web", Role: "requester"}
	cmd := workitemcreation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", IdempotencyKey: "one", Title: "VPN access"}
	registry := NewCreatorRegistry()
	creator := &preparedCreator{}
	require.NoError(t, registry.Register(creator))
	allocator := &testAllocator{}
	return client, NewService(client, preparedResolver{}, registry, NewWorkItemCreator(allocator), sameTransactionDirectory{}), identity, cmd, allocator, creator
}
func TestApplicationGenericAtomicCreationReplayAndConflict(t *testing.T) {
	client, s, i, c, n, _ := intakeFixture(t)
	ctx := context.Background()
	var app workitemcreation.Application = s
	first, err := app.Create(ctx, i, c)
	require.NoError(t, err)
	require.Equal(t, workitemcreation.ProfessionalReference{}, first.ProfessionalReference)
	again, err := app.Create(ctx, i, c)
	require.NoError(t, err)
	require.True(t, again.Replayed)
	require.Equal(t, first.WorkItemID, again.WorkItemID)
	require.Equal(t, first.Number, again.Number)
	require.Equal(t, 1, n.calls)
	require.Equal(t, 1, client.Ticket.Query().CountX(ctx))
	require.Equal(t, 1, client.IntakeRequest.Query().CountX(ctx))
	require.Equal(t, 1, client.IntakeResolutionSnapshot.Query().CountX(ctx))
	require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
	c.Title = "different"
	_, err = app.Create(ctx, i, c)
	require.ErrorIs(t, err, workitemcreation.ErrIdempotencyConflict)
}
func TestApplicationRejectsMissingDependencies(t *testing.T) {
	var s *Service
	_, err := s.Create(context.Background(), workitemcreation.Identity{}, workitemcreation.CreateWorkItemCommand{})
	require.Error(t, err)
}
func TestApplicationRejectsInvalidPreparationAndRollsBack(t *testing.T) {
	for _, scenario := range []string{"tenant", "requester", "class", "status", "priority", "number", "extension"} {
		t.Run(scenario, func(t *testing.T) {
			client, s, i, c, _, p := intakeFixture(t)
			p.mutate = func(plan *workitemcreation.CreationPlan) {
				switch scenario {
				case "tenant":
					plan.WorkItem.TenantID++
				case "requester":
					plan.WorkItem.RequesterID++
				case "class":
					plan.WorkItem.RecordClass = "incident"
				case "status":
					plan.WorkItem.Status = ""
				case "priority":
					plan.WorkItem.Priority = ""
				case "number":
					plan.WorkItem.TicketNumber = "bypass"
				case "extension":
					p.fail = true
				}
			}
			_, err := s.Create(context.Background(), i, c)
			require.Error(t, err)
			require.Zero(t, client.Ticket.Query().CountX(context.Background()))
			require.Zero(t, client.IntakeRequest.Query().CountX(context.Background()))
		})
	}
}

func TestApplicationRejectsReplayForDifferentRequester(t *testing.T) {
	_, s, i, c, _, _ := intakeFixture(t)
	_, err := s.Create(context.Background(), i, c)
	require.NoError(t, err)
	i.RequesterID++
	_, err = s.Create(context.Background(), i, c)
	require.Error(t, err)
}
func TestApplicationRejectsUnregisteredClass(t *testing.T) {
	client, s, i, c, _, _ := intakeFixture(t)
	c.RecordClass = "problem"
	c.IntakeKind = "problem"
	_, err := s.Create(context.Background(), i, c)
	require.ErrorIs(t, err, workitemcreation.ErrUnsupportedRecordClass)
	require.Zero(t, client.IntakeRequest.Query().CountX(context.Background()))
}
func TestApplicationDigestVersionMismatch(t *testing.T) {
	client, s, i, c, _, _ := intakeFixture(t)
	ctx := context.Background()
	_, digest, err := workitemcreation.CanonicalizeCommand(c)
	require.NoError(t, err)
	client.IntakeRequest.Create().SetTenantID(i.TenantID).SetActorTenantID(i.TenantID).SetActorID(i.ActorID).SetRequesterID(i.RequesterID).SetChannel(i.Channel).SetOperation("create_work_item").SetIdempotencyKey(c.IdempotencyKey).SetRequestDigest(digest).SetDigestVersion("intake-v2").SaveX(ctx)
	_, err = s.Create(ctx, i, c)
	require.ErrorIs(t, err, workitemcreation.ErrIdempotencyConflict)
	require.Zero(t, client.Ticket.Query().CountX(ctx))
}

func TestApplicationRejectsUnscopedDynamicFields(t *testing.T) {
	client, s, i, c, _, _ := intakeFixture(t)
	c.FormValues = map[string]any{"unknown": "lost"}
	_, err := s.Create(context.Background(), i, c)
	require.Error(t, err)
	require.Zero(t, client.Ticket.Query().CountX(context.Background()))
}

func TestApplicationEveryMissingCollaboratorFailsClosed(t *testing.T) {
	for _, missing := range []string{"client", "resolver", "registry", "writer", "receipt", "fields", "snapshot", "audit", "outbox"} {
		t.Run(missing, func(t *testing.T) {
			client, s, i, c, _, _ := intakeFixture(t)
			switch missing {
			case "client":
				s.client = nil
			case "resolver":
				s.resolver = nil
			case "registry":
				s.registry = nil
			case "writer":
				s.workItems = nil
			case "receipt":
				s.receipts = nil
			case "fields":
				s.fieldValues = nil
			case "snapshot":
				s.snapshots = nil
			case "audit":
				s.audits = nil
			case "outbox":
				s.outbox = nil
			}
			_, err := s.Create(context.Background(), i, c)
			require.ErrorIs(t, err, workitemcreation.ErrInternalFailure)
			require.Zero(t, client.IntakeRequest.Query().CountX(context.Background()))
		})
	}
}

func TestApplicationTypedNilCollaboratorFailsClosed(t *testing.T) {
	_, s, i, c, _, _ := intakeFixture(t)
	var resolver *preparedResolver
	s.resolver = resolver
	require.NotPanics(t, func() {
		_, err := s.Create(context.Background(), i, c)
		require.ErrorIs(t, err, workitemcreation.ErrInternalFailure)
	})
}

func TestApplicationReplayRechecksCurrentIdentity(t *testing.T) {
	client, s, i, c, _, _ := intakeFixture(t)
	_, err := s.Create(context.Background(), i, c)
	require.NoError(t, err)
	client.User.UpdateOneID(i.ActorID).SetActive(false).ExecX(context.Background())
	_, err = s.Create(context.Background(), i, c)
	require.ErrorIs(t, err, workitemcreation.ErrAuthenticationRequired)
	require.Equal(t, 1, client.Ticket.Query().CountX(context.Background()))
}

func seedCreationPermission(t *testing.T, client *ent.Client, tenantID int, roleName string) {
	t.Helper()
	ctx := context.Background()
	role := client.Role.Create().SetTenantID(tenantID).SetName(roleName).SetCode(roleName).SaveX(ctx)
	permission := client.Permission.Create().SetTenantID(tenantID).SetName("Create work").SetCode("create-work").SetResource("*").SetAction("*").SaveX(ctx)
	client.RolePermission.Create().SetTenantID(tenantID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
}

func TestApplicationReplayRechecksCurrentPermissions(t *testing.T) {
	client, s, i, c, _, _ := intakeFixture(t)
	first, err := s.Create(context.Background(), i, c)
	require.NoError(t, err)
	_, err = client.RolePermission.Delete().Exec(context.Background())
	require.NoError(t, err)
	_, err = s.Create(context.Background(), i, c)
	require.ErrorIs(t, err, workitemcreation.ErrPermissionDenied)
	require.Equal(t, first.WorkItemID, client.Ticket.Query().OnlyX(context.Background()).ID)
	require.Equal(t, 1, client.IntakeRequest.Query().CountX(context.Background()))
}

func (preparedResolver) ResolveWorkflow(context.Context, *ent.Tx, *workitemcreation.CreationPlan) error {
	return nil
}
