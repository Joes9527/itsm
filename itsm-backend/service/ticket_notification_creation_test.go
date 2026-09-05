package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
)

type creationNotificationWriter interface {
	EnqueueCreationTx(context.Context, *ent.Tx, *ent.Ticket, int, string, string, string, []int) error
}

func TestTicketCreationNotificationPersistsOnlyInOwningTransaction(t *testing.T) {
	f := newDurableNotificationFixture(t, "creation-intent")
	graph := &durableNotificationGraphSender{}
	mail := NewEmailService(EmailConfig{}, zap.NewNop().Sugar())
	mail.SetGraphProvider(func(int) (GraphMailSender, string, bool) { return graph, "support@example.test", true })
	f.notifications.SetEmailService(mail)
	writer, ok := any(f.notifications).(creationNotificationWriter)
	require.True(t, ok, "creation requires an owning transactional notification port")
	for _, commit := range []bool{false, true} {
		tx, err := f.client.Tx(f.ctx)
		require.NoError(t, err)
		require.NoError(t, writer.EnqueueCreationTx(f.ctx, tx, f.ticket, f.operator.ID, "ticket_created", "Frozen original content", "creation:one", []int{f.recipient.ID}))
		require.Empty(t, graph.sentCalls())
		if commit {
			require.NoError(t, tx.Commit())
		} else {
			require.NoError(t, tx.Rollback())
			require.Zero(t, f.client.TicketNotification.Query().CountX(f.ctx))
		}
	}
	require.Equal(t, 2, f.client.TicketNotification.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.Notification.Query().CountX(f.ctx))
	completed, err := f.notifications.ProcessPendingDeliveries(f.ctx, "creation-delivery", 10)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	require.Len(t, graph.sentCalls(), 1)
}
