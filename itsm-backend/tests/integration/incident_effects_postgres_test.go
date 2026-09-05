//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incidentruleexecution"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/migration"
	"itsm-backend/service"
)

type incidentEffectsFixture struct {
	db     *sql.DB
	client *ent.Client
	ctx    context.Context
	engine *service.IncidentRuleEngine
	svc    *service.IncidentService
	event  *ent.OutboxEvent
	inc    *ent.Incident
	actor  *ent.User
	tenant *ent.Tenant
}

func newIncidentEffectsFixture(t *testing.T) *incidentEffectsFixture {
	t.Helper()
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	require.NotEmpty(t, dsn, "explicit disposable DB required")
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "/sslvpn_test", parsed.Path)
	require.Equal(t, "127.0.0.1:36444", parsed.Host)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	schema := fmt.Sprintf("a4_incident_effects_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	client, err := ent.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, client.Schema.Create(ctx))
	scopedDB, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scopedDB.Close()) })
	_, err = scopedDB.ExecContext(ctx, migration.GetMigrationSQL("024_incident_rule_action_receipts"))
	require.NoError(t, err)

	tenant := client.Tenant.Create().SetCode("effects").SetName("effects").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("actor").SetName("actor").SetRole("agent").SetActive(true).SetEmail("actor@example.test").SetPasswordHash("test").SaveX(ctx)
	item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetTitle("effects").SetTicketNumber("INC-EFFECTS").SetRecordClass("incident").SetType("incident").SetStatus("new").SetPriority("high").SaveX(ctx)
	inc := client.Incident.Create().SetWorkItemID(item.ID).SetIncidentNumber(item.TicketNumber).SetSeverity("high").SetDetectedAt(time.Now()).SaveX(ctx)
	client.IntakeRequest.Create().SetTenantID(tenant.ID).SetActorID(actor.ID).SetRequesterID(actor.ID).SetChannel("api").SetOperation("create").SetIdempotencyKey("effects").SetRequestDigest("digest").SetDigestVersion("v1").SetStatus("completed").SetWorkItemID(item.ID).SaveX(ctx)
	payload, err := json.Marshal(map[string]interface{}{"tenantId": tenant.ID, "incidentId": inc.ID, "workItemId": item.ID, "actorId": actor.ID, "channel": "api"})
	require.NoError(t, err)
	event := client.OutboxEvent.Create().SetTenantID(tenant.ID).SetEventID(fmt.Sprintf("incident-created:%d", item.ID)).SetEventType("incident.created").SetAggregateType("work_item").SetAggregateID(fmt.Sprint(item.ID)).SetPayload(payload).SaveX(ctx)
	svc := service.NewIncidentService(client, zap.NewNop().Sugar(), nil)
	svc.SetAlertCreator(service.NewIncidentAlertingService(client, zap.NewNop().Sugar()))
	return &incidentEffectsFixture{scopedDB, client, ctx, svc.RuleEngine(), svc, event, inc, actor, tenant}
}
func (f *incidentEffectsFixture) rule(actions ...map[string]interface{}) *ent.IncidentRule {
	return f.client.IncidentRule.Create().SetTenantID(f.tenant.ID).SetName("effects").SetRuleType("automation").SetConditions(map[string]interface{}{}).SetActions(actions).SaveX(f.ctx)
}
func metricAction(name string) map[string]interface{} {
	return map[string]interface{}{"type": "collect_metric", "metric_type": "automation", "metric_name": name, "metric_value": 1.0}
}

func TestPostgresIncidentEffectsMigrationRegistered(t *testing.T) {
	require.NotEmpty(t, migration.GetMigrationSQL("024_incident_rule_action_receipts"))
}

