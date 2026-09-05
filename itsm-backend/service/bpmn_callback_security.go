package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/release"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/service/bpmn"
)

const (
	bpmnNoUserTaskCallbackHandlerID         = "__no_user_task_callback__"
	bpmnUnresolvedUserTaskCallbackHandlerID = "__unresolved_user_task_callback__"
	maxBPMNParticipantVariableDepth         = 8
	maxBPMNParticipantVariableEntries       = 1024
	maxBPMNCallbackConfigRefLength          = 128
)

type bpmnCallbackDescriptor struct {
	HandlerID string
	TaskType  string
	Action    string
	ConfigRef string
}

var reservedBPMNParticipantVariableKeys = map[string]struct{}{
	"action": {}, "allowed_actions": {}, "callback_action": {}, "callback_config_ref": {},
	"callback_handler_id": {}, "callback_task_type": {}, "service_task_type": {},
	"bpmn_callback_execution_key": {}, "handler_id": {}, "task_type": {},
	"tenant_id": {}, "tenantid": {}, "business_id": {}, "businessid": {},
	"business_type": {}, "businesstype": {}, "business_key": {}, "businesskey": {},
	"ticket_id": {}, "change_id": {}, "incident_id": {}, "problem_id": {},
	"request_id": {}, "release_id": {}, "work_item_id": {}, "workitemid": {},
	"webhook_url": {}, "webhook_headers": {}, "headers": {}, "payload": {},
	"method": {}, "timeout": {}, "taskpurpose": {}, "approvalmode": {},
	"approvalthreshold": {}, "rejectstrategy": {}, "timeoutaction": {},
	"allowdelegate": {}, "allowaddapprover": {}, "commentrequiredonreject": {},
	"approval_type": {}, "threshold": {}, "total": {}, "completed": {},
	"rejected": {}, "final_status": {}, "retry_count": {}, "last_retry_time": {},
	"delegated_from": {}, "delegated_time": {}, "escalation_level_internal": {},
	"escalation_reason_internal": {}, "escalated_time": {},
}

func isReservedBPMNParticipantVariableKey(key string) bool {
	_, reserved := reservedBPMNParticipantVariableKeys[strings.ToLower(strings.TrimSpace(key))]
	return reserved
}

func validateAndCloneBPMNParticipantVariables(variables map[string]interface{}, rejectReserved bool) (map[string]interface{}, error) {
	if variables == nil {
		return map[string]interface{}{}, nil
	}
	if len(variables) > maxBPMNParticipantVariableEntries {
		return nil, fmt.Errorf("任务表单变量数量超过限制")
	}
	result := make(map[string]interface{}, len(variables))
	for key, value := range variables {
		key = strings.TrimSpace(key)
		if key == "" || len(key) > 128 {
			return nil, fmt.Errorf("任务表单变量名无效")
		}
		if isReservedBPMNParticipantVariableKey(key) {
			if rejectReserved {
				return nil, fmt.Errorf("任务表单变量 %q 为系统保留字段", key)
			}
			continue
		}
		cloned, err := cloneBPMNJSONValue(value, 0)
		if err != nil {
			return nil, fmt.Errorf("任务表单变量 %q 类型无效: %w", key, err)
		}
		result[key] = cloned
	}
	return result, nil
}

func cloneBPMNJSONValue(value interface{}, depth int) (interface{}, error) {
	if depth > maxBPMNParticipantVariableDepth {
		return nil, fmt.Errorf("嵌套层级超过限制")
	}
	switch typed := value.(type) {
	case json.Number:
		if _, err := common.ParseExactJSONNumber(typed); err != nil {
			return nil, err
		}
		return typed, nil
	case nil, bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return typed, nil
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return nil, fmt.Errorf("非有限数值")
		}
		return typed, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, fmt.Errorf("非有限数值")
		}
		return typed, nil
	case map[string]interface{}:
		if len(typed) > maxBPMNParticipantVariableEntries {
			return nil, fmt.Errorf("对象字段数量超过限制")
		}
		copy := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			if strings.TrimSpace(key) == "" || len(key) > 128 {
				return nil, fmt.Errorf("对象字段名无效")
			}
			cloned, err := cloneBPMNJSONValue(item, depth+1)
			if err != nil {
				return nil, err
			}
			copy[key] = cloned
		}
		return copy, nil
	case []interface{}:
		if len(typed) > maxBPMNParticipantVariableEntries {
			return nil, fmt.Errorf("数组长度超过限制")
		}
		copy := make([]interface{}, len(typed))
		for i, item := range typed {
			cloned, err := cloneBPMNJSONValue(item, depth+1)
			if err != nil {
				return nil, err
			}
			copy[i] = cloned
		}
		return copy, nil
	case []string:
		copy := append([]string(nil), typed...)
		return copy, nil
	case []int:
		copy := append([]int(nil), typed...)
		return copy, nil
	default:
		return nil, fmt.Errorf("仅允许 JSON 标量、对象和数组")
	}
}

