package rls

import (
	"context"
	"database/sql"
	"fmt"

	entsql "entgo.io/ent/dialect/sql"
)

// rawConnection preserves Ent's public result contract without invoking its
// session-variable processor on *sql.Conn, which that processor does not support.
// Variables are applied first through a real *sql.Tx by prepareVariables.
type rawConnection struct{ entsql.ExecQuerier }

func (c rawConnection) Exec(ctx context.Context, q string, args, v any) error {
	values, ok := args.([]any)
	if !ok {
		return fmt.Errorf("rls: unsupported SQL argument type %T", args)
	}
	if v != nil {
		if _, ok := v.(*sql.Result); !ok {
			return fmt.Errorf("rls: unsupported SQL result type %T", v)
		}
	}
	result, err := c.ExecContext(ctx, q, values...)
	if err != nil {
		return err
	}
	if destination, ok := v.(*sql.Result); ok {
		*destination = result
	}
	return nil
}
func (c rawConnection) Query(ctx context.Context, q string, args, v any) error {
	values, ok := args.([]any)
	if !ok {
		return fmt.Errorf("rls: unsupported SQL argument type %T", args)
	}
	destination, ok := v.(*entsql.Rows)
	if !ok {
		return fmt.Errorf("rls: unsupported SQL rows type %T", v)
	}
	rows, err := c.QueryContext(ctx, q, values...)
	if err != nil {
		return err
	}
	*destination = entsql.Rows{ColumnScanner: rows}
	return nil
}

// Ent's public context API owns variable decoding/application. Running its
// processor on a supported SQL transaction preserves that API without private
// key inspection. The business statement remains outside this setup transaction
// for ordinary autocommit operations. Tenant and role checks follow setup.
func prepareVariables(ctx context.Context, tx entsql.ExecQuerier, scoped scope) error {
	for _, key := range []string{"app.current_tenant", "role", "session_authorization", "row_security"} {
		if _, ok := entsql.VarFromContext(ctx, key); ok {
			return fmt.Errorf("rls: reserved session variable %s cannot be supplied", key)
		}
	}
	if err := (entsql.Conn{ExecQuerier: tx}).Exec(ctx, "SELECT 1", []any{}, nil); err != nil {
		return fmt.Errorf("rls: apply Ent session variables: %w", err)
	}
	raw := rawConnection{tx}
	if err := requireTenantRole(ctx, raw); err != nil {
		return err
	}
	return raw.Exec(ctx, "SELECT set_config('app.current_tenant', $1, false)", []any{fmt.Sprint(scoped.tenant)}, nil)
}
