//go:build integration_postgres

package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/migration"
	"testing"
)

// Synthetic historical-shape fixture. Legacy identity/tenant columns derive from
// 8d71b4f0^ ent/migrate/schema.go; retained shared columns are the exact 022 list.
// Prerequisites derive from registered 007–021 SQL. This is not a deployed backup.
// No Schema.Create and no fabricated migration receipts are used here.
func preparationFixture(t *testing.T) (*sql.DB, context.Context) {
	db, ctx := migrationEntryFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar())
	require.NoError(t, m.EnsureMigrationsTable(ctx))
	_, err := db.ExecContext(ctx, `
 CREATE TABLE users(id bigint PRIMARY KEY);
 CREATE TABLE tickets(id bigint PRIMARY KEY, tenant_id bigint NOT NULL, ticket_number text NOT NULL, record_class text NOT NULL, generic_subtype text, type text, title text NOT NULL, description text, status text NOT NULL, priority text NOT NULL, requester_id bigint, opened_by_id bigint, assignee_id bigint, version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), resolved_at timestamptz, closed_at timestamptz, deleted_at timestamptz);
 CREATE TABLE incidents(id bigint PRIMARY KEY, work_item_id bigint, title text NOT NULL, description text, status text NOT NULL DEFAULT 'new', priority text NOT NULL DEFAULT 'medium', tenant_id bigint NOT NULL, incident_number text NOT NULL, reporter_id bigint, created_at timestamptz, updated_at timestamptz, deleted_at timestamptz);
 CREATE TABLE problems(id bigint PRIMARY KEY, work_item_id bigint, title text NOT NULL, description text, status text NOT NULL DEFAULT 'new', priority text NOT NULL DEFAULT 'medium', tenant_id bigint NOT NULL, created_at timestamptz, updated_at timestamptz, deleted_at timestamptz, root_cause text);
 CREATE TABLE changes(id bigint PRIMARY KEY, work_item_id bigint, title text NOT NULL, description text, status text NOT NULL DEFAULT 'new', priority text NOT NULL DEFAULT 'medium', tenant_id bigint NOT NULL, created_at timestamptz, updated_at timestamptz);
 CREATE TABLE conversations(id bigint PRIMARY KEY, tenant_id bigint);
 CREATE TABLE tool_invocations(id bigint PRIMARY KEY, conversation_id bigint);
 CREATE TABLE service_catalogs(id bigint PRIMARY KEY);
 CREATE TABLE service_requests(id bigint PRIMARY KEY);
 CREATE TABLE field_values(id bigint PRIMARY KEY, entity_type text);
 CREATE TABLE process_instances(id bigint PRIMARY KEY,tenant_id bigint,business_key text,status text,start_time timestamptz,end_time timestamptz);
 CREATE TABLE ticket_types(id bigint PRIMARY KEY);
 CREATE TABLE kaf_task_action_ledgers(id bigint PRIMARY KEY,tenant_id bigint);
 CREATE TABLE kaf_task_completion_receipts(id bigint PRIMARY KEY,tenant_id bigint);
 CREATE TABLE process_callback_outboxes(id bigint PRIMARY KEY);
 CREATE TABLE workflows(id bigint PRIMARY KEY, content text);
 INSERT INTO workflows VALUES(1,'retained historic BPMN evidence');
 CREATE TABLE audit_logs(id bigint PRIMARY KEY,tenant_id bigint,entity_type text,entity_id bigint,action text,created_at timestamptz);
 `)
	require.NoError(t, err)
	for _, mig := range migration.RegisteredMigrations {
		require.NoError(t, m.ApplyMigration(ctx, mig), mig.Version)
		if mig.Version == "021_add_callback_optional_declared" {
			break
		}
	}
	_, err = db.ExecContext(ctx, `INSERT INTO tickets(id,tenant_id,ticket_number,record_class,type,title,status,priority) VALUES(1,1,'INC001','incident','incident','old title','new','medium'),(2,1,'PRB001','problem','problem','old title','new','medium'),(3,1,'CHG001','change_request','change','old title','new','medium'),(4,2,'INC002','incident','incident','tenant two','new','medium');
 INSERT INTO incidents(id,work_item_id,title,status,priority,tenant_id,incident_number) VALUES(1,1,'old title','new','medium',1,'INC001'),(4,4,'tenant two','new','medium',2,'INC002');
 INSERT INTO problems(id,work_item_id,title,status,priority,tenant_id) VALUES(2,2,'old title','new','medium',1);
 INSERT INTO changes(id,work_item_id,title,status,priority,tenant_id) VALUES(3,3,'old title','new','medium',1);
 INSERT INTO audit_logs VALUES(1,1,'ticket',1,'create','2026-01-01');`)
	require.NoError(t, err)
	return db, ctx
}

