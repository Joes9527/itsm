//go:build integration_postgres

package integration

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database/rls"
	"itsm-backend/ent"
	sr "itsm-backend/handlers/service_request"
	"itsm-backend/handlers/shared/workflowcallback"
	"itsm-backend/migration"
	"os"
	"sync"
	"testing"
	"time"
)

const srAuthorityVersion = "028_service_request_work_item_authority"

func authorityRequest(t *testing.T, f *incidentEffectsFixture) (*ent.Ticket, *ent.ServiceRequest) {
	t.Helper()
	wi := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("authority").SetTicketNumber("SR-028").SetRecordClass("service_request_item").SetVersion(3).SaveX(f.ctx)
	request := f.client.ServiceRequest.Create().SetTicketID(wi.ID).SetCatalogID(1).SaveX(f.ctx)
	return wi, request
}

// These are explicitly consistent historical fixture values, not a repair of historical rows.
func consistentLegacyAuthority(t *testing.T, f *incidentEffectsFixture) {
	t.Helper()
	_, err := f.db.ExecContext(f.ctx, `ALTER TABLE service_requests ADD COLUMN tenant_id bigint, ADD COLUMN requester_id bigint, ADD COLUMN processor_id bigint, ADD COLUMN version bigint, ADD COLUMN created_at timestamptz, ADD COLUMN updated_at timestamptz, ADD COLUMN deleted_at timestamptz;
 UPDATE service_requests sr SET tenant_id=w.tenant_id, requester_id=w.requester_id, processor_id=w.assignee_id, version=w.version, created_at=w.created_at, updated_at=w.updated_at, deleted_at=w.deleted_at FROM tickets w WHERE w.id=sr.ticket_id;`)
	require.NoError(t, err)
}
func TestPostgresServiceRequestAuthorityRejectsSharedConflicts(t *testing.T) {
	for _, col := range []string{"tenant_id", "requester_id", "processor_id", "version", "created_at", "updated_at", "deleted_at"} {
		t.Run(col, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			_, request := authorityRequest(t, f)
			consistentLegacyAuthority(t, f)
			value := "999"
			if col == "created_at" || col == "updated_at" || col == "deleted_at" {
				value = "now()+interval '1 day'"
			}
			_, err := f.db.ExecContext(f.ctx, "UPDATE service_requests SET "+col+"="+value)
			require.NoError(t, err)
			err = migration.PrepareServiceRequestWorkItemAuthority(f.ctx, f.db)
			require.ErrorContains(t, err, fmt.Sprintf("ServiceRequest IDs %d", request.ID))
			require.ErrorContains(t, err, col)
			_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL(srAuthorityVersion))
			require.ErrorContains(t, err, col)
			var count int
			require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_requests' AND column_name='created_at'`).Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}
func TestPostgresServiceRequestAuthorityApplyReapplyEntAndTenantScope(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	wi, request := authorityRequest(t, f)
	consistentLegacyAuthority(t, f)
	secondTenant := f.client.Tenant.Create().SetCode("authority-other").SetName("other").SaveX(f.ctx)
	secondUser := f.client.User.Create().SetTenantID(secondTenant.ID).SetUsername("other").SetName("other").SetEmail("other@example.test").SetPasswordHash("test").SaveX(f.ctx)
	other := f.client.Ticket.Create().SetTenantID(secondTenant.ID).SetRequesterID(secondUser.ID).SetTitle("other").SetTicketNumber(wi.TicketNumber).SetRecordClass("service_request_item").SaveX(f.ctx)
	// Create the second consistent historical row with explicit shared values.
	_, err := f.db.ExecContext(f.ctx, `INSERT INTO service_requests(ticket_id,catalog_id,data_classification,needs_public_ip,compliance_ack,quantity,tenant_id,requester_id,processor_id,version,created_at,updated_at,deleted_at) SELECT id,1,'internal',false,false,1,tenant_id,requester_id,assignee_id,version,created_at,updated_at,deleted_at FROM tickets WHERE id=$1`, other.ID)
	require.NoError(t, err)
	require.NoError(t, migration.PrepareServiceRequestWorkItemAuthority(f.ctx, f.db))
	for i := 0; i < 2; i++ {
		_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL(srAuthorityVersion))
		require.NoError(t, err)
		require.NoError(t, f.client.Schema.Create(f.ctx))
		_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL(srAuthorityVersion))
		require.NoError(t, err)
	}
	repo := sr.NewEntRepository(f.client)
	got, err := repo.Get(f.ctx, request.ID, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, wi.Version, got.Version)
	_, err = repo.Get(f.ctx, request.ID, secondTenant.ID)
	require.Error(t, err)
	reset, err := os.ReadFile("../../migrations/028_service_request_work_item_authority_dev_reset.sql")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, string(reset))
	require.ErrorContains(t, err, "Refusing destructive")
	role := fmt.Sprintf("sr028_%d", time.Now().UnixNano())
	var schema string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&schema))
	_, err = f.db.ExecContext(f.ctx, "CREATE ROLE "+role+" NOLOGIN; GRANT USAGE ON SCHEMA "+schema+" TO "+role+"; GRANT SELECT,UPDATE ON tickets,service_requests TO "+role)
	require.NoError(t, err)
	defer func() {
		_, err := f.db.ExecContext(f.ctx, "DROP OWNED BY "+role+"; DROP ROLE "+role)
		require.NoError(t, err)
	}()
	for _, tenant := range []int{f.tenant.ID, secondTenant.ID, 0} {
		tx, err := f.db.BeginTx(f.ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(f.ctx, "SET LOCAL ROLE "+role)
		require.NoError(t, err)
		guc := ""
		if tenant > 0 {
			guc = fmt.Sprint(tenant)
		}
		_, err = tx.ExecContext(f.ctx, "SELECT set_config('app.current_tenant',$1,true)", guc)
		require.NoError(t, err)
		var count int
		require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT count(*) FROM service_requests WHERE id=$1", request.ID).Scan(&count))
		if tenant == f.tenant.ID {
			require.Equal(t, 1, count)
		} else {
			require.Zero(t, count)
		}
		result, err := tx.ExecContext(f.ctx, "UPDATE service_requests SET cost_center='tenant-check' WHERE id=$1", request.ID)
		require.NoError(t, err)
		n, err := result.RowsAffected()
		require.NoError(t, err)
		if tenant != f.tenant.ID {
			require.Zero(t, n)
		}
		require.NoError(t, tx.Rollback())
	}
	// Intended System worker scope uses the already privileged owned pool, never a policy bypass clause.
	system := tenantctx.WithSystemBypass(f.ctx)
	conn, err := rls.AcquireConn(system, f.db)
	require.NoError(t, err)
	var total int
	require.NoError(t, conn.QueryRowContext(system, "SELECT count(*) FROM service_requests").Scan(&total))
	require.Equal(t, 2, total)
	require.NoError(t, rls.ReleaseConn(system, conn))
	_, err = f.db.ExecContext(f.ctx, "DELETE FROM service_requests")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, string(reset))
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL(srAuthorityVersion))
	require.NoError(t, err)
}

