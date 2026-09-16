package service

import (
	"context"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/service/bpmn"
)

// taskUIActions projects existing command authority for the simple ticket task UI.
// It is advisory: every mutation still rechecks authority and lifecycle in its transaction.
func (e *CustomProcessEngine) taskUIActions(ctx context.Context, task *ent.ProcessTask) dto.BPMNTaskUIActions {
	result := dto.BPMNTaskUIActions{}
	purpose, _ := task.TaskVariables["taskPurpose"].(string)
	if task.TaskType != "user_task" || (purpose != "" && purpose != "fulfillment") || e.isAsyncProcessTask(task) {
		result.Reason = "请通过该任务的专用入口处理"
		return result
	}
	if ValidateBPMNTaskLifecycle(BPMNTaskCommandClaim, task.Status) == nil && (task.Assignee == "" || task.Assignee == "0") && e.authorizeTaskCommandActorWithClient(ctx, e.client, task, BPMNTaskCommandClaim) == nil {
		result.Claim = true
	}
	if strings.TrimSpace(task.FormKey) != "" {
		result.Reason = "此任务需要填写专用表单"
		return result
	}
	if ValidateBPMNTaskLifecycle(BPMNTaskCommandComplete, task.Status) == nil {
		err := e.authorizeTaskCommandActorWithClient(ctx, e.client, task, BPMNTaskCommandComplete)
		if err == nil {
			if reason, blocked := e.actorInputGateReason(task); blocked {
				// The declared action binds actor input the simple entry cannot
				// supply, so offering Complete would only produce a rejected
				// completion. Report why instead of duplicating the rule in UI.
				result.Reason = reason
			} else {
				result.Complete = true
			}
		} else if task.AssigneeSource == BPMNAssigneeSourceWorkItem {
			if isBPMNTaskAccessDenial(err) {
				result.Reason = "当前账号无权执行此任务，请联系管理员核验任务及业务权限"
			} else {
				result.Reason = "暂时无法核验任务执行权限，请刷新后重试"
			}
		}
	}
	return result
}

// actorInputGateReason reports whether the task declares a callback action whose
// payload carries actor-bound input that the simple completion entry cannot
// supply.
//
// It reads only the descriptor already persisted on the task row. Resolving a
// missing descriptor would persist one (descriptorForProcessTask), and a read
// projection must never write. When a declared descriptor cannot be resolved
// against the registry the gate fails closed, because the completion could not
// be validated either.
func (e *CustomProcessEngine) actorInputGateReason(task *ent.ProcessTask) (string, bool) {
	handlerID := strings.TrimSpace(task.CallbackHandlerID)
	if handlerID == "" {
		// No declared callback: nothing about this task needs extra actor input.
		return "", false
	}
	const unresolvable = "此任务声明的处理程序不可用，请联系管理员核验任务配置"
	if e.callbackRegistry == nil {
		return unresolvable, true
	}
	handler := e.callbackRegistry.GetHandler(handlerID)
	if handler == nil || handler.GetHandlerID() != handlerID {
		return unresolvable, true
	}
	provider, ok := handler.(bpmn.CallbackContractProvider)
	if !ok {
		return unresolvable, true
	}
	contract, ok := provider.CallbackContract(strings.TrimSpace(task.CallbackAction))
	if !ok {
		return "此任务声明的处理动作未注册，请联系管理员核验任务配置", true
	}
	if contract.RejectInvalidUserInput {
		return "此任务需要填写处理人等信息，简单入口无法提交；请通过该任务的专用入口处理", true
	}
	return "", false
}

// Scope identity reuse to this response only; commands retain fresh transaction reads.
func (e *CustomProcessEngine) taskUIReadProjection(ctx context.Context) (*CustomProcessEngine, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projected := *e
	resolver := *e.participationResolver
	resolver.readActor = nil
	if !scope.CanUpdateAllTasks {
		actor, err := e.participationResolver.resolveActor(ctx, scope)
		if err != nil {
			return nil, err
		}
		resolver.readActor = actor
	}
	projected.participationResolver = &resolver
	return &projected, nil
}
