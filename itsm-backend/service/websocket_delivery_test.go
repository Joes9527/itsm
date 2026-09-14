package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func newPushLoopback(t *testing.T, tenantID, userID int) (*WebSocketHub, *WebSocketClient, *websocket.Conn) {
	t.Helper()
	hub := NewWebSocketHub(zaptest.NewLogger(t).Sugar())
	ready := make(chan *WebSocketClient, 1)
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			close(done)
			return
		}
		client := &WebSocketClient{UserID: userID, TenantID: tenantID, Conn: conn, Hub: hub, Send: make(chan websocketFrame, 4)}
		hub.mu.Lock()
		hub.clients[client] = true
		hub.mu.Unlock()
		ready <- client
		client.WritePump()
		close(done)
	}))
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	var client *WebSocketClient
	select {
	case client = <-ready:
	case <-time.After(time.Second):
		t.Fatal("loopback not ready")
	}
	t.Cleanup(func() {
		hub.mu.Lock()
		if _, ok := hub.clients[client]; ok {
			client.IsClosed = true
			close(client.Send)
			delete(hub.clients, client)
		}
		hub.mu.Unlock()
		client.Conn.Close()
		socket.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("write pump did not stop")
		}
		server.Close()
	})
	return hub, client, socket
}

func TestWebSocketDeliveryRequiresSocketAcceptance(t *testing.T) {
	hub, _, socket := newPushLoopback(t, 9, 7)
	// A second eligible but stalled connection cannot hide real success.
	stalled := &WebSocketClient{UserID: 7, TenantID: 9, Send: make(chan websocketFrame, 1)}
	hub.mu.Lock()
	hub.clients[stalled] = true
	hub.mu.Unlock()
	require.NoError(t, hub.DeliverToUser(context.Background(), 9, 7, WebSocketMessage{Type: "notification", Payload: "local"}))
	require.NoError(t, socket.SetReadDeadline(time.Now().Add(time.Second)))
	_, payload, err := socket.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(payload), `"local"`)
}

func TestWebSocketDeliveryWriteFailureIsUnknown(t *testing.T) {
	hub, client, _ := newPushLoopback(t, 9, 7)
	require.NoError(t, client.Conn.Close())
	require.ErrorIs(t, hub.DeliverToUser(context.Background(), 9, 7, WebSocketMessage{Type: "notification"}), errPushUnknown)
}

func TestWebSocketDeliveryTimeoutAfterEnqueueIsUnknown(t *testing.T) {
	hub := NewWebSocketHub(zaptest.NewLogger(t).Sugar())
	client := &WebSocketClient{UserID: 7, TenantID: 9, Send: make(chan websocketFrame, 1)}
	hub.clients[client] = true
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := hub.DeliverToUser(ctx, 9, 7, WebSocketMessage{Type: "notification"})
	require.ErrorIs(t, err, errPushUnknown)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	// Late acknowledgements cannot block a write pump after the waiter leaves.
	frame := <-client.Send
	frame.finish(nil)
}

func TestTicketNotificationPushSocketAcceptedButCommitFails(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()
	tenant, recipient, item := createNotifTestData(t, client, ctx)
	hub, _, socket := newPushLoopback(t, tenant.ID, recipient.ID)
	logger := zaptest.NewLogger(t).Sugar()
	svc.SetWebSocketService(&WebSocketService{hub: hub, logger: logger})
	svc.SetDeliveryQueueClient(client)
	svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, logger))
	client.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(recipient.ID).SetEventType("ticket_updated").SetInAppEnabled(false).SetEmailEnabled(false).SetSmsEnabled(false).SetPushEnabled(true).SaveX(ctx)
	_, err := svc.SendNotification(ctx, item.ID, &dto.SendTicketNotificationRequest{UserIDs: []int{recipient.ID}, EventType: "ticket_updated", Content: "write before commit", DeliveryKey: "push-write-commit"}, tenant.ID)
	require.NoError(t, err)
	failure := errors.New("completion write failed")
	client.TicketNotification.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if mutation, ok := m.(*ent.TicketNotificationMutation); ok {
				if status, ok := mutation.Status(); ok && status == ticketNotificationStatusSent {
					return nil, failure
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	n, err := svc.ProcessPendingDeliveries(ctx, "push-commit", 10)
	require.ErrorIs(t, err, failure)
	require.Zero(t, n)
	require.NoError(t, socket.SetReadDeadline(time.Now().Add(time.Second)))
	_, payload, err := socket.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(payload), "write before commit")
	row := client.TicketNotification.Query().OnlyX(ctx)
	require.Equal(t, ticketNotificationStatusProcessing, row.Status)
	require.True(t, row.SentAt.IsZero())
	svc.now = func() time.Time { return row.LeaseExpiresAt.Add(time.Second) }
	_, _ = svc.ProcessPendingDeliveries(ctx, "push-recovery", 10)
	row = client.TicketNotification.Query().OnlyX(ctx)
	require.Equal(t, ticketNotificationStatusFailed, row.Status)
	require.Equal(t, "delivery_unknown", row.LastErrorClass)
	require.Equal(t, 1, row.AttemptCount)
	require.NoError(t, socket.SetReadDeadline(time.Now().Add(20*time.Millisecond)))
	_, _, err = socket.ReadMessage()
	require.Error(t, err, "recovery must not resend an uncertain delivery")
}

func TestTicketNotificationPushCompletesAfterSocketAcceptance(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()
	tenant, recipient, item := createNotifTestData(t, client, ctx)
	hub, _, socket := newPushLoopback(t, tenant.ID, recipient.ID)
	logger := zaptest.NewLogger(t).Sugar()
	svc.SetWebSocketService(&WebSocketService{hub: hub, logger: logger})
	svc.SetDeliveryQueueClient(client)
	svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, logger))
	client.NotificationPreference.Create().SetTenantID(tenant.ID).SetUserID(recipient.ID).SetEventType("ticket_updated").SetInAppEnabled(false).SetEmailEnabled(false).SetSmsEnabled(false).SetPushEnabled(true).SaveX(ctx)
	_, err := svc.SendNotification(ctx, item.ID, &dto.SendTicketNotificationRequest{UserIDs: []int{recipient.ID}, EventType: "ticket_updated", Content: "accepted push", DeliveryKey: "push-accepted"}, tenant.ID)
	require.NoError(t, err)
	n, err := svc.ProcessPendingDeliveries(ctx, "push-accepted", 10)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	row := client.TicketNotification.Query().OnlyX(ctx)
	require.Equal(t, ticketNotificationStatusSent, row.Status)
	require.False(t, row.SentAt.IsZero())
	require.NoError(t, socket.SetReadDeadline(time.Now().Add(time.Second)))
	_, payload, err := socket.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(payload), "accepted push")
}
