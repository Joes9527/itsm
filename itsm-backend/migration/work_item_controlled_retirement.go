package migration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/lib/pq"
)

var retirementTables = []string{"ticket_approvals", "workflow_tasks", "workflow_instances", "workflow_versions", "workflows"}

func retirementColumns() map[string][]string {
	result := map[string][]string{}
	for k, v := range preparationLegacyColumns {
		result[k] = append([]string(nil), v...)
	}
	result["releases"] = []string{"requires_approval"}
	result["ticket_categories"] = []string{"workflow_id"}
	return result
}

// The canonical SQL identity describes the fixed executor, not caller-supplied SQL.
const workItemRetirementSQL = `-- WorkItem R v1: exact approved objects from the frozen 022/027 registry and explicitly reviewed dependencies; schema-qualified RESTRICT; atomic evidence receipt.`

func retirementObjects(ctx context.Context, q migrationQuery, schema string) ([]RetirementObject, error) {
	var result []RetirementObject
	for _, table := range retirementTables {
		var exists bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=$1::regnamespace AND relname=$2 AND relkind='r')`, schema, table).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			result = append(result, RetirementObject{"table", schema, table, table})
		}
	}
	for table, cols := range retirementColumns() {
		for _, col := range cols {
			var exists bool
			if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=$1::regnamespace AND c.relname=$2 AND a.attname=$3 AND NOT a.attisdropped AND a.attnum>0)`, schema, table, col).Scan(&exists); err != nil {
				return nil, err
			}
			if exists {
				result = append(result, RetirementObject{"column", schema, table, col})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return fmt.Sprint(result[i]) < fmt.Sprint(result[j]) })
	return result, nil
}

