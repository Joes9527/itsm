//go:build integration_postgres

package integration

import (
	"fmt"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/migration"
)

func TestPostgresEmailAttachmentSourceMigration(t *testing.T) {
	const version = "025_email_attachment_source_identity"
	apply := migration.GetMigrationSQL(version)
	require.NotEmpty(t, apply, "attachment source identity needs a registered upgrade")
	f := newIncidentEffectsFixture(t)
	malformed := f.client.TicketAttachment.Create().SetTicketID(f.inc.WorkItemID).SetTenantID(f.tenant.ID).SetUploadedBy(f.actor.ID).SetFileName("old.txt").SetFilePath("old").SetFileSize(1).SetFileType("text/plain").SetSourceKey("invalid").SaveX(f.ctx)
	_, err := f.db.ExecContext(f.ctx, apply)
	require.ErrorContains(t, err, fmt.Sprintf("IDs %d", malformed.ID))
	f.client.TicketAttachment.DeleteOneID(malformed.ID).ExecX(f.ctx)
	asset, err := os.ReadFile("../../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(apply), strings.TrimSpace(string(asset)))
	verify, err := os.ReadFile("../../migrations/" + version + "_verify.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = f.db.ExecContext(f.ctx, apply)
		require.NoError(t, err)
	}
	require.NoError(t, f.client.Schema.Create(f.ctx))
	_, err = f.db.ExecContext(f.ctx, string(verify))
	require.NoError(t, err)
	attachment := f.client.TicketAttachment.Create().SetTicketID(f.inc.WorkItemID).SetTenantID(f.tenant.ID).SetUploadedBy(f.actor.ID).SetFileName("evidence.txt").SetFilePath("tickets/source/one").SetFileSize(8).SetFileType("text/plain").SetSourceKey(strings.Repeat("a", 64)).SaveX(f.ctx)
	_, err = f.client.TicketAttachment.Create().SetTicketID(f.inc.WorkItemID).SetTenantID(f.tenant.ID).SetUploadedBy(f.actor.ID).SetFileName("other.txt").SetFilePath("tickets/source/two").SetFileSize(8).SetFileType("text/plain").SetSourceKey(strings.Repeat("a", 64)).Save(f.ctx)
	require.Error(t, err, "same source cannot create a second attachment")
	_, err = f.db.ExecContext(f.ctx, "UPDATE ticket_attachments SET source_key=$1 WHERE id=$2", strings.Repeat("b", 64), attachment.ID)
	require.ErrorContains(t, err, "immutable")
	_, err = f.db.ExecContext(f.ctx, "UPDATE ticket_attachments SET tenant_id=tenant_id+1 WHERE id=$1", attachment.ID)
	require.ErrorContains(t, err, "immutable")
	_, err = f.client.TicketAttachment.Create().SetTicketID(f.inc.WorkItemID).SetTenantID(f.tenant.ID + 1).SetUploadedBy(f.actor.ID).SetFileName("foreign.txt").SetFilePath("tickets/source/foreign").SetFileSize(8).SetFileType("text/plain").SetSourceKey(strings.Repeat("c", 64)).Save(f.ctx)
	require.ErrorContains(t, err, "tenant")
	reset, err := os.ReadFile("../../migrations/" + version + "_dev_reset.sql")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, string(reset))
	require.ErrorContains(t, err, "requires empty")
	drv, _ := runtimeRLSDriver(t, f)
	runtimeClient := ent.NewClient(ent.Driver(drv))
	scoped := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	require.Equal(t, 1, runtimeClient.TicketAttachment.Query().CountX(scoped))
	require.Zero(t, runtimeClient.TicketAttachment.Query().CountX(tenantctx.WithTenantID(f.ctx, f.tenant.ID+1)))
	_, err = runtimeClient.TicketAttachment.Query().Count(f.ctx)
	require.Error(t, err)
	f.client.TicketAttachment.DeleteOneID(attachment.ID).ExecX(f.ctx)
	_, err = f.db.ExecContext(f.ctx, string(reset))
	require.NoError(t, err)
	require.NoError(t, f.client.Schema.Create(f.ctx))
	_, err = f.db.ExecContext(f.ctx, apply)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, string(verify))
	require.NoError(t, err)

}
