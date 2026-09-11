//go:build integration_postgres

package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/migration"
	"itsm-backend/service"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkItemControlledRetirementRejectsEmpty(t *testing.T) {
	db, ctx := preparationFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2", Operator: "test"})
	before := preparationLogicalDigest(t, db)
	require.Error(t, m.ApplyRetirement(ctx, migration.MigrationEvidence{}))
	require.Equal(t, before, preparationLogicalDigest(t, db))
}

// Empty support tables are exact prerequisites of registered023–036, not
// evidence of real application journeys. Every ordinary SQL actually executes.
func retirementFixture(t *testing.T, canonical ...bool) (*sql.DB, context.Context, *migration.Migrator, ed25519.PrivateKey) {
	return retirementFixtureConfigured(t, migration.MigrationControlConfig{DeploymentID: "owned-v2", Operator: "fixture-operator"}, canonical...)
}
func retirementFixtureConfigured(t *testing.T, control migration.MigrationControlConfig, canonical ...bool) (*sql.DB, context.Context, *migration.Migrator, ed25519.PrivateKey) {
	db, ctx := preparationFixture(t)
	_, err := db.ExecContext(ctx, `
 ALTER TABLE users ADD COLUMN tenant_id bigint;
 ALTER TABLE process_instances ADD COLUMN process_instance_id varchar NOT NULL UNIQUE;
 ALTER TABLE service_requests ADD COLUMN ticket_id bigint;
 ALTER TABLE service_catalogs ADD COLUMN tenant_id bigint;
 ALTER TABLE audit_logs ADD COLUMN user_id bigint,ADD COLUMN request_body text,ADD COLUMN resource text,ADD COLUMN request_id text,ADD COLUMN path text,ADD COLUMN method text;
 CREATE TABLE tenants(id bigint PRIMARY KEY);
 CREATE TABLE incident_rules(id bigint PRIMARY KEY,tenant_id bigint);
 CREATE TABLE outbox_events(id bigint PRIMARY KEY,tenant_id bigint,event_type text,event_id text,aggregate_type text,aggregate_id text,payload jsonb);
 CREATE TABLE incident_rule_executions(id bigint PRIMARY KEY,tenant_id bigint,rule_id bigint NOT NULL,incident_id bigint,status text,input_data jsonb,output_data jsonb);
 CREATE TABLE ticket_attachments(id bigint PRIMARY KEY,tenant_id bigint,ticket_id bigint,uploaded_by bigint,file_path text,file_size bigint,file_name text,file_type text);
 CREATE TABLE intake_requests(id bigint PRIMARY KEY,tenant_id bigint,actor_id bigint,requester_id bigint,work_item_id bigint,status text,channel text,operation text,idempotency_key text,request_digest text,digest_version int,created_at timestamptz,completed_at timestamptz);
 CREATE TABLE intake_resolution_snapshots(id bigint PRIMARY KEY);
 CREATE TABLE catalog_access_policies(id bigint PRIMARY KEY,catalog_id bigint,version bigint,provider text,external_system text,group_id text,duration_field text,duration_options jsonb);
 CREATE TABLE service_request_access_snapshots(id bigint PRIMARY KEY,work_item_id bigint,policy_id bigint,policy_version bigint,provider text,external_system text,subject_id text,group_id text,duration_key text,duration_seconds bigint);
 CREATE TABLE service_request_access_results(id bigint PRIMARY KEY,work_item_id bigint,process_task_id bigint,provider text,subject_id text,group_id text,evidence_ref text,verified_at timestamptz,outcome text,baseline text,expires_at timestamptz);
 `)
	require.NoError(t, err)
	if len(canonical) > 0 && canonical[0] {
		_, err = db.Exec(`DROP TABLE workflows RESTRICT;
 DROP POLICY tenant_isolation_incidents ON incidents;DROP POLICY tenant_isolation_problems ON problems;DROP POLICY tenant_isolation_changes ON changes;
 ALTER TABLE tickets DROP COLUMN type RESTRICT;
 ALTER TABLE incidents DROP COLUMN title RESTRICT,DROP COLUMN description RESTRICT,DROP COLUMN status RESTRICT,DROP COLUMN priority RESTRICT,DROP COLUMN tenant_id RESTRICT,DROP COLUMN incident_number RESTRICT,DROP COLUMN reporter_id RESTRICT,DROP COLUMN created_at RESTRICT,DROP COLUMN updated_at RESTRICT,DROP COLUMN deleted_at RESTRICT;
 ALTER TABLE problems DROP COLUMN title RESTRICT,DROP COLUMN description RESTRICT,DROP COLUMN status RESTRICT,DROP COLUMN priority RESTRICT,DROP COLUMN tenant_id RESTRICT,DROP COLUMN created_at RESTRICT,DROP COLUMN updated_at RESTRICT,DROP COLUMN deleted_at RESTRICT;
 ALTER TABLE changes DROP COLUMN title RESTRICT,DROP COLUMN description RESTRICT,DROP COLUMN status RESTRICT,DROP COLUMN priority RESTRICT,DROP COLUMN tenant_id RESTRICT,DROP COLUMN created_at RESTRICT,DROP COLUMN updated_at RESTRICT;
 `)
		require.NoError(t, err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	control.RetirementPublicKeys = map[string]ed25519.PublicKey{"fixture": pub}
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), control)
	e := preparationEvidence(t, m, ctx)
	e.Operator = control.Operator
	require.NoError(t, m.ApplyPreparation(ctx, e))
	_, err = m.RunMigrations(ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
	require.NoError(t, m.InspectMigrationTarget(ctx))
	return db, ctx, m, priv
}
func retirementEvidence(t *testing.T, m *migration.Migrator, ctx context.Context, priv ed25519.PrivateKey) migration.MigrationEvidence {
	inv, err := m.InspectRetirement(ctx)
	require.NoError(t, err)
	e := migration.MigrationEvidence{Target: inv.Target, CatalogRevision: migration.ControlledCatalogRevision, LedgerDigest: inv.LedgerDigest, InventoryDigest: inv.InventoryDigest, ApplicationDigest: fmt.Sprintf("%x", sha256.Sum256([]byte("fixture-app"))), Operator: "fixture-operator", ChangeRecord: "fixture-change"}
	now := time.Now().UTC().Add(-time.Hour)
	r := &migration.RetirementEvidence{PreparationDigest: inv.PreparationDigest, DataDigest: inv.DataDigest, Objects: inv.Objects, EmptyInventory: len(inv.Objects) == 0, ObservationStartedAt: now, ObservationEndedAt: now.Add(time.Minute), PausedAt: now.Add(2 * time.Minute), FinalRestorePointAt: now.Add(3 * time.Minute)}
	e.Retirement = r
	for _, kind := range []string{"backup", "restore", "journey", "observation"} {
		b := json.RawMessage(`{"fixture":"synthetic external report; not actual application acceptance"}`)
		h := fmt.Sprintf("%x", sha256.Sum256(b))
		r.Reports = append(r.Reports, migration.RetirementReport{Result: "passed", Kind: kind, Target: e.Target, ApplicationDigest: e.ApplicationDigest, LedgerDigest: e.LedgerDigest, InventoryDigest: e.InventoryDigest, PreparationDigest: r.PreparationDigest, DataDigest: r.DataDigest, RecordedAt: now.Add(4 * time.Minute), Content: b, Digest: h})
		switch kind {
		case "backup":
			e.BackupDigest = h
		case "restore":
			e.RestoreReportDigest = h
		case "journey":
			e.JourneyReportDigest = h
		case "observation":
			e.ObservationReportDigest = h
		}
	}
	signRetirement(t, &e, priv)
	return e
}
func signRetirement(t *testing.T, e *migration.MigrationEvidence, priv ed25519.PrivateKey) {
	digest, err := migration.RetirementEvidenceDigest(*e)
	require.NoError(t, err)
	a := &migration.RetirementAuthorization{KeyID: "fixture", Action: string(migration.OpRetire), EvidenceDigest: digest, Target: e.Target, Operator: e.Operator, ChangeRecord: e.ChangeRecord, ExpiresAt: time.Now().Add(time.Hour)}
	b, err := migration.RetirementAuthorizationPayload(*a)
	require.NoError(t, err)
	a.Signature = ed25519.Sign(priv, b)
	e.Retirement.Authorization = a
}
func TestWorkItemControlledRetirementLaterMigrationsAndExactExecution(t *testing.T) {
	db, ctx, m, priv := retirementFixture(t)
	e := retirementEvidence(t, m, ctx, priv)
	require.NoError(t, m.ApplyRetirement(ctx, e))
	require.NoError(t, m.InspectMigrationTarget(ctx))
	require.NoError(t, m.ApplyRetirement(ctx, e), "lost response replay")
	var title string
	require.NoError(t, db.QueryRow(`SELECT title FROM tickets WHERE id=1`).Scan(&title))
	require.Equal(t, "old title", title)
	e.ChangeRecord = "different"
	signRetirement(t, &e, priv)
	require.Error(t, m.ApplyRetirement(ctx, e))
}
func TestWorkItemControlledRetirementPreparationAllows034(t *testing.T) {
	db, ctx := preparationFixture(t)
	_, err := db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN tenant_id bigint`)
	require.NoError(t, err)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2", Operator: "test"})
	ePrep := preparationEvidence(t, m, ctx)
	ePrep.Operator = "test"
	require.NoError(t, m.ApplyPreparation(ctx, ePrep))
	_, err = db.ExecContext(ctx, migration.GetMigrationSQL("034_problem_investigation_completion"))
	require.NoError(t, err)
	require.NoError(t, m.InspectMigrationTarget(ctx))
}
func TestWorkItemControlledRetirementRequiresImplicitDependencyApproval(t *testing.T) {
	db, ctx, m, priv := retirementFixture(t)
	_, err := db.ExecContext(ctx, `CREATE INDEX legacy_content_index ON workflows(content)`)
	require.NoError(t, err)
	e := retirementEvidence(t, m, ctx, priv)
	found := false
	var roots []migration.RetirementObject
	for _, o := range e.Retirement.Objects {
		if o.Name == "legacy_content_index" {
			found = true
		}
		if o.Kind == "table" || o.Kind == "column" {
			roots = append(roots, o)
		}
	}
	require.True(t, found, "implicit index deletion must be inventoried")
	e.Retirement.Objects = roots
	signRetirement(t, &e, priv)
	before := preparationLogicalDigest(t, db)
	require.Error(t, m.ApplyRetirement(ctx, e))
	require.Equal(t, before, preparationLogicalDigest(t, db))
}
func TestWorkItemControlledRetirementRejectsDriftAndUntrustedEvidence(t *testing.T) {
	cases := map[string]func(*sql.DB, *migration.MigrationEvidence){
		"routine drift": func(db *sql.DB, e *migration.MigrationEvidence) {
			_, err := db.Exec(`CREATE FUNCTION late_routine() RETURNS text LANGUAGE sql AS $$ SELECT 'drift'::text $$`)
			require.NoError(t, err)
		},
		"missing backup": func(db *sql.DB, e *migration.MigrationEvidence) { e.BackupDigest = "" },
		"wrong target":   func(db *sql.DB, e *migration.MigrationEvidence) { e.Target.Schema = "other" },
		"unsigned":       func(db *sql.DB, e *migration.MigrationEvidence) { e.Retirement.Authorization = nil },
		"stale observation backup": func(db *sql.DB, e *migration.MigrationEvidence) {
			e.Retirement.Reports[0].RecordedAt = e.Retirement.ObservationStartedAt
		},
		"record drift": func(db *sql.DB, e *migration.MigrationEvidence) {
			_, err := db.Exec(`UPDATE incidents SET title='legacy mutation' WHERE id=1`)
			require.NoError(t, err)
		},
		"new write after final backup": func(db *sql.DB, e *migration.MigrationEvidence) {
			_, err := db.Exec(`UPDATE tickets SET title='after final backup' WHERE id=1`)
			require.NoError(t, err)
		},
		"dependency drift": func(db *sql.DB, e *migration.MigrationEvidence) {
			_, err := db.Exec(`CREATE VIEW late_legacy_view AS SELECT content FROM workflows`)
			require.NoError(t, err)
		},
		"missing object": func(db *sql.DB, e *migration.MigrationEvidence) {
			_, err := db.Exec(`ALTER TABLE incidents DROP COLUMN description RESTRICT`)
			require.NoError(t, err)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			db, ctx, m, priv := retirementFixture(t)
			e := retirementEvidence(t, m, ctx, priv)
			change(db, &e)
			before := preparationLogicalDigest(t, db)
			require.Error(t, m.ApplyRetirement(ctx, e))
			require.Equal(t, before, preparationLogicalDigest(t, db))
		})
	}
}
func TestWorkItemControlledRetirementAtomicFailures(t *testing.T) {
	for _, failure := range []string{"ddl restrict", "receipt insert", "attachment insert"} {
		t.Run(failure, func(t *testing.T) {
			db, ctx, m, priv := retirementFixture(t)
			if failure == "ddl restrict" {
				_, err := db.Exec(`CREATE VIEW retired_view AS SELECT content FROM workflows`)
				require.NoError(t, err)
			} else {
				table := "schema_migrations"
				if failure == "attachment insert" {
					table = "work_item_migration_evidence"
				}
				_, err := db.Exec(fmt.Sprintf(`CREATE FUNCTION fail_r_receipt() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.version='%s' THEN RAISE EXCEPTION 'injected R receipt fault';END IF;RETURN NEW;END$$;CREATE TRIGGER fail_r_receipt BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION fail_r_receipt()`, migration.WorkItemRetireVersion, table))
				require.NoError(t, err)
			}
			e := retirementEvidence(t, m, ctx, priv)
			if failure == "ddl restrict" {
				var view migration.RetirementObject
				var rest []migration.RetirementObject
				for _, o := range e.Retirement.Objects {
					if o.Kind == "view" {
						view = o
					} else {
						rest = append(rest, o)
					}
				}
				e.Retirement.Objects = append(rest, view)
				signRetirement(t, &e, priv)
			}
			before := preparationLogicalDigest(t, db)
			require.Error(t, m.ApplyRetirement(ctx, e))
			require.Equal(t, before, preparationLogicalDigest(t, db))
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=$1`, migration.WorkItemRetireVersion).Scan(&count))
			require.Zero(t, count)
		})
	}
}
func TestWorkItemControlledRetirementConcurrentAndLockTimeout(t *testing.T) {
	t.Run("concurrent same evidence", func(t *testing.T) {
		db, ctx, m, priv := retirementFixture(t)
		_ = db
		e := retirementEvidence(t, m, ctx, priv)
		start := make(chan struct{})
		results := make(chan error, 2)
		for n := 0; n < 2; n++ {
			go func() { <-start; results <- m.ApplyRetirement(ctx, e) }()
		}
		close(start)
		require.NoError(t, <-results)
		require.NoError(t, <-results)
		require.NoError(t, m.InspectMigrationTarget(ctx))
	})
	t.Run("advisory timeout", func(t *testing.T) {
		db, ctx, m, priv := retirementFixture(t)
		e := retirementEvidence(t, m, ctx, priv)
		before := preparationLogicalDigest(t, db)
		require.NoError(t, m.WithMigrationLock(ctx, func(context.Context) error {
			bounded, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			defer cancel()
			require.Error(t, m.ApplyRetirement(bounded, e))
			return nil
		}))
		require.Equal(t, before, preparationLogicalDigest(t, db))
	})
	t.Run("table lock timeout", func(t *testing.T) {
		db, ctx, m, priv := retirementFixture(t)
		e := retirementEvidence(t, m, ctx, priv)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.Exec(`LOCK TABLE tickets IN ACCESS EXCLUSIVE MODE`)
		require.NoError(t, err)
		require.ErrorContains(t, m.ApplyRetirement(ctx, e), "lock timeout")
		require.NoError(t, tx.Rollback())
		require.NoError(t, m.InspectMigrationTarget(ctx))
	})
}
func TestWorkItemControlledRetirementCrossSchemaDependenciesAndDecoys(t *testing.T) {
	db, ctx, m, priv := retirementFixture(t)
	var schema string
	require.NoError(t, db.QueryRow(`SELECT current_schema()`).Scan(&schema))
	decoy := schema + "_decoy"
	_, err := db.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(decoy) + `;CREATE TABLE ` + pq.QuoteIdentifier(decoy) + `.workflows(id bigint PRIMARY KEY,content text);INSERT INTO ` + pq.QuoteIdentifier(decoy) + `.workflows VALUES(1,'decoy')`)
	require.NoError(t, err)
	defer func() {
		_, err := db.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(decoy) + ` CASCADE`)
		require.NoError(t, err)
	}()
	e := retirementEvidence(t, m, ctx, priv)
	_, err = db.Exec(`CREATE VIEW ` + pq.QuoteIdentifier(decoy) + `.cross_ref AS SELECT content FROM ` + pq.QuoteIdentifier(schema) + `.workflows`)
	require.NoError(t, err)
	before := preparationLogicalDigest(t, db)
	require.Error(t, m.ApplyRetirement(ctx, e))
	require.Equal(t, before, preparationLogicalDigest(t, db))
	_, err = m.InspectRetirement(ctx)
	require.ErrorContains(t, err, "cross-schema")
	_, err = db.Exec(`DROP VIEW ` + pq.QuoteIdentifier(decoy) + `.cross_ref`)
	require.NoError(t, err)
	e = retirementEvidence(t, m, ctx, priv)
	require.NoError(t, m.ApplyRetirement(ctx, e))
	var text string
	require.NoError(t, db.QueryRow(`SELECT content FROM `+pq.QuoteIdentifier(decoy)+`.workflows`).Scan(&text))
	require.Equal(t, "decoy", text)
}
func TestWorkItemControlledRetirementPreservesObservationWrites(t *testing.T) {
	db, ctx, m, priv := retirementFixture(t)
	_, err := db.Exec(`UPDATE tickets SET title='current title' WHERE id=1;INSERT INTO tickets(id,tenant_id,ticket_number,record_class,title,status,priority) VALUES(10,1,'INC010','incident','observation row','new','medium');INSERT INTO incidents(id,work_item_id) VALUES(10,10)`)
	require.NoError(t, err)
	e := retirementEvidence(t, m, ctx, priv)
	require.NoError(t, m.ApplyRetirement(ctx, e))
	var title string
	require.NoError(t, db.QueryRow(`SELECT title FROM tickets WHERE id=10`).Scan(&title))
	require.Equal(t, "observation row", title)
	require.NoError(t, m.InspectMigrationTarget(ctx))
	_, err = db.Exec(`DROP POLICY tenant_isolation_incidents ON incidents`)
	require.NoError(t, err)
	require.Error(t, m.InspectMigrationTarget(ctx))
}

