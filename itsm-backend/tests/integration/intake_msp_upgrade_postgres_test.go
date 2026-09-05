//go:build integration_postgres

package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/migration"
)

func TestPostgresIntakeMSPCanonicalBootstrapBackfillsBeforeSchema(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	_, err := f.db.ExecContext(f.ctx, "ALTER TABLE intake_requests DROP COLUMN actor_tenant_id")
	require.NoError(t, err)
	// Exercise the actual schema-before-post-migration runner. General pgvector
	// infrastructure is outside this provenance upgrade fixture.
	setup := migration.CanonicalBootstrap{
		Prepare:      func(ctx context.Context) error { return migration.PrepareIntakeActorProvenance(ctx, f.db) },
		CreateSchema: func(ctx context.Context) error { return f.client.Schema.Create(ctx) },
		Migrator:     migration.NewMigrator(f.db, zap.NewNop().Sugar()),
	}
	require.NoError(t, migration.RunCanonicalBootstrap(f.ctx, setup))
	receipt := f.client.IntakeRequest.Query().OnlyX(f.ctx)
	require.Equal(t, f.actor.TenantID, receipt.ActorTenantID)
	require.NoError(t, migration.RunCanonicalBootstrap(f.ctx, setup))
	require.Equal(t, receipt.ID, f.client.IntakeRequest.Query().OnlyX(f.ctx).ID)
}

func TestPostgresIntakeMSPMigrationRejectsConflictingAuditAssociation(t *testing.T) {
	for _, action := range []string{"intake.created", "convert_to_problem"} {
		t.Run(action, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			other := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).
				SetOpenedByID(f.actor.ID).SetTitle("Other work").SetTicketNumber("OTHER").SaveX(f.ctx)
			otherReceipt := f.client.IntakeRequest.Create().SetTenantID(f.tenant.ID).SetActorTenantID(f.tenant.ID).
				SetActorID(f.actor.ID).SetRequesterID(f.actor.ID).SetChannel("api").SetOperation("create").
				SetIdempotencyKey("other").SetRequestDigest("other").SetDigestVersion("v1").
				SetStatus("completed").SetWorkItemID(other.ID).SaveX(f.ctx)
			audit := f.client.AuditLog.Create().SetTenantID(f.tenant.ID).SetUserID(f.actor.ID).
				SetAction(action).SetResource(fmt.Sprintf("work_item:%d", f.inc.WorkItemID)).
				SetPath("/intake").SetMethod("POST").
				SetRequestBody(fmt.Sprintf(`{"intakeRequestId":%d,"targetWorkItemId":%d}`, otherReceipt.ID, f.inc.WorkItemID)).SaveX(f.ctx)
			_, err := f.db.ExecContext(f.ctx, migration.GetMigrationSQL(intakeMSPVersion))
			require.ErrorContains(t, err, fmt.Sprintf("audit ID %d", audit.ID))
		})
	}
}