func TestWorkItemControlledPreparationPreservesHistory(t *testing.T) {
	db, ctx := preparationFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2"})
	inventory, err := m.InspectPreparation(ctx)
	require.NoError(t, err)
	evidence := migration.MigrationEvidence{Target: inventory.Target, CatalogRevision: migration.ControlledCatalogRevision, LedgerDigest: inventory.LedgerDigest, InventoryDigest: inventory.InventoryDigest, ApplicationDigest: "app", BackupDigest: "backup", RestoreReportDigest: "restore", JourneyReportDigest: "journey", ObservationReportDigest: "observation", Operator: "test", ChangeRecord: "test-3"}
	require.NoError(t, m.ApplyPreparation(ctx, evidence))
	var legacy, authoritative string
	_, err = db.ExecContext(ctx, `UPDATE tickets SET title='new title',version=version+1 WHERE id=1`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT i.title,t.title FROM incidents i JOIN tickets t ON t.id=i.work_item_id WHERE i.id=1`).Scan(&legacy, &authoritative))
	require.Equal(t, "old title", legacy)
	require.Equal(t, "new title", authoritative)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version IN ('022_drop_professional_extension_shared_fields','027_work_item_identity_field_retirement')`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='problems' AND column_name='investigation_completed_at'`).Scan(&count))
	require.Zero(t, count, "034 must remain absent during P")
	require.NoError(t, m.InspectMigrationTarget(ctx))
}

func preparationEvidence(t *testing.T, m *migration.Migrator, ctx context.Context) migration.MigrationEvidence {
	t.Helper()
	inv, err := m.InspectPreparation(ctx)
	require.NoError(t, err)
	return migration.MigrationEvidence{Target: inv.Target, CatalogRevision: migration.ControlledCatalogRevision, LedgerDigest: inv.LedgerDigest, InventoryDigest: inv.InventoryDigest, ApplicationDigest: "app", BackupDigest: "backup", RestoreReportDigest: "restore", JourneyReportDigest: "journey", ObservationReportDigest: "observation", Operator: "test", ChangeRecord: "task3"}
}
func TestWorkItemControlledPreparationRefusesInvalidShapes(t *testing.T) {
	cases := map[string]string{
		"unsafe base RLS":          `DROP POLICY tenant_isolation_tickets ON tickets;CREATE POLICY tenant_isolation_tickets ON tickets USING(true) WITH CHECK(true)`,
		"legacy identity conflict": `UPDATE tickets SET type='problem' WHERE id=1`,
		"unknown constraint":       `ALTER TABLE incidents ADD CONSTRAINT unknown_unique UNIQUE(title)`,
		"orphan":                   `UPDATE incidents SET work_item_id=999 WHERE id=1`,
		"duplicate":                `UPDATE incidents SET work_item_id=1 WHERE id=4`,
		"cross tenant":             `UPDATE incidents SET tenant_id=2 WHERE id=1`,
		"wrong class":              `UPDATE incidents SET work_item_id=2 WHERE id=1`,
		"old conflict":             `UPDATE incidents SET title='conflicting history' WHERE id=1`,
		"wrong named fk":           `ALTER TABLE incidents ADD CONSTRAINT incidents_tickets_work_item FOREIGN KEY(id) REFERENCES tickets(id)`,
		"wrong named index":        `CREATE UNIQUE INDEX incident_work_item_id ON incidents(id)`,
		"unknown policy":           `CREATE POLICY unknown_policy ON incidents USING(true)`,
		"forged known policy":      `DROP POLICY tenant_isolation_incidents ON incidents;CREATE POLICY tenant_isolation_incidents ON incidents USING(true) WITH CHECK(true)`,
		"unknown check":            `ALTER TABLE incidents ADD CONSTRAINT require_legacy_title CHECK(title IS NOT NULL)`,
		"unknown grant":            `GRANT SELECT ON incidents TO PUBLIC`,
		"unknown trigger":          `CREATE FUNCTION unknown_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW;END$$;CREATE TRIGGER unknown_trigger BEFORE INSERT ON incidents FOR EACH ROW EXECUTE FUNCTION unknown_trigger()`,
	}
	for name, sqlText := range cases {
		t.Run(name, func(t *testing.T) {
			db, ctx := preparationFixture(t)
			_, err := db.ExecContext(ctx, sqlText)
			require.NoError(t, err)
			m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2"})
			e := preparationEvidence(t, m, ctx)
			before := preparationLogicalDigest(t, db)
			require.Error(t, m.ApplyPreparation(ctx, e))
			require.Equal(t, before, preparationLogicalDigest(t, db))
			var exists bool
			require.NoError(t, db.QueryRow(`SELECT to_regclass(current_schema()||'.work_item_migration_evidence') IS NOT NULL`).Scan(&exists))
			require.False(t, exists)
		})
	}
}
func TestWorkItemControlledPreparationReceiptAndAttachmentAtomic(t *testing.T) {
	db, ctx := preparationFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2"})
	e := preparationEvidence(t, m, ctx)
	before := preparationLogicalDigest(t, db)
	wrong := e
	wrong.Target.DeploymentID = "other"
	require.Error(t, m.ApplyPreparation(ctx, wrong))
	require.Equal(t, before, preparationLogicalDigest(t, db))
	wrong = e
	wrong.BackupDigest = ""
	require.Error(t, m.ApplyPreparation(ctx, wrong))
	require.Equal(t, before, preparationLogicalDigest(t, db))
	require.Error(t, migration.NewMigrator(db, zap.NewNop().Sugar()).ApplyPreparation(ctx, e))
	require.Equal(t, before, preparationLogicalDigest(t, db))
	// Force receipt INSERT failure after DDL: the attachment and all P DDL roll back.
	_, err := db.ExecContext(ctx, `CREATE FUNCTION reject_p_receipt() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.version='037_work_item_structure_preparation' THEN RAISE EXCEPTION 'receipt fault'; END IF; RETURN NEW;END$$;CREATE TRIGGER reject_p_receipt BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION reject_p_receipt()`)
	require.NoError(t, err)
	before = preparationLogicalDigest(t, db)
	e = preparationEvidence(t, m, ctx)
	require.ErrorContains(t, m.ApplyPreparation(ctx, e), "receipt fault")
	require.Equal(t, before, preparationLogicalDigest(t, db))
	_, err = db.ExecContext(ctx, `DROP TRIGGER reject_p_receipt ON schema_migrations;DROP FUNCTION reject_p_receipt()`)
	require.NoError(t, err)
	e = preparationEvidence(t, m, ctx)
	require.NoError(t, m.ApplyPreparation(ctx, e))
	_, err = db.ExecContext(ctx, `UPDATE work_item_migration_evidence SET content=jsonb_set(content,'{StructureDigest}','"tampered"')`)
	require.NoError(t, err)
	require.ErrorContains(t, m.InspectMigrationTarget(ctx), "digest mismatch")
}
func TestWorkItemControlledPreparationRestrictedSQLRoles(t *testing.T) {
	db, ctx := preparationFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2"})
	require.NoError(t, m.ApplyPreparation(ctx, preparationEvidence(t, m, ctx)))
	// Role, grants and test records are confined to a rolled-back transaction.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	var schema string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema))
	role := "p_role_" + schema
	_, err = tx.ExecContext(ctx, `CREATE ROLE `+role+` NOLOGIN NOSUPERUSER NOBYPASSRLS; GRANT USAGE ON SCHEMA `+schema+` TO `+role+`; GRANT SELECT,INSERT,UPDATE,DELETE ON tickets,incidents,problems,changes TO `+role)
	require.NoError(t, err)
	for idx, domain := range []struct{ table, class string }{{"incidents", "incident"}, {"problems", "problem"}, {"changes", "change_request"}} {
		id := 100 + idx
		_, err = tx.ExecContext(ctx, `INSERT INTO tickets(id,tenant_id,ticket_number,record_class,title,status,priority) VALUES($1,2,$2,$3,'other','new','medium')`, id, "OTHER"+domain.class, domain.class)
		require.NoError(t, err)
	}
	_, err = tx.ExecContext(ctx, `SET LOCAL ROLE `+role+`;SELECT set_config('app.current_tenant','1',true)`)
	require.NoError(t, err)
	var super, bypass, owner bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT rolsuper,rolbypassrls,(SELECT relowner=current_user::regrole FROM pg_class WHERE oid='incidents'::regclass) FROM pg_roles WHERE rolname=current_user`).Scan(&super, &bypass, &owner))
	require.False(t, super)
	require.False(t, bypass)
	require.False(t, owner)
	for idx, domain := range []struct{ table, class string }{{"incidents", "incident"}, {"problems", "problem"}, {"changes", "change_request"}} {
		id := 200 + idx
		_, err = tx.ExecContext(ctx, `INSERT INTO tickets(id,tenant_id,ticket_number,record_class,title,status,priority) VALUES($1,1,$2,$3,'new','new','medium')`, id, "NEW"+domain.class, domain.class)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO `+domain.table+`(id,work_item_id) VALUES($1,$1)`, id)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `UPDATE tickets SET title='changed' WHERE id=$1`, id)
		require.NoError(t, err)
		var count int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM `+domain.table+` WHERE id=$1`, id).Scan(&count))
		require.Equal(t, 1, count)
		_, err = tx.ExecContext(ctx, `SAVEPOINT cross_tenant`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO `+domain.table+`(id,work_item_id) VALUES($1,$1)`, 100+idx)
		require.Error(t, err)
		_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT cross_tenant`)
		require.NoError(t, err)
	}
	var legacyCount int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM incidents WHERE id=4`).Scan(&legacyCount))
	require.Zero(t, legacyCount)
	_, err = tx.ExecContext(ctx, `SELECT content FROM work_item_migration_evidence`)
	require.Error(t, err, "ordinary business role cannot read global baseline evidence")
	require.NoError(t, tx.Rollback())
}

func TestWorkItemControlledPreparationAlreadyStructured(t *testing.T) {
	db, ctx := preparationFixture(t)
	_, err := db.ExecContext(ctx, migration.GetMigrationSQL(migration.WorkItemPrepareVersion))
	require.NoError(t, err)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2"})
	require.NoError(t, m.ApplyPreparation(ctx, preparationEvidence(t, m, ctx)))
	require.NoError(t, m.InspectMigrationTarget(ctx))
}

// Only pg_class.relpages/reltuples are excluded: aborted index scans update
// planner statistics independently of transactional DDL. All logical facts remain.
func preparationLogicalDigest(t *testing.T, db *sql.DB) string {
	t.Helper()
	// Entire schema column/index/constraint/function definitions and full ledger rows.
	queries := []string{
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT table_name,column_name,ordinal_position,column_default,is_nullable,data_type,udt_name,character_maximum_length FROM information_schema.columns WHERE table_schema=current_schema()) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT indexname,indexdef FROM pg_indexes WHERE schemaname=current_schema()) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT conname,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE connamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT proname,pg_get_functiondef(oid) AS definition FROM pg_proc WHERE pronamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT to_jsonb(c)-'relpages'-'reltuples' AS facts FROM pg_class c WHERE relnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT t.* FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT p.* FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid WHERE c.relnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT p.* FROM pg_sequence p JOIN pg_class c ON c.oid=p.seqrelid WHERE c.relnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT t.* FROM pg_type t WHERE typnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM schema_migrations x`,
	}
	out := ""
	for _, q := range queries {
		var s string
		require.NoError(t, db.QueryRow(q).Scan(&s))
		out += s
	}
	for _, table := range []string{"tickets", "incidents", "problems", "changes", "workflows", "audit_logs"} {
		var part string
		require.NoError(t, db.QueryRow(`SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY id)::text,'[]') FROM `+table+` r`).Scan(&part))
		out += part
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(out)))
}

