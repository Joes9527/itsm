package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
)

// DirectorySnapshot opens a restricted directory view of the caller's business
// snapshot. The returned close must succeed before any receipt may be claimed.
type DirectorySnapshot interface {
	Open(context.Context, *ent.Tx, int) (*ent.Client, func() error, error)
}

type directorySnapshot struct{ system *ent.Client }

// IntakeDirectorySnapshot is available only on the validated RuntimeClients
// composition: both pools are built from the same database and schema config.
func (c *RuntimeClients) IntakeDirectorySnapshot() DirectorySnapshot {
	if c == nil || c.Tenant == nil || c.System == nil {
		return nil
	}
	return c.intakeDirectory
}

var snapshotPattern = regexp.MustCompile(`^[0-9A-Fa-f]+-[0-9A-Fa-f]+-[0-9]+$`)

func (d *directorySnapshot) Open(ctx context.Context, target *ent.Tx, tenantID int) (*ent.Client, func() error, error) {
	scope, ok := tenantctx.TenantID(ctx)
	if !ok || scope != tenantID || tenantctx.IsSystemBypass(ctx) || target == nil || d == nil || d.system == nil {
		return nil, nil, fmt.Errorf("intake directory snapshot requires the authenticated target transaction")
	}
	rows, err := target.QueryContext(ctx, "SELECT pg_export_snapshot()")
	if err != nil {
		return nil, nil, fmt.Errorf("export intake snapshot: %w", err)
	}
	var snapshot string
	if !rows.Next() {
		err = errors.Join(rows.Err(), errors.New("snapshot export returned no row"))
	} else {
		err = rows.Scan(&snapshot)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, nil, fmt.Errorf("read intake snapshot: %w", err)
	}
	if !snapshotPattern.MatchString(snapshot) {
		return nil, nil, errors.New("invalid database snapshot identifier")
	}
	lookup := tenantctx.SystemContext(ctx, "intake:directory-snapshot", "read native identity at business snapshot")
	directory, err := d.system.BeginTx(lookup, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin directory snapshot: %w", err)
	}
	// Validated server-only identifier; this SET form does not accept parameters.
	if _, err = directory.ExecContext(lookup, "SET TRANSACTION SNAPSHOT '"+snapshot+"'"); err != nil {
		return nil, nil, errors.Join(fmt.Errorf("import intake snapshot: %w", err), directory.Rollback())
	}
	return directory.Client(), directory.Rollback, nil
}