func mergeBPMNTaskVariables(existing, incoming map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{}, len(existing)+len(incoming))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range incoming {
		merged[key] = value
	}
	return merged
}

func mergeBPMNTaskCompletionVariables(existing, incoming map[string]interface{}) map[string]interface{} {
	merged := mergeBPMNTaskVariables(existing, incoming)
	if _, counterSignSummary := existing["approval_type"]; !counterSignSummary {
		return merged
	}
	// Completion variables also feed process gateways. Keep those values on the
	// process instance, but do not let a boolean `approved` overwrite the
	// counter-sign count persisted on the parent task.
	for _, key := range []string{"approval_type", "threshold", "total", "completed", "approved", "rejected", "final_status"} {
		if value, ok := existing[key]; ok {
			merged[key] = value
		}
	}
	return merged
}

func filterBPMNCallbackPayload(handler bpmn.ServiceTaskHandlerInterface, action string, variables map[string]interface{}) (map[string]interface{}, error) {
	contract, err := callbackActionContractForHandler(handler, action)
	if err != nil {
		return nil, err
	}
	if normalizer, ok := handler.(bpmn.CallbackPayloadNormalizer); ok {
		payload, err := normalizer.NormalizeCallbackPayload(action, variables)
		if err != nil {
			return nil, err
		}
		if err := validateBPMNCallbackNormalizerContractOutput(contract, payload); err != nil {
			return nil, err
		}
		return normalizeBPMNCallbackContractPayload(contract, payload)
	}
	return normalizeBPMNCallbackContractPayload(contract, variables)
}

// filterPersistedBPMNCallbackPayload validates a durable payload without
// re-reading the dynamic source values that were resolved during enqueue.
func filterPersistedBPMNCallbackPayload(handler bpmn.ServiceTaskHandlerInterface, action string, variables map[string]interface{}) (map[string]interface{}, error) {
	contract, err := callbackActionContractForHandler(handler, action)
	if err != nil {
		return nil, err
	}
	return normalizeBPMNCallbackContractPayload(contract, variables)
}

func callbackActionContractForHandler(handler bpmn.ServiceTaskHandlerInterface, action string) (bpmn.CallbackActionContract, error) {
	provider, ok := handler.(bpmn.CallbackContractProvider)
	if !ok {
		return bpmn.CallbackActionContract{}, fmt.Errorf("callback handler has no synchronous contract")
	}
	contract, ok := provider.CallbackContract(action)
	if !ok {
		return bpmn.CallbackActionContract{}, fmt.Errorf("callback action is not declared")
	}
	if err := validateBPMNCallbackActionContract(contract); err != nil {
		return bpmn.CallbackActionContract{}, err
	}
	return contract, nil
}

func cloneBPMNCallbackPayload(payload map[string]interface{}) (map[string]interface{}, error) {
	clonedPayload := make(map[string]interface{}, len(payload))
	for key, value := range payload {
		cloned, err := cloneBPMNJSONValue(value, 0)
		if err != nil {
			return nil, fmt.Errorf("回调字段 %q 类型无效", key)
		}
		clonedPayload[key] = cloned
	}
	return clonedPayload, nil
}

// validateBPMNCallbackActionContract ensures a handler's declared callback
// contract cannot carry system-owned identity or ambiguous fields into a
// durable callback payload.
func validateBPMNCallbackActionContract(contract bpmn.CallbackActionContract) error {
	allowed, err := bpmnCallbackContractFieldSet(contract.PayloadFields)
	if err != nil {
		return err
	}
	for _, field := range append(append([]string(nil), contract.RequiredFields...), contract.PositiveIntegerFields...) {
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("回调必填字段未在负载契约中声明")
		}
	}
	return nil
}

