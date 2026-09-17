package service

import (
	"context"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processinstance"
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
			if reason, blocked := e.actorInputGateReason(ctx, task); blocked {
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
// Persisted descriptors are authoritative. Legacy rows are resolved from their
// pinned definition with reads only; never call descriptorForProcessTask here,
// because that command helper persists the descriptor.
func (e *CustomProcessEngine) actorInputGateReason(ctx context.Context, task *ent.ProcessTask) (string, bool) {
	handlerID := strings.TrimSpace(task.CallbackHandlerID)
	action := strings.TrimSpace(task.CallbackAction)
	if handlerID == "" {
		const unavailable = "暂时无法核验任务配置，请刷新后重试或联系管理员"
		instance, err := e.client.ProcessInstance.Query().Where(
			processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(task.TenantID),
		).Only(ctx)
		if err != nil {
			return unavailable, true
		}
		definition, err := e.client.ProcessDefinition.Query().Where(
			processdefinition.ID(instance.ProcessDefinitionID), processdefinition.TenantID(task.TenantID),
		).Only(ctx)
		if err != nil {
			return unavailable, true
		}
		parsed, err := e.parser.ParseXML(definition.BpmnXML)
		if err != nil || len(parsed.Processes) == 0 {
			return unavailable, true
		}
		node := e.findUserTask(parsed.Processes[0], task.TaskDefinitionKey)
		if node == nil {
			return unavailable, true
		}
		descriptor := e.callbackDescriptor(node.ServiceTaskType(), node.ServiceTaskAction(), node.CallbackConfigRef())
		handlerID, action = descriptor.HandlerID, descriptor.Action
	}
	if handlerID == bpmnNoUserTaskCallbackHandlerID {
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
	contract, ok := provider.CallbackContract(action)
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
