package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/service/bpmn"

	"github.com/google/uuid"
)

type kafDelegationOutbox interface {
	Enqueue(context.Context, *ent.Tx, NewOutboxEvent) (*ent.OutboxEvent, error)
}

// KafDelegationService owns the all-or-nothing persistence boundary for a
// KAF BPMN delegation. Transport is intentionally deferred to the outbox
// dispatcher after the database transaction commits.
type KafDelegationService struct {
	client *ent.Client
	outbox kafDelegationOutbox
	now    func() time.Time
}

func NewKafDelegationService(client *ent.Client) *KafDelegationService {
	return &KafDelegationService{
		client: client,
		outbox: NewOutboxEventRepository(client),
		now:    time.Now,
	}
}

// CreateDelegatedTask creates the BPMN wait state, its audit record, and its
// delivery event in one transaction. Any failed write rolls back all three.
func (s *KafDelegationService) CreateDelegatedTask(ctx context.Context, instanceID int, serviceTask *BPMNServiceTask) (*ent.ProcessTask, error) {
	if serviceTask == nil {
		return nil, fmt.Errorf("KAF delegation service task is required")
	}
	if s.outbox == nil {
		return nil, fmt.Errorf("KAF delegation outbox repository is required")
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start KAF delegation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	instanceQuery := tx.ProcessInstance.Query().Where(processinstance.IDEQ(instanceID))
	if tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int); tenantID > 0 {
		instanceQuery = instanceQuery.Where(processinstance.TenantIDEQ(tenantID))
	}
	instance, err := instanceQuery.Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load KAF delegation process instance: %w", err)
	}

	task, err := tx.ProcessTask.Create().
		SetTaskID(newKafDelegationTaskID()).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey(serviceTask.ID).
		SetTaskName(serviceTask.Name).
		SetTaskType(bpmn.KafDelegateTaskType).
		SetStatus(common.ProcessTaskStatusDelegated).
		SetTaskVariables(map[string]interface{}{
			bpmnMetaDataServiceTaskType: bpmn.KafDelegateTaskType,
			bpmnMetaDataAllowedActions:  serviceTask.AllowedActions(),
		}).
		SetCorrelationID(newKafDelegationCorrelationID()).
		SetTenantID(instance.TenantID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("create KAF delegated process task: %w", err)
	}

	auditCreate := tx.AuditLog.Create().
		SetTenantID(instance.TenantID).
		SetResource("process_task").
		SetAction("kaf_delegate.created").
		SetPath("bpmn/kaf_delegate").
		SetMethod("SYSTEM").
		SetStatusCode(http.StatusCreated).
		SetRequestBody(fmt.Sprintf(`{"taskId":%q,"correlationId":%q}`, task.TaskID, task.CorrelationID))
	if actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int); actorID > 0 {
		auditCreate.SetUserID(actorID)
	}
	if err := auditCreate.Exec(ctx); err != nil {
		return nil, fmt.Errorf("create KAF delegation audit log: %w", err)
	}

	event, err := newKafDelegateOutboxEvent(task, instance, s.now())
	if err != nil {
		return nil, err
	}
	if _, err := s.outbox.Enqueue(ctx, tx, event); err != nil {
		return nil, fmt.Errorf("enqueue KAF delegation outbox event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit KAF delegation transaction: %w", err)
	}
	return task, nil
}

func newKafDelegationTaskID() string {
	return "TASK-" + uuid.NewString()
}

func newKafDelegationCorrelationID() string {
	return uuid.NewString()
}

func newKafDelegateOutboxEvent(task *ent.ProcessTask, instance *ent.ProcessInstance, occurredAt time.Time) (NewOutboxEvent, error) {
	recordClass, err := kafDelegationRecordClass(instance)
	if err != nil {
		return NewOutboxEvent{}, err
	}
	if instance.BusinessID <= 0 {
		return NewOutboxEvent{}, fmt.Errorf("KAF delegation process instance %d has no business ID", instance.ID)
	}

	event := KafDelegateRequested{
		EventType:   "kaf_delegate_requested",
		EventID:     uuid.NewString(),
		TenantID:    strconv.Itoa(instance.TenantID),
		WorkItemID:  strconv.Itoa(instance.BusinessID),
		TicketID:    strconv.Itoa(instance.BusinessID),
		TaskID:      task.TaskID,
		RecordClass: recordClass,
		Actor: KafDelegateActor{
			ID:          "bpmn:" + instance.ProcessInstanceID,
			Kind:        "system",
			DisplayName: "ITSM BPMN",
		},
		Timestamp:     occurredAt.UTC().Format(time.RFC3339),
		Version:       instance.Version,
		CorrelationID: task.CorrelationID,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return NewOutboxEvent{}, fmt.Errorf("marshal KAF delegation outbox event: %w", err)
	}
	return NewOutboxEvent{
		EventID:       event.EventID,
		EventType:     event.EventType,
		TenantID:      instance.TenantID,
		AggregateType: "process_task",
		AggregateID:   task.TaskID,
		Payload:       payload,
	}, nil
}

func kafDelegationRecordClass(instance *ent.ProcessInstance) (string, error) {
	switch instance.BusinessType {
	case "incident":
		return "incident", nil
	case "service_request", "service_request_item":
		return "service_request_item", nil
	case "ticket":
		if recordClass, _ := instance.Variables["record_class"].(string); recordClass == "incident" || recordClass == "service_request_item" {
			return recordClass, nil
		}
	}
	return "", fmt.Errorf("unsupported KAF delegation record class %q", instance.BusinessType)
}