// normalizeBPMNCallbackContractConfigRef accepts only a bounded identifier-like
// definition reference. It deliberately rejects URLs, paths, whitespace, and
// other values that could contain transport details or credentials.
func normalizeBPMNCallbackContractConfigRef(contract bpmn.CallbackActionContract, configRef string) (string, error) {
	if configRef == "" {
		if contract.ConfigRefRequired {
			return "", fmt.Errorf("回调配置引用缺失")
		}
		return "", nil
	}
	if configRef != strings.TrimSpace(configRef) || len(configRef) > maxBPMNCallbackConfigRefLength || !isBPMNCallbackConfigRef(configRef) {
		return "", fmt.Errorf("回调配置引用无效")
	}
	return configRef, nil
}

func isBPMNCallbackConfigRef(configRef string) bool {
	for i := 0; i < len(configRef); i++ {
		char := configRef[i]
		isAlphaNumeric := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
		if i == 0 {
			if !isAlphaNumeric {
				return false
			}
			continue
		}
		if !isAlphaNumeric && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func normalizeBPMNCallbackContractPayload(contract bpmn.CallbackActionContract, payload map[string]interface{}) (map[string]interface{}, error) {
	allowed, err := bpmnCallbackContractFieldSet(contract.PayloadFields)
	if err != nil {
		return nil, err
	}

	normalized := make(map[string]interface{}, len(allowed))
	for _, field := range contract.PayloadFields {
		value, exists := payload[field]
		if !exists {
			continue
		}
		cloned, err := cloneBPMNJSONValue(value, 0)
		if err != nil {
			return nil, fmt.Errorf("回调字段 %q 类型无效", field)
		}
		normalized[field] = cloned
	}
	for _, field := range contract.PositiveIntegerFields {
		value, exists := normalized[field]
		if !exists {
			continue
		}
		integer, err := bpmn.CallbackInteger(value)
		if err != nil || integer <= 0 {
			return nil, fmt.Errorf("回调字段 %q 必须是有效正整数", field)
		}
		normalized[field] = strconv.Itoa(integer)
	}
	for _, field := range contract.RequiredFields {
		if _, exists := normalized[field]; !exists {
			return nil, fmt.Errorf("回调必填字段缺失")
		}
	}
	return normalized, nil
}

func validateBPMNCallbackNormalizerContractOutput(contract bpmn.CallbackActionContract, payload map[string]interface{}) error {
	allowed, err := bpmnCallbackContractFieldSet(contract.PayloadFields)
	if err != nil {
		return err
	}
	return validateBPMNCallbackContractPayloadFields(payload, allowed)
}

func bpmnCallbackContractFieldSet(fields []string) (map[string]struct{}, error) {
	if len(fields) > maxBPMNParticipantVariableEntries {
		return nil, fmt.Errorf("回调负载契约字段无效")
	}
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field == "" || field != strings.TrimSpace(field) || len(field) > 128 || isReservedBPMNParticipantVariableKey(field) {
			return nil, fmt.Errorf("回调负载契约字段无效")
		}
		if _, exists := allowed[field]; exists {
			return nil, fmt.Errorf("回调负载契约字段重复")
		}
		allowed[field] = struct{}{}
	}
	return allowed, nil
}

func validateBPMNCallbackContractPayloadFields(payload map[string]interface{}, allowed map[string]struct{}) error {
	if len(payload) > maxBPMNParticipantVariableEntries {
		return fmt.Errorf("回调负载字段数量超过限制")
	}
	for field := range payload {
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("回调负载包含未声明字段: %s", field)
		}
	}
	return nil
}

func (e *CustomProcessEngine) callbackDescriptor(taskType, action, configRef string) bpmnCallbackDescriptor {
	taskType = strings.TrimSpace(taskType)
	if taskType == "" {
		return bpmnCallbackDescriptor{HandlerID: bpmnNoUserTaskCallbackHandlerID}
	}
	handler := e.findHandlerByTaskType(taskType)
	if handler == nil {
		return bpmnCallbackDescriptor{
			HandlerID: bpmnUnresolvedUserTaskCallbackHandlerID,
			TaskType:  taskType,
			Action:    strings.TrimSpace(action),
			ConfigRef: strings.TrimSpace(configRef),
		}
	}
	return bpmnCallbackDescriptor{
		HandlerID: handler.GetHandlerID(),
		TaskType:  handler.GetTaskType(),
		Action:    strings.TrimSpace(action),
		ConfigRef: strings.TrimSpace(configRef),
	}
}

