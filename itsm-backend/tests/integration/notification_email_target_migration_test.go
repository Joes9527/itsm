//go:build candidate_scope

package integration

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"itsm-backend/migration"
)

func verifyNotificationEmailTargetMigration(t *testing.T, ctx context.Context, db *sql.DB, tenantID, ticketID, userID int, runtimeRole string) {
	t.Helper()
	migrationSQL := migration.GetMigrationSQL("045_notification_email_target")
	require.NotEmpty(t, migrationSQL, "email target protocol requires registered migration 045")
	_, err := db.ExecContext(ctx, `ALTER TABLE public.ticket_notifications DROP COLUMN IF EXISTS target_transport`)
	require.NoError(t, err)
	var legacyID int
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO public.ticket_notifications(tenant_id,ticket_id,user_id,type,channel,content,status,created_at,next_attempt_at,attempt_count) VALUES($1,$2,$3,'created','email','legacy unbound email','pending',now(),now(),0) RETURNING id`, tenantID, ticketID, userID).Scan(&legacyID))
	defer func() {
		_, err := db.ExecContext(ctx, `DELETE FROM public.ticket_notifications WHERE id=$1`, legacyID)
		require.NoError(t, err)
	}()
	_, err = db.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION public.preserve_notification_connector_target() TO "+runtimeRole)
	require.NoError(t, err)
	var canExecute bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT has_function_privilege($1,'public.preserve_notification_connector_target()','EXECUTE')`, runtimeRole).Scan(&canExecute))
	require.True(t, canExecute, "045 must revoke an existing explicit function grant")
	var before, after string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(n) ORDER BY id)::text FROM public.ticket_notifications n`).Scan(&before))
	_, err = db.ExecContext(ctx, migrationSQL)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(n)-'target_transport' ORDER BY id)::text FROM public.ticket_notifications n`).Scan(&after))
	require.JSONEq(t, before, after)
	var bound int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM public.ticket_notifications WHERE target_transport IS NOT NULL`).Scan(&bound))
	require.Zero(t, bound, "migration must not assign transport to existing rows")
	checkViolation := func(err error) *pq.Error {
		t.Helper()
		var pg *pq.Error
		require.ErrorAs(t, err, &pg)
		require.Equal(t, pq.ErrorCode("23514"), pg.Code)
		return pg
	}
	insert := `INSERT INTO public.ticket_notifications(tenant_id,ticket_id,user_id,type,channel,content,status,created_at,next_attempt_at,attempt_count,target_protocol_version,target_transport,target_connector_name,target_connector_provider,target_destination_digest) VALUES($1,$2,$3,'created','email','private email target','pending',now(),now(),0,$4,$5,$6,$7,$8) RETURNING id`
	for _, target := range []struct{ version, transport, name, provider, digest any }{
		{2, nil, "msgraph-email", "microsoft", strings.Repeat("a", 64)},
		{2, "graph", nil, "microsoft", strings.Repeat("a", 64)},
		{2, "graph", "msgraph-email", "other", strings.Repeat("a", 64)},
		{2, "smtp", "email", "smtp", strings.Repeat("a", 64)},
		{1, "smtp", nil, nil, strings.Repeat("a", 64)},
		{2, "smtp", nil, nil, "invalid"},
		{nil, "smtp", nil, nil, strings.Repeat("a", 64)},
		{2, "unknown", nil, nil, strings.Repeat("a", 64)},
	} {
		_, err = db.ExecContext(ctx, insert, tenantID, ticketID, userID, target.version, target.transport, target.name, target.provider, target.digest)
		checkViolation(err)
	}
	for _, transport := range []string{"graph", "smtp"} {
		var name, provider any
		if transport == "graph" {
			name, provider = "msgraph-email", "microsoft"
		}
		var id int
		require.NoError(t, db.QueryRowContext(ctx, insert, tenantID, ticketID, userID, 2, transport, name, provider, strings.Repeat("a", 64)).Scan(&id))
		defer func() {
			_, err := db.ExecContext(ctx, `DELETE FROM public.ticket_notifications WHERE id=$1`, id)
			require.NoError(t, err)
		}()
		for _, assignment := range []string{"target_transport=NULL", "target_protocol_version=NULL", "target_destination_digest=repeat('b',64)", "target_connector_name='changed'", "channel='sms'", "content='changed'", "user_id=user_id+1", "sla_alert_history_id=1"} {
			_, err = db.ExecContext(ctx, "UPDATE public.ticket_notifications SET "+assignment+" WHERE id=$1", id)
			checkViolation(err)
		}
		_, err = db.ExecContext(ctx, `UPDATE public.ticket_notifications SET status='processing',attempt_count=1 WHERE id=$1`, id)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `UPDATE public.ticket_notifications SET target_protocol_version=2,target_transport='smtp',target_destination_digest=repeat('a',64) WHERE id=$1`, legacyID)
	require.Equal(t, "notification target and delivery identity are immutable", checkViolation(err).Message, "complete valid email binding must be rejected by the immutable trigger")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT has_function_privilege($1,'public.preserve_notification_connector_target()','EXECUTE')`, runtimeRole).Scan(&canExecute))
	require.False(t, canExecute)
}
