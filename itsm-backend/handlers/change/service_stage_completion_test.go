package change

import (
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/stretchr/testify/require"
)

func TestChangeTaskCompletionWithoutRunningInstanceFailsClosed(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	_, err := f.svc.CompleteChangeTask(f.ctx, TaskCommand{Command: f.command("implement", f.requester), TaskID: "missing"})
	require.Error(t, err)
	require.Equal(t, "draft", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))

}
