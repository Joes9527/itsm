package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/kaftaskactionledger"
	"itsm-backend/ent/kaftaskcompletionreceipt"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
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

type kafDelegatedTaskCompletionEngine interface {
	CompleteKafDelegatedTask(ctx context.Context, ledgerID int, taskID string, variables map[string]interface{}) error
}

type kafActionLedgerContextKey struct{}

var (
	ErrKafDelegationForbidden     = errors.New("KAF delegation access is forbidden")
	ErrKafDelegationNotFound      = errors.New("KAF delegated task was not found")
	ErrKafDelegationInvalidCursor = errors.New("KAF delegated task cursor is invalid")
	ErrKafActionInvalid           = errors.New("KAF action is invalid")
	ErrKafActionConflict          = errors.New("KAF action version conflict")
)

const (
	kafActionComplete = "complete_bpmn_task"
	kafActionProgress = "update_progress"
	kafActionFailure  = "record_execution_failure"
	kafActionLeaseTTL = 30 * time.Second

	KafActionApplied        = "applied"
	KafActionAlreadyApplied = "already_applied"
)

// KafTaskContext is the only task-scoped data KAF may retrieve before it runs
// a delegated procedure. Tenant and business identifiers are derived from the
// authenticated task, never from request input.
type KafTaskContext struct {
	TaskID          string                 `json:"taskId"`
	TaskType        string                 `json:"taskType"`
	Status          string                 `json:"status"`
	CorrelationID   string                 `json:"correlationId"`
	TenantID        string                 `json:"tenantId"`
	RecordClass     string                 `json:"recordClass"`
	AllowedActions  []string               `json:"allowedActions"`
	ExpectedVersion int                    `json:"expectedVersion"`
	WaitingPoint    KafWaitingPoint        `json:"waitingPoint"`
	IntakeSnapshot  map[string]interface{} `json:"intakeSnapshot"`
	WorkItem        KafWorkItem            `json:"workItem"`
	Attachments     []KafAttachmentRef     `json:"attachments"`
}

type KafWaitingPoint struct {
	ProcessInstanceID string `json:"processInstanceId"`
	ProcessDefinition string `json:"processDefinition"`
	ActivityID        string `json:"activityId"`
	ActivityName      string `json:"activityName"`
}

type KafWorkItem struct {
	ID          int    `json:"id"`
	RecordClass string `json:"recordClass"`
	Title       string `json:"title,omitempty"`
	Priority    string `json:"priority,omitempty"`
	Status      string `json:"status,omitempty"`
}

// KafAttachmentRef intentionally omits storage paths, signed URLs and names
// that can expose sensitive content. The first delivery phase only receives a
// stable reference and a display-safe label when one is available.
type KafAttachmentRef struct {
	ID   int    `json:"id"`
	Name string `json:"name,omitempty"`
}

type KafActionRequest struct {
	Action          string             `json:"action"`
	ExpectedVersion int                `json:"expectedVersion"`
	Execution       KafActionExecution `json:"execution"`
	Payload         KafActionPayload   `json:"payload"`
}

type KafActionExecution struct {
	RunID            string `json:"runId"`
	StepID           string `json:"stepId"`
	IdempotencyKey   string `json:"idempotencyKey"`
	CorrelationID    string `json:"correlationId"`
	ProcedureRef     string `json:"procedureRef"`
	ProcedureVersion string `json:"procedureVersion"`
}

type KafActionPayload struct {
	ResultSummary  string   `json:"resultSummary"`
	EvidenceRefs   []string `json:"evidenceRefs"`
	FailureSummary string   `json:"failureSummary"`
}

type KafActionResult struct {
	Action          string `json:"action"`
	IdempotencyKey  string `json:"idempotencyKey"`
	ResultStatus    string `json:"resultStatus"`
	ExpectedVersion int    `json:"expectedVersion"`
}

