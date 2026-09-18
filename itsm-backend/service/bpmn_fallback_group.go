package service

import (
	"context"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/systemconfig"
	"itsm-backend/service/approver"
)

// ApprovalFallbackGroupConfigKey 是"兜底候选组"的**租户级系统配置键**。
//
// 配置面用既有的 systemconfigs 表（有管理写接口，见 SystemConfigService），
// 不发明新机制：运维在系统配置里改这个键即可，无需改代码或重启。
const ApprovalFallbackGroupConfigKey = "bpmnApprovalFallbackGroup"

// approvalFallbackAuditAction 是"本次审批落到兜底组"的审计动作名。
const approvalFallbackAuditAction = "approval_fallback_group_used"

// approvalFallbackGroup 返回该租户配置的兜底候选组。
//
// 未配置、配置为空、或读配置出错时**一律回落默认组**：兜底组为空会让审批任务
// 无人可领，这比"沿用默认组"严重得多，所以本函数绝不返回空串。
func (e *CustomProcessEngine) approvalFallbackGroup(ctx context.Context, tenantID int) string {
	if tenantID > 0 {
		cfg, err := e.client.SystemConfig.Query().
			Where(
				systemconfig.KeyEQ(ApprovalFallbackGroupConfigKey),
				systemconfig.TenantIDEQ(tenantID),
				systemconfig.DeletedAtIsNil(),
			).
			Only(ctx)
		switch {
		case err == nil:
			if value := strings.TrimSpace(cfg.Value); value != "" {
				return value
			}
		case !ent.IsNotFound(err):
			// 读配置失败不能把审批变成无人可领：留可见警告并回落默认组。
			e.logger.Warnw("读取兜底审批组配置失败，回落默认组",
				"tenantID", tenantID, "error", err)
		}
	}
	return approvalFallbackCandidateGroup
}

// recordApprovalFallback 记录"本次审批最终落到了兜底组"。
//
// 设计明确要求"先往上找，最后兜底组（**留痕**）"——留痕不等于日志：
// 只打日志的话，事后无法从审计里回答"这个任务当时为什么派给了兜底组"。
func (e *CustomProcessEngine) recordApprovalFallback(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User, task *BPMNUserTask, group string) error {
	metadata := map[string]interface{}{
		"reason": string(approver.ReasonFallbackGroup),
		"group":  group,
	}
	if task != nil {
		metadata["taskId"] = task.ID
		if task.TaskPurpose != "" {
			metadata["taskPurpose"] = task.TaskPurpose
		}
	}
	if requester != nil {
		metadata["requesterId"] = requester.ID
		metadata["departmentId"] = requester.DepartmentID
	}

	create := e.client.ProcessAuditLog.Create().
		SetProcessInstanceID(instance.ID).
		SetProcessInstanceKey(instance.ProcessInstanceID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetProcessDefinitionID(instance.ProcessDefinitionID).
		SetTenantID(instance.TenantID).
		SetAction(approvalFallbackAuditAction).
		SetMetadata(metadata)
	if task != nil {
		create = create.SetActivityID(task.ID).SetActivityType(ActivityTypeUserTask)
	}
	if requester != nil {
		create = create.SetUserID(requester.ID)
	}

	_, err := create.Save(ctx)
	return err
}
