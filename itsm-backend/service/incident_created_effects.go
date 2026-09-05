package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/incidentrule"
	"itsm-backend/ent/incidentruleactionreceipt"
	"itsm-backend/ent/incidentruleexecution"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
)

// The existing engine is the creation consumer. Registration remains a bootstrap
// decision; it is replay safe only because every action commits with its receipt.
func (*IncidentRuleEngine) EventType() string { return "incident.created" }
func (*IncidentRuleEngine) ReplaySafe() bool  { return true }
func (e *IncidentRuleEngine) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	return e.ExecuteCreatedEvent(ctx, event)
}

type incidentCreatedPayload struct {
	TenantID   int    `json:"tenantId"`
	IncidentID int    `json:"incidentId"`
	WorkItemID int    `json:"workItemId"`
	ActorID    int    `json:"actorId"`
	Channel    string `json:"channel"`
}

func validateIncidentCreatedEvent(ctx context.Context, client *ent.Client, event *ent.OutboxEvent) (incidentCreatedPayload, error) {
	var p incidentCreatedPayload
	if event == nil || event.ID <= 0 || event.EventType != "incident.created" {
		return p, blockOutboxDelivery("invalid incident creation event")
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, blockOutboxDelivery("malformed incident creation payload")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return p, blockOutboxDelivery("malformed incident creation payload")
	}
	if p.TenantID <= 0 || p.IncidentID <= 0 || p.WorkItemID <= 0 || p.ActorID <= 0 || p.Channel == "" || event.TenantID != p.TenantID || event.EventID != "incident-created:"+strconv.Itoa(p.WorkItemID) || event.AggregateType != "work_item" || event.AggregateID != strconv.Itoa(p.WorkItemID) {
		return p, blockOutboxDelivery("incident creation identity mismatch")
	}
	stored, err := client.OutboxEvent.Query().Where(outboxevent.ID(event.ID), outboxevent.TenantID(p.TenantID), outboxevent.EventID(event.EventID), outboxevent.EventType(event.EventType), outboxevent.AggregateType(event.AggregateType), outboxevent.AggregateID(event.AggregateID)).Only(ctx)
	if err != nil {
		return p, workflowStartReferenceError(err, "incident creation event")
	}
	var durable incidentCreatedPayload
	durableDecoder := json.NewDecoder(bytes.NewReader(stored.Payload))
	durableDecoder.DisallowUnknownFields()
	if err := durableDecoder.Decode(&durable); err != nil {
		return p, blockOutboxDelivery("malformed durable incident creation payload")
	}
	if durableDecoder.Decode(&extra) != io.EOF || durable != p {
		return p, blockOutboxDelivery("incident creation payload differs from durable source")
	}
	_, err = client.Incident.Query().Where(incident.ID(p.IncidentID), incident.WorkItemID(p.WorkItemID), incidentTenantScope(p.TenantID, ticket.RecordClass("incident"))).Only(ctx)
	if err != nil {
		return p, workflowStartReferenceError(err, "incident")
	}
	// The completed intake receipt is the authorization provenance. Rules execute
	// as tenant policy, not as an end user's discretionary Incident mutation.
	exists, err := client.IntakeRequest.Query().Where(intakerequest.TenantID(p.TenantID), intakerequest.WorkItemID(p.WorkItemID), intakerequest.ActorID(p.ActorID), intakerequest.Channel(p.Channel), intakerequest.Status("completed")).Exist(ctx)
	if err != nil {
		return p, err
	}
	if !exists {
		return p, blockOutboxDelivery("incident creation has no matching authorized intake receipt")
	}
	exists, err = client.User.Query().Where(user.ID(p.ActorID), user.TenantID(p.TenantID), user.Active(true)).Exist(ctx)
	if err != nil {
		return p, err
	}
	if !exists {
		return p, blockOutboxDelivery("incident creation actor is not active in tenant")
	}
	return p, nil
}

