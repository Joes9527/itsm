package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/connector"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/pkg/eventbus"
)

// JSONB may reorder object keys. Canonicalize digest input while preserving
// exact JSON numbers; source authorization still uses the original envelope.
func webhookJSONDigest(value interface{}) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var canonical interface{}
	if err = decoder.Decode(&canonical); err != nil {
		return "", err
	}
	return workitemmutation.Digest(canonical)
}

const WebhookDeliveryRequestedEventType = "webhook.event.delivery.requested"

type webhookTarget struct {
	Provider          string `json:"provider"`
	DestinationDigest string `json:"destinationDigest"`
}
type webhookDeliveryPayload struct {
	Source eventbus.Envelope `json:"source"`
	Target webhookTarget     `json:"target"`
}
type webhookIntentReceipt struct {
	EventID       string `json:"eventId"`
	PayloadDigest string `json:"payloadDigest"`
}
type webhookConsumptionReceipt struct {
	Source  eventbus.Envelope      `json:"source"`
	Intents []webhookIntentReceipt `json:"intents"`
}

// Only the destination digest is persisted; URLs and connector secrets are not
// copied into an event. A worker must compare the current destination before send.
func webhookTargetFromConfig(cfg connector.Config) (webhookTarget, error) {
	endpoint, ok := cfg.Settings["url"].(string)
	parsed, err := url.Parse(endpoint)
	if !ok || err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return webhookTarget{}, fmt.Errorf("webhook destination unavailable")
	}
	digest, err := workitemmutation.Digest(endpoint)
	return webhookTarget{Provider: cfg.Provider, DestinationDigest: digest}, err
}

func (s *WebhookEventSubscriber) consumeExecutionWebhook(ctx context.Context, event interface{}) error {
	if s.client == nil {
		return executionscope.ErrDenied
	}
	env, ok := event.(eventbus.Envelope)
	if !ok {
		return fmt.Errorf("candidate webhook requires the complete typed envelope")
	}
	wire, err := json.Marshal(env)
	if err != nil {
		return err
	}
	env, err = eventbus.DecodeExecutionEnvelope(wire)
	if err != nil {
		return err
	}
	known := false
	for _, topic := range WebhookEventTopics() {
		if topic == env.EventType {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unsupported webhook event type")
	}
	tenantID, err := strconv.Atoi(env.TenantID)
	if err != nil || tenantID <= 0 || strconv.Itoa(tenantID) != env.TenantID {
		return executionscope.ErrDenied
	}
	if id, ok := tenantctx.TenantID(ctx); ok && id != tenantID {
		return executionscope.ErrDenied
	}
	if tenantctx.IsSystemBypass(ctx) {
		return executionscope.ErrDenied
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ref := executionscope.Ref{DeploymentID: env.Execution.DeploymentID, ScopeID: env.Execution.ScopeID, TenantID: tenantID}
	if err = NewExecutionEventAuthority(s.client, s.policy).ValidateEventTx(ctx, tx, ref, env); err != nil {
		return err
	}
	var compact bytes.Buffer
	if err = json.Compact(&compact, env.Payload); err != nil {
		return err
	}
	env.Payload = compact.Bytes()
	env.OccurredAt = env.OccurredAt.UTC()
	sourceDigest, err := webhookJSONDigest(env)
	if err != nil {
		return err
	}
	operationID := "webhook_consume:" + env.EventID
	receipt, err := tx.AuditLog.Query().Where(auditlog.TenantID(tenantID), auditlog.UserID(0), auditlog.OperationID(operationID)).Only(ctx)
	if err == nil {
		if receipt.Resource != "event" || receipt.Action != "webhook.enqueued" || receipt.Path != "eventbus://"+env.EventType || receipt.Method != "CONSUME" || receipt.StatusCode != 202 || receipt.RequestDigest == nil || *receipt.RequestDigest != sourceDigest || receipt.ResultStatus == nil || *receipt.ResultStatus != "enqueued" || receipt.ResultVersion != nil || receipt.RequestBody == nil {
			return fmt.Errorf("webhook consumption receipt conflicts")
		}
		var saved webhookConsumptionReceipt
		if err = json.Unmarshal([]byte(*receipt.RequestBody), &saved); err != nil {
			return err
		}
		digest, e := webhookJSONDigest(saved.Source)
		if e != nil || digest != sourceDigest || len(saved.Intents) == 0 {
			return fmt.Errorf("webhook consumption receipt source conflicts")
		}
		seen := map[string]bool{}
		for _, intent := range saved.Intents {
			if seen[intent.EventID] {
				return fmt.Errorf("duplicate webhook receipt intent")
			}
			seen[intent.EventID] = true
			row, e := tx.OutboxEvent.Query().Where(outboxevent.TenantIDEQ(tenantID), outboxevent.EventIDEQ(intent.EventID), outboxevent.EventTypeEQ(WebhookDeliveryRequestedEventType), outboxevent.ExecutionWorkItemIDEQ(env.Execution.WorkItemID)).Only(ctx)
			if e != nil {
				return e
			}
			var payload webhookDeliveryPayload
			if e = json.Unmarshal(row.Payload, &payload); e != nil {
				return e
			}
			actual, e := webhookJSONDigest(row.Payload)
			if e != nil || actual != intent.PayloadDigest || row.EventID != "webhook-delivery:"+actual || row.AggregateType != "work_item" || row.AggregateID != fmt.Sprint(env.Execution.WorkItemID) {
				return fmt.Errorf("webhook intent content conflicts")
			}
			actualSource, e := webhookJSONDigest(payload.Source)
			if e != nil || actualSource != sourceDigest {
				return fmt.Errorf("webhook intent source conflicts")
			}
		}
		return nil
	}
	if !ent.IsNotFound(err) {
		return err
	}
	configs := s.manager.ListByTenant(tenantID)
	sort.Slice(configs, func(i, j int) bool { return configs[i].Provider < configs[j].Provider })
	saved := webhookConsumptionReceipt{Source: env}
	for _, cfg := range configs {
		if cfg.Name != "webhook" || !cfg.Enabled {
			continue
		}
		target, e := webhookTargetFromConfig(cfg)
		if e != nil {
			return e
		}
		payload := webhookDeliveryPayload{Source: env, Target: target}
		data, e := json.Marshal(payload)
		if e != nil {
			return e
		}
		digest, e := webhookJSONDigest(payload)
		if e != nil {
			return e
		}
		eventID := "webhook-delivery:" + digest
		_, e = enqueueOutboxEvent(ctx, tx.Client(), tx, NewOutboxEvent{TenantID: tenantID, ExecutionWorkItemID: env.Execution.WorkItemID, EventID: eventID, EventType: WebhookDeliveryRequestedEventType, AggregateType: "work_item", AggregateID: fmt.Sprint(env.Execution.WorkItemID), Payload: data})
		if e != nil {
			return e
		}
		saved.Intents = append(saved.Intents, webhookIntentReceipt{EventID: eventID, PayloadDigest: digest})
	}
	if len(saved.Intents) == 0 {
		return fmt.Errorf("webhook event has no configured target")
	}
	body, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	_, err = tx.AuditLog.Create().SetTenantID(tenantID).SetUserID(0).SetOperationID(operationID).SetResource("event").SetAction("webhook.enqueued").SetPath("eventbus://" + env.EventType).SetMethod("CONSUME").SetStatusCode(202).SetRequestDigest(sourceDigest).SetResultStatus("enqueued").SetRequestBody(string(body)).Save(ctx)
	if err != nil {
		return err
	}
	return tx.Commit()
}