func TestPostgresIncidentEffectsReceiptFaultRollsBackActualMutation(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprintf("after_receipt_%v", after), func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			f.rule(metricAction("created"))
			var fail atomic.Bool
			fail.Store(true)
			f.client.IncidentRuleActionReceipt.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if fail.Load() {
						if after {
							if _, err := next.Mutate(ctx, m); err != nil {
								return nil, err
							}
						}
						return nil, errors.New("receipt fault")
					}
					return next.Mutate(ctx, m)
				})
			})
			require.ErrorContains(t, f.engine.Deliver(f.ctx, f.event), "receipt fault")
			require.Zero(t, f.client.IncidentMetric.Query().CountX(f.ctx))
			require.Zero(t, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
			require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
			fail.Store(false)
			require.NoError(t, f.engine.Deliver(f.ctx, f.event))
			require.Equal(t, 1, f.client.IncidentMetric.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
		})
	}
}
func TestPostgresIncidentEffectsResumeFrozenActionsAndCandidateSet(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	rule := f.rule(metricAction("first"), metricAction("second"))
	rule.Update().SetConditions(map[string]interface{}{"priority": []string{"high"}}).SaveX(f.ctx)
	var fail atomic.Bool
	fail.Store(true)
	f.client.IncidentMetric.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			name, _ := m.(*ent.IncidentMetricMutation).MetricName()
			if name == "second" && fail.Load() {
				return nil, errors.New("second action interrupted")
			}
			return next.Mutate(ctx, m)
		})
	})
	require.ErrorContains(t, f.engine.Deliver(f.ctx, f.event), "second action interrupted")
	require.Equal(t, 1, f.client.IncidentMetric.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
	require.Equal(t, "running", f.client.IncidentRuleExecution.Query().Where(incidentruleexecution.ExecutionKey(f.event.EventID)).OnlyX(f.ctx).Status)
	f.client.Ticket.UpdateOneID(f.inc.WorkItemID).SetPriority("low").SaveX(f.ctx)
	rule.Update().SetConditions(map[string]interface{}{"priority": []string{"low"}}).SetActions([]map[string]interface{}{metricAction("edited")}).SetIsActive(false).SaveX(f.ctx)
	f.rule(metricAction("new-policy"))
	fail.Store(false)
	restarted := service.NewIncidentRuleEngine(f.client, zap.NewNop().Sugar(), nil)
	require.NoError(t, restarted.Deliver(f.ctx, f.event))
	require.NoError(t, restarted.Deliver(f.ctx, f.event))
	metrics := f.client.IncidentMetric.Query().Order(ent.Asc("id")).AllX(f.ctx)
	require.Len(t, metrics, 2)
	require.Equal(t, "first", metrics[0].MetricName)
	require.Equal(t, "second", metrics[1].MetricName)
	require.Equal(t, 2, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.IncidentRule.GetX(f.ctx, rule.ID).ExecutionCount)
}
func TestPostgresIncidentEffectsConcurrentReplayAndWorkerFencing(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.rule(metricAction("once"), map[string]interface{}{"type": "escalate", "level": 1, "reason": "threshold", "notify_users": []int{f.actor.ID}})
	start := make(chan struct{})
	results := make(chan error, 12)
	var ready sync.WaitGroup
	ready.Add(12)
	for range 12 {
		go func() { ready.Done(); <-start; results <- f.engine.Deliver(f.ctx, f.event) }()
	}
	ready.Wait()
	close(start)
	for range 12 {
		require.NoError(t, <-results)
	}
	require.Equal(t, 1, f.client.IncidentMetric.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.IncidentEvent.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.IncidentAlert.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.Notification.Query().CountX(f.ctx))
	require.Equal(t, 2, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
	require.Equal(t, 2, f.client.OutboxEvent.Query().CountX(f.ctx))
}

func TestPostgresIncidentEffectsWorkerAcknowledgmentLossAndFencing(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.rule(metricAction("once"), map[string]interface{}{"type": "escalate", "level": 1, "reason": "threshold", "notify_users": []int{f.actor.ID}})
	registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{f.engine}, "incident_alert_delivery")
	require.NoError(t, err)
	repo := service.NewOutboxEventRepository(f.client)
	worker, err := service.NewOutboxDeliveryWorker(repo, service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 10 * time.Second, MaxAttempts: 5}, zap.NewNop().Sugar(), registry)
	require.NoError(t, err)
	var loseAck atomic.Bool
	loseAck.Store(true)
	f.client.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			status, _ := m.(*ent.OutboxEventMutation).Status()
			if status == "published" && loseAck.Swap(false) {
				return nil, errors.New("acknowledgment lost")
			}
			return next.Mutate(ctx, m)
		})
	})
	require.ErrorContains(t, worker.DispatchOnce(f.ctx), "acknowledgment lost")
	claimed := f.client.OutboxEvent.GetX(f.ctx, f.event.ID)
	require.Equal(t, "publishing", claimed.Status)
	f.client.OutboxEvent.UpdateOneID(f.event.ID).SetClaimExpiresAt(time.Now().Add(-time.Hour)).SaveX(f.ctx)
	require.NoError(t, worker.DispatchOnce(f.ctx))
	require.Equal(t, "published", f.client.OutboxEvent.GetX(f.ctx, f.event.ID).Status)
	require.Error(t, repo.MarkPublished(f.ctx, f.event.ID, claimed.ClaimToken, time.Now()))
	require.Equal(t, 1, f.client.IncidentMetric.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.IncidentEvent.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.IncidentAlert.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.Notification.Query().CountX(f.ctx))
	require.Equal(t, 2, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
	delivery := f.client.OutboxEvent.Query().Where(outboxevent.EventType("incident_alert_delivery")).OnlyX(f.ctx)
	require.Equal(t, "pending", delivery.Status, "external send is not performed in action transaction")
}
func TestPostgresIncidentEffectsFreezeNoMatchingRules(t *testing.T) {
	for _, empty := range []bool{true, false} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			if !empty {
				f.rule(metricAction("excluded")).Update().SetConditions(map[string]interface{}{"priority": []string{"low"}}).SaveX(f.ctx)
			}
			require.NoError(t, f.engine.Deliver(f.ctx, f.event))
			f.rule(metricAction("late"))
			f.client.Ticket.UpdateOneID(f.inc.WorkItemID).SetPriority("low").SaveX(f.ctx)
			require.NoError(t, f.engine.Deliver(f.ctx, f.event))
			require.Zero(t, f.client.IncidentMetric.Query().CountX(f.ctx))
		})
	}
}
func TestPostgresIncidentEffectsConfiguredRecipientsAndPlaceholderFailures(t *testing.T) {
	for _, name := range []string{"unknown", "missing", "foreign", "inactive", "auto_assign", "fractional", "invalid_status", "optional"} {
		t.Run(name, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			action := map[string]interface{}{"type": "notify", "channels": []string{"email"}, "recipients": []string{f.actor.Email}}
			switch name {
			case "unknown":
				action["type"] = "unregistered"
			case "missing":
				delete(action, "recipients")
			case "foreign":
				other := f.client.Tenant.Create().SetName("other").SetCode("other").SaveX(f.ctx)
				receiver := f.client.User.Create().SetTenantID(other.ID).SetUsername("foreign").SetName("foreign").SetEmail("foreign@example.test").SetPasswordHash("test").SaveX(f.ctx)
				action["recipients"] = []string{receiver.Email}
			case "inactive":
				receiver := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("inactive").SetName("inactive").SetEmail("inactive@example.test").SetActive(false).SetPasswordHash("test").SaveX(f.ctx)
				action["recipients"] = []string{receiver.Email}
			case "auto_assign":
				action = map[string]interface{}{"type": "escalate", "level": 1, "reason": "threshold", "auto_assign": true}
			case "fractional":
				action = map[string]interface{}{"type": "assign", "assignee_id": 1.5}
			case "invalid_status":
				action = map[string]interface{}{"type": "change_status", "status": "closed"}
			case "optional":
				action["optional"] = true
			}
			f.rule(action)
			require.Error(t, f.engine.Deliver(f.ctx, f.event))
			require.Zero(t, f.client.IncidentAlert.Query().CountX(f.ctx))
			require.Zero(t, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
			require.Zero(t, f.client.IncidentEvent.Query().CountX(f.ctx))
			require.Zero(t, f.client.Incident.GetX(f.ctx, f.inc.ID).EscalationLevel)
		})
	}
}
func TestPostgresIncidentEffectsUpdateAndNotificationGraphRollback(t *testing.T) {
	for _, target := range []string{"timeline", "alert", "in_app", "outbox", "audit"} {
		t.Run(target, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) { return nil, errors.New("graph fault") })
			}
			switch target {
			case "timeline":
				f.client.IncidentEvent.Use(hook)
			case "alert":
				f.client.IncidentAlert.Use(hook)
			case "in_app":
				f.client.Notification.Use(hook)
			case "outbox":
				f.client.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if m.Op().Is(ent.OpCreate) {
							return nil, errors.New("graph fault")
						}
						return next.Mutate(ctx, m)
					})
				})
			case "audit":
				f.client.AuditLog.Use(hook)
			}
			f.rule(map[string]interface{}{"type": "escalate", "level": 1, "reason": "threshold", "notify_users": []int{f.actor.ID}})
			require.ErrorContains(t, f.engine.Deliver(f.ctx, f.event), "graph fault")
			require.Zero(t, f.client.Incident.GetX(f.ctx, f.inc.ID).EscalationLevel)
			require.Zero(t, f.client.IncidentEvent.Query().CountX(f.ctx))
			require.Zero(t, f.client.IncidentAlert.Query().CountX(f.ctx))
			require.Zero(t, f.client.Notification.Query().CountX(f.ctx))
			require.Zero(t, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(f.ctx))
		})
	}
}
func TestPostgresIncidentEffectsLifecycleOwnership(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.rule(map[string]interface{}{"type": "assign", "assignee_id": f.actor.ID}, map[string]interface{}{"type": "change_status", "status": "in_progress"})
	require.NoError(t, f.engine.Deliver(f.ctx, f.event))
	item := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	require.Equal(t, f.actor.ID, item.AssigneeID)
	require.Equal(t, "in_progress", item.Status)
	require.Equal(t, 2, f.client.IncidentEvent.Query().CountX(f.ctx))
	require.NoError(t, f.engine.Deliver(f.ctx, f.event))
	require.Equal(t, item.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
	closed := "closed"
	_, err := f.svc.UpdateIncident(f.ctx, f.inc.ID, &dto.UpdateIncidentRequest{Status: &closed}, f.tenant.ID)
	require.Error(t, err)
	require.Equal(t, "in_progress", f.client.Ticket.GetX(f.ctx, item.ID).Status)
}

func TestPostgresIncidentEffectsMigrationPreservesLegacyAndGuardsOwnership(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	apply := migration.GetMigrationSQL("024_incident_rule_action_receipts")
	asset, err := os.ReadFile("../../migrations/024_incident_rule_action_receipts.sql")
	require.NoError(t, err)
	require.Equal(t, apply, string(asset))
	reset, err := os.ReadFile("../../migrations/024_incident_rule_action_receipts_dev_reset.sql")
	require.NoError(t, err)
	verify, err := os.ReadFile("../../migrations/024_incident_rule_action_receipts_verify.sql")
	require.NoError(t, err)
	rule := f.rule(metricAction("created"))
	// An existing non-event execution must remain history, without an invented key.
	legacy := f.client.IncidentRuleExecution.Create().SetTenantID(f.tenant.ID).SetRuleID(rule.ID).SetIncidentID(f.inc.ID).SetStatus("completed").SaveX(f.ctx)
	for range 2 {
		_, err = f.db.ExecContext(f.ctx, apply)
		require.NoError(t, err)
	}
	_, err = f.db.ExecContext(f.ctx, string(verify))
	require.NoError(t, err)
	require.NoError(t, f.client.Schema.Create(f.ctx), "a later Ent bootstrap must preserve durable schema")
	var deleteAction string
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT c.confdeltype::text FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=ANY(c.conkey) WHERE c.contype='f' AND c.conrelid='incident_rule_executions'::regclass AND a.attname='rule_id'`).Scan(&deleteAction))
	require.Equal(t, "r", deleteAction, "Ent rebootstrap must preserve the owning rule FK's RESTRICT action")
	_, err = f.db.ExecContext(f.ctx, string(verify))
	require.NoError(t, err)
	old := f.client.IncidentRuleExecution.GetX(f.ctx, legacy.ID)
	require.Empty(t, old.ExecutionKey)
	require.Zero(t, old.SourceEventID)
	require.Equal(t, "rule", old.ExecutionKind)
	_, err = f.db.ExecContext(f.ctx, "INSERT INTO incident_rule_executions(tenant_id,execution_kind,execution_key,status,started_at,created_at,updated_at) VALUES($1,'creation_event',$2,'running',now(),now(),now())", f.tenant.ID, f.event.EventID)
	require.Error(t, err, "rebootstrap must not allow a root without source/actor identity")

	require.NoError(t, f.engine.Deliver(f.ctx, f.event))
	execution := f.client.IncidentRuleExecution.Query().Where(incidentruleexecution.SourceEventID(f.event.ID), incidentruleexecution.ExecutionKind("rule")).OnlyX(f.ctx)
	_, err = f.db.ExecContext(f.ctx, "UPDATE incident_rule_executions SET frozen_actions='[]' WHERE id=$1", execution.ID)
	require.ErrorContains(t, err, "immutable")
	_, err = f.db.ExecContext(f.ctx, "INSERT INTO incident_rule_action_receipts(tenant_id,execution_id,action_index,completed_at) VALUES($1,$2,0,now())", f.tenant.ID, execution.ID)
	require.Error(t, err, "duplicate committed action must fail")
	_, err = f.db.ExecContext(f.ctx, "INSERT INTO incident_rule_action_receipts(tenant_id,execution_id,action_index,completed_at) VALUES($1,$2,1,now())", f.tenant.ID+1, execution.ID)
	require.Error(t, err, "foreign tenant receipt must fail")
	_, err = f.db.ExecContext(f.ctx, "UPDATE incident_rule_action_receipts SET tenant_id=tenant_id+1 WHERE execution_id=$1", execution.ID)
	require.ErrorContains(t, err, "immutable")
	root := f.client.IncidentRuleExecution.Query().Where(incidentruleexecution.ExecutionKind("creation_event")).OnlyX(f.ctx)
	_, err = f.db.ExecContext(f.ctx, "INSERT INTO incident_rule_action_receipts(tenant_id,execution_id,action_index,completed_at) VALUES($1,$2,0,now())", f.tenant.ID, root.ID)
	require.Error(t, err, "creation selection is not an executable rule action")
	_, err = f.db.ExecContext(f.ctx, "DELETE FROM incident_rules WHERE id=$1", rule.ID)
	require.Error(t, err, "frozen rule identity must not detach on delete")
	_, err = f.db.ExecContext(f.ctx, string(reset))
	require.ErrorContains(t, err, "empty execution history")
	// Force an orphaned legacy row in a transaction, then prove migration diagnoses
	// the data instead of filling an identity or dropping history.
	tx, err := f.db.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(f.ctx, "ALTER TABLE incident_rule_executions DISABLE TRIGGER USER")
	require.NoError(t, err)
	_, err = tx.ExecContext(f.ctx, "UPDATE incident_rule_executions SET tenant_id=tenant_id+999 WHERE id=$1", legacy.ID)
	require.NoError(t, err)
	_, err = tx.ExecContext(f.ctx, apply)
	require.ErrorContains(t, err, "orphan or cross-tenant")
}

func TestPostgresIncidentEffectsRejectUntrustedSource(t *testing.T) {
	for _, name := range []string{"actor", "channel", "fractional_id", "stored_payload", "tenant"} {
		t.Run(name, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			f.rule(metricAction("forbidden"))
			offered := *f.event
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal(offered.Payload, &payload))
			switch name {
			case "actor":
				f.actor.Update().SetActive(false).SaveX(f.ctx)
			case "channel":
				payload["channel"] = "connector"
			case "fractional_id":
				payload["incidentId"] = 1.5
			case "stored_payload":
				payload["actorId"] = f.actor.ID + 1
			case "tenant":
				offered.TenantID++
			}
			var err error
			offered.Payload, err = json.Marshal(payload)
			require.NoError(t, err)
			require.Error(t, f.engine.Deliver(f.ctx, &offered))
			require.Zero(t, f.client.IncidentMetric.Query().CountX(f.ctx))
			require.Zero(t, f.client.IncidentRuleExecution.Query().CountX(f.ctx))
		})
	}
}

func TestPostgresIncidentEffectsRLSUnderNonBypassRole(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.rule(metricAction("once"))
	require.NoError(t, f.engine.Deliver(f.ctx, f.event))
	require.NoError(t, f.client.Schema.Create(f.ctx), "RLS proof follows a repeated Ent bootstrap")
	tx, err := f.db.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	var schema string
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&schema))
	// Only transactional grants on this disposable schema; no roles are created.
	_, err = tx.ExecContext(f.ctx, "GRANT USAGE ON SCHEMA "+schema+" TO pg_monitor; GRANT SELECT ON ALL TABLES IN SCHEMA "+schema+" TO pg_monitor; GRANT INSERT,UPDATE ON incident_rule_executions,incident_rule_action_receipts TO pg_monitor; GRANT USAGE ON ALL SEQUENCES IN SCHEMA "+schema+" TO pg_monitor; SET LOCAL ROLE pg_monitor")
	require.NoError(t, err)
	var super, bypass bool
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&super, &bypass))
	require.False(t, super)
	require.False(t, bypass)
	var count int
	_, err = tx.ExecContext(f.ctx, "SELECT set_config('app.current_tenant',$1,true)", fmt.Sprint(f.tenant.ID))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT count(*) FROM incident_rule_action_receipts").Scan(&count))
	require.Equal(t, 1, count)
	_, err = tx.ExecContext(f.ctx, "SELECT set_config('app.current_tenant',$1,true)", fmt.Sprint(f.tenant.ID+1))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT count(*) FROM incident_rule_action_receipts").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT count(*) FROM incident_rule_executions").Scan(&count))
	require.Zero(t, count)
	result, err := tx.ExecContext(f.ctx, "UPDATE incident_rule_executions SET status='failed'")
	require.NoError(t, err)
	changed, err := result.RowsAffected()
	require.NoError(t, err)
	require.Zero(t, changed)
	_, err = tx.ExecContext(f.ctx, "SAVEPOINT denied_write")
	require.NoError(t, err)
	_, err = tx.ExecContext(f.ctx, "INSERT INTO incident_rule_executions(tenant_id,rule_id,status,started_at,created_at,updated_at) VALUES($1,1,'running',now(),now(),now())", f.tenant.ID)
	require.ErrorContains(t, err, "row-level security")
	_, err = tx.ExecContext(f.ctx, "ROLLBACK TO SAVEPOINT denied_write")
	require.NoError(t, err)
}

func TestPostgresIncidentEffectsMigrationUpgradesActualLegacyShape(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	rule := f.rule(metricAction("created"))
	reset, err := os.ReadFile("../../migrations/024_incident_rule_action_receipts_dev_reset.sql")
	require.NoError(t, err)
	tx, err := f.db.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(f.ctx, string(reset))
	require.NoError(t, err)
	_, err = tx.ExecContext(f.ctx, "INSERT INTO incident_rule_executions(tenant_id,rule_id,incident_id,status,started_at,created_at,updated_at) VALUES($1,$2,$3,'completed',now(),now(),now())", f.tenant.ID, rule.ID, f.inc.ID)
	require.NoError(t, err)
	for range 2 {
		_, err = tx.ExecContext(f.ctx, migration.GetMigrationSQL("024_incident_rule_action_receipts"))
		require.NoError(t, err)
	}
	var kind string
	var key sql.NullString
	var source sql.NullInt64
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT execution_kind,execution_key,source_event_id FROM incident_rule_executions").Scan(&kind, &key, &source))
	require.Equal(t, "rule", kind)
	require.False(t, key.Valid)
	require.False(t, source.Valid)
}

func TestPostgresIncidentEffectsResetDoesNotTouchSearchPathShadow(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	reset, err := os.ReadFile("../../migrations/024_incident_rule_action_receipts_dev_reset.sql")
	require.NoError(t, err)
	tx, err := f.db.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	var local string
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&local))
	shadow := local + "_shadow"
	_, err = tx.ExecContext(f.ctx, "DROP TABLE incident_rule_action_receipts; CREATE SCHEMA "+shadow+"; CREATE TABLE "+shadow+".incident_rule_action_receipts(id bigint); SET LOCAL search_path TO "+local+","+shadow)
	require.NoError(t, err)
	for range 2 {
		_, err = tx.ExecContext(f.ctx, string(reset))
		require.NoError(t, err)
	}
	var exists bool
	require.NoError(t, tx.QueryRowContext(f.ctx, "SELECT to_regclass($1) IS NOT NULL", shadow+".incident_rule_action_receipts").Scan(&exists))
	require.True(t, exists, "empty development reset must stay inside its local schema")
}

// A frozen domain rejection requires intervention, while storage failure must retain retry recovery.
func TestPostgresIncidentEffectsWorkerClassifiesActionFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		action    map[string]interface{}
		permanent bool
	}{
		{"closed", map[string]interface{}{"type": "change_status", "status": "closed"}, true},
		{"invalid_level", map[string]interface{}{"type": "escalate", "level": 0, "reason": "frozen"}, true},
		{"unknown_recipient", map[string]interface{}{"type": "notify", "channels": []string{"email"}, "recipients": []string{"missing@example.test"}}, true},
		{"storage", metricAction("retry"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			f.rule(tc.action)
			if !tc.permanent {
				f.client.IncidentMetric.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						return nil, errors.New("metric storage unavailable")
					})
				})
			}
			registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{f.engine})
			require.NoError(t, err)
			worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(f.client), service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 10 * time.Second, MaxAttempts: 5}, zap.NewNop().Sugar(), registry)
			require.NoError(t, err)
			require.NoError(t, worker.DispatchOnce(f.ctx))
			event := f.client.OutboxEvent.GetX(f.ctx, f.event.ID)
			if tc.permanent {
				require.Equal(t, "blocked", event.Status)
			} else {
				require.Equal(t, "pending", event.Status)
				require.Equal(t, 1, event.AttemptCount)
			}
			require.Zero(t, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
			require.Equal(t, "new", f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Status)
		})
	}
}