func (e *IncidentRuleEngine) ExecuteCreatedEvent(ctx context.Context, event *ent.OutboxEvent) error {
	if e.client == nil {
		return fmt.Errorf("incident rule engine database unavailable")
	}
	p, err := validateIncidentCreatedEvent(ctx, e.client, event)
	if err != nil {
		return err
	}
	executions, err := e.freezeCreatedRules(ctx, event, p)
	if err != nil {
		return err
	}
	for _, execution := range executions {
		if execution.ExecutionKind == "creation_event" {
			continue
		}
		if err := e.resumeCreatedRule(ctx, event, p, execution.ID); err != nil {
			return err
		}
	}
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	root, err := tx.IncidentRuleExecution.Query().Where(incidentruleexecution.TenantID(p.TenantID), incidentruleexecution.ExecutionKey(event.EventID)).Only(ctx)
	if err != nil {
		return err
	}
	pending, err := tx.IncidentRuleExecution.Query().Where(incidentruleexecution.TenantID(p.TenantID), incidentruleexecution.SourceEventID(event.ID), incidentruleexecution.ExecutionKind("rule"), incidentruleexecution.StatusNotIn("completed", "skipped")).Exist(ctx)
	if err != nil {
		return err
	}
	if pending {
		return fmt.Errorf("incident creation actions remain incomplete")
	}
	_, err = tx.IncidentRuleExecution.UpdateOneID(root.ID).Where(incidentruleexecution.TenantID(p.TenantID)).SetStatus("completed").SetResult("creation required actions completed").SetCompletedAt(time.Now()).Save(ctx)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Freeze the complete ordered candidate set, including the empty set. Each
// candidate's condition is resolved once just before that rule starts, retaining
// the existing rule-order semantics when earlier rules change incident state.
func (e *IncidentRuleEngine) freezeCreatedRules(ctx context.Context, event *ent.OutboxEvent, p incidentCreatedPayload) ([]*ent.IncidentRuleExecution, error) {
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// An UPDATE is a portable row lock. It never alters lease/token/status fields.
	_, err = tx.OutboxEvent.UpdateOneID(event.ID).Where(outboxevent.TenantID(p.TenantID)).SetUpdatedAt(time.Now()).Save(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = validateIncidentCreatedEvent(ctx, tx.Client(), event); err != nil {
		return nil, err
	}
	exists, err := tx.IncidentRuleExecution.Query().Where(incidentruleexecution.TenantID(p.TenantID), incidentruleexecution.ExecutionKey(event.EventID)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		_, err = tx.IncidentRuleExecution.Create().SetTenantID(p.TenantID).SetIncidentID(p.IncidentID).SetSourceEventID(event.ID).SetActorID(p.ActorID).SetSource(p.Channel).SetExecutionKey(event.EventID).SetExecutionKind("creation_event").SetStatus("running").SetResult("creation rule selection frozen").Save(ctx)
		if err != nil {
			return nil, err
		}
		rules, err := tx.IncidentRule.Query().Where(incidentrule.TenantID(p.TenantID), incidentrule.IsActive(true)).Order(ent.Asc(incidentrule.FieldID)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, rule := range rules {
			_, err = tx.IncidentRuleExecution.Create().SetTenantID(p.TenantID).SetRuleID(rule.ID).SetIncidentID(p.IncidentID).SetSourceEventID(event.ID).SetActorID(p.ActorID).SetSource(p.Channel).SetExecutionKey(fmt.Sprintf("%s:rule:%d", event.EventID, rule.ID)).SetFrozenActions(rule.Actions).SetInputData(map[string]interface{}{"conditions": rule.Conditions}).SetStatus("pending").Save(ctx)
			if err != nil {
				return nil, err
			}
		}
	}
	executions, err := tx.IncidentRuleExecution.Query().Where(incidentruleexecution.TenantID(p.TenantID), incidentruleexecution.SourceEventID(event.ID)).Order(ent.Asc(incidentruleexecution.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return executions, nil
}

func (e *IncidentRuleEngine) resumeCreatedRule(ctx context.Context, event *ent.OutboxEvent, p incidentCreatedPayload, id int) error {
	for {
		done, err := e.applyNextCreatedAction(ctx, event, p, id)
		if err != nil {
			// Errors stay visible without overwriting a concurrent completed execution.
			_, recordErr := e.client.IncidentRuleExecution.Update().Where(incidentruleexecution.ID(id), incidentruleexecution.TenantID(p.TenantID), incidentruleexecution.SourceEventID(event.ID), incidentruleexecution.StatusIn("running", "failed")).SetStatus("failed").SetErrorMessage(err.Error()).Save(ctx)
			if recordErr != nil {
				return fmt.Errorf("record rule failure: %v (action: %w)", recordErr, err)
			}
			return err
		}
		if done {
			return nil
		}
	}
}

func (e *IncidentRuleEngine) applyNextCreatedAction(ctx context.Context, event *ent.OutboxEvent, p incidentCreatedPayload, id int) (bool, error) {
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	execution, err := tx.IncidentRuleExecution.UpdateOneID(id).Where(incidentruleexecution.TenantID(p.TenantID), incidentruleexecution.SourceEventID(event.ID), incidentruleexecution.IncidentID(p.IncidentID), incidentruleexecution.ActorID(p.ActorID), incidentruleexecution.Source(p.Channel)).SetUpdatedAt(time.Now()).Save(ctx)
	if err != nil {
		return false, err
	}
	if execution.Status == "completed" || execution.Status == "skipped" {
		return true, tx.Commit()
	}
	if _, err := validateIncidentCreatedEvent(ctx, tx.Client(), event); err != nil {
		return false, err
	}
	ownerExists, err := tx.IncidentRule.Query().Where(incidentrule.ID(execution.RuleID), incidentrule.TenantID(p.TenantID)).Exist(ctx)
	if err != nil {
		return false, err
	}
	if !ownerExists {
		return false, blockOutboxDelivery("rule owner no longer belongs to tenant")
	}

	current, err := tx.Incident.Query().Where(incident.ID(p.IncidentID), incidentTenantScope(p.TenantID)).WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return false, err
	}
	if execution.Status == "pending" {
		conditions, ok := execution.InputData["conditions"].(map[string]interface{})
		if !ok && execution.InputData["conditions"] != nil {
			return false, blockOutboxDelivery("invalid frozen rule conditions")
		}
		parsed, parseErr := e.parseConditions(conditions)
		met := true
		if parseErr == nil {
			for _, condition := range parsed {
				var matches bool
				matches, parseErr = condition.Evaluate(ctx, current)
				if parseErr != nil {
					break
				}
				if !matches {
					met = false
					break
				}
			}
		}
		if parseErr != nil {
			_, err = tx.IncidentRuleExecution.UpdateOneID(id).SetStatus("failed").SetErrorMessage(parseErr.Error()).Save(ctx)
			if err != nil {
				return false, err
			}
			if err = tx.Commit(); err != nil {
				return false, err
			}
			return false, blockOutboxDelivery("invalid frozen rule conditions: " + parseErr.Error())
		}
		status := "running"
		if !met {
			status = "skipped"
		}
		_, err = tx.IncidentRuleExecution.UpdateOneID(id).SetStatus(status).SetOutputData(map[string]interface{}{"conditionsMet": met}).Save(ctx)
		if err != nil {
			return false, err
		}
		// Persist the decision before starting an action so any later retry cannot
		// evaluate a changed incident or clock against this rule again.
		return !met, tx.Commit()
	}
	if met, ok := execution.OutputData["conditionsMet"].(bool); !ok || !met {
		return false, blockOutboxDelivery("rule decision is not executable")
	}
	actions, err := e.parseActions(execution.FrozenActions)
	if err != nil {
		return false, blockOutboxDelivery("invalid frozen rule actions: " + err.Error())
	}
	receipts, err := tx.IncidentRuleActionReceipt.Query().Where(incidentruleactionreceipt.TenantID(p.TenantID), incidentruleactionreceipt.ExecutionID(id)).Order(ent.Asc(incidentruleactionreceipt.FieldActionIndex)).All(ctx)
	if err != nil {
		return false, err
	}
	for index, receipt := range receipts {
		if receipt.ActionIndex != index {
			return false, blockOutboxDelivery("incident rule action receipt sequence is invalid")
		}
	}
	if len(receipts) > len(actions) {
		return false, blockOutboxDelivery("incident rule action receipt exceeds frozen actions")
	}
	if len(receipts) == len(actions) {
		_, err = tx.IncidentRuleExecution.UpdateOneID(id).SetStatus("completed").ClearErrorMessage().SetCompletedAt(time.Now()).Save(ctx)
		if err != nil {
			return false, err
		}
		count, err := tx.IncidentRule.Update().Where(incidentrule.ID(execution.RuleID), incidentrule.TenantID(p.TenantID)).AddExecutionCount(1).SetLastExecutedAt(time.Now()).Save(ctx)
		if err != nil {
			return false, err
		}
		if count != 1 {
			return false, blockOutboxDelivery("rule owner no longer belongs to tenant")
		}
		return true, tx.Commit()
	}
	index := len(receipts)
	actionKey := fmt.Sprintf("%s:action:%d", execution.ExecutionKey, index)
	actionCtx := WithIncidentAlertActor(ctx, p.ActorID, "incident_rule", actionKey)
	if err = actions[index].ExecuteTx(actionCtx, tx, current, p.TenantID); err != nil {
		var rejected *incidentActionRejection
		if errors.As(err, &rejected) {
			return false, blockOutboxDelivery(rejected.Error())
		}
		return false, err
	}
	if _, err = tx.IncidentRuleActionReceipt.Create().SetTenantID(p.TenantID).SetExecutionID(id).SetActionIndex(index).Save(ctx); err != nil {
		return false, err
	}
	_, err = tx.AuditLog.Create().SetTenantID(p.TenantID).SetUserID(p.ActorID).SetRequestID(actionKey).SetResource("incident_rule_action").SetAction("incident_rule.action_completed").SetPath("incidents/automation").SetMethod("OUTBOX").SetStatusCode(200).Save(ctx)
	if err != nil {
		return false, err
	}
	_, err = tx.IncidentRuleExecution.UpdateOneID(id).SetStatus("running").ClearErrorMessage().Save(ctx)
	if err != nil {
		return false, err
	}
	return false, tx.Commit()
}