// Whole selected-schema logical catalog snapshot includes cross-schema dependencies
// pointing into it. It deliberately excludes planner statistics and table contents.
func retirementStructure(ctx context.Context, q migrationQuery, schema string) (string, error) {
	var data string
	err := q.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'relations',(SELECT coalesce(jsonb_agg(jsonb_build_array(c.relname,c.relkind,c.relowner,c.relacl,c.relrowsecurity,c.relforcerowsecurity) ORDER BY c.relname),'[]') FROM pg_class c WHERE c.relnamespace=$1::regnamespace),
 'columns',(SELECT coalesce(jsonb_agg(jsonb_build_array(c.relname,a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attidentity,a.attgenerated,a.attacl,pg_get_expr(d.adbin,d.adrelid)) ORDER BY c.relname,a.attnum),'[]') FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE c.relnamespace=$1::regnamespace AND a.attnum>0 AND NOT a.attisdropped),
 'constraints',(SELECT coalesce(jsonb_agg(jsonb_build_array(c.conname,c.conrelid::regclass::text,pg_get_constraintdef(c.oid)) ORDER BY c.oid),'[]') FROM pg_constraint c WHERE c.connamespace=$1::regnamespace),
 'indexes',(SELECT coalesce(jsonb_agg(jsonb_build_array(indexname,indexdef) ORDER BY indexname),'[]') FROM pg_indexes WHERE schemaname=$1::text),
 'policies',(SELECT coalesce(jsonb_agg(jsonb_build_array(c.relname,p.polname,p.polcmd,p.polpermissive,p.polroles,pg_get_expr(p.polqual,p.polrelid),pg_get_expr(p.polwithcheck,p.polrelid)) ORDER BY c.relname,p.polname),'[]') FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid WHERE c.relnamespace=$1::regnamespace),
 'functions',(SELECT coalesce(jsonb_agg(pg_get_functiondef(p.oid) ORDER BY p.oid),'[]') FROM pg_proc p WHERE p.pronamespace=$1::regnamespace AND p.prokind IN ('f','p','w')),
 'views',(SELECT coalesce(jsonb_agg(jsonb_build_array(c.relname,pg_get_viewdef(c.oid)) ORDER BY c.relname),'[]') FROM pg_class c WHERE c.relnamespace=$1::regnamespace AND c.relkind IN ('v','m')),
 'triggers',(SELECT coalesce(jsonb_agg(pg_get_triggerdef(t.oid) ORDER BY t.oid),'[]') FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relnamespace=$1::regnamespace),
 'dependencies',(SELECT coalesce(jsonb_agg(jsonb_build_array(d.classid::regclass::text,d.objid,d.objsubid,d.refclassid::regclass::text,d.refobjid,d.refobjsubid,d.deptype) ORDER BY d.classid,d.objid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid),'[]') FROM pg_depend d WHERE (d.refclassid='pg_class'::regclass AND d.refobjid IN (SELECT oid FROM pg_class WHERE relnamespace=$1::regnamespace)) OR (d.classid='pg_class'::regclass AND d.objid IN (SELECT oid FROM pg_class WHERE relnamespace=$1::regnamespace)))
 )::text`, schema).Scan(&data)
	if err != nil {
		return "", err
	}
	return checksumSQL(data), nil
}

func retirementData(ctx context.Context, q migrationQuery, schema string, surviving bool) (string, error) {
	rows, err := q.QueryContext(ctx, `SELECT relname FROM pg_class WHERE relnamespace=$1::regnamespace AND relkind='r' AND relname NOT IN ('schema_migrations','work_item_migration_evidence') ORDER BY relname`, schema)
	if err != nil {
		return "", err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			rows.Close()
			return "", err
		}
		tables = append(tables, table)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	var data []string
	for _, table := range tables {
		if surviving {
			skip := false
			for _, v := range retirementTables {
				if table == v {
					skip = true
				}
			}
			if skip {
				continue
			}
		}
		expr := `to_jsonb(r)`
		if surviving {
			for _, col := range retirementColumns()[table] {
				expr += ` - ` + pq.QuoteLiteral(col)
			}
		}
		var content string
		if err = q.QueryRowContext(ctx, `SELECT coalesce(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text FROM (SELECT `+expr+` v FROM `+preparationRelation(schema, table)+` r) s`).Scan(&content); err != nil {
			return "", err
		}
		data = append(data, table, content)
	}
	return evidenceDigest(data)
}

func loadPreparationAttachment(ctx context.Context, q migrationQuery, schema, digest string) (preparationAttachment, error) {
	var a preparationAttachment
	var b []byte
	var stored string
	if err := q.QueryRowContext(ctx, `SELECT content,digest FROM `+preparationRelation(schema, "work_item_migration_evidence")+` WHERE version=$1`, WorkItemPrepareVersion).Scan(&b, &stored); err != nil {
		return a, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return a, err
	}
	actual, err := evidenceDigest(a)
	if err != nil {
		return a, err
	}
	if stored != digest || actual != digest {
		return a, fmt.Errorf("preparation attachment digest mismatch")
	}
	return a, nil
}

func (m *Migrator) InspectRetirement(ctx context.Context) (RetirementInventory, error) {
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return RetirementInventory{}, err
	}
	defer tx.Rollback()
	return m.retirementInventory(ctx, tx)
}

func (m *Migrator) retirementInventory(ctx context.Context, q migrationQuery) (RetirementInventory, error) {
	var i RetirementInventory
	target, err := m.preparationTarget(ctx, q)
	if err != nil {
		return i, err
	}
	i.Target = target
	applied, err := inspectMigrationTarget(ctx, q, m.controlConfig)
	if err != nil {
		return i, err
	}
	plan, err := PlanMigrations(ControlledMigrationCatalog(), applied, OpRetire, nil)
	if err != nil {
		return i, err
	}
	if len(plan.Executable) != 1 || plan.Executable[0].Version != WorkItemRetireVersion {
		return i, fmt.Errorf("all P and ordinary prerequisites must be completed before retirement")
	}
	for _, a := range applied {
		if a.Version == WorkItemPrepareVersion {
			i.PreparationDigest = *a.EvidenceDigest
		}
	}
	if i.PreparationDigest == "" {
		return i, fmt.Errorf("preparation receipt is required")
	}
	if i.LedgerDigest, err = evidenceDigest(applied); err != nil {
		return i, err
	}
	if i.InventoryDigest, err = retirementStructure(ctx, q, target.Schema); err != nil {
		return i, err
	}
	if i.DataDigest, err = retirementData(ctx, q, target.Schema, false); err != nil {
		return i, err
	}
	i.Objects, err = retirementObjects(ctx, q, target.Schema)
	if err != nil {
		return i, err
	}
	deps, err := retirementDependencies(ctx, q, target.Schema)
	if err != nil {
		return i, err
	}
	i.Objects = append(deps, i.Objects...)
	return i, err
}

// validateRetirementBaseline checks only rows present at P. New-path rows need
// not populate retired fields. Missing original rows fail closed for audit review.
func validateRetirementBaseline(ctx context.Context, q migrationQuery, schema string, a preparationAttachment) error {
	scopes := map[string][]string{}
	for _, r := range a.Baseline {
		if len(r.Columns) == 0 {
			return fmt.Errorf("preparation baseline column scope missing; explicit evidence review required")
		}
		if prev := scopes[r.Table]; prev != nil && !reflect.DeepEqual(prev, r.Columns) {
			return fmt.Errorf("inconsistent preparation column scope")
		}
		scopes[r.Table] = r.Columns
	}
	live, err := preparationBaselineWithScope(ctx, q, schema, scopes)
	if err != nil {
		return err
	}
	index := map[string]PreparationBaselineRow{}
	for _, r := range live {
		index[r.Table+"/"+r.ID] = r
	}
	for _, r := range a.Baseline {
		if !reflect.DeepEqual(index[r.Table+"/"+r.ID], r) {
			return fmt.Errorf("original preparation row changed or is missing: %s/%s; audit review required", r.Table, r.ID)
		}
	}
	return nil
}

type retirementAttachment struct {
	Evidence            MigrationEvidence
	PostStructureDigest string
	PreservedDataDigest string
}

func verifyRetirementReceipt(ctx context.Context, q migrationQuery, schema, digest string, config MigrationControlConfig, executedAt time.Time) error {
	var b []byte
	var stored string
	if err := q.QueryRowContext(ctx, `SELECT content,digest FROM `+preparationRelation(schema, "work_item_migration_evidence")+` WHERE version=$1`, WorkItemRetireVersion).Scan(&b, &stored); err != nil {
		return err
	}
	var a retirementAttachment
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return err
	}
	actual, err := evidenceDigest(a)
	if err != nil {
		return err
	}
	if actual != digest || stored != digest {
		return fmt.Errorf("retirement attachment digest mismatch")
	}
	if err = validateRetirementEvidenceAt(a.Evidence, executedAt); err != nil {
		return err
	}
	if config.DeploymentID == "" || a.Evidence.Target.DeploymentID != config.DeploymentID || a.Evidence.Target.Schema != schema {
		return fmt.Errorf("trusted historical retirement deployment identity missing or mismatched")
	}
	var database string
	if err = q.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return err
	}
	if a.Evidence.Target.Database != database {
		return fmt.Errorf("historical retirement database mismatch")
	}
	if err = verifyRetirementAuthorization(a.Evidence, historicalRetirementKeys(config), executedAt); err != nil {
		return err
	}
	var pdigest string
	if err = q.QueryRowContext(ctx, `SELECT evidence_digest FROM `+preparationRelation(schema, "schema_migrations")+` WHERE version=$1`, WorkItemPrepareVersion).Scan(&pdigest); err != nil {
		return err
	}
	if pdigest != a.Evidence.Retirement.PreparationDigest {
		return fmt.Errorf("retirement preparation binding mismatch")
	}
	if _, err = loadPreparationAttachment(ctx, q, schema, pdigest); err != nil {
		return err
	}
	objects, err := retirementObjects(ctx, q, schema)
	if err != nil {
		return err
	}
	if len(objects) != 0 {
		return fmt.Errorf("retired objects reappeared")
	}
	structure, err := retirementStructure(ctx, q, schema)
	if err != nil {
		return err
	}
	if structure != a.PostStructureDigest {
		return fmt.Errorf("post-retirement structure drift")
	}
	if err := validateEvidenceACL(ctx, q, schema, config.InspectionRole); err != nil {
		return err
	}
	p, err := loadPreparationAttachment(ctx, q, schema, pdigest)
	if err != nil {
		return err
	}
	if p.InspectionRole != config.InspectionRole {
		return fmt.Errorf("trusted inspection role differs from preparation receipt")
	}

	return nil
}

func (m *Migrator) ApplyRetirement(ctx context.Context, e MigrationEvidence) error {
	if err := ValidateRetirementEvidence(e); err != nil {
		return err
	}
	// Verify cryptographic proof before acquiring resources; only a matching
	// committed receipt can reuse a historically valid, now-expired envelope.
	if err := verifyRetirementAuthorization(e, historicalRetirementKeys(m.controlConfig), e.Retirement.FinalRestorePointAt); err != nil {
		return err
	}
	// Bounds apply to advisory-lock acquisition as well as transaction statements.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return m.WithMigrationLock(ctx, func(ctx context.Context) error {
		// Re-read committed state after acquiring table locks; a pre-lock
		// repeatable snapshot can miss writes committed while waiting.
		tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='20s'`); err != nil {
			return err
		}
		target, err := m.preparationTarget(ctx, tx)
		if err != nil {
			return err
		}
		if target != e.Target {
			return fmt.Errorf("retirement target mismatch")
		}
		applied, err := inspectMigrationTarget(ctx, tx, m.controlConfig)
		if err != nil {
			return err
		}
		for _, a := range applied {
			if a.Version == WorkItemRetireVersion {
				var b []byte
				if err = tx.QueryRowContext(ctx, `SELECT content FROM `+preparationRelation(target.Schema, "work_item_migration_evidence")+` WHERE version=$1`, WorkItemRetireVersion).Scan(&b); err != nil {
					return err
				}
				var prior retirementAttachment
				if err = json.Unmarshal(b, &prior); err != nil {
					return err
				}
				old, _ := evidenceDigest(prior.Evidence)
				incoming, _ := evidenceDigest(e)
				if old != incoming {
					return fmt.Errorf("retirement retry evidence conflicts with existing receipt")
				}
				return nil
			}
		}
		// Every owned table participates in the final recovery snapshot. Lock all of
		// them before taking it; locks are retained through DDL and the sole receipt.
		rows, err := tx.QueryContext(ctx, `SELECT relname FROM pg_class WHERE relnamespace=$1::regnamespace AND relkind='r' ORDER BY relname`, target.Schema)
		if err != nil {
			return err
		}
		var tables []string
		for rows.Next() {
			var t string
			if err = rows.Scan(&t); err != nil {
				rows.Close()
				return err
			}
			tables = append(tables, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, t := range tables {
			if _, err = tx.ExecContext(ctx, `LOCK TABLE `+preparationRelation(target.Schema, t)+` IN ACCESS EXCLUSIVE MODE`); err != nil {
				return err
			}
		}
		inv, err := m.retirementInventory(ctx, tx)
		if err != nil {
			return err
		}
		if e.LedgerDigest != inv.LedgerDigest || e.InventoryDigest != inv.InventoryDigest || e.Retirement.DataDigest != inv.DataDigest || e.Retirement.PreparationDigest != inv.PreparationDigest {
			return fmt.Errorf("retirement ledger/inventory/final recovery snapshot drift")
		}
		started := time.Now().UTC()
		if err = m.authorizeRetirement(e); err != nil {
			return err
		}
		p, err := loadPreparationAttachment(ctx, tx, target.Schema, inv.PreparationDigest)
		if err != nil {
			return err
		}
		if err = validateRetirementBaseline(ctx, tx, target.Schema, p); err != nil {
			return err
		}
		if err = validateRetirementManifest(ctx, tx, target.Schema, inv.Objects, e.Retirement.Objects); err != nil {
			return err
		}
		before, err := retirementData(ctx, tx, target.Schema, true)
		if err != nil {
			return err
		}
		for _, o := range e.Retirement.Objects {
			if _, err = tx.ExecContext(ctx, retirementDDL(o)); err != nil {
				return fmt.Errorf("retirement %s %s.%s: %w", o.Kind, o.Table, o.Name, err)
			}
		}
		left, err := retirementObjects(ctx, tx, target.Schema)
		if err != nil {
			return err
		}
		if len(left) != 0 {
			return fmt.Errorf("retirement manifest did not remove every approved legacy object")
		}
		after, err := retirementData(ctx, tx, target.Schema, true)
		if err != nil {
			return err
		}
		if before != after {
			return fmt.Errorf("retirement changed authoritative records/history")
		}
		structure, err := retirementStructure(ctx, tx, target.Schema)
		if err != nil {
			return err
		}
		attachment := retirementAttachment{e, structure, before}
		data, err := json.Marshal(attachment)
		if err != nil {
			return err
		}
		digest := checksumSQL(string(data))
		_, err = tx.ExecContext(ctx, `INSERT INTO `+preparationRelation(target.Schema, "schema_migrations")+`(version,description,checksum,execution_ms,release_version,catalog_revision,evidence_digest,applied_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, WorkItemRetireVersion, "Retire WorkItem legacy structures with controlled evidence", checksumSQL(workItemRetirementSQL), time.Since(started).Milliseconds(), m.releaseVersion, ControlledCatalogRevision, digest, started)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO `+preparationRelation(target.Schema, "work_item_migration_evidence")+`(version,content,digest) VALUES($1,$2,$3)`, WorkItemRetireVersion, string(data), digest)
		if err != nil {
			return err
		}
		if err = verifyRetirementReceipt(ctx, tx, target.Schema, digest, m.controlConfig, started); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func retirementDDL(o RetirementObject) string {
	table := preparationRelation(o.Schema, o.Table)
	name := pq.QuoteIdentifier(o.Name)
	switch o.Kind {
	case "table":
		return `DROP TABLE ` + table + ` RESTRICT`
	case "column":
		return `ALTER TABLE ` + table + ` DROP COLUMN ` + name + ` RESTRICT`
	case "constraint":
		return `ALTER TABLE ` + table + ` DROP CONSTRAINT ` + name + ` RESTRICT`
	case "trigger":
		return `DROP TRIGGER ` + name + ` ON ` + table + ` RESTRICT`
	case "policy":
		return `DROP POLICY ` + name + ` ON ` + table + ` RESTRICT`
	case "sequence":
		return `DROP SEQUENCE ` + preparationRelation(o.Schema, o.Name) + ` RESTRICT`
	case "index":
		return `DROP INDEX ` + preparationRelation(o.Schema, o.Name) + ` RESTRICT`
	case "view":
		return `DROP VIEW ` + preparationRelation(o.Schema, o.Name) + ` RESTRICT`
	}
	panic("validated object kind required")
}

func validateRetirementManifest(ctx context.Context, q migrationQuery, schema string, inventory, approved []RetirementObject) error {
	a := append([]RetirementObject(nil), approved...)
	i := append([]RetirementObject(nil), inventory...)
	sort.Slice(a, func(x, y int) bool { return fmt.Sprint(a[x]) < fmt.Sprint(a[y]) })
	sort.Slice(i, func(x, y int) bool { return fmt.Sprint(i[x]) < fmt.Sprint(i[y]) })
	if !reflect.DeepEqual(a, i) && !(len(a) == 0 && len(i) == 0) {
		return fmt.Errorf("approved exact object/dependency inventory differs from actual inventory")
	}
	return nil
}

// Intrinsic table row types, defaults and constraint-owned backing indexes are
// parts of their listed parent object. Independently addressable dependents must
// themselves be approved; cross-schema dependents always block this operation.
func retirementDependencies(ctx context.Context, q migrationQuery, schema string) ([]RetirementObject, error) {
	rows, err := q.QueryContext(ctx, `WITH dependencies AS (
 SELECT DISTINCT d.classid,d.objid FROM pg_depend d JOIN pg_class ref ON d.refclassid='pg_class'::regclass AND d.refobjid=ref.oid
 LEFT JOIN pg_attribute a ON a.attrelid=ref.oid AND a.attnum=d.refobjsubid
 WHERE ref.relnamespace=$1::regnamespace AND (ref.relname=ANY($2) OR (ref.relname||'.'||coalesce(a.attname,''))=ANY($3))
 ), objects AS (
 SELECT 'constraint' kind,n.nspname, t.relname tab,k.conname name FROM dependencies d JOIN pg_constraint k ON d.classid='pg_constraint'::regclass AND d.objid=k.oid JOIN pg_class t ON t.oid=k.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace
 UNION SELECT 'index',n.nspname,t.relname,c.relname FROM dependencies d JOIN pg_class c ON d.classid='pg_class'::regclass AND d.objid=c.oid AND c.relkind='i' JOIN pg_index ix ON ix.indexrelid=c.oid JOIN pg_class t ON t.oid=ix.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE NOT EXISTS(SELECT 1 FROM pg_constraint k WHERE k.conindid=c.oid)
 UNION SELECT 'view',n.nspname,c.relname,c.relname FROM dependencies d JOIN pg_rewrite r ON d.classid='pg_rewrite'::regclass AND d.objid=r.oid JOIN pg_class c ON c.oid=r.ev_class JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='v'
 UNION SELECT 'trigger',n.nspname,c.relname,t.tgname FROM dependencies d JOIN pg_trigger t ON d.classid='pg_trigger'::regclass AND d.objid=t.oid JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE NOT t.tgisinternal
 UNION SELECT 'policy',n.nspname,c.relname,p.polname FROM dependencies d JOIN pg_policy p ON d.classid='pg_policy'::regclass AND d.objid=p.oid JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 UNION SELECT 'sequence',n.nspname,c.relname,c.relname FROM dependencies d JOIN pg_class c ON d.classid='pg_class'::regclass AND d.objid=c.oid AND c.relkind='S' JOIN pg_namespace n ON n.oid=c.relnamespace
 ) SELECT kind,nspname,tab,name FROM objects ORDER BY kind,tab,name`, schema, pq.Array(retirementTables), pq.Array(retirementColumnNames()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetirementObject
	for rows.Next() {
		var o RetirementObject
		if err = rows.Scan(&o.Kind, &o.Schema, &o.Table, &o.Name); err != nil {
			return nil, err
		}
		if o.Schema != schema {
			return nil, fmt.Errorf("cross-schema retirement dependency %s.%s requires separate review", o.Schema, o.Name)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func retirementColumnNames() []string {
	var r []string
	for t, cols := range retirementColumns() {
		for _, c := range cols {
			r = append(r, t+"."+c)
		}
	}
	sort.Strings(r)
	return r
}

// Version-owned deletion inventory: 027 owns only the two identity columns;
// every other exact retirement target is owned by immutable 022. Common admission
// uses pg_catalog so an inspection role need not have business SELECT privileges.
func verifyHistoricalRetirementInventory(ctx context.Context, q migrationQuery, schema string, applied []Migration) error {
	versions := map[string]bool{}
	for _, a := range applied {
		versions[a.Version] = true
	}
	if !versions["022_drop_professional_extension_shared_fields"] && !versions["027_work_item_identity_field_retirement"] {
		return nil
	}

	// Historical deletion is a name/namespace assertion, not permission to
	// reinterpret a reappeared relation of another kind as a new baseline.
	if versions["022_drop_professional_extension_shared_fields"] {
		var reappeared sql.NullString
		err := q.QueryRowContext(ctx, `SELECT min(relname::text) FROM pg_class WHERE relnamespace=$1::regnamespace AND relname=ANY($2::text[])`, schema, pq.Array(retirementTables)).Scan(&reappeared)
		if err != nil {
			return err
		}
		if reappeared.Valid {
			return fmt.Errorf("historical retirement receipt 022_drop_professional_extension_shared_fields contradicts retained structure: relation %s", reappeared.String)
		}
	}
	objects, err := retirementObjects(ctx, q, schema)
	if err != nil {
		return err
	}
	for _, object := range objects {
		version := "022_drop_professional_extension_shared_fields"
		if object.Kind == "column" && ((object.Table == "tickets" && object.Name == "type") || (object.Table == "incidents" && object.Name == "incident_number")) {
			version = "027_work_item_identity_field_retirement"
		}
		if versions[version] {
			return fmt.Errorf("historical retirement receipt %s contradicts retained structure: %s.%s", version, object.Table, object.Name)
		}
	}
	return nil
}
