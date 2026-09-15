package migration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"itsm-backend/common/workitemidentity"

	"github.com/lib/pq"
)

// Retained columns are an explicit evidence registry, never a second write path.
var preparationLegacyColumns = map[string][]string{
	"tickets":   {"type"},
	"incidents": {"title", "description", "status", "priority", "reporter_id", "assignee_id", "category", "subcategory", "source", "tenant_id", "version", "created_at", "updated_at", "resolved_at", "closed_at", "deleted_at", "incident_number"},
	"problems":  {"title", "description", "status", "priority", "category", "assignee_id", "created_by", "tenant_id", "created_at", "updated_at", "resolved_at", "closed_at", "deleted_at"},
	"changes":   {"title", "description", "status", "priority", "assignee_id", "created_by", "tenant_id", "related_tickets", "created_at", "updated_at"},
}

var (
	preparationTables           = []string{"tickets", "incidents", "problems", "changes"}
	preparationHistoricalTables = []string{"ticket_approvals", "workflow_tasks", "workflow_instances", "workflow_versions", "workflows", "audit_logs", "change_status_events", "process_instances", "process_tasks", "process_approval_decisions", "process_callback_outboxes"}
)

// PreparationBaseline contains immutable per-record evidence hashes; public values
// are not duplicated. Subsequent authoritative updates must not rewrite it.
type PreparationBaselineRow struct {
	Columns    []string `json:",omitempty"`
	Table      string
	ID         string
	Digest     string
	WorkItemID string
	TenantID   string
}
type preparationAttachment struct {
	Evidence        MigrationEvidence
	Baseline        []PreparationBaselineRow
	StructureDigest string
	ReviewedGrants  []MigrationRoleGrant
	InspectionRole  string `json:",omitempty"`
}

func preparationRelation(schema, table string) string {
	return pq.QuoteIdentifier(schema) + "." + pq.QuoteIdentifier(table)
}

func preparationBaseline(ctx context.Context, q migrationQuery, schema string) ([]PreparationBaselineRow, error) {
	return preparationBaselineWithScope(ctx, q, schema, nil)
}