// Test-only fault at the driver commit-response boundary: PostgreSQL commits,
// then the caller receives an error instead of success. No production hooks.
type lostRetirementConnector struct {
	driver.Connector
	armed atomic.Bool
}
type lostRetirementConn struct {
	driver.Conn
	owner *lostRetirementConnector
}
type lostRetirementTx struct {
	driver.Tx
	owner *lostRetirementConnector
}

func (c *lostRetirementConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &lostRetirementConn{conn, c}, nil
}
func (c *lostRetirementConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
	if err != nil {
		return nil, err
	}
	return &lostRetirementTx{tx, c.owner}, nil
}
func (t *lostRetirementTx) Commit() error {
	if err := t.Tx.Commit(); err != nil {
		return err
	}
	if t.owner.armed.Swap(false) {
		return errors.New("injected lost commit response after server commit")
	}
	return nil
}
func TestWorkItemControlledRetirementLostCommitResponse(t *testing.T) {
	db, ctx, m, priv := retirementFixture(t)
	e := retirementEvidence(t, m, ctx, priv)
	var schema string
	require.NoError(t, db.QueryRow(`SELECT current_schema()`).Scan(&schema))
	u := migrationEntryTarget(t)
	v := u.Query()
	v.Set("search_path", schema)
	u.RawQuery = v.Encode()
	connector, err := pq.NewConnector(u.String())
	require.NoError(t, err)
	faulty := &lostRetirementConnector{Connector: connector}
	faulty.armed.Store(true)
	faultDB := sql.OpenDB(faulty)
	defer faultDB.Close()
	faultM := migration.NewMigrator(faultDB, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2", Operator: "fixture-operator", RetirementPublicKeys: map[string]ed25519.PublicKey{"fixture": priv.Public().(ed25519.PublicKey)}})
	require.ErrorContains(t, faultM.ApplyRetirement(ctx, e), "lost commit response")
	require.NoError(t, m.InspectMigrationTarget(ctx))
	require.NoError(t, m.ApplyRetirement(ctx, e))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=$1`, migration.WorkItemRetireVersion).Scan(&count))
	require.Equal(t, 1, count)
}

func TestWorkItemControlledRetirementExpiredReplay(t *testing.T) {
	_, ctx, m, priv := retirementFixture(t)
	e := retirementEvidence(t, m, ctx, priv)
	e.Retirement.Authorization.ExpiresAt = time.Now().Add(3 * time.Second)
	payload, err := migration.RetirementAuthorizationPayload(*e.Retirement.Authorization)
	require.NoError(t, err)
	e.Retirement.Authorization.Signature = ed25519.Sign(priv, payload)
	require.NoError(t, m.ApplyRetirement(ctx, e))
	time.Sleep(time.Until(e.Retirement.Authorization.ExpiresAt) + 10*time.Millisecond)
	require.NoError(t, m.InspectMigrationTarget(ctx))
	require.NoError(t, m.ApplyRetirement(ctx, e), "committed identical retry must not need new authorization")
}

func (c *lostRetirementConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *lostRetirementConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}

func TestWorkItemControlledRetirementExplicitEmptyInventory(t *testing.T) {
	_, ctx, m, priv := retirementFixture(t, true)
	e := retirementEvidence(t, m, ctx, priv)
	require.Empty(t, e.Retirement.Objects)
	require.True(t, e.Retirement.EmptyInventory)
	e.Retirement.EmptyInventory = false
	signRetirement(t, &e, priv)
	require.Error(t, m.ApplyRetirement(ctx, e))
	e.Retirement.EmptyInventory = true
	signRetirement(t, &e, priv)
	require.NoError(t, m.ApplyRetirement(ctx, e))
	require.NoError(t, m.InspectMigrationTarget(ctx))
}
func TestWorkItemControlledRetirementRechecksCommittedWriteAfterLockWait(t *testing.T) {
	db, ctx, m, priv := retirementFixture(t)
	e := retirementEvidence(t, m, ctx, priv)
	writer, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer writer.Rollback()
	_, err = writer.Exec(`LOCK TABLE audit_logs IN ROW EXCLUSIVE MODE;UPDATE tickets SET title='committed during R lock wait' WHERE id=1`)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { result <- m.ApplyRetirement(ctx, e) }()
	require.Eventually(t, func() bool {
		var count int
		err := db.QueryRow(`SELECT count(*) FROM pg_locks l JOIN pg_class c ON c.oid=l.relation WHERE c.relnamespace=current_schema()::regnamespace AND c.relname='audit_logs' AND NOT l.granted`).Scan(&count)
		return err == nil && count > 0
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, writer.Commit())
	require.Error(t, <-result, "locked recheck must see the newly committed row, not the pre-lock MVCC snapshot")
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=$1`, migration.WorkItemRetireVersion).Scan(&count))
	require.Zero(t, count)
}

