//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	catalog "itsm-backend/handlers/service_catalog"
	"itsm-backend/migration"
	"itsm-backend/service"
	"sync"
	"testing"
)

func TestPostgresCatalogAuthorityRetirement(t *testing.T) {
	for _, tc := range []struct {
		legacy, target string
		conflict       bool
	}{
		{"Request", "service_request_item", false}, {"Incident", "", false}, {"Change", "change_request", false},
		{"Request", "generic", true}, {"Alien", "", true}, {"Incident", "change_request", true},
	} {
		t.Run(tc.legacy+tc.target, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			_, err := f.db.ExecContext(f.ctx, `ALTER TABLE service_catalogs ADD COLUMN IF NOT EXISTS itsm_type text`)
			require.NoError(t, err)
			row := f.client.ServiceCatalog.Create().SetTenantID(f.tenant.ID).SetName("migration").SetTargetClass(tc.target).SaveX(f.ctx)
			_, err = f.db.ExecContext(f.ctx, `UPDATE service_catalogs SET itsm_type=$1 WHERE id=$2`, tc.legacy, row.ID)
			require.NoError(t, err)
			_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL("029_catalog_target_class_authority"))
			if tc.conflict {
				require.ErrorContains(t, err, "Catalog IDs")
				return
			}
			require.NoError(t, err)
			var remaining int
			require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_catalogs' AND column_name='itsm_type'`).Scan(&remaining))
			require.Zero(t, remaining)
		})
	}
}

func TestPostgresCatalogDefinitionAtomicUpdate(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	logger := zap.NewNop().Sugar()
	svc := catalog.NewService(catalog.NewEntRepository(f.client), f.client, logger, catalogPublicationDirectory{})
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(service.NewTicketServiceForTest(f.client, logger)))
	svc.SetCreatorRegistry(registry)
	actor := f.client.User.UpdateOneID(f.actor.ID).SetRole("super_admin").SaveX(f.ctx)
	identity := creation.Identity{TenantID: f.tenant.ID, ActorID: actor.ID, Role: actor.Role}
	created, err := svc.Create(f.ctx, f.tenant.ID, dto.CreateServiceCatalogRequest{Name: "Old host", Category: "IT", TargetClass: "generic", Fields: []map[string]interface{}{{"name": "field", "label": "Old field", "type": "text"}}})
	require.NoError(t, err)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.client.FieldDefinition.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if mutation, ok := m.(*ent.FieldDefinitionMutation); ok {
				if label, ok := mutation.Label(); ok && label == "New field" {
					once.Do(func() {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
						}
					})
				}
				if label, ok := mutation.Label(); ok && label == "Fail field" {
					return nil, errors.New("injected definition failure")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	done := make(chan error, 1)
	go func() {
		name := "New host"
		_, err := svc.Update(f.ctx, f.tenant.ID, created.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: created.CatalogVersion, Name: &name, Fields: []map[string]interface{}{{"name": "field", "label": "New field", "type": "text"}}})
		done <- err
	}()
	select {
	case <-entered:
	case <-f.ctx.Done():
		t.Fatal("writer did not reach field save")
	}
	old, err := svc.Read(f.ctx, identity, created.ID)
	close(release)
	require.NoError(t, err)
	require.Equal(t, "Old host", old.Name)
	require.Equal(t, "Old field", old.Fields[0].Label)
	require.Equal(t, created.CatalogVersion, old.CatalogVersion)
	require.NoError(t, <-done)
	current, err := svc.Read(f.ctx, identity, created.ID)
	require.NoError(t, err)
	require.Equal(t, "New host", current.Name)
	require.Equal(t, "New field", current.Fields[0].Label)
	require.NotEqual(t, created.CatalogVersion, current.CatalogVersion)
	failedName := "Must rollback"
	_, err = svc.Update(f.ctx, f.tenant.ID, created.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: current.CatalogVersion, Name: &failedName, Fields: []map[string]interface{}{{"name": "field", "label": "Fail field", "type": "text"}}})
	require.Error(t, err)
	afterFailure, err := svc.Read(f.ctx, identity, created.ID)
	require.NoError(t, err)
	require.Equal(t, current.CatalogVersion, afterFailure.CatalogVersion)
	require.Equal(t, "New host", afterFailure.Name)
	// Both editors reviewed the same snapshot; only one commit is allowed.
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, name := range []string{"First editor", "Second editor"} {
		go func(name string) {
			<-start
			_, err := svc.Update(f.ctx, f.tenant.ID, created.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: current.CatalogVersion, Name: &name})
			results <- err
		}(name)
	}
	close(start)
	success, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, creation.ErrCatalogVersionConflict)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
}

type catalogPublicationDirectory struct{}

func (catalogPublicationDirectory) Open(_ context.Context, tx *ent.Tx, _ int) (*ent.Client, func() error, error) {
	return tx.Client(), func() error { return nil }, nil
}
