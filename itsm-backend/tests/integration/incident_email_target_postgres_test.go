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
	for _, scenario := range []string{"stable", "legacy v1", "recipient changed", "target changed", "missing acceptance", "unknown field", "unsupported channel", "duplicate manifest", "matching delivery receipt", "conflicting delivery receipt", "receipt failure", "publication failure", "claim valid", "claim wrong token", "claim replaced token", "claim expired", "claim missing attempt", "claim wrong attempt", "claim wrong event", "claim wrong tenant", "claim missing tenant", "source alert missing", "source alert incident changed", "source workitem changed", "source tenant changed", "claim wrong status", "claim wrong row", "claim wrong type", "claim forged tenant"} {
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

			if strings.HasPrefix(scenario, "claim ") || strings.HasPrefix(scenario, "source ") {
				repo := service.NewOutboxEventRepository(client, policy)
				claimed, err := repo.ClaimDueByEventType(ctx, time.Now().UTC(), 10, "incident_alert_delivery", false)
				require.NoError(t, err)
				require.Len(t, claimed, 1)
				invocation := *claimed[0]
				if scenario != "claim missing attempt" {
					require.NoError(t, repo.MarkDeliveryAttemptStarted(ctx, invocation.ID, invocation.ClaimToken, invocation.EventID))
				}
				callCtx := ctx
				switch scenario {
				case "source alert missing":
					client.IncidentAlert.DeleteOneID(int(payload["alertId"].(float64))).ExecX(ctx)
				case "source alert incident changed":
					replacement := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetTitle("other incident").SetTicketNumber("INC-OTHER").SetRecordClass("incident").SetStatus("new").SaveX(ctx)
					other := client.Incident.Create().SetWorkItemID(replacement.ID).SetSeverity("high").SetDetectedAt(time.Now()).SaveX(ctx)
					client.IncidentAlert.UpdateOneID(int(payload["alertId"].(float64))).SetIncidentID(other.ID).ExecX(ctx)
				case "source workitem changed":
					replacement := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetTitle("replacement incident").SetTicketNumber("INC-REPLACEMENT").SetRecordClass("incident").SetStatus("new").SaveX(ctx)
					inc.Update().SetWorkItemID(replacement.ID).ExecX(ctx)
				case "source tenant changed":
					foreign := client.Tenant.Create().SetCode("foreign-mail").SetName("Foreign mail").SaveX(ctx)
					item.Update().SetTenantID(foreign.ID).ExecX(ctx)
				case "claim wrong status":
					client.OutboxEvent.UpdateOneID(event.ID).SetStatus("pending").ExecX(ctx)
				case "claim wrong row":
					invocation.ID += 10000
				case "claim wrong type":
					invocation.EventType = "another-type"
				case "claim forged tenant":
					invocation.TenantID = tenant.ID + 1
				case "claim wrong token":
					invocation.ClaimToken = "not-the-current-token"
				case "claim replaced token":
					client.OutboxEvent.UpdateOneID(event.ID).SetClaimToken("replacement-token").ExecX(ctx)
				case "claim expired":
					client.OutboxEvent.UpdateOneID(event.ID).SetClaimExpiresAt(time.Now().UTC().Add(-time.Minute)).ExecX(ctx)
				case "claim wrong attempt":
					client.OutboxEvent.UpdateOneID(event.ID).SetLastError("delivery_attempt_started:another-event").ExecX(ctx)
				case "claim wrong event":
					invocation.EventID = "another-event"
				case "claim wrong tenant":
					callCtx = tenantctx.WithTenantID(ctx, tenant.ID+1)
				case "claim missing tenant":
					callCtx = context.Background()
				}
				before, auditBefore := incidentMailPersistenceSnapshot(t, ctx, client, event.ID)
				auditCount := client.AuditLog.Query().CountX(ctx)
				handler := service.NewIncidentAlertDeliveryHandler(client, policy, mailer)
				err = handler.Deliver(callCtx, &invocation)
				if scenario == "claim valid" {
					require.NoError(t, err)
					require.Equal(t, int32(1), accepted.Load())
					proof := <-received
					require.Equal(t, "RCPT TO:<"+actor.Email+">", proof.Recipient)
					require.Contains(t, proof.Data, base64.StdEncoding.EncodeToString([]byte("private body")))
					require.Equal(t, auditCount+1, client.AuditLog.Query().CountX(ctx))
				} else {
					require.Error(t, err)
					require.Zero(t, connections.Load())
					require.Equal(t, auditCount, client.AuditLog.Query().CountX(ctx))
				}
				after, auditAfter := incidentMailPersistenceSnapshot(t, ctx, client, event.ID)
				require.JSONEq(t, before, after, "handler must not modify the persisted claim")
				if scenario != "claim valid" {
					require.JSONEq(t, auditBefore, auditAfter, "rejection must preserve existing audit contents")
				}
				return
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

// Read PostgreSQL rows directly: Ent JSON intentionally omits claim_token,
// last_error and payload, so it cannot prove complete claim preservation.
func incidentMailPersistenceSnapshot(t *testing.T, ctx context.Context, client *ent.Client, eventID int) (string, string) {
	t.Helper()
	rows, err := client.QueryContext(ctx, `SELECT row_to_json(o)::text, (SELECT coalesce(json_agg(a ORDER BY id), '[]'::json)::text FROM audit_logs a) FROM outbox_events o WHERE o.id=$1`, eventID)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var event, audits string
	require.NoError(t, rows.Scan(&event, &audits))
	require.False(t, rows.Next())
	require.NoError(t, rows.Err())
	return event, audits
}
