package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/ticket"
	"itsm-backend/service/bpmn"
)

// WorkflowDefinitionStarter uses the engine's durable, exact-definition start.
// Delivery receipt state and leases are owned by the shared Outbox worker.
type WorkflowDefinitionStarter interface {
	StartProcessByDefinitionID(context.Context, ProcessDefinitionIdentity, string, string, int, map[string]interface{}, string) (*ent.ProcessInstance, error)
}
type WorkflowStartOutboxHandler struct {
	client    *ent.Client
	directory *ent.Client
	engine    WorkflowDefinitionStarter
}

func NewWorkflowStartOutboxHandler(client *ent.Client, engine WorkflowDefinitionStarter, directory *ent.Client) *WorkflowStartOutboxHandler {
	return &WorkflowStartOutboxHandler{client: client, engine: engine, directory: directory}
}
func (*WorkflowStartOutboxHandler) EventType() string { return "workflow.start.requested" }

func (*WorkflowStartOutboxHandler) ReplaySafe() bool { return true }

type workflowStartPayload struct {
	DefinitionDigest  string         `json:"workflowDefinitionDigest"`
	Variables         map[string]any `json:"variables"`
	TenantID          int            `json:"tenantId"`
	WorkItemID        int            `json:"workItemId"`
	RecordClass       string         `json:"recordClass"`
	DefinitionID      int            `json:"workflowDefinitionId"`
	DefinitionKey     string         `json:"workflowDefinitionKey"`
	DefinitionVersion string         `json:"workflowDefinitionVersion"`
	ActorID           int            `json:"actorId"`
	Channel           string         `json:"channel"`
	IntakeRequestID   int            `json:"intakeRequestId"`
	DedupeKey         string         `json:"dedupeKey"`
}

func (h *WorkflowStartOutboxHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	if h.client == nil || h.engine == nil {
		return fmt.Errorf("workflow start dependencies unavailable")
	}
	if event == nil || event.EventType != h.EventType() {
		return blockOutboxDelivery("invalid workflow start event")
	}
	var p workflowStartPayload
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&p); err != nil {
		return blockOutboxDelivery("malformed workflow start payload")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return blockOutboxDelivery("malformed workflow start payload")
	}
	key := fmt.Sprintf("workflow-start:%d:%d", p.WorkItemID, p.DefinitionID)
	if p.TenantID <= 0 || p.WorkItemID <= 0 || p.ActorID <= 0 || p.IntakeRequestID <= 0 || p.DefinitionID <= 0 || p.Channel == "" || p.DefinitionKey == "" || p.DefinitionVersion == "" || len(p.DefinitionDigest) != 64 || event.TenantID != p.TenantID || event.EventID != key || p.DedupeKey != key || event.AggregateType != "work_item" || event.AggregateID != strconv.Itoa(p.WorkItemID) {
		return blockOutboxDelivery("workflow start identity mismatch")
	}
	item, err := h.client.Ticket.Query().Where(ticket.ID(p.WorkItemID), ticket.TenantID(p.TenantID), ticket.RecordClass(p.RecordClass)).Only(ctx)
	if err != nil {
		return workflowStartReferenceError(err, "work item")
	}
	receipt, err := h.client.IntakeRequest.Query().Where(intakerequest.ID(p.IntakeRequestID), intakerequest.TenantID(p.TenantID), intakerequest.ActorID(p.ActorID), intakerequest.Channel(p.Channel), intakerequest.Status("completed"), intakerequest.WorkItemID(p.WorkItemID)).Only(ctx)
	if err != nil {
		return workflowStartReferenceError(err, "intake receipt")
	}
	snapshot, err := h.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.IntakeRequestID(receipt.ID), intakeresolutionsnapshot.TenantID(p.TenantID), intakeresolutionsnapshot.WorkItemID(item.ID)).Only(ctx)
	if err != nil {
		return workflowStartReferenceError(err, "resolution snapshot")
	}
	if snapshot.NoProcess || snapshot.WorkflowDefinitionID == nil || *snapshot.WorkflowDefinitionID != p.DefinitionID || snapshot.WorkflowDefinitionKey != p.DefinitionKey || snapshot.WorkflowDefinitionVersion != p.DefinitionVersion || snapshot.RecordClass != p.RecordClass || snapshot.Channel != p.Channel || snapshot.RequestDigest != receipt.RequestDigest {
		return blockOutboxDelivery("workflow start does not match frozen resolution")
	}
	if p.Variables == nil {
		return blockOutboxDelivery("frozen workflow variables missing")
	}
	for key, want := range map[string]string{"work_item_id": strconv.Itoa(item.ID), "tenant_id": strconv.Itoa(item.TenantID), "record_class": item.RecordClass, "requester_id": strconv.Itoa(receipt.RequesterID), "triggered_by": strconv.Itoa(receipt.ActorID), "channel": receipt.Channel} {
		if fmt.Sprint(p.Variables[key]) != want {
			return blockOutboxDelivery("frozen workflow variable identity mismatch")
		}
	}
	policy, err := authorization.ResolveWorkItemPolicy(item.RecordClass)
	if err != nil {
		return blockOutboxDelivery("unsupported workflow business identity")
	}
	actor, err := loadIntakeActor(ctx, h.directory, receipt)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, intakeStartActorKey{}, intakeStartActor{actor: *actor, targetTenantID: p.TenantID, workItemID: item.ID, receiptID: receipt.ID})
	p.Variables["actor_tenant_id"] = receipt.ActorTenantID
	p.Variables["intake_request_id"] = receipt.ID
	ctx = WithTrustedBPMNTenantContext(ctx, p.TenantID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, p.ActorID)
	_, err = h.engine.StartProcessByDefinitionID(ctx, ProcessDefinitionIdentity{ID: p.DefinitionID, Key: p.DefinitionKey, Version: p.DefinitionVersion, Digest: p.DefinitionDigest}, fmt.Sprintf("%s:%d", policy.BusinessType, item.ID), string(policy.BusinessType), item.ID, p.Variables, key)
	var conflict *processStartConflictError
	var definitionErr *processStartDefinitionError
	if errors.As(err, &conflict) || errors.As(err, &definitionErr) {
		return blockOutboxDelivery(err.Error())
	}
	return err
}
func workflowStartReferenceError(err error, reference string) error {
	if ent.IsNotFound(err) {
		return blockOutboxDelivery("workflow start " + reference + " mismatch")
	}
	return fmt.Errorf("load workflow start %s: %w", reference, err)
}
