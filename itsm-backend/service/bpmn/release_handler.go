package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/release"

	"go.uber.org/zap"
)

// ReleaseServiceTaskHandler 发布服务任务处理器，对应 release_approval_flow.bpmn 的 5 个
// 用户任务节点（技术评审/发布审批/计划发布/执行发布/验证确认，metaData 都声明
// service_task_type=release_task）。之前完全没有注册处理器，这 5 个节点走完流程
// 对 Release 实体零真实副作用。
//
// 状态转换直接操作 Ent，不能调用 service/release_service.go 的
// ReleaseService.UpdateReleaseStatus——service 包本身依赖 service/bpmn 做 callback
// 注册（bpmn_process_engine.go 里的 callbackRegistry *bpmn.CallbackRegistry），
// 反向依赖会导致 import 循环。状态机白名单校验规则复制自 isValidReleaseStatusTransition
// （release_service.go），改动状态机规则时两处要一起改。
type ReleaseServiceTaskHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
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
func (h *ReleaseServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "tech_review":
		return h.techReview(ctx, variables)
	case "approval":
		// 有意的空操作：ReleaseService.ApplyReleaseApproval 是审批的真正业务入口，
		// 它会先桥接完成这个 BPMN 任务（触发本方法执行），再在自己函数体里把
		// Release.Status 转到 scheduled/cancelled——这一刻权威状态还没写，这里没有
		// 足够信息（不知道是 approve 还是 reject）也不需要重复做这件事。
		return &dto.ServiceTaskResult{Success: true, Message: "审批决策由 ReleaseService.ApplyReleaseApproval 统一处理"}, nil
	case "schedule":
		return h.updateStatus(ctx, variables, string(dto.ReleaseStatusScheduled))
	case "execute":
		return h.updateStatus(ctx, variables, string(dto.ReleaseStatusInProgress))
	case "verify":
		return h.updateStatus(ctx, variables, string(dto.ReleaseStatusCompleted))
	default:
		return &dto.ServiceTaskResult{Success: true, Message: "无操作执行"}, nil
	}
}

// Validate 验证配置
func (h *ReleaseServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

func (h *ReleaseServiceTaskHandler) releaseID(variables map[string]interface{}) (int, error) {
	id := GetIntFromVars(variables, "business_id")
	if id <= 0 {
		return 0, fmt.Errorf("无效的 business_id")
	}
	return id, nil
}

// techReview 记录技术评审意见。评审通过/不通过在这个流程设计里不对应独立的发布状态
// （Release.status 只有 draft/scheduled/in-progress/completed/cancelled/failed/
// rolled_back），所以这里只追加评审记录到 release_notes，不改状态。
func (h *ReleaseServiceTaskHandler) techReview(ctx context.Context, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	releaseID, err := h.releaseID(variables)
	if err != nil {
		return nil, err
	}
	// fail closed：租户未知时不允许落到一条不带租户约束的全表查询上
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	comment, _ := variables["comment"].(string)

	entity, err := h.client.Release.Query().
		Where(release.ID(releaseID), release.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取发布记录失败: %w", err)
	}
	notes := entity.ReleaseNotes
	if comment != "" {
		if notes != "" {
			notes += "\n"
		}
		notes += fmt.Sprintf("[技术评审] %s", comment)
	}
	if _, err := entity.Update().SetReleaseNotes(notes).Save(ctx); err != nil {
		return nil, fmt.Errorf("记录技术评审失败: %w", err)
	}
	h.logger.Infow("Release tech review recorded via BPMN", "release_id", releaseID)
	return &dto.ServiceTaskResult{Success: true, Message: "技术评审意见已记录"}, nil
}

func (h *ReleaseServiceTaskHandler) updateStatus(ctx context.Context, variables map[string]interface{}, status string) (*dto.ServiceTaskResult, error) {
	releaseID, err := h.releaseID(variables)
	if err != nil {
		return nil, err
	}
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	current, err := h.client.Release.Query().
		Where(release.ID(releaseID), release.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询发布记录失败: %w", err)
	}

	if current.Status != status && !isValidReleaseStatusTransitionForBPMN(current.Status, status) {
		return nil, fmt.Errorf("非法的发布状态转换: %s -> %s", current.Status, status)
	}

	update := current.Update().SetStatus(status)
	if status == string(dto.ReleaseStatusCompleted) {
		update = update.SetActualReleaseDate(time.Now())
	}
	if _, err := update.Save(ctx); err != nil {
		return nil, fmt.Errorf("更新发布状态失败: %w", err)
	}

	h.logger.Infow("Release status updated via BPMN", "release_id", releaseID, "status", status)
	return &dto.ServiceTaskResult{Success: true, Message: fmt.Sprintf("发布 %d 状态已更新为 %s", releaseID, status)}, nil
}

// isValidReleaseStatusTransitionForBPMN 复制自 service/release_service.go 的
// isValidReleaseStatusTransition。service/bpmn 包不能依赖 service 包（见上方类型
// 注释的循环依赖说明），只能在这里独立维护一份同款规则。
func isValidReleaseStatusTransitionForBPMN(current, newStatus string) bool {
	if current == newStatus {
		return true
	}
	transitions := map[string]map[string]struct{}{
		string(dto.ReleaseStatusDraft): {
			string(dto.ReleaseStatusScheduled): {},
			string(dto.ReleaseStatusCancelled): {},
		},
		string(dto.ReleaseStatusScheduled): {
			string(dto.ReleaseStatusInProgress): {},
			string(dto.ReleaseStatusCancelled):  {},
		},
		string(dto.ReleaseStatusInProgress): {
			string(dto.ReleaseStatusCompleted):  {},
			string(dto.ReleaseStatusFailed):     {},
			string(dto.ReleaseStatusRolledBack): {},
			string(dto.ReleaseStatusCancelled):  {},
		},
		string(dto.ReleaseStatusFailed): {
			string(dto.ReleaseStatusScheduled):  {},
			string(dto.ReleaseStatusRolledBack): {},
			string(dto.ReleaseStatusCancelled):  {},
		},
		string(dto.ReleaseStatusCompleted):  {},
		string(dto.ReleaseStatusCancelled):  {},
		string(dto.ReleaseStatusRolledBack): {},
	}
	allowed, ok := transitions[current]
	if !ok {
		return false
	}
	_, ok = allowed[newStatus]
	return ok
}

// 确保 ReleaseServiceTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*ReleaseServiceTaskHandler)(nil)
