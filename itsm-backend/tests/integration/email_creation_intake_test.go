package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func TestIntakeEmailSourceGraphIsAtomicAndTrusted(t *testing.T) {
	for _, fault := range []string{"", "workflow start", "snapshot"} {
		t.Run("failure="+fault, func(t *testing.T) {
			ctx := context.Background()
			f := newUnifiedIntakeFixture(t)
			client, app, identity, command := f.client, f.app, f.identity, f.command

			identity.Channel, identity.Provider = "email", "msgraph_email"
			command.SourceReference = &creation.SourceReference{Provider: "msgraph_email", EventID: "internet-message", ConversationID: "email-thread"}
			payload, _ := json.Marshal(command)
			var raw map[string]any
			require.NoError(t, json.Unmarshal(payload, &raw))
			raw["email"] = map[string]any{"mailbox": "support@example.test", "graphMessageId": "graph-message", "senderEmail": "u@example.test", "hasAttachments": true, "triageComment": "AI suggestion"}
			payload, _ = json.Marshal(raw)
			require.NoError(t, json.Unmarshal(payload, &command))
			var reached *bool
			if fault != "" {
				reached = installEntryMutationFailure(client, fault)
			}
			result, err := app.Create(ctx, identity, command)
			if fault != "" {
				require.Error(t, err)
				require.True(t, *reached)
				assertNoEntryGraph(t, client)
				require.Zero(t, client.TicketComment.Query().CountX(ctx))
				return
			}
			require.NoError(t, err)
			item := client.Ticket.GetX(ctx, result.WorkItemID)
			require.Equal(t, "internet-message", item.ExternalMessageID)
			require.Equal(t, "email-thread", item.ConversationID)
			require.Equal(t, "u@example.test", item.CreatorEmail)
			require.Equal(t, 2, client.OutboxEvent.Query().CountX(ctx))
			require.Equal(t, 1, client.TicketComment.Query().CountX(ctx))
			replay, err := app.Create(ctx, identity, command)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, 2, client.OutboxEvent.Query().CountX(ctx))
			require.Equal(t, 1, client.TicketComment.Query().CountX(ctx))
			identity.Channel, identity.Provider = "itsm_web", "msgraph_email"
			_, err = app.Create(ctx, identity, command)
			require.ErrorIs(t, err, creation.ErrPermissionDenied, "provider text cannot grant email-source authority")
		})
	}
}
