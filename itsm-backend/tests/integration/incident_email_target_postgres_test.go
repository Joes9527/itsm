//go:build candidate_scope

package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/service"
)

func incidentMailTestDigest(t *testing.T, value interface{}) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var canonical interface{}
	require.NoError(t, decoder.Decode(&canonical))
	raw, err = json.Marshal(canonical)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// SMTP only listens on loopback and counts DATA acceptance, not just a connect.
type incidentMailAccepted struct {
	Recipient string
	Data      string
}

func startIncidentMailReceiver(t *testing.T) (service.EmailConfig, *atomic.Int32, *atomic.Int32, <-chan incidentMailAccepted) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	connections, accepted := &atomic.Int32{}, &atomic.Int32{}
	received := make(chan incidentMailAccepted, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				reader := bufio.NewReader(conn)
				read := func() (string, error) { line, e := reader.ReadString('\n'); return strings.TrimSpace(line), e }
				write := func(s string) { _, _ = fmt.Fprint(conn, s+"\r\n") }
				write("220 private smtp")
				if _, err := read(); err != nil {
					return
				}
				write("250-private smtp\r\n250 AUTH PLAIN")
				if _, err := read(); err != nil {
					return
				}
				write("235 authenticated")
				if _, err := read(); err != nil {
					return
				}
				write("250 sender ok")
				recipient, err := read()
				if err != nil {
					return
				}
				write("250 recipient ok")
				if _, err := read(); err != nil {
					return
				}
				write("354 end with dot")
				var body strings.Builder
				for {
					line, err := read()
					if err != nil {
						return
					}
					if line == "." {
						break
					}
					body.WriteString(line)
					body.WriteByte('\n')
				}
				received <- incidentMailAccepted{recipient, body.String()}
				accepted.Add(1)
				write("250 accepted")
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	return service.EmailConfig{DeliveryTransport: "smtp", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "private-test", Password: "private-test", From: "sender@example.invalid"}, connections, accepted, received
}

