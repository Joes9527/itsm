//go:build integration

package bpmn_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"itsm-backend/ent"
	changehandler "itsm-backend/handlers/change"
	servicerequesthandler "itsm-backend/handlers/service_request"
	. "itsm-backend/service/bpmn"

	entgo "entgo.io/ent"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func openCallbackCASPostgresClient(t *testing.T, dsn, schemaName string, createSchema bool) (*sql.DB, *ent.Client) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	require.NoError(t, db.PingContext(context.Background()))
	if createSchema {
		_, err = db.ExecContext(context.Background(), fmt.Sprintf(`CREATE SCHEMA "%s"`, schemaName))
		require.NoError(t, err)
	}
	_, err = db.ExecContext(context.Background(), fmt.Sprintf(`SET search_path TO "%s"`, schemaName))
	require.NoError(t, err)
	return db, ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
}

func installCallbackQueryBarrier(client *ent.Client, entityType string, arrived chan<- struct{}, release <-chan struct{}) {
	var once sync.Once
	client.Intercept(entgo.InterceptFunc(func(next entgo.Querier) entgo.Querier {
		return entgo.QuerierFunc(func(ctx context.Context, query entgo.Query) (entgo.Value, error) {
			value, err := next.Query(ctx, query)
			if err != nil {
				return value, err
			}
			queryContext := entgo.QueryFromContext(ctx)
			if queryContext != nil && queryContext.Type == entityType {
				once.Do(func() {
					arrived <- struct{}{}
					select {
					case <-release:
					case <-ctx.Done():
					}
				})
			}
			return value, nil
		})
	}))
}

type callbackCASResult struct {
	status CallbackEffectStatus
	err    error
}

func collectCallbackCASResults(t *testing.T, results <-chan callbackCASResult) []string {
	t.Helper()
	statuses := make([]string, 0, 2)
	for range 2 {
		select {
		case result := <-results:
			require.NoError(t, result.err)
			statuses = append(statuses, string(result.status))
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for concurrent callback result")
		}
	}
	sort.Strings(statuses)
	return statuses
}

func TestChangeCallbackConcurrentImplementPostgresHasSingleAppliedEffect(t *testing.T) {
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	schemaName := "callback_cas_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	setupDB, setupClient := openCallbackCASPostgresClient(t, dsn, schemaName, true)
	require.NoError(t, setupClient.Schema.Create(context.Background()))
	_, workerClient := openCallbackCASPostgresClient(t, dsn, schemaName, false)
	t.Cleanup(func() {
		_ = workerClient.Close()
		_ = setupClient.Close()
		_ = setupDB.Close()
		cleanupDB, err := sql.Open("postgres", dsn)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = cleanupDB.ExecContext(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
			_ = cleanupDB.Close()
		}
	})
	ctx := context.Background()
	tenant := setupClient.Tenant.Create().SetName("CAS").SetCode("cas-" + schemaName).SetDomain(schemaName + ".test").SetStatus("active").SaveX(ctx)
	actor := setupClient.User.Create().SetUsername("actor-" + schemaName).SetEmail(schemaName + "@test.local").SetPasswordHash("x").SetName("actor").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	workItem := setupClient.Ticket.Create().SetTitle("concurrent change").SetTicketNumber("CHG-CAS-1").SetStatus("scheduled").SetRecordClass("change_request").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SaveX(ctx)
	changeEntity := setupClient.Change.Create().SetWorkItemID(workItem.ID).SaveX(ctx)

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	installCallbackQueryBarrier(setupClient, ent.TypeChange, arrived, release)
	installCallbackQueryBarrier(workerClient, ent.TypeChange, arrived, release)
	handlers := []*ChangeServiceTaskHandler{
		NewChangeServiceTaskHandler(setupClient, zaptest.NewLogger(t).Sugar()),
		NewChangeServiceTaskHandler(workerClient, zaptest.NewLogger(t).Sugar()),
	}
	handlers[0].SetChangeService(changehandler.NewService(nil, setupClient, zaptest.NewLogger(t).Sugar()))
	handlers[1].SetChangeService(changehandler.NewService(nil, workerClient, zaptest.NewLogger(t).Sugar()))
	results := make(chan callbackCASResult, 2)
	for _, handler := range handlers {
		go func(handler *ChangeServiceTaskHandler) {
			effect, err := handler.Execute(context.WithValue(context.Background(), BPMNTenantIDContextKey, tenant.ID), nil, map[string]interface{}{
				"action": "implement_change", "change_id": changeEntity.ID,
			})
			if err != nil {
				results <- callbackCASResult{err: err}
				return
			}
			results <- callbackCASResult{status: effect.Status}
		}(handler)
	}
	<-arrived
	<-arrived
	close(release)

	got := collectCallbackCASResults(t, results)
	require.Equal(t, []string{string(CallbackEffectApplied), string(CallbackEffectIdempotent)}, got)
}