func preparationBaselineWithScope(ctx context.Context, q migrationQuery, schema string, scopes map[string][]string) ([]PreparationBaselineRow, error) {
	var result []PreparationBaselineRow
	capture := func(table, expression string) error {
		if cols := scopes[table]; len(cols) > 0 {
			var keys []string
			for _, c := range cols {
				keys = append(keys, pq.QuoteLiteral(c))
			}
			expression = `(SELECT jsonb_object_agg(key,value) FROM jsonb_each(to_jsonb(r)) WHERE key IN (` + strings.Join(keys, ",") + `))`
		}
		identity := `coalesce(to_jsonb(r)->>'work_item_id','')`
		tenant := `coalesce(to_jsonb(r)->>'tenant_id','')`
		if table == "tickets" {
			identity = `r.id::text`
		}
		if table == "incidents" || table == "problems" || table == "changes" {
			tenant = `coalesce((SELECT t.tenant_id::text FROM ` + preparationRelation(schema, "tickets") + ` t WHERE t.id=r.work_item_id),'')`
		}
		rows, err := q.QueryContext(ctx, `SELECT id::text, (`+expression+`)::text,`+identity+`,`+tenant+` FROM `+preparationRelation(schema, table)+` r ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, data, workItemID, tenantID string
			if err = rows.Scan(&id, &data, &workItemID, &tenantID); err != nil {
				return err
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal([]byte(data), &fields); err != nil {
				return err
			}
			var columns []string
			for k := range fields {
				columns = append(columns, k)
			}
			sort.Strings(columns)
			result = append(result, PreparationBaselineRow{Table: table, ID: id, Digest: checksumSQL(data), WorkItemID: workItemID, TenantID: tenantID, Columns: columns})
		}
		return rows.Err()
	}
	for _, table := range preparationTables {
		keys := []string{"'id'", "'work_item_id'"}
		if table == "tickets" {
			keys = append(keys, "'ticket_number'", "'created_at'", "'tenant_id'")
		}
		for _, col := range preparationLegacyColumns[table] {
			keys = append(keys, pq.QuoteLiteral(col))
		}
		expr := `(SELECT coalesce(jsonb_object_agg(key,value),'{}'::jsonb) FROM jsonb_each(to_jsonb(r)) WHERE key IN (` + strings.Join(keys, ",") + `))`
		if err := capture(table, expr); err != nil {
			return nil, err
		}
	}
	for _, table := range preparationHistoricalTables {
		var exists bool
		if err := q.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, preparationRelation(schema, table)).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			if err := capture(table, `to_jsonb(r)`); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// Fingerprint only P-owned structural surfaces; subsequent ordinary migrations
// may add professional fields without invalidating the preparation receipt.
func preparationStructure(ctx context.Context, q migrationQuery, schema string) (string, error) {
	var result string
	err := q.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'constraints',(SELECT coalesce(jsonb_agg(jsonb_build_array(c.relname,k.conname,pg_get_constraintdef(k.oid)) ORDER BY c.relname,k.conname),'[]'::jsonb) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid WHERE c.relnamespace=$1::text::regnamespace AND c.relname IN ('incidents','problems','changes') AND k.contype IN ('f','u','p') AND (k.contype='p' OR EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=k.conrelid AND a.attnum=ANY(k.conkey) AND a.attname IN ('work_item_id','title','description','status','priority','reporter_id','assignee_id','category','subcategory','source','tenant_id','version','created_at','updated_at','resolved_at','closed_at','deleted_at','incident_number','created_by','related_tickets')))),
 'indexes',(SELECT coalesce(jsonb_agg(jsonb_build_array(tablename,indexname,indexdef) ORDER BY indexname),'[]'::jsonb) FROM pg_indexes WHERE schemaname=$1::text AND indexname IN ('incident_work_item_id','problem_work_item_id','change_work_item_id')),
 'policies',(SELECT coalesce(jsonb_agg(jsonb_build_array(c.relname,p.polname,p.polcmd,p.polpermissive,p.polroles,pg_get_expr(p.polqual,p.polrelid),pg_get_expr(p.polwithcheck,p.polrelid)) ORDER BY c.relname,p.polname),'[]'::jsonb) FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid WHERE c.relnamespace=$1::text::regnamespace AND c.relname IN ('tickets','incidents','problems','changes')),
 'rls',(SELECT jsonb_agg(jsonb_build_array(relname,relrowsecurity,relforcerowsecurity) ORDER BY relname) FROM pg_class WHERE relnamespace=$1::text::regnamespace AND relname IN ('tickets','incidents','problems','changes')),
 'grants',(SELECT coalesce(jsonb_agg(jsonb_build_array(c.relname,c.relacl) ORDER BY c.relname),'[]'::jsonb) FROM pg_class c WHERE c.relnamespace=$1::text::regnamespace AND c.relname IN ('tickets','incidents','problems','changes')),
 'ownership',(SELECT jsonb_agg(jsonb_build_array(c.relname,a.attnotnull) ORDER BY c.relname) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=$1::text::regnamespace AND c.relname IN ('incidents','problems','changes') AND a.attname='work_item_id' AND NOT a.attisdropped)
 )::text`, schema).Scan(&result)
	if err != nil {
		return "", err
	}
	indexes, err := preparationIndexes(ctx, q, schema)
	if err != nil {
		return "", err
	}
	return evidenceDigest(struct {
		Structure string
		Indexes   []preparationIndex
	}{result, indexes})
}

func (m *Migrator) InspectPreparation(ctx context.Context) (PreparationInventory, error) {
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return PreparationInventory{}, err
	}
	defer tx.Rollback()
	inv, _, err := m.preparationInventory(ctx, tx)
	return inv, err
}

func (m *Migrator) preparationInventory(ctx context.Context, q migrationQuery) (PreparationInventory, []PreparationBaselineRow, error) {
	var inv PreparationInventory
	target, err := m.preparationTarget(ctx, q)
	if err != nil {
		return inv, nil, err
	}
	inv.Target = target
	applied, err := inspectMigrationTarget(ctx, q, m.controlConfig)
	if err != nil {
		return inv, nil, err
	}
	if _, err = PlanMigrations(ControlledMigrationCatalog(), applied, OpPrepare, nil); err != nil {
		return inv, nil, err
	}
	inv.LedgerDigest, err = evidenceDigest(applied)
	if err != nil {
		return inv, nil, err
	}
	baseline, err := preparationBaseline(ctx, q, target.Schema)
	if err != nil {
		return inv, nil, err
	}
	structure, err := preparationStructure(ctx, q, target.Schema)
	if err != nil {
		return inv, nil, err
	}
	inv.InventoryDigest, err = evidenceDigest(struct {
		Baseline  []PreparationBaselineRow
		Structure string
	}{baseline, structure})
	return inv, baseline, err
}

