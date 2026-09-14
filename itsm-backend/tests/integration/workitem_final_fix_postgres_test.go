//go:build integration_postgres

package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/migration"
)

func TestFinalFixPostRetirementCanonicalReconciliation(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	db, ctx, m := currentRuntimeFixture(t, migration.MigrationControlConfig{Operator: "fixture-operator", DeploymentID: "owned-v2", RetirementPublicKeys: map[string]ed25519.PublicKey{"fixture": pub}})

	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	tenant := client.Tenant.Create().SetName("post R reconciliation").SetCode("post-r-reconcile").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("reconcile").SetName("reconcile").SetPasswordHash("fixture").SetEmail("reconcile@example.test").SetRole("operator").SetActive(true).SaveX(ctx)
	item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetRecordClass("generic").SetTitle("preserved business fact").SetStatus("new").SetTicketNumber("POST-R-RECONCILE-1").SaveX(ctx)
	var factsBefore string
	require.NoError(t, db.QueryRow("SELECT to_jsonb(t)::text FROM tickets t WHERE id=$1", item.ID).Scan(&factsBefore))
	require.NoError(t, m.ReconcileSchemaInvariants(ctx))
	e := retirementEvidence(t, m, ctx, priv)
	require.NoError(t, m.ApplyRetirement(ctx, e))
	before := entryDigest(t, db)
	require.NoError(t, migration.RunPostSchemaMigrations(ctx, m))
	require.NoError(t, migration.RunCanonicalBootstrap(ctx, migration.CanonicalBootstrap{Migrator: m, CreateSchema: func(context.Context) error { t.Fatal("existing target must not overlay"); return nil }}))
	require.NoError(t, m.InspectRuntimeMigrations(ctx))
	require.NoError(t, m.ApplyRetirement(ctx, e))
	require.Equal(t, before, entryDigest(t, db))
	var factsAfter string
	require.NoError(t, db.QueryRow("SELECT to_jsonb(t)::text FROM tickets t WHERE id=$1", item.ID).Scan(&factsAfter))
	require.Equal(t, factsBefore, factsAfter)

	_, err = db.Exec("ALTER TABLE catalog_access_policies DROP CONSTRAINT catalog_access_policy_finite; ALTER TABLE catalog_access_policies ADD CONSTRAINT catalog_access_policy_finite CHECK(version>0)")
	require.NoError(t, err)
	changed := entryDigest(t, db)
	require.ErrorContains(t, migration.RunPostSchemaMigrations(ctx, m), "post-retirement structure drift")
	require.Equal(t, changed, entryDigest(t, db))
}

func TestFinalFixHistoricalRetirementInventory(t *testing.T) {
	for _, mutation := range []string{"", "CREATE TABLE workflows(id bigint)", "CREATE TABLE workflows(id bigint) PARTITION BY HASH(id)", "CREATE VIEW workflows AS SELECT 1 AS id", "CREATE TABLE workflow_tasks(id bigint)", "CREATE TABLE workflow_instances(id bigint)", "CREATE TABLE workflow_versions(id bigint)", "CREATE TABLE ticket_approvals(id bigint)", "ALTER TABLE releases ADD COLUMN requires_approval boolean", "ALTER TABLE ticket_categories ADD COLUMN workflow_id bigint", "ALTER TABLE incidents ADD COLUMN assignee_id bigint", "ALTER TABLE incidents ADD COLUMN reporter_id bigint", "ALTER TABLE incidents ADD COLUMN deleted_at timestamptz", "ALTER TABLE changes ADD COLUMN related_tickets jsonb"} {
		t.Run(mutation, func(t *testing.T) {
			db, ctx := preparationFixture(t)
			_, err := db.Exec("CREATE TABLE releases(id bigint, requires_approval boolean); CREATE TABLE ticket_categories(id bigint,workflow_id bigint)")
			require.NoError(t, err)
			version := "022_drop_professional_extension_shared_fields"
			ddl := migration.GetMigrationSQL(version)
			_, err = db.Exec(ddl)
			require.NoError(t, err)
			_, err = db.Exec("INSERT INTO schema_migrations(version,description,checksum) VALUES($1,'actual historical execution',$2)", version, fmt.Sprintf("%x", sha256.Sum256([]byte(ddl))))
			require.NoError(t, err)
			m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{Operator: "test", DeploymentID: "owned-v2"})
			if mutation == "" {
				require.NoError(t, m.InspectMigrationTarget(ctx))
				e := preparationEvidence(t, m, ctx)
				require.NoError(t, m.ApplyPreparation(ctx, e))
				return
			}
			_, err = db.Exec(mutation)
			require.NoError(t, err)
			before := entryDigest(t, db)
			require.ErrorContains(t, m.InspectMigrationTarget(ctx), "historical retirement receipt")
			_, err = m.InspectPreparation(ctx)
			require.ErrorContains(t, err, "historical retirement receipt")
			require.ErrorContains(t, m.ApplyPreparation(ctx, migration.MigrationEvidence{Operator: "test"}), "historical retirement receipt")
			require.Equal(t, before, entryDigest(t, db))
		})
	}
}