func TestWorkItemControlledPreparationReviewedGrants(t *testing.T) {
	db, ctx := preparationFixture(t)
	var schema string
	require.NoError(t, db.QueryRow(`SELECT current_schema()`).Scan(&schema))
	role := "reviewed_" + schema
	_, err := db.ExecContext(ctx, `CREATE ROLE `+role+` NOLOGIN NOSUPERUSER NOBYPASSRLS;GRANT USAGE ON SCHEMA `+schema+` TO `+role+`;GRANT SELECT,INSERT,UPDATE,DELETE ON tickets,incidents,problems,changes TO `+role)
	require.NoError(t, err)
	defer func() {
		_, err := db.ExecContext(context.Background(), `REVOKE SELECT,INSERT,UPDATE,DELETE ON tickets,incidents,problems,changes FROM `+role+`;REVOKE USAGE ON SCHEMA `+schema+` FROM `+role+`;DROP ROLE `+role)
		require.NoError(t, err)
	}()
	var grants []migration.MigrationRoleGrant
	for _, table := range []string{"tickets", "incidents", "problems", "changes"} {
		grants = append(grants, migration.MigrationRoleGrant{Role: role, Table: table, Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE"}})
	}
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2", ReviewedGrants: grants})
	require.NoError(t, m.ApplyPreparation(ctx, preparationEvidence(t, m, ctx)))
	require.NoError(t, m.InspectMigrationTarget(ctx))
	_, err = db.ExecContext(ctx, `GRANT TRUNCATE ON incidents TO `+role)
	require.NoError(t, err)
	require.Error(t, m.InspectMigrationTarget(ctx))
	_, err = db.ExecContext(ctx, `REVOKE TRUNCATE ON incidents FROM `+role)
	require.NoError(t, err)
}

func TestWorkItemControlledPreparationRolesInsidePTransaction(t *testing.T) {
	db, ctx := preparationFixture(t)
	var schema string
	require.NoError(t, db.QueryRow(`SELECT current_schema()`).Scan(&schema))
	role := "probe_" + schema
	_, err := db.ExecContext(ctx, `CREATE ROLE `+role+` NOLOGIN NOSUPERUSER NOBYPASSRLS`)
	require.NoError(t, err)
	defer func() { _, err := db.ExecContext(context.Background(), `DROP ROLE `+role); require.NoError(t, err) }()
	// Test-only receipt trigger observes the DDL in the actual ApplyPreparation
	// transaction. Its nested exception block rolls back grants, role and records.
	probe := fmt.Sprintf(`CREATE FUNCTION probe_preparation_transaction() RETURNS trigger LANGUAGE plpgsql AS $probe$
 DECLARE tbl text; cls text; idx integer; hidden integer; denied boolean; unsafe boolean;
 BEGIN
 IF NEW.version='037_work_item_structure_preparation' THEN
  IF EXISTS(SELECT 1 FROM schema_migrations WHERE version=NEW.version) THEN RAISE EXCEPTION 'P receipt was prematurely committed'; END IF;
  IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='problems' AND column_name='investigation_completed_at') THEN RAISE EXCEPTION '034 leaked into P'; END IF;
  BEGIN
   GRANT USAGE ON SCHEMA %[1]s TO %[2]s;
   GRANT SELECT,INSERT,UPDATE,DELETE ON tickets,incidents,problems,changes TO %[2]s;
   INSERT INTO tickets(id,tenant_id,ticket_number,record_class,title,status,priority) VALUES (301,2,'OTHER-I','incident','other','new','medium'),(302,2,'OTHER-P','problem','other','new','medium'),(303,2,'OTHER-C','change_request','other','new','medium');
   SET LOCAL ROLE %[2]s;
   PERFORM set_config('app.current_tenant','1',true);
   SELECT rolsuper OR rolbypassrls OR (SELECT relowner=current_user::regrole FROM pg_class WHERE oid='incidents'::regclass) INTO unsafe FROM pg_roles WHERE rolname=current_user;
   IF unsafe THEN RAISE EXCEPTION 'role bypasses RLS'; END IF;
   idx:=0;
   FOR tbl,cls IN SELECT * FROM (VALUES ('incidents','incident'),('problems','problem'),('changes','change_request')) v(t,c) LOOP
    idx:=idx+1;
    INSERT INTO tickets(id,tenant_id,ticket_number,record_class,title,status,priority) VALUES(400+idx,1,'NEW-'||idx,cls,'new','new','medium');
    EXECUTE format('INSERT INTO %%I(id,work_item_id) VALUES($1,$1)',tbl) USING 400+idx;
    UPDATE tickets SET title='changed',version=version+1 WHERE id=400+idx;
    EXECUTE format('UPDATE %%I SET work_item_id=work_item_id WHERE id=$1',tbl) USING 400+idx;
    EXECUTE format('SELECT count(*) FROM %%I WHERE id=$1',tbl) INTO hidden USING 400+idx;
    IF hidden<>1 THEN RAISE EXCEPTION 'own tenant record missing'; END IF;
    denied:=false;
    BEGIN
     EXECUTE format('INSERT INTO %%I(id,work_item_id) VALUES($1,$1)',tbl) USING 300+idx;
    EXCEPTION WHEN insufficient_privilege THEN denied:=true;
    END;
    IF NOT denied THEN RAISE EXCEPTION 'cross tenant creation admitted'; END IF;
   END LOOP;
   SELECT count(*) INTO hidden FROM incidents WHERE id=4;
   IF hidden<>0 THEN RAISE EXCEPTION 'cross tenant read admitted'; END IF;
   denied:=false;
   BEGIN PERFORM content FROM work_item_migration_evidence;
   EXCEPTION WHEN insufficient_privilege THEN denied:=true;
   END;
   IF NOT denied THEN RAISE EXCEPTION 'ordinary role read global evidence'; END IF;
   RAISE EXCEPTION USING ERRCODE='P0301',MESSAGE='roll back successful test-only probes';
  EXCEPTION WHEN SQLSTATE 'P0301' THEN NULL;
  END;
  IF current_user<>session_user THEN RAISE EXCEPTION 'test role escaped rollback'; END IF;
  IF EXISTS(SELECT 1 FROM tickets WHERE id>=300) THEN RAISE EXCEPTION 'test records escaped rollback'; END IF;
 END IF;
 RETURN NEW;
 END $probe$;
 CREATE TRIGGER probe_preparation_transaction BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION probe_preparation_transaction();`, schema, role)
	_, err = db.ExecContext(ctx, probe)
	require.NoError(t, err)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2"})
	require.NoError(t, m.ApplyPreparation(ctx, preparationEvidence(t, m, ctx)))
	require.NoError(t, m.InspectMigrationTarget(ctx))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM tickets WHERE id>=300`).Scan(&count))
	require.Zero(t, count)
	_, err = db.ExecContext(ctx, `DROP TRIGGER probe_preparation_transaction ON schema_migrations;DROP FUNCTION probe_preparation_transaction()`)
	require.NoError(t, err)
	t.Log("P transaction role probes passed; nested rollback removed grants, role switch and all probe records")
}

func TestWorkItemControlledPreparationWithoutLaterReports(t *testing.T) {
	db, ctx := preparationFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2"})
	e := preparationEvidence(t, m, ctx)
	e.JourneyReportDigest = ""
	e.ObservationReportDigest = ""
	require.NoError(t, m.ApplyPreparation(ctx, e))
	require.NoError(t, m.InspectMigrationTarget(ctx))
}
func TestWorkItemControlledPreparationUnreviewedEnforcingIndexes(t *testing.T) {
	for name, indexSQL := range map[string]string{
		"expression":        `CREATE UNIQUE INDEX unexpected_legacy_title ON incidents ((coalesce(title,'')))`,
		"partial predicate": `CREATE UNIQUE INDEX unexpected_legacy_title ON incidents (id) WHERE title IS NOT NULL`,
		"plain column":      `CREATE UNIQUE INDEX unexpected_legacy_title ON incidents (title)`,
	} {
		t.Run(name+" before P", func(t *testing.T) {
			db, ctx := preparationFixture(t)
			m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2"})
			old := preparationEvidence(t, m, ctx)
			_, err := db.ExecContext(ctx, indexSQL)
			require.NoError(t, err)
			e := preparationEvidence(t, m, ctx)
			if old.InventoryDigest == e.InventoryDigest {
				t.Error("enforcing index dependencies missing from inventory")
			}
			before := preparationLogicalDigest(t, db)
			require.ErrorContains(t, m.ApplyPreparation(ctx, e), "unreviewed enforcing index")
			require.Equal(t, before, preparationLogicalDigest(t, db))
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=$1`, migration.WorkItemPrepareVersion).Scan(&count))
			require.Zero(t, count)
			var attached bool
			require.NoError(t, db.QueryRow(`SELECT to_regclass(current_schema()||'.work_item_migration_evidence') IS NOT NULL`).Scan(&attached))
			require.False(t, attached)
		})
		t.Run(name+" after P", func(t *testing.T) {
			db, ctx := preparationFixture(t)
			m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2"})
			require.NoError(t, m.ApplyPreparation(ctx, preparationEvidence(t, m, ctx)))
			_, err := db.ExecContext(ctx, indexSQL)
			require.NoError(t, err)
			require.Error(t, m.InspectMigrationTarget(ctx), "new enforcing indexes must invalidate P receipt structure")
		})
	}
}
