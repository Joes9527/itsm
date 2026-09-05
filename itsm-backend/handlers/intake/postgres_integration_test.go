//go:build integration_postgres

package intake

import (
	"context"
	"fmt"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/fieldvalue"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processbinding"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/workitemnumbersequence"
	"itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/repository/workitemnumber"
	"os"
	"sync"
	"testing"
	"time"
)

// Real PostgreSQL upsert contention exercises the application and its owning
// transaction, not just a fake receipt collaborator.
func TestPostgresConcurrentApplicationCreation(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable INTAKE_POSTGRES_TEST_DSN is required")
	}
	client, err := ent.Open("postgres", dsn)
	require.NoError(t, err)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, client.Schema.Create(ctx), "requires a disposable database")
	name := fmt.Sprintf("intake-%d", time.Now().UnixNano())
	tenant := client.Tenant.Create().SetName(name).SetCode(name).SaveX(ctx)
	user := client.User.Create().SetTenantID(tenant.ID).SetUsername(name).SetName(name).SetEmail(name + "@example.test").SetPasswordHash("test").SetRole("requester").SaveX(ctx)
	seedCreationPermission(t, client, tenant.ID, "requester")
	i := workitemcreation.Identity{TenantID: tenant.ID, ActorID: user.ID, RequesterID: user.ID, Channel: "itsm_web", Role: "requester"}
	c := workitemcreation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", IdempotencyKey: "concurrent", Title: "VPN"}
	registry := NewCreatorRegistry()
	require.NoError(t, registry.Register(&preparedCreator{}))
	service := NewService(client, preparedResolver{}, registry, NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), sameTransactionDirectory{})
	type outcome struct {
		result *workitemcreation.CreateWorkItemResult
		err    error
	}
	results := make(chan outcome, 20)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(20)
	for range 20 {
		go func() { ready.Done(); <-start; r, e := service.Create(ctx, i, c); results <- outcome{r, e} }()
	}
	ready.Wait()
	close(start)
	var first *workitemcreation.CreateWorkItemResult
	created := 0
	for range 20 {
		out := <-results
		require.NoError(t, out.err)
		require.NotNil(t, out.result)
		if first == nil {
			first = out.result
		}
		require.Equal(t, first.WorkItemID, out.result.WorkItemID)
		require.Equal(t, first.Number, out.result.Number)
		if !out.result.Replayed {
			created++
		}
	}
	require.Equal(t, 1, created)
	require.Equal(t, 1, client.IntakeRequest.Query().Where(intakerequest.TenantIDEQ(i.TenantID)).CountX(ctx))
	require.Equal(t, 1, client.Ticket.Query().Where(ticket.TenantIDEQ(i.TenantID)).CountX(ctx))
	require.Equal(t, int64(1), client.WorkItemNumberSequence.Query().Where(workitemnumbersequence.TenantIDEQ(i.TenantID)).OnlyX(ctx).LastValue)
}

func TestPostgresConcurrentProfessionalCreation(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable INTAKE_POSTGRES_TEST_DSN is required")
	}
	client, err := ent.Open("postgres", dsn)
	require.NoError(t, err)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, client.Schema.Create(ctx))
	name := fmt.Sprintf("professional-%d", time.Now().UnixNano())
	tenant := client.Tenant.Create().SetName(name).SetCode(name).SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername(name).SetEmail(name + "@example.test").SetName(name).SetPasswordHash("test").SetRole("requester").SaveX(ctx)
	seedCreationPermission(t, client, tenant.ID, "requester")
	identity := workitemcreation.Identity{TenantID: tenant.ID, ActorID: actor.ID, RequesterID: actor.ID, Role: "requester", Channel: "itsm_web"}
	f := resolverFixtureWithClient(t, client, identity)
	command := f.catalogCommand(t)
	type outcome struct {
		result *workitemcreation.CreateWorkItemResult
		err    error
	}
	outcomes := make(chan outcome, 12)
	start := make(chan struct{})
	for range 12 {
		go func() { <-start; result, err := f.app.Create(ctx, identity, command); outcomes <- outcome{result, err} }()
	}
	close(start)
	var first *workitemcreation.CreateWorkItemResult
	created := 0
	for range 12 {
		out := <-outcomes
		require.NoError(t, out.err)
		if first == nil {
			first = out.result
		}
		require.Equal(t, first.WorkItemID, out.result.WorkItemID)
		require.Equal(t, first.ProfessionalReference, out.result.ProfessionalReference)
		if !out.result.Replayed {
			created++
		}
	}
	require.Equal(t, 1, created)
	item := client.Ticket.GetX(ctx, first.WorkItemID)
	require.False(t, item.SLAResponseDeadline.IsZero())
	require.False(t, item.SLAResolutionDeadline.IsZero())
	require.Equal(t, 1, client.IntakeRequest.Query().Where(intakerequest.TenantIDEQ(tenant.ID)).CountX(ctx))
	require.Equal(t, 1, client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(item.ID)).CountX(ctx))
	require.Equal(t, 1, client.FieldValue.Query().Where(fieldvalue.TenantIDEQ(tenant.ID)).CountX(ctx))
	require.Equal(t, 1, client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.TenantIDEQ(tenant.ID)).CountX(ctx))
	require.Equal(t, 1, client.OutboxEvent.Query().Where(outboxevent.TenantIDEQ(tenant.ID)).CountX(ctx))
	require.Equal(t, 1, client.AuditLog.Query().Where(auditlog.TenantIDEQ(tenant.ID)).CountX(ctx))
}