// ApplyPreparation executes only the canonical P definition under the existing
// migration lock. No historical receipts or business rows are synthesized.
func (m *Migrator) ApplyPreparation(ctx context.Context, e MigrationEvidence) error {
	if strings.TrimSpace(m.controlConfig.Operator) == "" || e.Operator != m.controlConfig.Operator {
		return fmt.Errorf("preparation operator does not match trusted operational identity")
	}
	return m.WithMigrationLock(ctx, func(ctx context.Context) error {
		tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		inv, _, err := m.preparationInventory(ctx, tx)
		if err != nil {
			return err
		}
		if err = validatePreparationEvidence(e, inv); err != nil {
			return err
		}
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, WorkItemPrepareVersion).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("preparation already has a receipt; inspect original evidence")
		}
		// Lock before re-reading retained rows; inventory evidence must match under locks.
		for _, table := range preparationTables {
			if _, err = tx.ExecContext(ctx, `LOCK TABLE `+preparationRelation(inv.Target.Schema, table)+` IN ACCESS EXCLUSIVE MODE`); err != nil {
				return err
			}
		}
		inv2, baseline2, err := m.preparationInventory(ctx, tx)
		if err != nil {
			return err
		}
		if inv2 != inv {
			return fmt.Errorf("preparation inventory changed while locking")
		}
		baseline := baseline2
		started := time.Now()
		if err = validatePreparationShape(ctx, tx, inv.Target.Schema, false, m.controlConfig.ReviewedGrants); err != nil {
			return err
		}
		if err = ensureMigrationLedgerColumns(ctx, tx); err != nil {
			return fmt.Errorf("prepare migration ledger: %w", err)
		}
		if _, err = tx.ExecContext(ctx, workItemPreparationSQL); err != nil {
			return fmt.Errorf("prepare structure: %w", err)
		}
		if err = validatePreparationShape(ctx, tx, inv.Target.Schema, true, m.controlConfig.ReviewedGrants); err != nil {
			return err
		}
		after, err := preparationBaseline(ctx, tx, inv.Target.Schema)
		if err != nil {
			return err
		}
		beforeDigest, _ := evidenceDigest(baseline)
		afterDigest, _ := evidenceDigest(after)
		if beforeDigest != afterDigest {
			return fmt.Errorf("preparation changed retained historical evidence")
		}
		structure, err := preparationStructure(ctx, tx, inv.Target.Schema)
		if err != nil {
			return err
		}
		attachment := preparationAttachment{e, baseline, structure, m.controlConfig.ReviewedGrants, m.controlConfig.InspectionRole}
		data, err := json.Marshal(attachment)
		if err != nil {
			return err
		}
		digest := checksumSQL(string(data))
		// Attachment has no execution status; schema_migrations remains the sole ledger.
		_, err = tx.ExecContext(ctx, `CREATE TABLE `+preparationRelation(inv.Target.Schema, "work_item_migration_evidence")+` (version varchar(255) PRIMARY KEY REFERENCES `+preparationRelation(inv.Target.Schema, "schema_migrations")+`(version), content jsonb NOT NULL, digest text NOT NULL)`)
		if err != nil {
			return err
		}
		if m.controlConfig.InspectionRole != "" {
			if err = validateInspectionRole(ctx, tx, inv.Target.Schema, m.controlConfig.InspectionRole); err != nil {
				return err
			}
			role := pq.QuoteIdentifier(m.controlConfig.InspectionRole)
			if _, err = tx.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+pq.QuoteIdentifier(inv.Target.Schema)+" TO "+role+"; GRANT SELECT ON "+preparationRelation(inv.Target.Schema, "work_item_migration_evidence")+","+preparationRelation(inv.Target.Schema, "schema_migrations")+" TO "+role); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,description,checksum,execution_ms,release_version,catalog_revision,evidence_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, WorkItemPrepareVersion, "Prepare WorkItem structure with controlled evidence", checksumSQL(workItemPreparationSQL), time.Since(started).Milliseconds(), m.releaseVersion, ControlledCatalogRevision, digest)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO `+preparationRelation(inv.Target.Schema, "work_item_migration_evidence")+`(version,content,digest) VALUES($1,$2,$3)`, WorkItemPrepareVersion, string(data), digest)
		if err != nil {
			return err
		}
		if err = verifyPreparationReceipt(ctx, tx, inv.Target.Schema, digest, m.controlConfig, false); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func verifyPreparationReceipt(ctx context.Context, q migrationQuery, schema, digest string, config MigrationControlConfig, structuralOnly bool) error {
	if err := validateEvidenceACL(ctx, q, schema, config.InspectionRole); err != nil {
		return err
	}
	var data []byte
	var stored string
	if err := q.QueryRowContext(ctx, `SELECT content,digest FROM `+preparationRelation(schema, "work_item_migration_evidence")+` WHERE version=$1`, WorkItemPrepareVersion).Scan(&data, &stored); err != nil {
		return fmt.Errorf("preparation attachment missing: %w", err)
	}
	var attachment preparationAttachment
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&attachment); err != nil {
		return err
	}
	canonical, err := json.Marshal(attachment)
	if err != nil {
		return err
	}
	if stored != digest || checksumSQL(string(canonical)) != digest {
		return fmt.Errorf("preparation evidence attachment digest mismatch")
	}
	if attachment.InspectionRole != config.InspectionRole {
		return fmt.Errorf("trusted inspection role differs from original preparation receipt")
	}
	if config.DeploymentID != "" && attachment.Evidence.Target.DeploymentID != config.DeploymentID {
		return fmt.Errorf("preparation deployment identity mismatch")
	}
	var database string
	if err := q.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return err
	}
	if attachment.Evidence.Target.Database != database || attachment.Evidence.Target.Schema != schema {
		return fmt.Errorf("preparation receipt target mismatch")
	}
	structure, err := preparationStructure(ctx, q, schema)
	if err != nil {
		return err
	}
	if structure != attachment.StructureDigest {
		return fmt.Errorf("preparation structure differs from verified receipt")
	}
	return validatePreparationShapeMode(ctx, q, schema, true, attachment.ReviewedGrants, !structuralOnly)
}

func validatePreparationShape(ctx context.Context, q migrationQuery, schema string, prepared bool, grants []MigrationRoleGrant) error {
	return validatePreparationShapeMode(ctx, q, schema, prepared, grants, true)
}

// Structural runtime admission intentionally performs no global business-row reads.
func validatePreparationShapeMode(ctx context.Context, q migrationQuery, schema string, prepared bool, grants []MigrationRoleGrant, validateRows bool) error {
	indexes, err := preparationIndexes(ctx, q, schema)
	if err != nil {
		return err
	}
	if err = validatePreparationIndexes(indexes); err != nil {
		return err
	}

	if err := validatePreparationGrants(ctx, q, schema, grants); err != nil {
		return err
	}
	for _, table := range preparationTables {
		rel := preparationRelation(schema, table)
		var oid string
		if err := q.QueryRowContext(ctx, `SELECT $1::regclass::oid::text`, rel).Scan(&oid); err != nil {
			return fmt.Errorf("required table %s: %w", table, err)
		}
		var bad bool
		if err := validatePreparationTriggers(ctx, q, schema, table, prepared); err != nil {
			return err
		}
		for _, column := range preparationLegacyColumns[table] {
			var nullable bool
			var def sql.NullString
			err := q.QueryRowContext(ctx, `SELECT NOT a.attnotnull,pg_get_expr(d.adbin,d.adrelid) FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=$1::oid AND a.attname=$2 AND NOT a.attisdropped`, oid, column).Scan(&nullable, &def)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			if prepared && (!nullable || def.Valid) {
				return fmt.Errorf("legacy column %s.%s still requires writes/default", table, column)
			}
			if !prepared && def.Valid {
				allowed := map[string]bool{"'new'::text": true, "'open'::text": true, "'medium'::text": true, "'manual'::text": true, "'incident'::text": true, "'general'::text": true, "1": true, "now()": true, "CURRENT_TIMESTAMP": true, "'[]'::jsonb": true, "''::text": true}
				if !allowed[def.String] {
					return fmt.Errorf("unreviewed legacy default %s.%s: %s", table, column, def.String)
				}
			}
			if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=ANY(c.conkey) WHERE c.conrelid=$1::oid AND a.attname=$2 AND c.contype='c')`, oid, column).Scan(&bad); err != nil {
				return err
			}
			if bad {
				return fmt.Errorf("unreviewed legacy CHECK on %s.%s", table, column)
			}
		}
		if table == "tickets" {
			var policyCount int
			if err := q.QueryRowContext(ctx, `SELECT count(*) FROM pg_policy WHERE polrelid=$1::oid`, oid).Scan(&policyCount); err != nil {
				return err
			}
			direct := `(tenant_id = (NULLIF(current_setting('app.current_tenant'::text, true), ''::text))::bigint)`
			err := q.QueryRowContext(ctx, `SELECT c.relrowsecurity AND EXISTS(SELECT 1 FROM pg_policy p WHERE p.polrelid=c.oid AND p.polname='tenant_isolation_tickets' AND p.polcmd='*' AND p.polpermissive AND p.polroles=ARRAY[0]::oid[] AND pg_get_expr(p.polqual,p.polrelid)=$2 AND pg_get_expr(p.polwithcheck,p.polrelid)=$2) FROM pg_class c WHERE c.oid=$1::oid`, oid, direct).Scan(&bad)
			if err != nil {
				return err
			}
			if !bad || policyCount != 1 {
				return fmt.Errorf("unreviewed WorkItem base tenant policy")
			}
			if !prepared {
				err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+rel+` t WHERE to_jsonb(t)->>'type' IS NOT NULL AND to_jsonb(t)->>'type'<>'' AND NOT ((record_class='generic' AND to_jsonb(t)->>'type'=coalesce(generic_subtype,'') AND NOT (to_jsonb(t)->>'type'=ANY($1::text[]))) OR (record_class IN ('incident','problem','catalog_task') AND to_jsonb(t)->>'type'=record_class) OR (record_class='change_request' AND to_jsonb(t)->>'type' IN ('change','change_request')) OR (record_class='service_request_item' AND to_jsonb(t)->>'type' IN ('service_request','service_request_item'))))`, pq.Array(preparationReservedProfessionalIdentities())).Scan(&bad)
				if err != nil {
					return err
				}
				if bad {
					return fmt.Errorf("legacy WorkItem identity conflict")
				}
			}
			continue
		}
		expectedClass := map[string]string{"incidents": "incident", "problems": "problem", "changes": "change_request"}[table]
		if validateRows {
			if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+rel+` e LEFT JOIN `+preparationRelation(schema, "tickets")+` t ON t.id=e.work_item_id WHERE t.id IS NULL OR t.record_class<>$1) OR EXISTS(SELECT 1 FROM `+rel+` GROUP BY work_item_id HAVING count(*)>1)`, expectedClass).Scan(&bad); err != nil {
				return err
			}
			if bad {
				return fmt.Errorf("invalid, orphan, duplicate or wrong-class %s ownership", table)
			}
		}
		if !prepared {
			// Compare only retained non-null fields with defined authoritative mappings.
			pairs := map[string]string{"title": "title", "description": "description", "status": "status", "priority": "priority", "tenant_id": "tenant_id", "assignee_id": "assignee_id", "created_by": "opened_by_id", "reporter_id": "requester_id", "version": "version", "created_at": "created_at", "updated_at": "updated_at", "resolved_at": "resolved_at", "closed_at": "closed_at", "deleted_at": "deleted_at", "incident_number": "ticket_number"}
			for _, column := range preparationLegacyColumns[table] {
				target, ok := pairs[column]
				if !ok {
					continue
				}
				query := `SELECT EXISTS(SELECT 1 FROM ` + rel + ` e JOIN ` + preparationRelation(schema, "tickets") + ` t ON t.id=e.work_item_id WHERE to_jsonb(e)->>$1 IS NOT NULL AND (to_jsonb(e)->>$1) IS DISTINCT FROM (to_jsonb(t)->>$2))`
				if err := q.QueryRowContext(ctx, query, column, target).Scan(&bad); err != nil {
					return err
				}
				if bad {
					return fmt.Errorf("retained public value conflict %s.%s", table, column)
				}
			}
		}
		var count int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM pg_policy WHERE polrelid=$1::oid`, oid).Scan(&count); err != nil {
			return err
		}
		if count > 1 {
			return fmt.Errorf("unknown additional policy on %s", table)
		}
		rows, err := q.QueryContext(ctx, `SELECT polname,polcmd::text,polpermissive,polroles::text,pg_get_expr(polqual,polrelid),pg_get_expr(polwithcheck,polrelid) FROM pg_policy WHERE polrelid=$1::oid`, oid)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name, cmd, roles string
			var permissive bool
			var using, check sql.NullString
			if err = rows.Scan(&name, &cmd, &permissive, &roles, &using, &check); err != nil {
				rows.Close()
				return err
			}
			if (name != "tenant_isolation" && name != "tenant_isolation_"+table) || cmd != "*" || !permissive || roles != "{0}" {
				rows.Close()
				return fmt.Errorf("unknown policy definition on %s", table)
			}
			if !prepared {
				old := `(tenant_id = (NULLIF(current_setting('app.current_tenant'::text, true), ''::text))::bigint)`
				canonicalPolicy := func(alias string) string {
					return fmt.Sprintf("(EXISTS ( SELECT 1 FROM tickets %s WHERE ((%s.id = %s.work_item_id) AND (%s.tenant_id = (NULLIF(current_setting('app.current_tenant'::text, true), ''::text))::bigint) AND (%s.deleted_at IS NULL))))", alias, alias, table, alias, alias)
				}
				compact := func(x string) string { return strings.Join(strings.Fields(x), "") }
				known := func(x string) bool {
					return x == old || compact(x) == compact(canonicalPolicy("w")) || compact(x) == compact(canonicalPolicy("work_item"))
				}
				if !known(using.String) || !known(check.String) {
					rows.Close()
					return fmt.Errorf("unreviewed prior policy expression on %s", table)
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		var constraints int
		legacy := append([]string{"work_item_id"}, preparationLegacyColumns[table]...)
		err = q.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint c WHERE c.conrelid=$1::oid AND c.contype<>'p' AND c.conname<>$2 AND EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=c.conrelid AND a.attnum=ANY(c.conkey) AND a.attname=ANY($3))`, oid, table+"_tickets_work_item", pq.Array(legacy)).Scan(&constraints)
		if err != nil {
			return err
		}
		if constraints > 0 {
			return fmt.Errorf("unreviewed constraint on retained or ownership columns in %s", table)
		}
		fk := table + "_tickets_work_item"
		if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid=$1::oid AND (c.conname=$2 OR (c.contype='f' AND (SELECT attnum FROM pg_attribute WHERE attrelid=$1::oid AND attname='work_item_id')=ANY(c.conkey))) AND NOT (c.conname=$2 AND c.contype='f' AND c.convalidated AND NOT c.condeferrable AND c.confdeltype='a' AND c.confupdtype='a' AND c.confrelid=$3::regclass AND c.conkey=ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid=$1::oid AND attname='work_item_id')]::smallint[] AND c.confkey=ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid=$3::regclass AND attname='id')]::smallint[]))`, oid, fk, preparationRelation(schema, "tickets")).Scan(&bad); err != nil {
			return err
		}
		if bad {
			return fmt.Errorf("unknown or conflicting ownership constraint %s", fk)
		}
		index := strings.TrimSuffix(table, "s") + "_work_item_id"
		if table == "changes" {
			index = "change_work_item_id"
		}
		if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c LEFT JOIN pg_index i ON i.indexrelid=c.oid WHERE c.relnamespace=$1::text::regnamespace AND c.relname=$2 AND NOT(coalesce(i.indrelid=$3::oid AND i.indisunique AND i.indisvalid AND i.indisready AND i.indnkeyatts=1 AND i.indnatts=1 AND i.indpred IS NULL AND i.indexprs IS NULL AND i.indkey[0]=(SELECT attnum FROM pg_attribute WHERE attrelid=$3::oid AND attname='work_item_id'),false)))`, schema, index, oid).Scan(&bad); err != nil {
			return err
		}
		if bad {
			return fmt.Errorf("unknown or conflicting ownership index %s", index)
		}
		if prepared {
			if err = q.QueryRowContext(ctx, `SELECT (SELECT attnotnull FROM pg_attribute WHERE attrelid=$1::oid AND attname='work_item_id') AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=$1::oid AND conname=$2) AND EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=$3::regnamespace AND relname=$4) AND (SELECT relrowsecurity FROM pg_class WHERE oid=$1::oid)`, oid, fk, schema, index).Scan(&bad); err != nil {
				return err
			}
			if !bad || count != 1 {
				return fmt.Errorf("preparation postcondition missing on %s", table)
			}
		}
	}
	return nil
}

