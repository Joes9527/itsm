package bpmn

import (
	"context"
	"fmt"

	"itsm-backend/dto"
	"itsm-backend/ent"

	"go.uber.org/zap"
)

// ReleaseServiceTaskHandler parses durable process callbacks and delegates all
// professional persistence to ReleaseDomainService. It never writes Release
// records directly.
type ReleaseServiceTaskHandler struct {
	HandlerBase
	client         *ent.Client
	logger         *zap.SugaredLogger
	releaseService ReleaseDomainService
}

// SetReleaseService injects the owning Release application service.
func (h *ReleaseServiceTaskHandler) SetReleaseService(service ReleaseDomainService) {
	h.releaseService = service
}

// NewReleaseServiceTaskHandler 创建发布处理器
func NewReleaseServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *ReleaseServiceTaskHandler {
	return &ReleaseServiceTaskHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *ReleaseServiceTaskHandler) GetTaskType() string {
	return "release_task"
}

// GetHandlerID 返回处理器标识
func (h *ReleaseServiceTaskHandler) GetHandlerID() string {
	return "release_service_handler"
}

// Execute 执行发布任务
func (h *ReleaseServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "tech_review":
		comment, _ := variables["comment"].(string)
		return h.applyDomainCommand(ctx, variables, ReleaseWorkflowCommand{
			Action: ReleaseWorkflowActionTechReview, Comment: comment,
		})
	case "approval":
		return h.approvalDecisionEffect(ctx, task, variables)
	case "schedule":
		return h.applyStatus(ctx, variables, string(dto.ReleaseStatusScheduled))
	case "execute":
		return h.applyStatus(ctx, variables, string(dto.ReleaseStatusInProgress))
	case "verify":
		return h.applyStatus(ctx, variables, string(dto.ReleaseStatusCompleted))
	default:
		return BlockedEffect(CallbackBlockHandlerContract, "unsupported release callback action"), nil
	}
}

func (h *ReleaseServiceTaskHandler) approvalDecisionEffect(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	effect, err := persistedApprovalDecisionEffect(ctx, h.client, task, variables, "release approval decision is recorded")
	if err != nil || effect.Status == CallbackEffectBlocked {
		return effect, err
	}
	action, _ := task.TaskVariables["approvalAction"].(string)
	if action == "approve" {
		return effect, nil
	}
	if action != "reject" {
		return BlockedEffect(CallbackBlockTargetMissing, "unsupported release approval action"), nil
	}
	return h.applyDomainCommand(ctx, variables, ReleaseWorkflowCommand{Action: ReleaseWorkflowActionReject})
}

func (h *ReleaseServiceTaskHandler) releaseID(variables map[string]interface{}) (int, error) {
	id := GetIntFromVars(variables, "business_id")
	if id <= 0 {
		return 0, fmt.Errorf("无效的 business_id")
	}
	return id, nil
}

func (h *ReleaseServiceTaskHandler) applyStatus(ctx context.Context, variables map[string]interface{}, status string) (*CallbackEffect, error) {
	return h.applyDomainCommand(ctx, variables, ReleaseWorkflowCommand{
		Action: ReleaseWorkflowActionStatus, TargetStatus: status,
	})
}

func (h *ReleaseServiceTaskHandler) applyDomainCommand(ctx context.Context, variables map[string]interface{}, command ReleaseWorkflowCommand) (*CallbackEffect, error) {
	if h.releaseService == nil {
		return nil, fmt.Errorf("release service is unavailable")
	}
	releaseID, err := h.releaseID(variables)
	if err != nil {
		return nil, err
	}
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	command.ReleaseID = releaseID
	command.TenantID = tenantID
	mutation, err := h.releaseService.ApplyReleaseWorkflowCallback(ctx, command)
	if err != nil {
		return nil, err
	}
	if mutation == nil {
		return nil, fmt.Errorf("release service returned no callback outcome")
	}
	h.logger.Infow("Release workflow callback applied", "release_id", releaseID, "action", command.Action, "changed", mutation.Changed)
	if mutation.Changed {
		return AppliedEffect(mutation.Message, nil), nil
	}
	return IdempotentEffect(mutation.Message, nil), nil
}

// 确保 ReleaseServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*ReleaseServiceTaskHandler)(nil)
