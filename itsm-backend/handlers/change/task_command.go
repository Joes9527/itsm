package change

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
)

type TaskCommand struct {
	Command
	TaskID string
}
type TaskProgress struct {
	Progress     string                   `json:"progress"`
	TaskID       string                   `json:"taskId"`
	ExecutionKey string                   `json:"executionKey"`
	Reason       string                   `json:"reason,omitempty"`
	Result       *workitemmutation.Result `json:"result,omitempty"`
}
type taskAcceptance struct {
	TaskID          string `json:"taskId"`
	ExecutionKey    string `json:"executionKey"`
	Action          string `json:"action"`
	ExpectedVersion int    `json:"expectedVersion"`
	ChangeID        int    `json:"changeId"`
}
type TaskProgressQuery struct {
	Meta     workitemmutation.Meta
	ChangeID int
	Action   string
}

var changeTaskActions = map[string]string{"assess": "assess_risk", "approve": "approve_change", "reject": "approve_change", "schedule": "schedule_change", "implement": "implement_change", "record_outcome": "verify_change", "review": "review_change", "close": "close_change"}

// CompleteChangeTask accepts one real user task; its durable callback owns the
// professional command. Retrying the HTTP request never completes the task again.
func (s *Service) CompleteChangeTask(ctx context.Context, cmd TaskCommand) (out TaskProgress, resultErr error) {
	var empty TaskProgress
	m := cmd.Meta
	cmd.Action = strings.TrimSpace(cmd.Action)
	cmd.Evidence = strings.TrimSpace(cmd.Evidence)
	cmd.Outcome = strings.TrimSpace(cmd.Outcome)
	if m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.OperationID) == "" || strings.TrimSpace(m.Source) == "" || cmd.TaskID == "" {
		return empty, common.NewValidationError("actor,tenant,version,operationId and taskId required", nil)
	}
	action, ok := changeTaskActions[cmd.Action]
	if !ok {
		return empty, common.NewValidationError("unsupported Change task action", nil)
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	if err := validateChangeTaskInput(cmd); err != nil {
		return empty, err
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	digest, err := workitemmutation.Digest(struct {
		Action, TaskID, Evidence, Outcome string
		ChangeID, Version, PIRID          int
		Start, End, ActualEnd             *time.Time
	}{cmd.Action, cmd.TaskID, cmd.Evidence, cmd.Outcome, cmd.ChangeID, m.ExpectedVersion, cmd.PIRID, cmd.PlannedStart, cmd.PlannedEnd, cmd.ActualEnd})
	if err != nil {
		return empty, err
	}
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	attempted := false
	defer func() {
		if resultErr == nil || !attempted {
			return
		}
		if tx.Rollback() != nil {
			return
		}
		fresh, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return
		}
		defer fresh.Rollback()
		current, err := s.authorizeCommand(ctx, fresh, cmd.Command)
		if err != nil {
			resultErr = err
			return
		}
		_, ok, err := workitemmutation.Replay(ctx, fresh.Client(), m, current.WorkItemID, digest)
		if err != nil {
			resultErr = err
			return
		}
		if !ok {
			return
		}
		progress, err := s.acceptedTaskProgress(ctx, fresh.Client(), TaskProgressQuery{Meta: m, ChangeID: cmd.ChangeID, Action: cmd.Action}, current.WorkItemID)
		if err != nil {
			resultErr = err
			return
		}
		if progress.Result != nil {
			progress.Result.Replayed = true
		}
		out = progress
		resultErr = nil
	}()
	current, err := s.authorizeCommand(ctx, tx, cmd.Command)
	if err != nil {
		return empty, err
	}
	if _, ok, err := workitemmutation.Replay(ctx, tx.Client(), m, current.WorkItemID, digest); ok || err != nil {
		if err != nil {
			return empty, err
		}
		progress, err := s.acceptedTaskProgress(ctx, tx.Client(), TaskProgressQuery{Meta: m, ChangeID: cmd.ChangeID, Action: cmd.Action}, current.WorkItemID)
		if progress.Result != nil {
			progress.Result.Replayed = true
		}
		return progress, err
	}
	if current.Edges.WorkItem.Version != m.ExpectedVersion {
		return empty, common.NewVersionConflictError("change", cmd.ChangeID, m.ExpectedVersion, current.Edges.WorkItem.Version)
	}
	if s.processEngine == nil {
		return empty, common.NewValidationError("change process engine required", nil)
	}
	task, err := tx.ProcessTask.Query().Where(processtask.TaskID(cmd.TaskID), processtask.TenantID(m.TenantID)).Only(ctx)
	if err != nil {
		return empty, common.NewNotFoundError("Change task")
	}
	key, keyErr := dto.WorkItemBusinessKey(dto.RecordClassChangeRequest, current.WorkItemID)
	if keyErr != nil {
		return empty, keyErr
	}
	instance, err := tx.ProcessInstance.Query().Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(m.TenantID), processinstance.BusinessID(current.WorkItemID), processinstance.BusinessType(string(dto.BusinessTypeChangeRequest)), processinstance.BusinessKey(key), processinstance.Status("running")).Only(ctx)
	if err != nil {
		return empty, common.NewValidationError("task does not belong to this running Change", nil)
	}
	if task.CallbackHandlerID != "change_service_handler" || task.CallbackAction != action || instance.CurrentActivityID != task.TaskDefinitionKey {
		return empty, common.NewValidationError("task does not own requested Change action", nil)
	}
	if err := s.requireExecutionTx(ctx, tx, m.TenantID, current.WorkItemID); err != nil {
		return empty, err
	}
	if err = workitemmutation.RequireSettledChangeCallbacks(ctx, tx, m.TenantID, current.WorkItemID); err != nil {
		if _, unresolved := err.(*workitemmutation.UnresolvedChangeCallbackError); unresolved {
			err = common.NewConflictError("Change workflow", err.Error())
		}
		return empty, err
	}

	vars := map[string]interface{}{"version": m.ExpectedVersion, "evidence": cmd.Evidence}
	if cmd.Outcome != "" {
		vars["outcome"] = cmd.Outcome
	}
	if cmd.PIRID > 0 {
		vars["pir_id"] = cmd.PIRID
	}
	for _, field := range []struct {
		key   string
		value *time.Time
	}{{"planned_start_date", cmd.PlannedStart}, {"planned_end_date", cmd.PlannedEnd}, {"actual_end_date", cmd.ActualEnd}} {
		if field.value != nil {
			vars[field.key] = field.value.Format(time.RFC3339Nano)
		}
	}
	if cmd.Action == "approve" || cmd.Action == "reject" {
		vars["approvalAction"] = cmd.Action
		vars["approvalComment"] = cmd.Evidence
		vars["approvalResult"] = "approved"
		if cmd.Action == "reject" {
			vars["approvalResult"] = "rejected"
		}
	}
	taskCtx := service.WithBPMNAccessScope(ctx, service.BPMNAccessScope{TenantID: m.TenantID, UserID: m.ActorID})
	taskCtx = context.WithValue(taskCtx, bpmn.BPMNTenantIDContextKey, m.TenantID)
	taskCtx = context.WithValue(taskCtx, bpmn.BPMNUserIDContextKey, m.ActorID)
	attempted = true
	if err = s.processEngine.CompleteTaskTx(taskCtx, tx, task.TaskID, vars); err != nil {
		return empty, err
	}
	// CompleteTaskTx has already fenced instance then task. Touch the existing
	// WorkItem tuple last so a concurrent metadata RR snapshot cannot also commit.
	// Use the existing SQL port to avoid Ent's updatedAt default: acceptance
	// changes no public WorkItem version, status or timestamp.
	fence, err := tx.ExecContext(ctx, "UPDATE tickets SET version=version WHERE id=$1 AND tenant_id=$2 AND version=$3 AND record_class='change_request' AND deleted_at IS NULL", current.WorkItemID, m.TenantID, m.ExpectedVersion)
	if err != nil {
		return empty, err
	}
	count, err := fence.RowsAffected()
	if err != nil {
		return empty, err
	}
	if count != 1 {
		return empty, common.NewVersionConflictError("change", cmd.ChangeID, m.ExpectedVersion, current.Edges.WorkItem.Version)
	}
	callback, err := tx.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.TenantID(m.TenantID), processcallbackoutbox.ProcessTaskID(task.ID), processcallbackoutbox.ActorID(m.ActorID)).Only(ctx)
	if err != nil {
		return empty, fmt.Errorf("completed Change task callback missing: %w", err)
	}
	link := taskAcceptance{TaskID: task.TaskID, ExecutionKey: callback.ExecutionKey, Action: cmd.Action, ExpectedVersion: m.ExpectedVersion, ChangeID: cmd.ChangeID}
	data, err := json.Marshal(link)
	if err != nil {
		return empty, err
	}
	_, err = tx.AuditLog.Create().SetTenantID(m.TenantID).SetUserID(m.ActorID).SetOperationID(m.OperationID).SetRequestDigest(digest).SetResultVersion(m.ExpectedVersion).SetResultStatus("accepted").SetResource("work_item").SetAction("change.task_completion").SetPath(strconv.Itoa(current.WorkItemID)).SetMethod(m.Source).SetRequestID(m.CorrelationID).SetStatusCode(202).SetRequestBody(string(data)).Save(ctx)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return s.GetTaskProgress(ctx, TaskProgressQuery{Meta: m, ChangeID: cmd.ChangeID, Action: cmd.Action})
}