// Canonical SQL is independently registered; historical 022/027 stay unchanged.
// Only the explicit retained-column registry is relaxed, never dropped/backfilled.
var workItemPreparationSQL = buildWorkItemPreparationSQL()

func buildWorkItemPreparationSQL() string {
	var out strings.Builder
	out.WriteString("DO $prepare$ BEGIN\n")
	for _, table := range preparationTables {
		for _, column := range preparationLegacyColumns[table] {
			fmt.Fprintf(&out, "IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=%s AND column_name=%s) THEN EXECUTE format('ALTER TABLE %%I.%%I ALTER COLUMN %%I DROP NOT NULL, ALTER COLUMN %%I DROP DEFAULT',current_schema(),%s,%s,%s); END IF;\n", pq.QuoteLiteral(table), pq.QuoteLiteral(column), pq.QuoteLiteral(table), pq.QuoteLiteral(column), pq.QuoteLiteral(column))
		}
		if table == "tickets" {
			continue
		}
		fk := table + "_tickets_work_item"
		index := strings.TrimSuffix(table, "s") + "_work_item_id"
		fmt.Fprintf(&out, "IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=format('%%I.%%I',current_schema(),%s)::regclass AND conname=%s) THEN EXECUTE format('ALTER TABLE %%I.%%I ADD CONSTRAINT %%I FOREIGN KEY(work_item_id) REFERENCES %%I.tickets(id)',current_schema(),%s,%s,current_schema()); END IF;\n", pq.QuoteLiteral(table), pq.QuoteLiteral(fk), pq.QuoteLiteral(table), pq.QuoteLiteral(fk))
		fmt.Fprintf(&out, "EXECUTE format('ALTER TABLE %%I.%%I ALTER COLUMN work_item_id SET NOT NULL',current_schema(),%s);\nEXECUTE format('CREATE UNIQUE INDEX IF NOT EXISTS %%I ON %%I.%%I(work_item_id)',%s,current_schema(),%s);\n", pq.QuoteLiteral(table), pq.QuoteLiteral(index), pq.QuoteLiteral(table))
		for _, policy := range []string{"tenant_isolation", "tenant_isolation_" + table} {
			fmt.Fprintf(&out, "EXECUTE format('DROP POLICY IF EXISTS %%I ON %%I.%%I',%s,current_schema(),%s);\n", pq.QuoteLiteral(policy), pq.QuoteLiteral(table))
		}
		fmt.Fprintf(&out, `EXECUTE format('CREATE POLICY %%I ON %%I.%%I AS PERMISSIVE FOR ALL TO PUBLIC USING (EXISTS (SELECT 1 FROM %%I.tickets w WHERE w.id=%%I.work_item_id AND w.tenant_id=NULLIF(current_setting(''app.current_tenant'',true),'''')::bigint AND w.deleted_at IS NULL)) WITH CHECK (EXISTS (SELECT 1 FROM %%I.tickets w WHERE w.id=%%I.work_item_id AND w.tenant_id=NULLIF(current_setting(''app.current_tenant'',true),'''')::bigint AND w.deleted_at IS NULL))',%s,current_schema(),%s,current_schema(),%s,current_schema(),%s);
EXECUTE format('ALTER TABLE %%I.%%I ENABLE ROW LEVEL SECURITY',current_schema(),%s);
`, pq.QuoteLiteral("tenant_isolation_"+table), pq.QuoteLiteral(table), pq.QuoteLiteral(table), pq.QuoteLiteral(table), pq.QuoteLiteral(table))
	}
	out.WriteString("END $prepare$;")
	return out.String()
}