func TestServiceRequestCallbackConcurrentCompletePostgresHasSingleAppliedAggregate(t *testing.T) {
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	schemaName := "callback_sr_cas_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	setupDB, setupClient := openCallbackCASPostgresClient(t, dsn, schemaName, true)
	require.NoError(t, setupClient.Schema.Create(context.Background()))
	_, workerClient := openCallbackCASPostgresClient(t, dsn, schemaName, false)
	t.Cleanup(func() {
		_ = workerClient.Close()
		_ = setupClient.Close()
		_ = setupDB.Close()
		cleanupDB, err := sql.Open("postgres", dsn)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = cleanupDB.ExecContext(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
			_ = cleanupDB.Close()
		}
	})

	ctx := context.Background()
	tenant := setupClient.Tenant.Create().SetName("SR CAS").SetCode("sr-cas-" + schemaName).SetDomain(schemaName + ".test").SetStatus("active").SaveX(ctx)
	actor := setupClient.User.Create().SetUsername("sr-actor-" + schemaName).SetEmail(schemaName + "@test.local").SetPasswordHash("x").SetName("actor").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	workItem := setupClient.Ticket.Create().SetTitle("concurrent request").SetTicketNumber("SR-CAS-1").SetStatus("in_progress").SetRecordClass("service_request_item").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SaveX(ctx)
	request := setupClient.ServiceRequest.Create().SetTenantID(tenant.ID).SetTicketID(workItem.ID).SetCatalogID(1).SetRequesterID(actor.ID).SaveX(ctx)

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	installCallbackQueryBarrier(setupClient, ent.TypeServiceRequest, arrived, release)
	installCallbackQueryBarrier(workerClient, ent.TypeServiceRequest, arrived, release)
	logger := zaptest.NewLogger(t).Sugar()
	handlers := []*ServiceRequestServiceTaskHandler{
		NewServiceRequestServiceTaskHandler(setupClient, logger),
		NewServiceRequestServiceTaskHandler(workerClient, logger),
	}
	handlers[0].SetServiceRequestService(servicerequesthandler.NewService(nil, setupClient, logger, nil))
	handlers[1].SetServiceRequestService(servicerequesthandler.NewService(nil, workerClient, logger, nil))
	results := make(chan callbackCASResult, 2)
	for _, handler := range handlers {
		go func(handler *ServiceRequestServiceTaskHandler) {
			effect, err := handler.Execute(context.WithValue(context.Background(), BPMNTenantIDContextKey, tenant.ID), nil, map[string]interface{}{
				"action": "complete_request", "request_id": request.ID, "completion_note": "done",
			})
			if err != nil {
				results <- callbackCASResult{err: err}
				return
			}
			results <- callbackCASResult{status: effect.Status}
		}(handler)
	}
	<-arrived
	<-arrived
	close(release)

	got := collectCallbackCASResults(t, results)
	require.Equal(t, []string{string(CallbackEffectApplied), string(CallbackEffectIdempotent)}, got)
	storedRequest := setupClient.ServiceRequest.GetX(ctx, request.ID)
	storedWorkItem := setupClient.Ticket.GetX(ctx, workItem.ID)
	require.Equal(t, "done", storedRequest.CompletionNote)
	require.False(t, storedRequest.CompletedAt.IsZero())
	require.Equal(t, "resolved", storedWorkItem.Status)
	require.False(t, storedWorkItem.ResolvedAt.IsZero())
}