func TestPostgresServiceRequestCompletionAndExtensionUpdateUseWorkItemLockOrder(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	wi, request := authorityRequest(t, f)
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	type operationKey struct{}
	const updateOperation = "extension-update"
	const completionOperation = "completion"
	workItemLocked := make(chan struct{})
	callbackMutating := make(chan struct{})
	releaseUpdate := make(chan struct{})
	var releaseOnce, callbackOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseUpdate) }) }
	defer release()
	f.client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(mutationCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
			operation, _ := mutationCtx.Value(operationKey{}).(string)
			if operation == completionOperation {
				callbackOnce.Do(func() { close(callbackMutating) })
			}
			value, err := next.Mutate(mutationCtx, mutation)
			if operation == updateOperation && mutation.Type() == ent.TypeTicket && err == nil {
				close(workItemLocked)
				select {
				case <-releaseUpdate:
				case <-mutationCtx.Done():
					return nil, mutationCtx.Err()
				}
			}
			return value, err
		})
	})
	repo := sr.NewEntRepository(f.client)
	input, err := repo.Get(ctx, request.ID, f.tenant.ID)
	require.NoError(t, err)
	input.CostCenter = "concurrent update"
	updateDone := make(chan error, 1)
	go func() { updateDone <- repo.Update(context.WithValue(ctx, operationKey{}, updateOperation), input) }()
	select {
	case <-workItemLocked:
	case <-ctx.Done():
		t.Fatal("extension update did not acquire WorkItem lock: ", ctx.Err())
	}
	service := sr.NewService(repo, f.client, zaptest.NewLogger(t).Sugar(), nil)
	type callbackOutcome struct {
		result workflowcallback.Result
		err    error
	}
	completionDone := make(chan callbackOutcome, 1)
	go func() {
		result, err := service.ApplyServiceRequestWorkflowCallback(context.WithValue(ctx, operationKey{}, completionOperation), workflowcallback.ServiceRequestCommand{RequestID: request.ID, TenantID: f.tenant.ID, Action: "complete_request", CompletionNote: "completed"})
		completionDone <- callbackOutcome{result, err}
	}()
	select {
	case <-callbackMutating:
	case <-ctx.Done():
		t.Fatal("completion did not reach aggregate mutation: ", ctx.Err())
	}
	// Observe PostgreSQL's actual lock wait before releasing the owning writer.
	// With the old SR->WorkItem order completion holds SR here and creates a
	// deadlock when the repository continues. WorkItem-first holds no SR lock.
	for {
		var waiting bool
		err := f.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a WHERE a.datname=current_database() AND a.wait_event_type='Lock' AND cardinality(pg_blocking_pids(a.pid))>0 AND a.query LIKE 'UPDATE "tickets"%')`).Scan(&waiting)
		require.NoError(t, err)
		if waiting {
			break
		}
		select {
		case result := <-completionDone:
			t.Fatalf("completion finished before the lock interleaving: %+v", result)
		case <-ctx.Done():
			t.Fatal("no PostgreSQL WorkItem lock wait observed: ", ctx.Err())
		default:
		}
	}
	release()
	require.NoError(t, <-updateDone, "extension update must commit without PostgreSQL deadlock")
	outcome := <-completionDone
	require.NoError(t, outcome.err, "completion must classify stale WorkItem CAS instead of deadlocking")
	require.Equal(t, workflowcallback.StatusBlocked, outcome.result.Status)
	current := f.client.Ticket.GetX(ctx, wi.ID)
	require.Equal(t, wi.Version+1, current.Version)
	require.Equal(t, wi.Status, current.Status)
	currentRequest := f.client.ServiceRequest.GetX(ctx, request.ID)
	require.Equal(t, "concurrent update", currentRequest.CostCenter)
	require.Empty(t, currentRequest.CompletionNote, "losing completion must not leave a partial extension write")
	require.True(t, currentRequest.CompletedAt.IsZero())
}