// validatePreparationGrants accepts exact reviewed privileges only. A role-name
// allowlist alone would accidentally admit TRUNCATE, grant options or bypasses.
func validatePreparationGrants(ctx context.Context, q migrationQuery, schema string, grants []MigrationRoleGrant) error {
	expected := map[string]bool{}
	for _, g := range grants {
		if g.Role == "" || g.Role == "PUBLIC" || g.Role == "public" {
			return fmt.Errorf("invalid reviewed business role")
		}
		validTable := false
		for _, table := range preparationTables {
			validTable = validTable || g.Table == table
		}
		if !validTable {
			return fmt.Errorf("reviewed grant outside preparation tables")
		}
		var unsafe bool
		err := q.QueryRowContext(ctx, `SELECT r.rolsuper OR r.rolbypassrls OR EXISTS(SELECT 1 FROM pg_class c WHERE c.relnamespace=$2::regnamespace AND c.relname IN ('tickets','incidents','problems','changes') AND pg_has_role(r.oid,c.relowner,'MEMBER')) OR EXISTS(SELECT 1 FROM pg_roles elevated WHERE (elevated.rolsuper OR elevated.rolbypassrls) AND pg_has_role(r.oid,elevated.oid,'MEMBER')) FROM pg_roles r WHERE r.rolname=$1`, g.Role, schema).Scan(&unsafe)
		if err != nil {
			return err
		}
		if unsafe {
			return fmt.Errorf("reviewed role is owner, superuser or bypass-capable")
		}
		for _, priv := range g.Privileges {
			switch priv {
			case "SELECT", "INSERT", "UPDATE", "DELETE":
			default:
				return fmt.Errorf("unreviewed privilege %s", priv)
			}
			key := g.Table + "\x00" + g.Role + "\x00" + priv
			if expected[key] {
				return fmt.Errorf("duplicate reviewed grant")
			}
			expected[key] = true
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT c.relname,coalesce(r.rolname,'PUBLIC'),a.privilege_type,a.is_grantable FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a LEFT JOIN pg_roles r ON r.oid=a.grantee WHERE c.relnamespace=$1::regnamespace AND c.relname IN ('tickets','incidents','problems','changes') AND a.grantee<>c.relowner`, schema)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var table, role, priv string
		var grantable bool
		if err = rows.Scan(&table, &role, &priv, &grantable); err != nil {
			return err
		}
		key := table + "\x00" + role + "\x00" + priv
		if !expected[key] || grantable {
			return fmt.Errorf("unknown grant on %s to %s (%s)", table, role, priv)
		}
		delete(expected, key)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(expected) > 0 {
		return fmt.Errorf("required reviewed grant missing")
	}
	var columnGrant bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=$1::regnamespace AND c.relname IN ('tickets','incidents','problems','changes') AND a.attacl IS NOT NULL)`, schema).Scan(&columnGrant)
	if err != nil {
		return err
	}
	if columnGrant {
		return fmt.Errorf("unreviewed column-level grant")
	}
	return nil
}

// preparationIndex records enforcing behavior independently of pg_constraint:
// standalone unique indexes have no constraint row. Dependencies include both
// index keys and expression/predicate references in pg_depend.
type preparationIndex struct {
	Table      string
	Name       string
	Definition string
	Enforcing  bool
	Valid      bool
	Ready      bool
}

func preparationIndexes(ctx context.Context, q migrationQuery, schema string) ([]preparationIndex, error) {
	var indexes []preparationIndex
	for _, table := range preparationTables {
		columns := append([]string{"work_item_id"}, preparationLegacyColumns[table]...)
		rows, err := q.QueryContext(ctx, `SELECT idx.relname,pg_get_indexdef(i.indexrelid),i.indisunique OR i.indisexclusion,i.indisvalid,i.indisready
 FROM pg_index i JOIN pg_class idx ON idx.oid=i.indexrelid
 WHERE i.indrelid=$1::regclass AND (
  EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=i.indrelid AND a.attname=ANY($2) AND NOT a.attisdropped AND (
   a.attnum=ANY(i.indkey::smallint[]) OR EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=i.indexrelid AND d.refclassid='pg_class'::regclass AND d.refobjid=i.indrelid AND d.refobjsubid=a.attnum)))
  OR ((i.indisunique OR i.indisexclusion) AND (i.indexprs IS NOT NULL OR i.indpred IS NOT NULL))
 ) ORDER BY idx.relname`, preparationRelation(schema, table), pq.Array(columns))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			index := preparationIndex{Table: table}
			if err = rows.Scan(&index.Name, &index.Definition, &index.Enforcing, &index.Valid, &index.Ready); err != nil {
				rows.Close()
				return nil, err
			}
			indexes = append(indexes, index)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return indexes, nil
}

func validatePreparationIndexes(indexes []preparationIndex) error {
	canonical := map[string]string{"incidents": "incident_work_item_id", "problems": "problem_work_item_id", "changes": "change_work_item_id"}
	for _, index := range indexes {
		// The existing exact canonical-index validator checks its definition. Every
		// other enforcing index touching retained fields requires separate review.
		// Expression/partial enforcement is conservative even for constant/whole-row
		// expressions whose column dependencies cannot establish null-write safety.
		if index.Enforcing && index.Name != canonical[index.Table] {
			return fmt.Errorf("unreviewed enforcing index %s.%s", index.Table, index.Name)
		}
	}
	return nil
}

// Canonical classes plus only the two historical aliases asserted by immutable 027.
func preparationReservedProfessionalIdentities() []string {
	result := []string{"change", "service_request"}
	for _, class := range workitemidentity.RecordClasses() {
		if class != workitemidentity.RecordClassGeneric {
			result = append(result, class)
		}
	}
	return result
}