// Current complete Ent schema plus explicitly retained legacy columns supports
// the real deletion owner; this is a canonical-shaped fixture, not a deployed dump.
func TestWorkItemControlledRetirementOwningSoftDeleteAndPostRetirementWrites(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	logger := zap.NewNop().Sugar()
	require.NoError(t, migration.NewMigrator(db, logger).EnsureMigrationsTable(ctx))
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	require.NoError(t, client.Schema.Create(ctx))
	tenant := client.Tenant.Create().SetName("retirement deletion").SetCode("r-delete").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("r-deleter").SetName("r deleter").SetPasswordHash("fixture").SetEmail("r-delete@example.test").SetRole("operator").SetActive(true).SaveX(ctx)
	role := client.Role.Create().SetTenantID(tenant.ID).SetCode("operator").SetName("operator").SetIsActive(true).SaveX(ctx)
	for _, verb := range []string{"read", "delete"} {
		permission := client.Permission.Create().SetTenantID(tenant.ID).SetCode("r-" + verb).SetName(verb).SetResource("incident").SetAction(verb).SaveX(ctx)
		client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).ExecX(ctx)
	}
	item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetRecordClass("incident").SetTitle("retained deletion title").SetStatus("new").SetTicketNumber("R-DEL-1").SaveX(ctx)
	incident := client.Incident.Create().SetWorkItemID(item.ID).SaveX(ctx)
	_, err := db.Exec(`ALTER TABLE tickets ADD COLUMN type text;UPDATE tickets SET type=record_class;
 ALTER TABLE incidents ADD COLUMN title text,ADD COLUMN tenant_id bigint;
 UPDATE incidents i SET title=t.title,tenant_id=t.tenant_id FROM tickets t WHERE t.id=i.work_item_id;
 CREATE TABLE workflows(id bigint PRIMARY KEY,content text);INSERT INTO workflows VALUES(1,'retained workflow');`)
	require.NoError(t, err)
	ordinary := migration.NewMigrator(db, logger)
	for _, mig := range migration.RegisteredMigrations {
		require.NoError(t, ordinary.ApplyMigration(ctx, mig))
		if mig.Version == "021_add_callback_optional_declared" {
			break
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	m := migration.NewMigrator(db, logger, migration.MigrationControlConfig{DeploymentID: "owned-v2", Operator: "fixture-operator", RetirementPublicKeys: map[string]ed25519.PublicKey{"fixture": pub}})
	ePrep := preparationEvidence(t, m, ctx)
	ePrep.Operator = "fixture-operator"
	require.NoError(t, m.ApplyPreparation(ctx, ePrep))
	later := false
	for _, d := range migration.ControlledMigrationCatalog() {
		if d.Migration.Version == migration.WorkItemPrepareVersion {
			later = true
			continue
		}
		if !later || d.Stage != migration.StageOrdinary {
			continue
		}
		sqlText := migration.GetMigrationSQL(d.Migration.Version)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, sqlText)
		if err != nil {
			tx.Rollback()
			t.Fatalf("%s: %v", d.Migration.Version, err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,description,checksum,rollback_sql) VALUES($1,$2,$3,$4)`, d.Migration.Version, d.Migration.Description, fmt.Sprintf("%x", sha256.Sum256([]byte(sqlText))), d.Migration.RollbackSQL)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
	}
	var original string
	require.NoError(t, db.QueryRow(`SELECT content::text FROM work_item_migration_evidence WHERE version=$1`, migration.WorkItemPrepareVersion).Scan(&original))
	svc := service.NewIncidentService(client, logger)
	require.NoError(t, svc.DeleteIncident(ctx, incident.ID, workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, Source: "http"}))
	after := client.Ticket.GetX(ctx, item.ID)
	require.NotNil(t, after.DeletedAt)
	require.Equal(t, item.Version+1, after.Version)
	var retained string
	var rows int
	require.NoError(t, db.QueryRow(`SELECT title FROM incidents WHERE id=$1`, incident.ID).Scan(&retained))
	require.Equal(t, item.Title, retained)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM incidents WHERE id=$1 AND work_item_id=$2`, incident.ID, item.ID).Scan(&rows))
	require.Equal(t, 1, rows)
	var attachment string
	require.NoError(t, db.QueryRow(`SELECT content::text FROM work_item_migration_evidence WHERE version=$1`, migration.WorkItemPrepareVersion).Scan(&attachment))
	require.Equal(t, original, attachment)
	evidence := retirementEvidence(t, m, ctx, priv)
	require.NoError(t, m.ApplyRetirement(ctx, evidence))
	// Normal post-R business writes must not be compared to a frozen data snapshot.
	newer := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetRecordClass("generic").SetTitle("after retirement").SetStatus("new").SetTicketNumber("R-POST-1").SaveX(ctx)
	client.Ticket.UpdateOneID(newer.ID).SetTitle("post-R update").ExecX(ctx)
	require.NoError(t, m.InspectMigrationTarget(ctx))
	require.NoError(t, m.ApplyRetirement(ctx, evidence))
	require.Equal(t, "post-R update", client.Ticket.GetX(ctx, newer.ID).Title)
	require.NotNil(t, client.Ticket.GetX(ctx, item.ID).DeletedAt)
}

