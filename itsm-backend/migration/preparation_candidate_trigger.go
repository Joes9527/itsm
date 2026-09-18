package migration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// P remains strict about historical triggers. A later canonical migration may
// add its own trigger only when both its receipt and exact installed definition
// match. This does not adopt an arbitrary same-named trigger during initial P.
func validatePreparationTriggers(ctx context.Context, q migrationQuery, schema, table string, prepared bool) error {
	registered := false
	if prepared && schema == "public" && table == "tickets" {
		var checksum string
		err := q.QueryRowContext(ctx, `SELECT checksum FROM `+preparationRelation(schema, "schema_migrations")+` WHERE version=$1`, CandidateExecutionScopeVersion).Scan(&checksum)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			if checksum != checksumSQL(GetMigrationSQL(CandidateExecutionScopeVersion)) {
				return fmt.Errorf("candidate trigger migration checksum mismatch")
			}
			registered = true
		}
	}
	if !registered {
		var exists bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=$1::regclass AND NOT tgisinternal)`, preparationRelation(schema, table)).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("unreviewed trigger on %s", table)
		}
		return nil
	}
	// Derive the expected body from the authoritative migration SQL, rather than
	// maintain a second function implementation or accept only a trigger name.
	_, definition, found := strings.Cut(GetMigrationSQL(CandidateExecutionScopeVersion), "CREATE FUNCTION public.register_new_execution_member() RETURNS trigger")
	if !found {
		return fmt.Errorf("candidate enrollment function missing from migration")
	}
	_, body, found := strings.Cut(definition, "AS $$")
	if !found {
		return fmt.Errorf("candidate enrollment body missing from migration")
	}
	body, _, found = strings.Cut(body, "$$;")
	if !found {
		return fmt.Errorf("candidate enrollment body unterminated")
	}
	var valid bool
	err := q.QueryRowContext(ctx, `SELECT count(*)=1 AND COALESCE(bool_and(
 t.tgname='register_new_execution_member' AND t.tgtype=5 AND t.tgenabled='O'
 AND t.tgnargs=0 AND t.tgqual IS NULL AND t.tgconstraint=0 AND t.tgattr=''::int2vector
 AND p.proname='register_new_execution_member' AND p.pronamespace=c.relnamespace
 AND p.proowner=c.relowner AND p.prosecdef AND p.prokind='f' AND p.pronargs=0
 AND p.prorettype='pg_catalog.trigger'::regtype AND l.lanname='plpgsql'
 AND p.proconfig=ARRAY['search_path=pg_catalog']::text[] AND p.prosrc=$2
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(p.proacl,pg_catalog.acldefault('f',p.proowner))) a WHERE a.grantee<>p.proowner AND a.privilege_type='EXECUTE')
 ),false)
 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid
 JOIN pg_catalog.pg_proc p ON p.oid=t.tgfoid JOIN pg_catalog.pg_language l ON l.oid=p.prolang
 WHERE t.tgrelid=$1::regclass AND NOT t.tgisinternal`, preparationRelation(schema, table), body).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("unreviewed or missing registered trigger on %s", table)
	}
	return nil
}
