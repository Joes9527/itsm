//go:build candidate_scope

package integration

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/service"
)

// Producer fixtures declare a private SMTP destination. Any transport attempt
// is rejected and fails the test; this is not delivery acceptance evidence.
func candidateNotificationEmailConfig(t *testing.T) service.EmailConfig {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			calls.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		require.Zero(t, calls.Load(), "producer-only fixture unexpectedly attempted SMTP")
	})
	return service.EmailConfig{DeliveryTransport: "smtp", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "private-fixture", From: "fixture@example.invalid"}
}

func newCandidateNotificationOwner(t *testing.T, client *ent.Client, logger *zap.SugaredLogger, policy *database.ExecutionPolicy) *service.TicketNotificationService {
	t.Helper()
	owner := service.NewTicketNotificationService(client, logger, policy)
	mailer := service.NewEmailService(candidateNotificationEmailConfig(t), logger)
	mailer.SetDeliveryTargetDependencies(nil, policy)
	owner.SetEmailService(mailer)
	return owner
}