func TestWorkItemControlledRetirementPhysicalDisappearanceNeverUsesHTTPAudit(t *testing.T) {
	for _, audit := range []string{"none", "matching HTTP audit", "wrong identity", "cross tenant"} {
		t.Run(audit, func(t *testing.T) {
			db, ctx, m, priv := retirementFixture(t)
			// Hostile fixture only: no application owner physically purges this row.
			_, err := db.Exec(`DELETE FROM incidents WHERE id=1`)
			require.NoError(t, err)
			if audit != "none" {
				tenant, id := 1, 1
				if audit == "wrong identity" {
					id = 4
				}
				if audit == "cross tenant" {
					tenant = 2
				}
				_, err = db.Exec(`INSERT INTO audit_logs(id,tenant_id,user_id,action,created_at,path,method,resource,request_body) VALUES(99,$1,1,'delete',now(),$2,'DELETE','incidents','{}')`, tenant, fmt.Sprintf("/api/v1/incidents/%d", id))
				require.NoError(t, err)
			}
			e := retirementEvidence(t, m, ctx, priv)
			before := preparationLogicalDigest(t, db)
			require.ErrorContains(t, m.ApplyRetirement(ctx, e), "original preparation row changed or is missing")
			require.Equal(t, before, preparationLogicalDigest(t, db))
		})
	}
}