// GetTaskProgress reads an acceptance owned by this actor and operation. Action
// must match the accepted action, and determines the current domain permission.
func (s *Service) GetTaskProgress(ctx context.Context, q TaskProgressQuery) (TaskProgress, error) {
	var empty TaskProgress
	if q.Meta.TenantID <= 0 || q.Meta.ActorID <= 0 || strings.TrimSpace(q.Meta.OperationID) == "" || q.ChangeID <= 0 {
		return empty, common.NewValidationError("actor,tenant,Change and operationId required", nil)
	}
	if _, ok := changeTaskActions[q.Action]; !ok {
		return empty, common.NewValidationError("unsupported Change task action", nil)
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != q.Meta.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, q.Meta.TenantID)
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	current, err := s.authorizeCommand(ctx, tx, Command{Meta: q.Meta, ChangeID: q.ChangeID, Action: q.Action})
	if err != nil {
		return empty, err
	}
	return s.acceptedTaskProgress(ctx, tx.Client(), q, current.WorkItemID)
}

func (s *Service) acceptedTaskProgress(ctx context.Context, client *ent.Client, q TaskProgressQuery, itemID int) (TaskProgress, error) {
	var result TaskProgress
	m := q.Meta
	row, err := client.AuditLog.Query().Where(auditlog.TenantID(m.TenantID), auditlog.UserID(m.ActorID), auditlog.OperationID(m.OperationID), auditlog.Action("change.task_completion"), auditlog.Path(strconv.Itoa(itemID)), auditlog.Resource("work_item")).Only(ctx)
	if ent.IsNotFound(err) {
		return result, common.NewNotFoundError("Change task acceptance")
	}
	if err != nil {
		return result, err
	}
	var link taskAcceptance
	if row.RequestBody == nil {
		return result, fmt.Errorf("task acceptance identity missing")
	}
	if err = json.Unmarshal([]byte(*row.RequestBody), &link); err != nil {
		return result, err
	}
	if link.Action != q.Action || link.ChangeID != q.ChangeID || link.ExpectedVersion <= 0 || link.TaskID == "" || link.ExecutionKey == "" || row.RequestDigest == nil || row.ResultStatus == nil || *row.ResultStatus != "accepted" || row.ResultVersion == nil || *row.ResultVersion != link.ExpectedVersion {
		return result, &workitemmutation.OperationConflictError{OperationID: m.OperationID}
	}
	result.TaskID = link.TaskID
	result.ExecutionKey = link.ExecutionKey
	domainAction := q.Action
	if q.Action == "approve" || q.Action == "reject" {
		domainAction = "authorize"
	}
	receipt, err := client.AuditLog.Query().Where(auditlog.OperationID(link.ExecutionKey), auditlog.TenantID(m.TenantID), auditlog.UserID(m.ActorID), auditlog.Path(strconv.Itoa(itemID)), auditlog.Resource("work_item"), auditlog.Action("change."+domainAction)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return result, err
	}
	if err == nil {
		if receipt.ResultVersion == nil || *receipt.ResultVersion != link.ExpectedVersion+1 || receipt.ResultStatus == nil || receipt.RequestDigest == nil || receipt.Method != "workflow" {
			return result, fmt.Errorf("invalid lifecycle receipt identity")
		}
		result.Result = &workitemmutation.Result{WorkItemID: itemID, Version: *receipt.ResultVersion, Status: *receipt.ResultStatus}
	}
	callback, err := client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ExecutionKey(link.ExecutionKey), processcallbackoutbox.TenantID(m.TenantID), processcallbackoutbox.ActorID(m.ActorID), processcallbackoutbox.ActorSource("workflow"), processcallbackoutbox.TaskID(link.TaskID), processcallbackoutbox.CallbackKind("user_task_callback"), processcallbackoutbox.HandlerID("change_service_handler"), processcallbackoutbox.Action(changeTaskActions[q.Action])).Only(ctx)
	if ent.IsNotFound(err) && result.Result != nil {
		result.Progress = "effect_applied"
		result.Reason = "continuation_progress_unavailable"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if bpmn.GetIntFromVars(callback.Variables, "version") != link.ExpectedVersion {
		return result, fmt.Errorf("callback version disagrees with acceptance")
	}
	task, err := client.ProcessTask.Query().Where(processtask.ID(callback.ProcessTaskID), processtask.TenantID(m.TenantID), processtask.TaskID(link.TaskID), processtask.ProcessInstanceID(callback.ProcessInstanceID), processtask.Status("completed"), processtask.TaskDefinitionKey(callback.ElementID)).Only(ctx)
	if err != nil {
		return result, fmt.Errorf("callback completed task identity mismatch: %w", err)
	}
	key, keyErr := dto.WorkItemBusinessKey(dto.RecordClassChangeRequest, itemID)
	if keyErr != nil {
		return result, keyErr
	}
	owns, err := client.ProcessInstance.Query().Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(m.TenantID), processinstance.BusinessID(itemID), processinstance.BusinessType(string(dto.BusinessTypeChangeRequest)), processinstance.BusinessKey(key)).Exist(ctx)
	if err != nil {
		return result, err
	}
	if !owns {
		return result, fmt.Errorf("callback Change instance identity mismatch")
	}
	switch callback.Status {
	case "pending", "processing", "blocked", "completed":
		result.Progress = callback.Status
	default:
		return result, fmt.Errorf("unsupported callback progress")
	}
	result.Reason = callback.LastErrorClass
	if result.Progress == "blocked" && result.Reason == "" {
		result.Reason = "callback_blocked"
	}
	if result.Progress == "completed" && result.Result == nil {
		return result, fmt.Errorf("completed callback lacks professional receipt")
	}
	return result, nil
}

