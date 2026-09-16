//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/service"
)

func TestPostgresBindingDeactivationConcurrentCAS(t *testing.T) {
	db := openBPMNAssignmentSourceMigrationDB(t)
	db.SetMaxOpenConns(4)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, client.Schema.Create(ctx))
	binding := client.ProcessBinding.Create().SetTenantID(1).SetBusinessType("cloud_public_ops").SetProcessDefinitionKey("legacy").SetIsActive(true).SaveX(ctx)
	binding = client.ProcessBinding.GetX(ctx, binding.ID)
	firstAudit := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondWrite := make(chan struct{})
	var auditOnce, writeOnce sync.Once
	client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			auditOnce.Do(func() { close(firstAudit) })
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return next.Mutate(ctx, m)
		})
	})
	svc := service.NewProcessBindingService(client)
	actor := service.ActionActor{TenantID: 1, UserID: 7, Role: "super_admin"}
	request := dto.DeactivateProcessBindingRequest{Reason: "retire reviewed legacy binding", ExpectedUpdatedAt: binding.UpdatedAt}
	results := make(chan error, 2)
	go func() { _, err := svc.DeactivateBinding(ctx, actor, binding.ID, request); results <- err }()
	select {
	case <-firstAudit:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The first update is now uncommitted. The second transaction observes the
	// old active row and reaches its own CAS before the first is released.
	client.ProcessBinding.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			writeOnce.Do(func() { close(secondWrite) })
			return next.Mutate(ctx, m)
		})
	})
	go func() { _, err := svc.DeactivateBinding(ctx, actor, binding.ID, request); results <- err }()
	select {
	case <-secondWrite:
		close(releaseFirst)
	case <-ctx.Done():
		close(releaseFirst)
		t.Fatal(ctx.Err())
	}
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		var app *common.AppError
		require.True(t, errors.As(err, &app), "%v", err)
		require.Equal(t, common.ErrCodeConflict, app.Code)
		conflicts++
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	require.False(t, client.ProcessBinding.GetX(ctx, binding.ID).IsActive)
	require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
	_, err := svc.DeactivateBinding(ctx, actor, binding.ID, request)
	require.NoError(t, err)
	require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
}

func TestPostgresBindingDeactivationRejectsInterveningEdit(t *testing.T) {
	db := openBPMNAssignmentSourceMigrationDB(t)
	db.SetMaxOpenConns(4)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
	binding := client.ProcessBinding.Create().SetTenantID(1).SetBusinessType("ticket").SetProcessDefinitionKey("legacy").SetIsActive(true).SaveX(ctx)
	binding = client.ProcessBinding.GetX(ctx, binding.ID)
	client.ProcessBinding.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			// A legitimate editor commits after the command read but before its write.
			_, err := db.ExecContext(ctx, "UPDATE process_bindings SET priority=42, updated_at=updated_at+interval '1 second' WHERE id=$1", binding.ID)
			if err != nil {
				return nil, err
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err := service.NewProcessBindingService(client).DeactivateBinding(ctx, service.ActionActor{TenantID: 1, UserID: 7, Role: "super_admin"}, binding.ID, dto.DeactivateProcessBindingRequest{Reason: "reviewed", ExpectedUpdatedAt: binding.UpdatedAt})
	var app *common.AppError
	require.True(t, errors.As(err, &app), "%v", err)
	require.Equal(t, common.ErrCodeConflict, app.Code)
	current := client.ProcessBinding.GetX(ctx, binding.ID)
	require.True(t, current.IsActive)
	require.Equal(t, 42, current.Priority)
	require.Equal(t, 0, client.AuditLog.Query().CountX(ctx))
}