// KafDelegatedTaskPage provides an opaque continuation token so recovery can
// drain a tenant's delegated work without an unbounded response.
type KafDelegatedTaskPage struct {
	Items      []KafTaskContext `json:"items"`
	Limit      int              `json:"limit"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type kafDelegatedTaskCursor struct {
	TaskID int `json:"taskId"`
}

func NewKafDelegationService(client *ent.Client) *KafDelegationService {
	return &KafDelegationService{
		client: client,
		outbox: NewOutboxEventRepository(client),
		now:    time.Now,
	}
}

// AuthorizeTask centralizes the exact KAF principal, tenant and delegated-state
// semantics previously owned by CustomProcessEngine. The engine intentionally
// uses this for every registered async handler, so task-type validation belongs
// to the KAF-only HTTP boundary rather than this shared authorization method.
func (s *KafDelegationService) AuthorizeTask(ctx context.Context, task *ent.ProcessTask) error {
	if task == nil {
		return fmt.Errorf("%w: task is required", ErrKafDelegationForbidden)
	}
	if err := s.authorizeActor(ctx, task); err != nil {
		return err
	}
	if task.Status != common.ProcessTaskStatusDelegated {
		return fmt.Errorf("%w: task status %q is not delegated", ErrKafDelegationForbidden, task.Status)
	}
	return nil
}

func requireKafDelegatedTaskType(task *ent.ProcessTask) error {
	if task == nil || task.TaskType != bpmn.KafDelegateTaskType {
		return fmt.Errorf("%w: task is not a KAF delegation", ErrKafDelegationForbidden)
	}
	return nil
}

func (s *KafDelegationService) authorizeActor(ctx context.Context, task *ent.ProcessTask) error {
	userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if userID <= 0 {
		return fmt.Errorf("%w: authenticated KAF automation actor is required", ErrKafDelegationForbidden)
	}
	actor, err := s.client.User.Query().Where(user.IDEQ(userID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("%w: KAF automation actor does not exist", ErrKafDelegationForbidden)
	}
	if strings.ToLower(strings.TrimSpace(actor.Role)) != kafAutomationRole {
		return fmt.Errorf("%w: actor is not a KAF automation account", ErrKafDelegationForbidden)
	}
	if actor.TenantID != task.TenantID {
		return fmt.Errorf("%w: actor tenant does not match delegated task", ErrKafDelegationForbidden)
	}
	return nil
}

func (s *KafDelegationService) taskForTenant(ctx context.Context, taskID string) (*ent.ProcessTask, error) {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID <= 0 {
		return nil, fmt.Errorf("%w: tenant context is required", ErrKafDelegationForbidden)
	}
	task, err := s.client.ProcessTask.Query().
		Where(processtask.TaskIDEQ(taskID), processtask.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %s", ErrKafDelegationNotFound, taskID)
		}
		return nil, fmt.Errorf("load KAF delegated task: %w", err)
	}
	return task, nil
}

func (s *KafDelegationService) GetTaskContext(ctx context.Context, taskID string) (*KafTaskContext, error) {
	task, err := s.taskForTenant(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := requireKafDelegatedTaskType(task); err != nil {
		return nil, err
	}
	if err := s.AuthorizeTask(ctx, task); err != nil {
		return nil, err
	}
	instance, err := s.client.ProcessInstance.Query().
		Where(processinstance.IDEQ(task.ProcessInstanceID), processinstance.TenantIDEQ(task.TenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load KAF process instance: %w", err)
	}
	recordClass, err := kafDelegationRecordClass(ctx, s.client, instance)
	if err != nil {
		return nil, err
	}
	workItem := KafWorkItem{ID: instance.BusinessID, RecordClass: recordClass}
	if instance.BusinessID > 0 {
		item, itemErr := s.client.Ticket.Query().
			Where(ticket.IDEQ(instance.BusinessID), ticket.TenantIDEQ(task.TenantID), ticket.DeletedAtIsNil()).
			Only(ctx)
		if itemErr == nil {
			workItem.Title = item.Title
			workItem.Priority = item.Priority
			workItem.Status = item.Status
		}
	}
	return &KafTaskContext{
		TaskID: task.TaskID, TaskType: task.TaskType, Status: task.Status,
		CorrelationID: task.CorrelationID, TenantID: strconv.Itoa(task.TenantID), RecordClass: recordClass,
		AllowedActions: kafAllowedActions(task), ExpectedVersion: instance.Version,
		WaitingPoint:   KafWaitingPoint{ProcessInstanceID: instance.ProcessInstanceID, ProcessDefinition: instance.ProcessDefinitionKey, ActivityID: instance.CurrentActivityID, ActivityName: instance.CurrentActivityName},
		IntakeSnapshot: frozenKafIntakeSnapshot(instance.Variables), WorkItem: workItem,
		Attachments: []KafAttachmentRef{},
	}, nil
}

func (s *KafDelegationService) ListDelegatedTasks(ctx context.Context, limit int) ([]KafTaskContext, error) {
	page, err := s.ListDelegatedTaskPage(ctx, limit, "")
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (s *KafDelegationService) ListDelegatedTaskPage(ctx context.Context, limit int, cursor string) (*KafDelegatedTaskPage, error) {
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID <= 0 {
		return nil, fmt.Errorf("%w: tenant context is required", ErrKafDelegationForbidden)
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if err := s.authorizeListActor(ctx, tenantID); err != nil {
		return nil, err
	}
	decodedCursor, err := decodeKafDelegatedTaskCursor(cursor)
	if err != nil {
		return nil, err
	}
	predicates := []predicate.ProcessTask{
		processtask.TenantIDEQ(tenantID), processtask.TaskTypeEQ(bpmn.KafDelegateTaskType),
		processtask.StatusEQ(common.ProcessTaskStatusDelegated),
	}
	if decodedCursor != nil {
		predicates = append(predicates, processtask.IDGT(decodedCursor.TaskID))
	}
	tasks, err := s.client.ProcessTask.Query().Where(predicates...).
		Order(ent.Asc(processtask.FieldID)).Limit(limit + 1).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list KAF delegated tasks: %w", err)
	}
	page := &KafDelegatedTaskPage{Items: []KafTaskContext{}, Limit: limit}
	if len(tasks) > limit {
		page.NextCursor, err = encodeKafDelegatedTaskCursor(tasks[limit-1])
		if err != nil {
			return nil, err
		}
		tasks = tasks[:limit]
	}
	items := make([]KafTaskContext, 0, len(tasks))
	for _, task := range tasks {
		if err := s.AuthorizeTask(ctx, task); err != nil {
			return nil, err
		}
		item, err := s.GetTaskContext(ctx, task.TaskID)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	page.Items = items
	return page, nil
}

func decodeKafDelegatedTaskCursor(raw string) (*kafDelegatedTaskCursor, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed encoding", ErrKafDelegationInvalidCursor)
	}
	var cursor kafDelegatedTaskCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.TaskID <= 0 {
		return nil, fmt.Errorf("%w: malformed value", ErrKafDelegationInvalidCursor)
	}
	return &cursor, nil
}

func encodeKafDelegatedTaskCursor(task *ent.ProcessTask) (string, error) {
	encoded, err := json.Marshal(kafDelegatedTaskCursor{TaskID: task.ID})
	if err != nil {
		return "", fmt.Errorf("encode KAF delegated task cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func (s *KafDelegationService) authorizeListActor(ctx context.Context, tenantID int) error {
	return s.authorizeActor(ctx, &ent.ProcessTask{TaskType: bpmn.KafDelegateTaskType, TenantID: tenantID})
}

func (s *KafDelegationService) ExecuteAction(ctx context.Context, taskID string, req KafActionRequest, engine ProcessEngine) (*KafActionResult, error) {
	if err := validateKafAction(req); err != nil {
		return nil, err
	}
	task, err := s.taskForTenant(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := requireKafDelegatedTaskType(task); err != nil {
		return nil, err
	}
	if err := validateKafActionKey(task, req); err != nil {
		return nil, err
	}
	if err := s.authorizeKafActionTask(ctx, task, req.Action); err != nil {
		return nil, err
	}
	if !kafActionAllowed(task, req.Action) {
		return nil, fmt.Errorf("%w: action %q is not allowed for this delegated task", ErrKafActionInvalid, req.Action)
	}
	if task.CorrelationID != req.Execution.CorrelationID {
		return nil, fmt.Errorf("%w: correlation ID does not match task", ErrKafActionInvalid)
	}
	if req.Action == kafActionComplete && engine == nil {
		return nil, fmt.Errorf("complete KAF BPMN task: process engine is required")
	}
	ledger, claimed, err := s.ClaimKafAction(ctx, task, req)
	if err != nil {
		return nil, err
	}
	result := KafActionResult{
		Action: req.Action, IdempotencyKey: req.Execution.IdempotencyKey,
		ResultStatus: KafActionAlreadyApplied, ExpectedVersion: req.ExpectedVersion,
	}
	if !claimed {
		if ledger.ResultStatus != "applied" {
			return nil, fmt.Errorf("%w: action is not claimable", ErrKafActionConflict)
		}
		return &result, nil
	}
	if reconciled, err := s.reconcileCompletedKafAction(ctx, task, req, ledger, engine); err != nil {
		return nil, err
	} else if reconciled {
		return &result, nil
	}
	instance, err := s.client.ProcessInstance.Query().Where(
		processinstance.IDEQ(task.ProcessInstanceID), processinstance.TenantIDEQ(task.TenantID),
	).Only(ctx)
	if err != nil {
		_ = s.finalizeKafAction(ctx, ledger, "failed_retryable", "process_instance_load_failed")
		return nil, fmt.Errorf("load KAF action process instance: %w", err)
	}
	if instance.Version != req.ExpectedVersion {
		_ = s.finalizeKafAction(ctx, ledger, "failed_retryable", "expected_version_conflict")
		return nil, fmt.Errorf("%w: expected %d, current %d", ErrKafActionConflict, req.ExpectedVersion, instance.Version)
	}
	if req.Action == kafActionComplete {
		variables := cloneKafVariables(task.TaskVariables)
		variables["kaf_execution"] = map[string]string{
			"run_id": req.Execution.RunID, "step_id": req.Execution.StepID,
			"procedure_ref": req.Execution.ProcedureRef, "procedure_version": req.Execution.ProcedureVersion,
		}
		variables["kaf_result_summary"] = strings.TrimSpace(req.Payload.ResultSummary)
		completionEngine, ok := engine.(kafDelegatedTaskCompletionEngine)
		if !ok {
			_ = s.finalizeKafAction(ctx, ledger, "failed_terminal", "kaf_completion_engine_required")
			return nil, fmt.Errorf("complete delegated BPMN task: receipt-aware KAF completion engine is required")
		}
		completionCtx := context.WithValue(ctx, kafActionLedgerContextKey{}, ledger.ID)
		if err := completionEngine.CompleteKafDelegatedTask(completionCtx, ledger.ID, taskID, variables); err != nil {
			_ = s.finalizeKafAction(ctx, ledger, "failed_retryable", "bpmn_completion_failed")
			return nil, fmt.Errorf("complete delegated BPMN task: %w", err)
		}
	} else if err := s.persistNonCompletingAction(ctx, task, instance, req); err != nil {
		_ = s.finalizeKafAction(ctx, ledger, "failed_retryable", "action_persistence_failed")
		return nil, err
	}
	if err := s.recordActionAudit(ctx, task, req, http.StatusOK); err != nil {
		_ = s.finalizeKafAction(ctx, ledger, "failed_retryable", "audit_write_failed")
		return nil, fmt.Errorf("record KAF action audit: %w", err)
	}
	if err := s.finalizeKafAction(ctx, ledger, "applied", ""); err != nil {
		return nil, err
	}
	result.ResultStatus = KafActionApplied
	return &result, nil
}

func (s *KafDelegationService) authorizeKafActionTask(ctx context.Context, task *ent.ProcessTask, action string) error {
	if task.Status == common.ProcessTaskStatusDelegated {
		return s.AuthorizeTask(ctx, task)
	}
	if action == kafActionComplete && task.Status == common.ProcessTaskStatusCompleted {
		return s.authorizeActor(ctx, task)
	}
	return s.AuthorizeTask(ctx, task)
}

// reconcileCompletedKafAction resolves a prior KAF completion using its receipt
// and never invokes generic ProcessEngine.CompleteTask for an already-completed task.
func (s *KafDelegationService) reconcileCompletedKafAction(ctx context.Context, task *ent.ProcessTask, req KafActionRequest, ledger *ent.KafTaskActionLedger, engine ProcessEngine) (bool, error) {
	if req.Action != kafActionComplete {
		return false, nil
	}
	completedTask, err := s.client.ProcessTask.Query().Where(
		processtask.TaskIDEQ(task.TaskID), processtask.TenantIDEQ(task.TenantID),
	).Only(ctx)
	if err != nil {
		return false, fmt.Errorf("reconcile KAF completed task: %w", err)
	}
	if completedTask.Status != common.ProcessTaskStatusCompleted {
		return false, nil
	}
	if completedTask.CorrelationID != req.Execution.CorrelationID || !matchesKafExecution(completedTask.TaskVariables, req.Execution) {
		return false, fmt.Errorf("%w: completed task execution context does not match action scope", ErrKafActionInvalid)
	}
	receipt, err := s.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.LedgerIDEQ(ledger.ID),
	).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return false, fmt.Errorf("load KAF completion receipt: %w", err)
	}
	if ent.IsNotFound(err) || receipt.Status != "callback_succeeded" {
		completionEngine, ok := engine.(kafDelegatedTaskCompletionEngine)
		if !ok {
			return false, fmt.Errorf("recover delegated BPMN callback: receipt-aware KAF completion engine is required")
		}
		if err := completionEngine.CompleteKafDelegatedTask(context.WithValue(ctx, kafActionLedgerContextKey{}, ledger.ID), ledger.ID, task.TaskID, completedTask.TaskVariables); err != nil {
			return false, fmt.Errorf("recover delegated BPMN callback: %w", err)
		}
	}
	if err := s.finalizeKafAction(ctx, ledger, "applied", ""); err != nil {
		return false, err
	}
	return true, nil
}

func matchesKafExecution(variables map[string]interface{}, execution KafActionExecution) bool {
	value, ok := variables["kaf_execution"].(map[string]interface{})
	if !ok {
		return false
	}
	return value["run_id"] == execution.RunID && value["step_id"] == execution.StepID &&
		value["procedure_ref"] == execution.ProcedureRef && value["procedure_version"] == execution.ProcedureVersion
}

func validateKafAction(req KafActionRequest) error {
	if req.Action != kafActionComplete && req.Action != kafActionProgress && req.Action != kafActionFailure {
		return fmt.Errorf("%w: unsupported action", ErrKafActionInvalid)
	}
	if req.ExpectedVersion <= 0 || strings.TrimSpace(req.Execution.RunID) == "" || strings.TrimSpace(req.Execution.StepID) == "" ||
		strings.TrimSpace(req.Execution.IdempotencyKey) == "" || strings.TrimSpace(req.Execution.CorrelationID) == "" ||
		strings.TrimSpace(req.Execution.ProcedureRef) == "" || strings.TrimSpace(req.Execution.ProcedureVersion) == "" {
		return fmt.Errorf("%w: execution metadata is required", ErrKafActionInvalid)
	}
	if req.Action == kafActionComplete && strings.TrimSpace(req.Payload.ResultSummary) == "" {
		return fmt.Errorf("%w: resultSummary is required", ErrKafActionInvalid)
	}
	if req.Action == kafActionFailure && strings.TrimSpace(req.Payload.FailureSummary) == "" {
		return fmt.Errorf("%w: failureSummary is required", ErrKafActionInvalid)
	}
	return nil
}

func validateKafActionKey(task *ent.ProcessTask, req KafActionRequest) error {
	canonicalKey := fmt.Sprintf("%d:%s:%s:%s", task.TenantID, task.TaskID, req.Execution.RunID, req.Execution.StepID)
	if req.Execution.IdempotencyKey != canonicalKey {
		return fmt.Errorf("%w: idempotency key must equal execution scope", ErrKafActionInvalid)
	}
	return nil
}

// ClaimKafAction persists the immutable execution scope and atomically leases
// it for execution. A false claim is returned only for an already-applied row.
func (s *KafDelegationService) ClaimKafAction(ctx context.Context, task *ent.ProcessTask, req KafActionRequest) (*ent.KafTaskActionLedger, bool, error) {
	if task == nil {
		return nil, false, fmt.Errorf("%w: task is required", ErrKafActionInvalid)
	}
	if err := validateKafActionKey(task, req); err != nil {
		return nil, false, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("start KAF action claim transaction: %w", err)
	}
	ledger, err := tx.KafTaskActionLedger.Create().
		SetTenantID(task.TenantID).SetTaskID(task.TaskID).
		SetRunID(req.Execution.RunID).SetStepID(req.Execution.StepID).
		SetAction(req.Action).SetIdempotencyKey(req.Execution.IdempotencyKey).
		SetCorrelationID(req.Execution.CorrelationID).
		SetProcedureRef(req.Execution.ProcedureRef).SetProcedureVersion(req.Execution.ProcedureVersion).
		Save(ctx)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit KAF action claim transaction: %w", err)
		}
	} else {
		_ = tx.Rollback()
		if !ent.IsConstraintError(err) {
			return nil, false, fmt.Errorf("create KAF action ledger: %w", err)
		}
		ledger, err = s.loadKafActionLedger(ctx, task, req)
		if err != nil {
			return nil, false, err
		}
	}
	if err := validateKafActionLedger(ledger, task, req); err != nil {
		return nil, false, err
	}
	if ledger.ResultStatus == "applied" {
		return ledger, false, nil
	}
	if ledger.ResultStatus == "failed_terminal" {
		return nil, false, fmt.Errorf("%w: action has a terminal failure", ErrKafActionInvalid)
	}
	now := s.now()
	leaseOwner := uuid.NewString()
	updated, err := s.client.KafTaskActionLedger.Update().Where(
		kaftaskactionledger.IDEQ(ledger.ID),
		kaftaskactionledger.Or(
			kaftaskactionledger.ResultStatusIn("pending", "failed_retryable"),
			kaftaskactionledger.And(
				kaftaskactionledger.ResultStatusEQ("executing"),
				kaftaskactionledger.LeaseExpiresAtLT(now),
			),
		),
	).SetResultStatus("executing").SetLeaseOwner(leaseOwner).SetLeaseExpiresAt(now.Add(kafActionLeaseTTL)).Save(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("claim KAF action ledger: %w", err)
	}
	if updated != 1 {
		current, loadErr := s.loadKafActionLedger(ctx, task, req)
		if loadErr != nil {
			return nil, false, loadErr
		}
		if current.ResultStatus == "applied" {
			return current, false, nil
		}
		return nil, false, fmt.Errorf("%w: action lease is active", ErrKafActionConflict)
	}
	ledger, err = s.client.KafTaskActionLedger.Get(ctx, ledger.ID)
	if err != nil {
		return nil, false, fmt.Errorf("load claimed KAF action ledger: %w", err)
	}
	return ledger, true, nil
}

func (s *KafDelegationService) loadKafActionLedger(ctx context.Context, task *ent.ProcessTask, req KafActionRequest) (*ent.KafTaskActionLedger, error) {
	ledger, err := s.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(task.TenantID), kaftaskactionledger.TaskIDEQ(task.TaskID),
		kaftaskactionledger.RunIDEQ(req.Execution.RunID), kaftaskactionledger.StepIDEQ(req.Execution.StepID),
	).Only(ctx)
	if err == nil {
		return ledger, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("load KAF action ledger: %w", err)
	}
	ledger, err = s.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(task.TenantID), kaftaskactionledger.IdempotencyKeyEQ(req.Execution.IdempotencyKey),
	).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load KAF action ledger by idempotency key: %w", err)
	}
	return ledger, nil
}

func validateKafActionLedger(ledger *ent.KafTaskActionLedger, task *ent.ProcessTask, req KafActionRequest) error {
	if ledger.TenantID != task.TenantID || ledger.TaskID != task.TaskID || ledger.RunID != req.Execution.RunID || ledger.StepID != req.Execution.StepID ||
		ledger.Action != req.Action || ledger.IdempotencyKey != req.Execution.IdempotencyKey || ledger.CorrelationID != req.Execution.CorrelationID ||
		ledger.ProcedureRef != req.Execution.ProcedureRef || ledger.ProcedureVersion != req.Execution.ProcedureVersion {
		return fmt.Errorf("%w: execution scope was already used with different immutable metadata", ErrKafActionInvalid)
	}
	return nil
}

func (s *KafDelegationService) finalizeKafAction(ctx context.Context, ledger *ent.KafTaskActionLedger, status, errorCode string) error {
	if ledger == nil || ledger.LeaseOwner == "" {
		return fmt.Errorf("%w: action lease is required for finalization", ErrKafActionConflict)
	}
	update := s.client.KafTaskActionLedger.Update().Where(
		kaftaskactionledger.IDEQ(ledger.ID), kaftaskactionledger.ResultStatusEQ("executing"),
		kaftaskactionledger.LeaseOwnerEQ(ledger.LeaseOwner),
	).SetResultStatus(status).ClearLeaseOwner().ClearLeaseExpiresAt()
	if status == "applied" {
		payload, err := json.Marshal(map[string]interface{}{"action": ledger.Action, "idempotencyKey": ledger.IdempotencyKey})
		if err != nil {
			return fmt.Errorf("marshal KAF action result payload: %w", err)
		}
		update.SetResultPayload(payload).ClearLastErrorCode()
	} else {
		update.SetLastErrorCode(errorCode)
	}
	updated, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("finalize KAF action ledger: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("%w: action lease was lost before finalization", ErrKafActionConflict)
	}
	return nil
}

func (s *KafDelegationService) persistNonCompletingAction(ctx context.Context, task *ent.ProcessTask, instance *ent.ProcessInstance, req KafActionRequest) error {
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if actorID <= 0 {
		return fmt.Errorf("%w: authenticated KAF automation actor is required", ErrKafDelegationForbidden)
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start KAF action transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	updated, err := tx.ProcessInstance.Update().Where(
		processinstance.IDEQ(instance.ID), processinstance.TenantIDEQ(task.TenantID), processinstance.VersionEQ(req.ExpectedVersion),
	).SetVersion(req.ExpectedVersion + 1).Save(ctx)
	if err != nil {
		return fmt.Errorf("update KAF action version: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("%w: task was changed by another actor", ErrKafActionConflict)
	}
	workItem, err := tx.Ticket.Query().Where(
		ticket.IDEQ(instance.BusinessID), ticket.TenantIDEQ(task.TenantID), ticket.DeletedAtIsNil(),
	).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("persist KAF action timeline: WorkItem %d was not found", instance.BusinessID)
		}
		return fmt.Errorf("load KAF action WorkItem: %w", err)
	}
	if _, err := tx.TicketComment.Create().
		SetTicketID(workItem.ID).
		SetUserID(actorID).
		SetContent(kafActionTimelineContent(req)).
		SetIsInternal(true).
		SetTenantID(task.TenantID).
		Save(ctx); err != nil {
		return fmt.Errorf("persist KAF action timeline: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit KAF action transaction: %w", err)
	}
	return nil
}

func kafActionTimelineContent(req KafActionRequest) string {
	summary := strings.TrimSpace(req.Payload.ResultSummary)
	prefix := "KAF progress:"
	if req.Action == kafActionFailure {
		summary = strings.TrimSpace(req.Payload.FailureSummary)
		prefix = "KAF execution failure:"
	}
	return prefix + " " + summarizeOutboxError(summary)
}

func (s *KafDelegationService) recordActionAudit(ctx context.Context, task *ent.ProcessTask, req KafActionRequest, statusCode int) error {
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	body, err := json.Marshal(map[string]interface{}{
		"taskId": task.TaskID, "correlationId": task.CorrelationID, "action": req.Action,
		"runId": req.Execution.RunID, "stepId": req.Execution.StepID, "procedureRef": req.Execution.ProcedureRef,
		"procedureVersion": req.Execution.ProcedureVersion, "idempotencyKey": req.Execution.IdempotencyKey,
		"resultCode": statusCode,
	})
	if err != nil {
		return err
	}
	create := s.client.AuditLog.Create().SetTenantID(task.TenantID).SetResource("work_item").
		SetAction("kaf_delegate." + req.Action).SetPath("bpmn/process-tasks/actions").SetMethod("POST").
		SetStatusCode(statusCode).SetRequestBody(string(body))
	if actorID > 0 {
		create.SetUserID(actorID)
	}
	return create.Exec(ctx)
}

func kafAllowedActions(task *ent.ProcessTask) []string {
	value, _ := task.TaskVariables[bpmnMetaDataAllowedActions].(string)
	return splitNonEmptyCSV(value)
}

func kafActionAllowed(task *ent.ProcessTask, action string) bool {
	for _, allowed := range kafAllowedActions(task) {
		if allowed == action {
			return true
		}
	}
	return false
}

func frozenKafIntakeSnapshot(variables map[string]interface{}) map[string]interface{} {
	snapshot, _ := variables["intake_snapshot"].(map[string]interface{})
	if snapshot == nil {
		return map[string]interface{}{}
	}
	return cloneKafVariables(snapshot)
}

func cloneKafVariables(variables map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(variables))
	for key, value := range variables {
		copy[key] = value
	}
	return copy
}

// CreateDelegatedTask creates the BPMN wait state, its activity update, audit
// record, and delivery event in one transaction. Any failed write rolls back
// the complete delegation transition.
func (s *KafDelegationService) CreateDelegatedTask(ctx context.Context, instanceID int, serviceTask *BPMNServiceTask) (*ent.ProcessTask, error) {
	if serviceTask == nil {
		return nil, fmt.Errorf("KAF delegation service task is required")
	}
	if s.outbox == nil {
		return nil, fmt.Errorf("KAF delegation outbox repository is required")
	}
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if tenantID <= 0 {
		return nil, fmt.Errorf("KAF delegation tenant context is required")
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start KAF delegation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	instance, err := tx.ProcessInstance.Query().
		Where(processinstance.IDEQ(instanceID), processinstance.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load KAF delegation process instance: %w", err)
	}

	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	if actorID > 0 {
		actorExists, err := tx.User.Query().
			Where(user.IDEQ(actorID), user.TenantIDEQ(instance.TenantID)).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("validate KAF delegation audit actor: %w", err)
		}
		if !actorExists {
			return nil, fmt.Errorf("KAF delegation audit actor %d does not belong to tenant %d", actorID, instance.TenantID)
		}
	}

	if _, err := tx.ProcessInstance.UpdateOne(instance).
		SetCurrentActivityID(serviceTask.ID).
		SetCurrentActivityName(serviceTask.ID).
		Save(ctx); err != nil {
		return nil, fmt.Errorf("update KAF delegation process activity: %w", err)
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
	if actorID > 0 {
		auditCreate.SetUserID(actorID)
	}
	if err := auditCreate.Exec(ctx); err != nil {
		return nil, fmt.Errorf("create KAF delegation audit log: %w", err)
	}

	event, err := newKafDelegateOutboxEvent(ctx, tx.Client(), task, instance, s.now())
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

func newKafDelegateOutboxEvent(ctx context.Context, client *ent.Client, task *ent.ProcessTask, instance *ent.ProcessInstance, occurredAt time.Time) (NewOutboxEvent, error) {
	recordClass, err := kafDelegationRecordClass(ctx, client, instance)
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

func kafDelegationRecordClass(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance) (string, error) {
	switch instance.BusinessType {
	case "incident":
		return "incident", nil
	case "service_request", "service_request_item":
		return "service_request_item", nil
	case "ticket":
		if instance.BusinessID <= 0 {
			return "", fmt.Errorf("KAF delegation ticket instance %d has no work item ID", instance.ID)
		}
		workItem, err := client.Ticket.Query().
			Where(ticket.IDEQ(instance.BusinessID), ticket.TenantIDEQ(instance.TenantID), ticket.DeletedAtIsNil()).
			Only(ctx)
		if err != nil {
			return "", fmt.Errorf("load KAF delegation work item: %w", err)
		}
		if provided, present := instance.Variables["record_class"]; present {
			recordClass, ok := provided.(string)
			if !ok || recordClass != workItem.RecordClass {
				return "", fmt.Errorf("KAF delegation record class variable conflicts with persisted work item record class")
			}
		}
		if workItem.RecordClass == "incident" || workItem.RecordClass == "service_request_item" {
			return workItem.RecordClass, nil
		}
		return "", fmt.Errorf("unsupported KAF delegation work item record class %q", workItem.RecordClass)
	}
	return "", fmt.Errorf("unsupported KAF delegation record class %q", instance.BusinessType)
}
