//go:build integration_postgres

package integration

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/migration"
	"testing"
	"time"
)

const retirementVersion = "027_work_item_identity_field_retirement"

func TestPostgresIdentityRetirementRejectsConflictsAndPreservesOwner(t *testing.T) {
	for _, tc := range []struct{ name, statement, diagnostic string }{
		{"type", "UPDATE tickets SET type='problem'", "WorkItem IDs"},
		{"number", "UPDATE incidents SET incident_number='CONFLICT'", "Incident IDs"},
		{"class", "UPDATE tickets SET record_class='generic'", "Incident IDs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			_, err := f.db.ExecContext(f.ctx, `ALTER TABLE tickets ADD COLUMN type text; ALTER TABLE incidents ADD COLUMN incident_number text; UPDATE incidents SET incident_number=(SELECT ticket_number FROM tickets WHERE tickets.id=incidents.work_item_id)`)
			require.NoError(t, err)
			_, err = f.db.ExecContext(f.ctx, tc.statement)
			require.NoError(t, err)
			_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL(retirementVersion))
			require.ErrorContains(t, err, tc.diagnostic)
			var count int
			require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='incidents' AND column_name='incident_number'`).Scan(&count))
			require.Equal(t, 1, count, "failure must retain legacy evidence")
		})
	}
}

func TestPostgresIdentityRetirementApplyReapplyAndEntBootstrap(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	item := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	_, err := f.db.ExecContext(f.ctx, `ALTER TABLE tickets ADD COLUMN type text; ALTER TABLE incidents ADD COLUMN incident_number text; UPDATE tickets SET type='incident'; UPDATE incidents SET incident_number=(SELECT ticket_number FROM tickets WHERE tickets.id=incidents.work_item_id)`)
	require.NoError(t, err)
	generic := f.client.Ticket.Create().SetTitle("generic").SetRecordClass("generic").SetGenericSubtype("improvement").SetTicketNumber("GENERIC-027").SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SaveX(f.ctx)
	_, err = f.db.ExecContext(f.ctx, `UPDATE tickets SET type='improvement' WHERE id=$1`, generic.ID)
	require.NoError(t, err)
	for attempt := 0; attempt < 2; attempt++ {
		_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL(retirementVersion))
		require.NoError(t, err)
		require.NoError(t, f.client.Schema.Create(f.ctx))
		_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL(retirementVersion))
		require.NoError(t, err)
		require.Equal(t, item.TicketNumber, f.client.Ticket.GetX(f.ctx, item.ID).TicketNumber)
		require.Equal(t, "improvement", f.client.Ticket.GetX(f.ctx, generic.ID).GenericSubtype)
	}
	_, err = f.db.ExecContext(f.ctx, `INSERT INTO incidents (work_item_id) VALUES ($1)`, item.ID)
	require.Error(t, err)
	_, err = f.db.ExecContext(f.ctx, migration.GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.NoError(t, err)
	role := fmt.Sprintf("identity027_%d", time.Now().UnixNano())
	var schema string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&schema))
	_, err = f.db.ExecContext(f.ctx, "CREATE ROLE "+role+" NOLOGIN; GRANT USAGE ON SCHEMA "+schema+" TO "+role+"; GRANT SELECT ON tickets, incidents TO "+role)
	require.NoError(t, err)
	defer func() {
		_, err := f.db.ExecContext(f.ctx, "DROP OWNED BY "+role+"; DROP ROLE "+role)
		require.NoError(t, err)
	}()
	for _, tenant := range []int{f.tenant.ID, f.tenant.ID + 1, 0} {
		tx, err := f.db.BeginTx(f.ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(f.ctx, "SET LOCAL ROLE "+role)
		require.NoError(t, err)
		guc := ""
		if tenant > 0 {
			guc = fmt.Sprint(tenant)
		}
		_, err = tx.ExecContext(f.ctx, "SELECT set_config('app.current_tenant',$1,true)", guc)
		require.NoError(t, err)
		var visible int
		require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT count(*) FROM incidents").Scan(&visible))
		if tenant == f.tenant.ID {
			require.Equal(t, 1, visible)
		} else {
			require.Zero(t, visible)
		}
		require.NoError(t, tx.Rollback())
	}
	t.Logf("027 preserved WorkItem %d and Incident %d; reapply and Ent bootstrap verified", item.ID, f.inc.ID)
}
