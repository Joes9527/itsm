//go:build integration_postgres

package intake

import (
	"context"
	"fmt"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"
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
	user := client.User.Create().SetTenantID(tenant.ID).SetUsername(name).SetName(name).SetEmail(name + "@example.test").SetPasswordHash("test").SaveX(ctx)
	i := workitemcreation.Identity{TenantID: tenant.ID, ActorID: user.ID, RequesterID: user.ID, Channel: "itsm_web", Role: "requester"}
	c := workitemcreation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", IdempotencyKey: "concurrent", Title: "VPN"}
	registry := NewCreatorRegistry()
	require.NoError(t, registry.Register(&preparedCreator{}))
	service := NewService(client, preparedResolver{}, registry, NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()))
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
