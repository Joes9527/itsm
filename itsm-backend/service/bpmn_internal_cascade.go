package service

import (
	"context"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"
)

type BPMNInternalSource string

const BPMNInternalSourceChangeCABCascade BPMNInternalSource = "change_cab_cascade"

type BPMNInternalCascadeRequest struct {
	TenantID   int
	InstanceID int
	TaskID     string
	NodeKey    string
	Source     BPMNInternalSource
	Variables  map[string]interface{}
}

type bpmnInternalCascadeContextKey struct{}

type bpmnInternalCascadeContext struct {
	BPMNInternalCascadeRequest
}

type bpmnInternalCascadeCompleter interface {
	completeInternalCascadeTask(context.Context, BPMNInternalCascadeRequest) error
}

// CompleteBPMNInternalCascade is the only actorless BPMN completion boundary.
// Its private implementation interface prevents arbitrary ProcessEngine adapters
// from manufacturing a broader bypass.
func CompleteBPMNInternalCascade(ctx context.Context, engine ProcessEngine, req BPMNInternalCascadeRequest) error {
	completer, ok := engine.(bpmnInternalCascadeCompleter)
	if !ok {
		return common.NewForbiddenError("流程引擎不支持受控内部级联")
	}
	return completer.completeInternalCascadeTask(ctx, req)
}

func (e *CustomProcessEngine) completeInternalCascadeTask(ctx context.Context, req BPMNInternalCascadeRequest) error {
	if req.TenantID <= 0 || req.InstanceID <= 0 || req.TaskID == "" ||
		req.Source != BPMNInternalSourceChangeCABCascade || !isChangeCABCascadeNode(req.NodeKey) {
		return common.NewForbiddenError("无效的内部流程级联请求")
	}
	if scope, present := bpmnAccessScopeValue(ctx); present {
		validated, err := BPMNAccessScopeFromContext(ctx)
		if err != nil || validated.TenantID != req.TenantID || scope.TenantID != req.TenantID {
			return common.NewForbiddenError("内部流程级联租户不一致")
		}
	}
	task, err := e.client.ProcessTask.Query().Where(
		processtask.TaskID(req.TaskID),
		processtask.TenantID(req.TenantID),
		processtask.ProcessInstanceID(req.InstanceID),
		processtask.TaskDefinitionKey(req.NodeKey),
	).Only(ctx)
	if err != nil {
		return common.NewForbiddenError("内部流程级联目标无效")
	}
	if _, err := e.client.ProcessInstance.Query().Where(
		processinstance.ID(task.ProcessInstanceID),
		processinstance.TenantID(req.TenantID),
	).Only(ctx); err != nil {
		return common.NewForbiddenError("内部流程级联实例无效")
	}

	internalCtx := context.WithValue(ctx, bpmnInternalCascadeContextKey{}, bpmnInternalCascadeContext{req})
	internalCtx = context.WithValue(internalCtx, bpmn.BPMNTenantIDContextKey, req.TenantID)
	return e.CompleteTask(internalCtx, req.TaskID, req.Variables)
}

func isChangeCABCascadeNode(nodeKey string) bool {
	return nodeKey == "Activity_Schedule" || nodeKey == "Activity_Reject"
}

func authorizeInternalCascadeTask(ctx context.Context, client *ent.Client, task *ent.ProcessTask) (bool, error) {
	internal, ok := ctx.Value(bpmnInternalCascadeContextKey{}).(bpmnInternalCascadeContext)
	if !ok {
		return false, nil
	}
	req := internal.BPMNInternalCascadeRequest
	if task == nil || req.Source != BPMNInternalSourceChangeCABCascade ||
		!isChangeCABCascadeNode(req.NodeKey) || task.TaskID != req.TaskID ||
		task.ProcessInstanceID != req.InstanceID || task.TaskDefinitionKey != req.NodeKey ||
		task.TenantID != req.TenantID {
		return true, common.NewForbiddenError("内部流程级联授权不匹配")
	}
	if _, err := client.ProcessInstance.Query().Where(
		processinstance.ID(req.InstanceID), processinstance.TenantID(req.TenantID),
	).Only(ctx); err != nil {
		return true, common.NewForbiddenError("内部流程级联实例不匹配")
	}
	return true, nil
}

func internalCascadeAuditMetadata(ctx context.Context) (map[string]interface{}, bool) {
	internal, ok := ctx.Value(bpmnInternalCascadeContextKey{}).(bpmnInternalCascadeContext)
	if !ok {
		return nil, false
	}
	return map[string]interface{}{
		"actor_type":  "system",
		"source":      string(internal.Source),
		"node_key":    internal.NodeKey,
		"instance_id": internal.InstanceID,
	}, true
}