// Standard-owner PostgreSQL protocol evidence; this does not claim candidate
// restricted-role/RLS or Graph activation admission.
func TestCandidateIncidentEmailTargetProtocol(t *testing.T) {
	for _, scenario := range []string{"stable", "legacy v1", "recipient changed", "target changed", "missing acceptance", "unknown field", "unsupported channel", "duplicate manifest", "matching delivery receipt", "conflicting delivery receipt", "receipt failure", "publication failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			client := newCandidatePrivatePostgresClient(t, ctx)
			require.NoError(t, client.Schema.Create(ctx))
			tenant := client.Tenant.Create().SetCode("private-mail").SetName("Private mail").SaveX(ctx)
			actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("private-mail").SetName("Private mail").SetRole("agent").SetActive(true).SetEmail("recipient@example.invalid").SetPasswordHash("private").SaveX(ctx)
			item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetTitle("private incident").SetTicketNumber("INC-PRIVATE-MAIL").SetRecordClass("incident").SetStatus("new").SaveX(ctx)
			inc := client.Incident.Create().SetWorkItemID(item.ID).SetSeverity("high").SetDetectedAt(time.Now()).SaveX(ctx)
			ctx = service.WithIncidentAlertActor(tenantctx.WithTenantID(ctx, tenant.ID), actor.ID, "user", "private-incident-mail")
			policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "private-incident-mail", Capabilities: map[string]string{"outbox": "enabled"}})
			require.NoError(t, err)
			cfg, connections, accepted, received := startIncidentMailReceiver(t)
			mailer := service.NewEmailService(cfg, zap.NewNop().Sugar())
			mailer.SetDeliveryTargetDependencies(nil, policy)
			producer := service.NewIncidentAlertingService(client, zap.NewNop().Sugar(), policy)
			producer.SetEmailService(mailer)
			_, err = producer.CreateIncidentAlert(ctx, &dto.CreateIncidentAlertRequest{IncidentID: inc.ID, AlertType: "monitoring", AlertName: "bound", Message: "private body", Severity: "high", Channels: []string{"email", "in_app"}, Recipients: []string{actor.Email}}, tenant.ID)
			require.NoError(t, err)
			require.Zero(t, connections.Load())
			event := client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("incident_alert_delivery")).OnlyX(ctx)
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal(event.Payload, &payload))
			require.Equal(t, float64(2), payload["version"])
			require.Equal(t, float64(item.ID), payload["workItemId"])
			require.Equal(t, float64(inc.ID), payload["incidentId"])
			acceptance := client.AuditLog.Query().Where(auditlog.ActionEQ("incident_alert.delivery_accepted")).OnlyX(ctx)
			var manifest map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(*acceptance.RequestBody), &manifest))
			intents := manifest["intents"].([]interface{})
			require.Len(t, intents, 1)
			payloadDigest := incidentMailTestDigest(t, payload)
			require.Equal(t, payloadDigest, intents[0].(map[string]interface{})["payloadDigest"])
			switch scenario {
			case "legacy v1":
				payload["version"] = 1
				delete(payload, "target")
				delete(payload, "workItemId")
				delete(payload, "incidentId")
			case "recipient changed":
				payload["recipients"] = []string{"changed@example.invalid"}
			case "target changed":
				payload["target"].(map[string]interface{})["destinationDigest"] = strings.Repeat("a", 64)
			case "unknown field":
				payload["unexpected"] = "field"
			case "unsupported channel":
				payload["channel"] = "unsupported"
			case "missing acceptance":
				require.NoError(t, client.AuditLog.DeleteOne(acceptance).Exec(ctx))
			case "duplicate manifest":
				duplicate := map[string]interface{}{"eventId": event.EventID, "payloadDigest": strings.Repeat("b", 64)}
				manifest["intents"] = append(intents, duplicate)
				encoded, err := json.Marshal(manifest)
				require.NoError(t, err)
				acceptance.Update().SetRequestBody(string(encoded)).SetRequestDigest(incidentMailTestDigest(t, manifest)).SaveX(ctx)
			case "matching delivery receipt", "conflicting delivery receipt":
				digest := payloadDigest
				if scenario == "conflicting delivery receipt" {
					digest = strings.Repeat("c", 64)
				}
				client.AuditLog.Create().SetTenantID(tenant.ID).SetUserID(actor.ID).SetOperationID("incident_alert_deliver:" + event.EventID).SetRequestID("private-incident-mail").SetResource("incident_alert").SetAction("incident_alert.delivered").SetPath("outbox://" + event.EventID).SetMethod("POST").SetStatusCode(200).SetRequestDigest(digest).SetResultStatus("delivered").SaveX(ctx)
			}
			if scenario == "legacy v1" || scenario == "recipient changed" || scenario == "target changed" || scenario == "unknown field" || scenario == "unsupported channel" {
				encoded, err := json.Marshal(payload)
				require.NoError(t, err)
				event.Update().SetPayload(encoded).SaveX(ctx)
			}
			if scenario == "receipt failure" || scenario == "publication failure" {
				client.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if am, ok := m.(*ent.AuditLogMutation); ok && scenario == "receipt failure" {
							if action, _ := am.Action(); action == "incident_alert.delivered" {
								if _, err := next.Mutate(ctx, m); err != nil {
									return nil, err
								}
								return nil, errors.New("private receipt failure")
							}
						}
						if om, ok := m.(*ent.OutboxEventMutation); ok && scenario == "publication failure" {
							if status, _ := om.Status(); status == "published" {
								if _, err := next.Mutate(ctx, m); err != nil {
									return nil, err
								}
								return nil, errors.New("private publication failure")
							}
						}
						return next.Mutate(ctx, m)
					})
				})
			}
			registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewIncidentAlertDeliveryHandler(client, policy, mailer)})
			require.NoError(t, err)
			worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(client, policy), service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 2 * time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
			require.NoError(t, err)
			err = worker.DispatchOnce(ctx)
			if scenario == "publication failure" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			current := client.OutboxEvent.GetX(ctx, event.ID)
			switch scenario {
			case "stable":
				require.Equal(t, "published", current.Status)
				require.Equal(t, int32(1), accepted.Load())
				require.Equal(t, 1, client.AuditLog.Query().Where(auditlog.ActionEQ("incident_alert.delivered")).CountX(ctx))
			case "receipt failure":
				require.Equal(t, "blocked", current.Status)
				require.Contains(t, current.LastError, "delivery_unknown:")
				require.Equal(t, int32(1), accepted.Load())
				require.Zero(t, client.AuditLog.Query().Where(auditlog.ActionEQ("incident_alert.delivered")).CountX(ctx))
			case "publication failure":
				require.Equal(t, "publishing", current.Status)
				require.Equal(t, int32(1), accepted.Load())
				current.Update().SetClaimExpiresAt(time.Now().Add(-time.Minute)).SaveX(ctx)
			default:
				require.Equal(t, "blocked", current.Status)
				require.Zero(t, connections.Load())
			}
			if scenario == "legacy v1" {
				require.Contains(t, current.LastError, "unsupported incident alert delivery payload version")
			}
			if scenario == "conflicting delivery receipt" {
				require.Contains(t, current.LastError, "receipt conflicts")
			}
			if scenario == "matching delivery receipt" {
				require.Contains(t, current.LastError, "delivery_unknown:")
			}
			if scenario == "duplicate manifest" {
				require.Contains(t, current.LastError, "digest mismatch")
			}
			var receiptSnapshot []byte
			if scenario == "publication failure" {
				row := client.AuditLog.Query().Where(auditlog.ActionEQ("incident_alert.delivered")).OnlyX(ctx)
				receiptSnapshot, err = json.Marshal(row)
				require.NoError(t, err)
			}
			if scenario == "stable" || scenario == "receipt failure" || scenario == "publication failure" {
				proof := <-received
				require.Equal(t, "RCPT TO:<"+actor.Email+">", proof.Recipient)
				require.Contains(t, proof.Data, base64.StdEncoding.EncodeToString([]byte("private body")))
			}
			require.NoError(t, worker.DispatchOnce(ctx))
			if scenario == "stable" || scenario == "receipt failure" || scenario == "publication failure" {
				require.Equal(t, int32(1), accepted.Load())
			} else {
				require.Zero(t, connections.Load())
			}
			if scenario == "publication failure" {
				recovered := client.OutboxEvent.GetX(ctx, event.ID)
				require.Equal(t, "blocked", recovered.Status)
				require.Contains(t, recovered.LastError, "delivery_unknown:")
				row := client.AuditLog.Query().Where(auditlog.ActionEQ("incident_alert.delivered")).OnlyX(ctx)
				after, err := json.Marshal(row)
				require.NoError(t, err)
				require.JSONEq(t, string(receiptSnapshot), string(after))
			}
		})
	}
}