// HTTPStatus reports200 only for an actual professional receipt; blocked
// continuation remains actionable even when its business effect already exists.
func (p TaskProgress) HTTPStatus() int {
	if p.Progress == "blocked" {
		return 409
	}
	if p.Result != nil {
		return 200
	}
	if p.Progress == "pending" || p.Progress == "processing" {
		return 202
	}
	return 409
}

func validateChangeTaskInput(cmd TaskCommand) error {
	invalid := func(message string) error { return common.NewValidationError(message, nil) }
	if cmd.ChangeID <= 0 {
		return invalid("positive Change identity required")
	}
	if cmd.ApprovalDecisionID != 0 {
		return invalid("approval decision identity is engine-owned")
	}
	switch cmd.Action {
	case "assess", "reject", "review", "close", "record_outcome":
		if cmd.Evidence == "" {
			return invalid("explicit evidence required")
		}
	}
	if cmd.Action == "review" || cmd.Action == "close" {
		if cmd.PIRID <= 0 {
			return invalid("explicit PIR identity required")
		}
	} else if cmd.PIRID != 0 {
		return invalid("PIR is not input to this action")
	}
	if cmd.Action == "record_outcome" {
		if !validOutcome(cmd.Outcome) || cmd.ActualEnd == nil {
			return invalid("explicit outcome and actual end required")
		}
	} else if cmd.Outcome != "" || cmd.ActualEnd != nil {
		return invalid("outcome is not input to this action")
	}
	if cmd.Action == "schedule" {
		if cmd.PlannedStart == nil || cmd.PlannedEnd == nil || !cmd.PlannedEnd.After(*cmd.PlannedStart) {
			return invalid("valid implementation window required")
		}
	} else if cmd.PlannedStart != nil || cmd.PlannedEnd != nil {
		return invalid("window is not input to this action")
	}
	return nil
}