func TestFinalFixReservedGenericIdentity(t *testing.T) {
	for _, alias := range []string{"incident", "problem", "change", "change_request", "service_request", "service_request_item", "catalog_task", "question"} {
		t.Run(alias, func(t *testing.T) {
			db, ctx := preparationFixture(t)
			_, err := db.Exec("INSERT INTO tickets(id,tenant_id,ticket_number,record_class,generic_subtype,type,title,status,priority) VALUES(999,1,'IDENTITY999','generic',$1,$1,'identity','new','medium')", alias)
			require.NoError(t, err)
			m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{Operator: "test", DeploymentID: "owned-v2"})
			e := preparationEvidence(t, m, ctx)
			before := preparationLogicalDigest(t, db)
			err = m.ApplyPreparation(ctx, e)
			if alias == "question" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "legacy WorkItem identity conflict")
				require.Equal(t, before, preparationLogicalDigest(t, db))
			}
		})
	}
}

func TestFinalFixHistorical022And027Profiles(t *testing.T) {
	for _, mutation := range []string{"", "ALTER TABLE tickets ADD COLUMN type text", "ALTER TABLE incidents ADD COLUMN incident_number text"} {
		t.Run(mutation, func(t *testing.T) {
			db, ctx := migrationEntryFixture(t)
			m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{Operator: "test", DeploymentID: "owned-v2"})
			require.NoError(t, m.EnsureMigrationsTable(ctx))
			client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			require.NoError(t, client.Schema.Create(ctx))
			_, err := m.RunMigrations(ctx, migration.PostSchemaMigrations())
			require.NoError(t, err)
			// Historical profile: execute every original 022-027 SQL in order and record
			// only committed executions. This does not use today's P-dependent planner.
			for _, version := range []string{"022_drop_professional_extension_shared_fields", "023_add_process_start_request_digest", "024_incident_rule_action_receipts", "025_email_attachment_source_identity", "026_intake_actor_provenance", "027_work_item_identity_field_retirement"} {
				ddl := migration.GetMigrationSQL(version)
				require.NotEmpty(t, ddl, version)
				tx, err := db.BeginTx(ctx, nil)
				require.NoError(t, err)
				_, err = tx.Exec(ddl)
				if err != nil {
					tx.Rollback()
					t.Fatalf("historical %s: %v", version, err)
				}
				_, err = tx.Exec("INSERT INTO schema_migrations(version,description,checksum) VALUES($1,'actual historical execution',$2)", version, fmt.Sprintf("%x", sha256.Sum256([]byte(ddl))))
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
			}
			require.NoError(t, m.InspectMigrationTarget(ctx))
			e := preparationEvidence(t, m, ctx)
			if mutation == "" {
				require.NoError(t, m.ApplyPreparation(ctx, e))
				return
			}
			_, err = db.Exec(mutation)
			require.NoError(t, err)
			before := entryDigest(t, db)
			require.ErrorContains(t, m.InspectMigrationTarget(ctx), "historical retirement receipt")
			_, err = m.InspectPreparation(ctx)
			require.ErrorContains(t, err, "historical retirement receipt")
			require.ErrorContains(t, m.ApplyPreparation(ctx, e), "historical retirement receipt")
			require.Equal(t, before, entryDigest(t, db))
		})
	}
}

func TestFinalFixIncompatibleAccessConstraintBeforeRetirement(t *testing.T) {
	db, ctx, m := currentRuntimeFixture(t, migration.MigrationControlConfig{Operator: "fixture-operator", DeploymentID: "owned-v2"})
	require.NoError(t, m.ReconcileSchemaInvariants(ctx))
	_, err := db.Exec("ALTER TABLE catalog_access_policies DROP CONSTRAINT catalog_access_policy_finite; ALTER TABLE catalog_access_policies ADD CONSTRAINT catalog_access_policy_finite CHECK(version>0)")
	require.NoError(t, err)
	before := entryDigest(t, db)
	require.ErrorContains(t, m.ReconcileSchemaInvariants(ctx), "incompatible access constraint")
	require.Equal(t, before, entryDigest(t, db))
}

func TestFinalFixValidProfessionalIdentity(t *testing.T) {
	for alias, class := range map[string]string{"incident": "incident", "problem": "problem", "change": "change_request", "change_request": "change_request", "service_request": "service_request_item", "service_request_item": "service_request_item", "catalog_task": "catalog_task"} {
		t.Run(alias, func(t *testing.T) {
			db, ctx := preparationFixture(t)
			// Retain the valid existing professional fixtures and add a valid identity
			// assertion. This tests the 027 type semantic rule, not extension creation.
			_, err := db.Exec("INSERT INTO tickets(id,tenant_id,ticket_number,record_class,type,title,status,priority) VALUES(999,1,'PROF999',$1,$2,'identity','new','medium')", class, alias)
			require.NoError(t, err)
			m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{Operator: "test", DeploymentID: "owned-v2"})
			require.NoError(t, m.ApplyPreparation(ctx, preparationEvidence(t, m, ctx)))
		})
	}
}