func (e *CustomProcessEngine) descriptorForProcessTask(ctx context.Context, client *ent.Client, task *ent.ProcessTask, process *BPMNProcess) (bpmnCallbackDescriptor, error) {
	if strings.TrimSpace(task.CallbackHandlerID) != "" {
		return bpmnCallbackDescriptor{
			HandlerID: task.CallbackHandlerID,
			TaskType:  task.CallbackTaskType,
			Action:    task.CallbackAction,
			ConfigRef: task.CallbackConfigRef,
		}, nil
	}

	var descriptor bpmnCallbackDescriptor
	if userTask := e.findUserTask(process, task.TaskDefinitionKey); userTask != nil {
		// A legacy user task without a declaration is the only valid no-callback case.
		descriptor = e.callbackDescriptor(userTask.ServiceTaskType(), userTask.ServiceTaskAction(), userTask.CallbackConfigRef())
	} else if serviceTask := e.findServiceTask(process, task.TaskDefinitionKey); serviceTask != nil {
		taskType := serviceTask.ServiceTaskType()
		if taskType == "" {
			taskType = e.definitionDeclaredServiceTaskType(serviceTask)
		}
		// KAF tasks created before immutable callback descriptors were added
		// persist their authoritative type on ProcessTask.
		if taskType == "" && task.TaskType == bpmn.KafDelegateTaskType {
			taskType = task.TaskType
		}
		if taskType == "" {
			return bpmnCallbackDescriptor{}, fmt.Errorf("服务任务 %s 未声明回调类型", task.TaskDefinitionKey)
		}
		descriptor = e.callbackDescriptor(taskType, serviceTask.ServiceTaskAction(), serviceTask.CallbackConfigRef())
	} else {
		return bpmnCallbackDescriptor{}, fmt.Errorf("任务节点不存在于已部署流程定义: %s", task.TaskDefinitionKey)
	}

	update := client.ProcessTask.UpdateOneID(task.ID).
		Where(processtask.TenantID(task.TenantID)).
		SetCallbackHandlerID(descriptor.HandlerID).
		SetCallbackTaskType(descriptor.TaskType).
		SetCallbackAction(descriptor.Action).
		SetCallbackConfigRef(descriptor.ConfigRef)
	if err := update.Exec(ctx); err != nil {
		return bpmnCallbackDescriptor{}, fmt.Errorf("持久化任务回调描述符失败: %w", err)
	}
	task.CallbackHandlerID = descriptor.HandlerID
	task.CallbackTaskType = descriptor.TaskType
	task.CallbackAction = descriptor.Action
	task.CallbackConfigRef = descriptor.ConfigRef
	return descriptor, nil
}