// currentRuntimeFixture creates the current generated base only on a new owned
// empty schema, then executes the public prerequisite, controlled P and ordinary
// paths. It is a structural fixture, not application journey evidence.
func currentRuntimeFixture(t *testing.T, control migration.MigrationControlConfig) (*sql.DB, context.Context, *migration.Migrator) {
	db, ctx := migrationEntryFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), control)
	require.NoError(t, m.EnsureMigrationsTable(ctx))
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	require.NoError(t, client.Schema.Create(ctx))
	_, err := m.RunMigrations(ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
	e := preparationEvidence(t, m, ctx)
	e.Operator = control.Operator
	require.NoError(t, m.ApplyPreparation(ctx, e))
	_, err = m.RunMigrations(ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
	return db, ctx, m
}

func TestControlledEntryCurrentRequiredStructure(t *testing.T) {
	for _, mutation := range []string{
		"ALTER TABLE problems DROP COLUMN verified_at",
		"DROP TABLE problem_investigation_steps; DROP TABLE problem_investigations",
		"ALTER TABLE problems ALTER COLUMN verified_at TYPE text USING verified_at::text",
		"ALTER TABLE problem_solutions ALTER COLUMN estimated_cost TYPE text USING estimated_cost::text",
	} {
		t.Run(mutation, func(t *testing.T) {
			db, ctx, m := currentRuntimeFixture(t, migration.MigrationControlConfig{DeploymentID: "owned-v2", Operator: "fixture-operator"})
			require.NoError(t, m.InspectRuntimeMigrations(ctx))
			var receiptCount int
			require.NoError(t, db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version LIKE '034_%'").Scan(&receiptCount))
			require.Equal(t, 1, receiptCount)
			_, err := db.ExecContext(ctx, mutation)
			require.NoError(t, err)
			before := entryDigest(t, db)
			require.Error(t, m.InspectRuntimeMigrations(ctx))
			require.Equal(t, before, entryDigest(t, db))
		})
	}
}

func TestControlledEntryCurrentBootstrapDoesNotOverlayEnt(t *testing.T) {
	db, ctx, m := currentRuntimeFixture(t, migration.MigrationControlConfig{DeploymentID: "owned-v2", Operator: "fixture-operator"})
	before := entryDigest(t, db)
	overlays, seeds := 0, 0
	require.NoError(t, migration.RunCanonicalBootstrap(ctx, migration.CanonicalBootstrap{
		Migrator:     m,
		Prepare:      func(context.Context) error { overlays++; return nil },
		CreateSchema: func(context.Context) error { overlays++; return nil },
		Seed:         func(context.Context) error { seeds++; return nil },
	}))
	require.Zero(t, overlays)
	require.Equal(t, 1, seeds)
	require.Equal(t, before, entryDigest(t, db))
}
