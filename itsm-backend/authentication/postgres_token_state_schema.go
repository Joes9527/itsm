package authentication

import (
	"context"
	"database/sql"
	"errors"
)

// These catalog assertions are evaluated in each state transaction. A missing
// or changed policy must be an unavailable authority, never an empty result.
func (s *postgresTokenStateStore) readyTx(ctx context.Context, tx *sql.Tx) error {
	var roleSafe bool
	err := tx.QueryRowContext(ctx, `SELECT
 current_user=session_user AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb
 AND NOT has_schema_privilege(current_user,'public','CREATE')
 AND NOT has_database_privilege(current_user,current_database(),'CREATE')
 AND current_setting('synchronous_commit')='on' AND current_setting('row_security')='on'
 AND NOT EXISTS(SELECT 1 FROM pg_roles elevated WHERE (elevated.rolsuper OR elevated.rolbypassrls OR elevated.rolcreaterole) AND pg_has_role(current_user,elevated.oid,'MEMBER'))
 FROM pg_roles r WHERE r.rolname=current_user`).Scan(&roleSafe)
	if err != nil {
		return err
	}
	if !roleSafe {
		return errors.New("authentication runtime role is not restricted")
	}
	var tableSafe bool
	err = tx.QueryRowContext(ctx, `SELECT
 a.relkind='r' AND t.relkind='r' AND NOT a.relrowsecurity AND t.relrowsecurity AND t.relforcerowsecurity
 AND NOT pg_has_role(current_user,a.relowner,'MEMBER') AND NOT pg_has_role(current_user,t.relowner,'MEMBER')
 AND has_table_privilege(current_user,a.oid,'SELECT') AND NOT has_table_privilege(current_user,a.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 AND NOT has_any_column_privilege(current_user,a.oid,'INSERT,UPDATE,REFERENCES')
 AND has_table_privilege(current_user,t.oid,'SELECT') AND has_table_privilege(current_user,t.oid,'INSERT')
 AND NOT has_table_privilege(current_user,t.oid,'UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 AND NOT has_any_column_privilege(current_user,t.oid,'UPDATE,REFERENCES')
 FROM pg_class a,pg_class t WHERE a.oid='public.auth_state_authorities'::regclass AND t.oid='public.auth_token_states'::regclass`).Scan(&tableSafe)
	if err != nil {
		return err
	}
	if !tableSafe {
		return errors.New("authentication state table permissions are not restricted")
	}
	var columns int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM
 (VALUES ('authority_id','uuid'::regtype),('purpose','text'::regtype),('token_digest','text'::regtype),('tenant_id','bigint'::regtype),('actor_id','bigint'::regtype),('expires_at','timestamptz'::regtype),('recorded_at','timestamptz'::regtype)) expected(name,kind)
 JOIN pg_attribute a ON a.attrelid='public.auth_token_states'::regclass AND a.attname=expected.name AND a.atttypid=expected.kind AND a.attnotnull AND NOT a.attisdropped`).Scan(&columns)
	if err != nil {
		return err
	}
	if columns != 7 {
		return errors.New("authentication state columns do not match the supported schema")
	}
	var keySafe bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid='public.auth_token_states'::regclass AND c.contype='p' AND NOT c.condeferrable AND c.convalidated AND pg_get_constraintdef(c.oid)='PRIMARY KEY (authority_id, purpose, token_digest)')
 AND EXISTS(SELECT 1 FROM pg_constraint c WHERE c.conrelid='public.auth_token_states'::regclass AND c.contype='f' AND c.confrelid='public.auth_state_authorities'::regclass AND NOT c.condeferrable AND c.convalidated AND pg_get_constraintdef(c.oid)='FOREIGN KEY (authority_id) REFERENCES auth_state_authorities(authority_id)')`).Scan(&keySafe)
	if err != nil {
		return err
	}
	if !keySafe {
		return errors.New("authentication state keys do not match the supported schema")
	}
	var policies int
	err = tx.QueryRowContext(ctx, "SELECT count(*) FROM pg_policy WHERE polrelid='public.auth_token_states'::regclass").Scan(&policies)
	if err != nil {
		return err
	}
	if policies != 1 {
		return errors.New("authentication state tenant policy count mismatch")
	}
	var name, command, using, check, roles string
	var permissive bool
	err = tx.QueryRowContext(ctx, `SELECT polname,polcmd::text,polpermissive,polroles::text,pg_get_expr(polqual,polrelid),pg_get_expr(polwithcheck,polrelid) FROM pg_policy WHERE polrelid='public.auth_token_states'::regclass`).Scan(&name, &command, &permissive, &roles, &using, &check)
	if err != nil {
		return err
	}
	const expected = "(tenant_id = (NULLIF(current_setting('app.current_tenant'::text, true), ''::text))::bigint)"
	if name != "auth_token_state_tenant" || command != "*" || !permissive || roles != "{0}" || using != expected || check != expected {
		return errors.New("authentication state tenant policy definition mismatch")
	}
	var version int
	err = tx.QueryRowContext(ctx, "SELECT schema_version FROM public.auth_state_authorities WHERE authority_id=$1", s.authorityID).Scan(&version)
	if err != nil {
		return err
	}
	if version != 1 {
		return errors.New("unsupported authentication authority version")
	}
	return nil
}