type interleavingCatalog struct {
	workitemcreation.CatalogResolver
	captured chan struct{}
	resume   chan struct{}
	once     sync.Once
}

func (c *interleavingCatalog) ResolveCreationCatalog(ctx context.Context, tx *ent.Tx, identity workitemcreation.Identity, id int) (*workitemcreation.ResolvedCatalog, []workitemcreation.ResolvedFieldDefinition, error) {
	catalog, fields, err := c.CatalogResolver.ResolveCreationCatalog(ctx, tx, identity, id)
	if err != nil {
		return nil, nil, err
	}
	c.once.Do(func() { close(c.captured) })
	select {
	case <-c.resume:
		return catalog, fields, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}

func TestPostgresCreationConfigurationSnapshotIsCoherent(t *testing.T) {
	for _, changed := range []string{"binding", "field definition"} {
		t.Run(changed, func(t *testing.T) {
			dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
			if dsn == "" {
				t.Skip("disposable INTAKE_POSTGRES_TEST_DSN is required")
			}
			client, err := ent.Open("postgres", dsn)
			require.NoError(t, err)
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			require.NoError(t, client.Schema.Create(ctx))
			name := fmt.Sprintf("coherence-%d", time.Now().UnixNano())
			tenant := client.Tenant.Create().SetName(name).SetCode(name).SaveX(ctx)
			actor := client.User.Create().SetTenantID(tenant.ID).SetUsername(name).SetEmail(name + "@example.test").SetName(name).SetPasswordHash("test").SetRole("requester").SaveX(ctx)
			seedCreationPermission(t, client, tenant.ID, "requester")
			identity := workitemcreation.Identity{TenantID: tenant.ID, ActorID: actor.ID, RequesterID: actor.ID, Role: "requester", Channel: "itsm_web"}
			f := resolverFixtureWithClient(t, client, identity)
			client.ServiceCatalog.UpdateOne(f.catalog).SetRequiresApproval(false).ExecX(ctx)
			command := f.catalogCommand(t)
			resolver := f.app.resolver.(*Resolver)
			capture := &interleavingCatalog{CatalogResolver: resolver.catalog, captured: make(chan struct{}), resume: make(chan struct{})}
			resolver.catalog = capture
			type outcome struct {
				result *workitemcreation.CreateWorkItemResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() { result, err := f.app.Create(ctx, identity, command); done <- outcome{result, err} }()
			select {
			case <-capture.captured:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// Another real transaction commits after A captured its accepted revision,
			// but before A selects the route or re-reads field validation definitions.
			if changed == "binding" {
				_, err = client.ProcessBinding.Update().Where(processbinding.TenantIDEQ(tenant.ID)).SetConditions(map[string]any{"no_process": true}).Save(ctx)
			} else {
				_, err = client.FieldDefinition.Create().SetTenantID(tenant.ID).SetEntityType("service_catalog").SetEntityID(f.catalog.ID).SetName("new_required").SetLabel("New required").SetFieldType("text").SetRequired(true).Save(ctx)
			}
			close(capture.resume)
			require.NoError(t, err)
			out := <-done
			require.NoError(t, out.err)
			snapshot := client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemIDEQ(out.result.WorkItemID)).OnlyX(ctx)
			require.Equal(t, command.CatalogVersion, snapshot.CatalogVersion)
			require.Equal(t, command.FormSchemaVersion, snapshot.FormSchemaVersion)
			require.Equal(t, "vpn", snapshot.WorkflowDefinitionKey)
			require.False(t, snapshot.NoProcess, "new route must not be paired with the old revision")
			require.Equal(t, "pending", out.result.WorkflowStartStatus)
			command.IdempotencyKey = "new-attempt"
			_, err = f.app.Create(ctx, identity, command)
			require.ErrorIs(t, err, workitemcreation.ErrCatalogVersionConflict, "a new snapshot must reject the old confirmed revision")
		})
	}
}