func (e *CustomProcessEngine) definitionDeclaredServiceTaskType(task *BPMNServiceTask) string {
	if task == nil {
		return ""
	}
	for _, candidate := range []string{task.Implementation, task.Class, task.DelegateExpression, task.OperationRef} {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return ""
}

func (e *CustomProcessEngine) authoritativeCallbackVariables(
	ctx context.Context,
	instance *ent.ProcessInstance,
	handler bpmn.ServiceTaskHandlerInterface,
	payload map[string]interface{},
) (map[string]interface{}, error) {
	if instance == nil || instance.TenantID <= 0 {
		return nil, fmt.Errorf("回调流程实例缺少权威租户")
	}
	variables := copyBPMNCallbackVariables(payload)
	variables["tenant_id"] = instance.TenantID
	if instance.BusinessType != "" {
		variables["business_type"] = instance.BusinessType
	}
	if instance.BusinessID > 0 {
		variables["business_id"] = instance.BusinessID
	}
	if handler.GetTaskType() == "cc_task" {
		initiatorID, err := strconv.Atoi(strings.TrimSpace(instance.Initiator))
		if err != nil || initiatorID <= 0 {
			return nil, fmt.Errorf("CC回调流程发起人无效")
		}
		if _, err := e.client.User.Query().Where(
			user.ID(initiatorID), user.TenantID(instance.TenantID), user.Active(true),
		).Only(ctx); err != nil {
			return nil, fmt.Errorf("CC回调流程发起人无效")
		}
		variables["addedBy"] = initiatorID
	}

	if !isBuiltInBusinessCallbackHandler(handler.GetTaskType()) {
		return variables, nil
	}
	if instance.BusinessType == "" || instance.BusinessID <= 0 {
		return nil, fmt.Errorf("回调流程实例缺少权威业务身份")
	}

	businessType := strings.ToLower(strings.TrimSpace(instance.BusinessType))
	workItemID := 0
	switch businessType {
	case "ticket", "generic":
		if _, err := e.client.Ticket.Query().Where(
			ticket.ID(instance.BusinessID), ticket.TenantID(instance.TenantID),
		).Only(ctx); err != nil {
			return nil, fmt.Errorf("权威工单目标不存在")
		}
		workItemID = instance.BusinessID
		variables["ticket_id"] = workItemID
	case "change", "change_request":
		entity, err := e.client.Change.Query().Where(
			change.WorkItemID(instance.BusinessID), change.HasWorkItemWith(ticket.TenantID(instance.TenantID), ticket.DeletedAtIsNil()),
		).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("权威变更目标不存在")
		}
		workItemID = instance.BusinessID
		variables["change_id"] = entity.ID
		variables["ticket_id"] = workItemID
	case "incident":
		entity, err := e.client.Incident.Query().Where(
			incident.WorkItemID(instance.BusinessID), incident.HasWorkItemWith(ticket.TenantID(instance.TenantID), ticket.DeletedAtIsNil()),
		).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("权威事件目标不存在")
		}
		workItemID = instance.BusinessID
		variables["incident_id"] = entity.ID
		variables["ticket_id"] = workItemID
	case "problem":
		entity, err := e.client.Problem.Query().Where(
			problem.WorkItemID(instance.BusinessID), problem.HasWorkItemWith(ticket.TenantID(instance.TenantID), ticket.DeletedAtIsNil()),
		).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("权威问题目标不存在")
		}
		workItemID = instance.BusinessID
		variables["problem_id"] = entity.ID
		variables["ticket_id"] = workItemID
	case "service_request", "service_request_item":
		entity, err := e.client.ServiceRequest.Query().Where(
			servicerequest.TicketID(instance.BusinessID), servicerequest.TenantID(instance.TenantID),
		).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("权威服务请求目标不存在")
		}
		workItemID = instance.BusinessID
		variables["request_id"] = entity.ID
		variables["ticket_id"] = workItemID
	case "release":
		if _, err := e.client.Release.Query().Where(
			release.ID(instance.BusinessID), release.TenantID(instance.TenantID),
		).Only(ctx); err != nil {
			return nil, fmt.Errorf("权威发布目标不存在")
		}
		variables["release_id"] = instance.BusinessID
	default:
		return nil, fmt.Errorf("不支持的权威业务类型")
	}

	switch handler.GetTaskType() {
	case "change_task":
		if _, ok := variables["change_id"]; !ok {
			return nil, fmt.Errorf("变更回调与流程业务类型不匹配")
		}
	case "incident_task":
		if _, ok := variables["incident_id"]; !ok {
			return nil, fmt.Errorf("事件回调与流程业务类型不匹配")
		}
	case "service_request_task":
		if _, ok := variables["request_id"]; !ok {
			return nil, fmt.Errorf("服务请求回调与流程业务类型不匹配")
		}
	case "release_task":
		if businessType != "release" {
			return nil, fmt.Errorf("发布回调与流程业务类型不匹配")
		}
	case "ticket_task", "generic_task", "cc_task":
		if workItemID <= 0 {
			return nil, fmt.Errorf("工单回调与流程业务类型不匹配")
		}
		variables["business_id"] = workItemID
	}
	return variables, nil
}

func isBuiltInBusinessCallbackHandler(taskType string) bool {
	switch taskType {
	case "change_task", "incident_task", "ticket_task", "generic_task",
		"service_request_task", "notification_task", "cc_task", "webhook_task", "release_task":
		return true
	default:
		return false
	}
}
