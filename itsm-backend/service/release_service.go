package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/release"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
)

// ReleaseService 发布管理服务
type ReleaseService struct {
	client             *ent.Client
	logger             *zap.SugaredLogger
	approvalBridge     *BPMNApprovalBridge
	processTriggerSvc  ProcessTriggerServiceInterface
}

// NewReleaseService 创建发布管理服务
func NewReleaseService(client *ent.Client, logger *zap.SugaredLogger) *ReleaseService {
	svc := &ReleaseService{
		client: client,
		logger: logger,
	}
	if client != nil {
		// P0-1：发布审批桥接到 BPMN 任务，避免流程实例悬挂
		svc.approvalBridge = NewBPMNApprovalBridge(client, logger)
	}
	return svc
}

// SetProcessTriggerService 注入流程触发服务（创建发布后自动启动 release_approval_flow）。
// 与 TicketService.SetProcessTriggerService 同一模式：由 bootstrap 装配。
func (s *ReleaseService) SetProcessTriggerService(p ProcessTriggerServiceInterface) {
	s.processTriggerSvc = p
}

// CreateRelease 创建发布
func (s *ReleaseService) CreateRelease(ctx context.Context, req *dto.CreateReleaseRequest, createdBy, tenantID int) (*dto.ReleaseResponse, error) {
	releaseEntity, err := s.client.Release.Create().
		SetReleaseNumber(req.ReleaseNumber).
		SetTitle(req.Title).
		SetDescription(req.Description).
		SetType(req.Type).
		SetStatus(string(dto.ReleaseStatusDraft)).
		SetSeverity(req.Severity).
		SetEnvironment(req.Environment).
		SetCreatedBy(createdBy).
		SetTenantID(tenantID).
		SetNillableChangeID(req.ChangeID).
		SetNillableOwnerID(req.OwnerID).
		SetNillablePlannedReleaseDate(req.PlannedReleaseDate).
		SetNillablePlannedStartDate(req.PlannedStartDate).
		SetNillablePlannedEndDate(req.PlannedEndDate).
		SetReleaseNotes(req.ReleaseNotes).
		SetRollbackProcedure(req.RollbackProcedure).
		SetValidationCriteria(req.ValidationCriteria).
		SetIsEmergency(req.IsEmergency).
		SetRequiresApproval(req.RequiresApproval).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create release", "error", err, "tenant_id", tenantID)
		return nil, fmt.Errorf("failed to create release: %w", err)
	}

	// 设置关联字段（Update() 返回构造器，必须 Save(ctx) 才落库）
	updater := releaseEntity.Update()
	needsUpdate := false
	if len(req.AffectedSystems) > 0 {
		updater = updater.SetAffectedSystems(req.AffectedSystems)
		needsUpdate = true
	}
	if len(req.AffectedComponents) > 0 {
		updater = updater.SetAffectedComponents(req.AffectedComponents)
		needsUpdate = true
	}
	if len(req.DeploymentSteps) > 0 {
		updater = updater.SetDeploymentSteps(req.DeploymentSteps)
		needsUpdate = true
	}
	if len(req.Tags) > 0 {
		updater = updater.SetTags(req.Tags)
		needsUpdate = true
	}
	if needsUpdate {
		updated, err := updater.Save(ctx)
		if err != nil {
			s.logger.Errorw("Failed to set release associations", "error", err, "release_id", releaseEntity.ID)
			return nil, fmt.Errorf("failed to set release associations: %w", err)
		}
		releaseEntity = updated
	}

	// 获取创建人信息
	creator, _ := s.client.User.Get(ctx, createdBy)
	creatorName := ""
	if creator != nil {
		creatorName = creator.Name
	}

	// 触发发布流程（按 ProcessBinding 默认绑定解析 release_approval_flow）。
	// fail-soft 与工单/事件域一致：触发失败只告警不阻断创建——域侧状态流转的
	// 桥接对"无关联流程实例"回退纯业务路径，发布生命周期本身不依赖流程。
	if s.processTriggerSvc != nil {
		if _, triggerErr := s.processTriggerSvc.TriggerByBusinessType(
			ctx, dto.BusinessTypeRelease, releaseEntity.ID, nil, strconv.Itoa(createdBy), tenantID,
		); triggerErr != nil {
			s.logger.Warnw("Failed to trigger release workflow",
				"release_id", releaseEntity.ID, "tenant_id", tenantID, "error", triggerErr)
		}
	}

	response := dto.ToReleaseResponse(releaseEntity)
	response.CreatedByName = creatorName

	s.logger.Infow("Release created successfully", "release_id", releaseEntity.ID, "tenant_id", tenantID)
	return response, nil
}

// GetReleaseByID 根据ID获取发布
func (s *ReleaseService) GetReleaseByID(ctx context.Context, id, tenantID int) (*dto.ReleaseResponse, error) {
	releaseEntity, err := s.client.Release.Query().
		Where(release.IDEQ(id), release.TenantIDEQ(tenantID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		s.logger.Errorw("Failed to get release", "error", err, "release_id", id)
		return nil, fmt.Errorf("failed to get release: %w", err)
	}

	response := dto.ToReleaseResponse(releaseEntity)

	// 获取创建人信息
	if releaseEntity.CreatedBy > 0 {
		creator, _ := s.client.User.Get(ctx, releaseEntity.CreatedBy)
		if creator != nil {
			response.CreatedByName = creator.Name
		}
	}

	// 获取负责人信息
	if releaseEntity.OwnerID != nil && *releaseEntity.OwnerID > 0 {
		owner, _ := s.client.User.Get(ctx, *releaseEntity.OwnerID)
		if owner != nil {
			response.OwnerName = &owner.Name
		}
	}

	return response, nil
}

// ListReleases 获取发布列表
func (s *ReleaseService) ListReleases(ctx context.Context, tenantID int, page, pageSize int, status, releaseType string) (*dto.ReleaseListResponse, error) {
	query := s.client.Release.Query().Where(release.TenantIDEQ(tenantID))

	if status != "" {
		query = query.Where(release.StatusEQ(status))
	}
	if releaseType != "" {
		query = query.Where(release.TypeEQ(releaseType))
	}

	// 统计总数
	total, err := query.Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count releases", "error", err, "tenant_id", tenantID)
		return nil, fmt.Errorf("failed to count releases: %w", err)
	}

	// 分页查询
	if page > 0 && pageSize > 0 {
		offset := (page - 1) * pageSize
		query = query.Offset(offset).Limit(pageSize)
	}

	releaseEntities, err := query.Order(ent.Desc(release.FieldCreatedAt)).All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to list releases", "error", err, "tenant_id", tenantID)
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	releases := dto.ToReleaseResponseList(releaseEntities)

	return &dto.ReleaseListResponse{
		Total:    total,
		Releases: releases,
	}, nil
}

// UpdateRelease 更新发布
func (s *ReleaseService) UpdateRelease(ctx context.Context, id, tenantID int, req *dto.UpdateReleaseRequest) (*dto.ReleaseResponse, error) {
	releaseEntity, err := s.client.Release.Query().
		Where(release.IDEQ(id), release.TenantIDEQ(tenantID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		s.logger.Errorw("Failed to get release", "error", err, "release_id", id)
		return nil, fmt.Errorf("failed to get release: %w", err)
	}

	update := releaseEntity.Update()

	if req.Title != nil {
		update.SetTitle(*req.Title)
	}
	if req.Description != nil {
		update.SetDescription(*req.Description)
	}
	if req.Type != nil {
		update.SetType(*req.Type)
	}
	if req.Environment != nil {
		update.SetEnvironment(*req.Environment)
	}
	if req.Severity != nil {
		update.SetSeverity(*req.Severity)
	}
	if req.ChangeID != nil {
		update.SetChangeID(*req.ChangeID)
	}
	if req.OwnerID != nil {
		update.SetOwnerID(*req.OwnerID)
	}
	if req.PlannedReleaseDate != nil {
		update.SetPlannedReleaseDate(*req.PlannedReleaseDate)
	}
	if req.PlannedStartDate != nil {
		update.SetPlannedStartDate(*req.PlannedStartDate)
	}
	if req.PlannedEndDate != nil {
		update.SetPlannedEndDate(*req.PlannedEndDate)
	}
	if req.ActualReleaseDate != nil {
		update.SetActualReleaseDate(*req.ActualReleaseDate)
	}
	if req.ReleaseNotes != nil {
		update.SetReleaseNotes(*req.ReleaseNotes)
	}
	if req.RollbackProcedure != nil {
		update.SetRollbackProcedure(*req.RollbackProcedure)
	}
	if req.ValidationCriteria != nil {
		update.SetValidationCriteria(*req.ValidationCriteria)
	}
	if req.IsEmergency != nil {
		update.SetIsEmergency(*req.IsEmergency)
	}
	if req.RequiresApproval != nil {
		update.SetRequiresApproval(*req.RequiresApproval)
	}

	if len(req.AffectedSystems) > 0 {
		update.SetAffectedSystems(req.AffectedSystems)
	}
	if len(req.AffectedComponents) > 0 {
		update.SetAffectedComponents(req.AffectedComponents)
	}
	if len(req.DeploymentSteps) > 0 {
		update.SetDeploymentSteps(req.DeploymentSteps)
	}
	if len(req.Tags) > 0 {
		update.SetTags(req.Tags)
	}

	updated, err := update.Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to update release", "error", err, "release_id", id)
		return nil, fmt.Errorf("failed to update release: %w", err)
	}

	return dto.ToReleaseResponse(updated), nil
}

// UpdateReleaseStatus 更新发布状态
// C-1 修复：新增 isValidReleaseStatusTransition 白名单校验，防止审批被绕过：
//   - draft → scheduled / cancelled
//   - scheduled → in-progress / cancelled
//   - in-progress → completed / failed / rolled_back / cancelled
//   - completed / cancelled / rolled_back / failed 为终态（不可被复活）
func (s *ReleaseService) UpdateReleaseStatus(ctx context.Context, id, tenantID int, status string) (*dto.ReleaseResponse, error) {
	status = func() string { s1 := status; return s1 }()
	releaseEntity, err := s.client.Release.Query().
		Where(release.IDEQ(id), release.TenantIDEQ(tenantID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		s.logger.Errorw("Failed to get release", "error", err, "release_id", id)
		return nil, fmt.Errorf("failed to get release: %w", err)
	}

	// 1. 状态机白名单校验
	if !isValidReleaseStatusTransition(releaseEntity.Status, status) {
		return nil, fmt.Errorf("非法的发布状态转换: %s -> %s", releaseEntity.Status, status)
	}

	update := releaseEntity.Update().SetStatus(status)

	// 如果状态是已完成，设置实际发布日期
	if status == string(dto.ReleaseStatusCompleted) {
		now := time.Now()
		update.SetActualReleaseDate(now)
	}

	updated, err := update.Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to update release status", "error", err, "release_id", id, "status", status)
		return nil, fmt.Errorf("failed to update release status: %w", err)
	}

	// P1 域侧桥接：状态写完后完成对应的 release_approval_flow 阶段节点（注入 business_id），
	// 让流程推进到下一节点。release_task handler 会再做一次同值状态写入，状态机白名单
	// 对同值转换幂等放行。桥接失败（存在待办任务但完成不了）则中止，避免发布状态与
	// 流程状态分叉。actorUserID 传 0：阶段流转的授权边界在域层（JWT + 资源权限 +
	// 租户隔离），不强制 BPMN 任务 assignee 匹配——任务未配置处理人时强制校验会误伤
	// 合法流转（authorizeTaskActor 对 userID<=0 按设计跳过）。
	if s.approvalBridge != nil {
		if taskKey, ok := releaseStageTaskKey(status); ok {
			if _, bridgeErr := s.approvalBridge.CompleteBusinessStageTask(
				ctx, tenantID, 0, string(dto.BusinessTypeRelease), id, taskKey, nil,
			); bridgeErr != nil {
				return nil, fmt.Errorf("同步流程阶段任务失败: %w", bridgeErr)
			}
		}
	}

	s.logger.Infow("Release status updated", "release_id", id, "status", status)
	return dto.ToReleaseResponse(updated), nil
}

// releaseStageTaskKey 返回发布状态流转对应的 release_approval_flow 用户任务节点键。
// 模板只有 5 个节点（技术评审/审批/计划/执行/验证），failed/rolled_back/cancelled
// 等转换没有对应节点，返回 ok=false 表示不桥接（纯域侧状态流转）。
func releaseStageTaskKey(status string) (string, bool) {
	switch status {
	case string(dto.ReleaseStatusScheduled):
		return "Activity_Schedule", true
	case string(dto.ReleaseStatusInProgress):
		return "Activity_Execute", true
	case string(dto.ReleaseStatusCompleted):
		return "Activity_Verify", true
	default:
		return "", false
	}
}

// isValidReleaseStatusTransition 发布状态转换白名单校验
func isValidReleaseStatusTransition(current, newStatus string) bool {
	if current == newStatus {
		// 幂等：同一状态不报错
		return true
	}
	baseTransitions := map[string]map[string]struct{}{
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
			// 失败后允许重新排期或标记为回滚/取消
			string(dto.ReleaseStatusScheduled):  {},
			string(dto.ReleaseStatusRolledBack): {},
			string(dto.ReleaseStatusCancelled):  {},
		},
		// 终态：completed / cancelled / rolled_back 不允许再转换
		string(dto.ReleaseStatusCompleted):  {},
		string(dto.ReleaseStatusCancelled):  {},
		string(dto.ReleaseStatusRolledBack): {},
	}
	allowed, ok := baseTransitions[current]
	if !ok {
		return false
	}
	_, ok = allowed[newStatus]
	return ok
}

// ApplyReleaseApproval 处理发布审批（approve/reject）：校验审批人身份后先桥接完成对应的
// BPMN 待办任务（以流程任务为权威审批来源），再更新发布状态：
//   - approve: draft → scheduled
//   - reject:  draft → cancelled
//
// 无关联运行中流程实例时回退纯业务审批；若存在待办流程任务但完成失败，
// 则中止业务审批，避免发布状态与流程状态分叉（P0-1 双轨审批收敛）。
func (s *ReleaseService) ApplyReleaseApproval(ctx context.Context, id, tenantID, actorID int, action, comment string) (*dto.ReleaseResponse, error) {
	if action != "approve" && action != "reject" {
		return nil, fmt.Errorf("无效的审批操作: %s", action)
	}

	releaseEntity, err := s.client.Release.Query().
		Where(release.IDEQ(id), release.TenantIDEQ(tenantID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		s.logger.Errorw("Failed to get release for approval", "error", err, "release_id", id)
		return nil, fmt.Errorf("failed to get release: %w", err)
	}

	// 仅草稿态允许审批，防止已排期/已执行的发布被重复审批
	if releaseEntity.Status != string(dto.ReleaseStatusDraft) {
		return nil, fmt.Errorf("当前发布状态不允许审批: %s", releaseEntity.Status)
	}

	// 审批人校验：必须是本租户有效用户，且创建人不能审批自己的发布
	exists, err := s.client.User.Query().
		Where(user.ID(actorID), user.TenantID(tenantID), user.Active(true)).
		Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("校验审批人失败: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("审批人不存在或已停用")
	}
	if actorID == releaseEntity.CreatedBy {
		return nil, fmt.Errorf("发布创建人不能审批自己的发布")
	}

	// P0-1：审批先桥接完成对应的 BPMN 待办任务，失败则中止（fail-closed）
	if s.approvalBridge != nil {
		if _, bridgeErr := s.approvalBridge.CompleteBusinessApprovalTask(
			ctx, tenantID, actorID, string(dto.BusinessTypeRelease), id, action, comment,
		); bridgeErr != nil {
			return nil, fmt.Errorf("同步流程审批任务失败: %w", bridgeErr)
		}
	}

	targetStatus := string(dto.ReleaseStatusScheduled)
	if action == "reject" {
		targetStatus = string(dto.ReleaseStatusCancelled)
	}
	updated, err := releaseEntity.Update().SetStatus(targetStatus).Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to update release approval status", "error", err, "release_id", id, "status", targetStatus)
		return nil, fmt.Errorf("failed to update release status: %w", err)
	}

	// approve 分支：Release.status 在这里已经直接写成 scheduled，但 Activity_Approval
	// 完成后引擎推进到的下一个节点是 Activity_Schedule——一个独立的用户任务，不会自动
	// 完成。之前只有 UpdateReleaseStatus(id,'scheduled') 会触发这个桥接，而审批路径从不
	// 调用它，导致流程实例永久悬挂在"计划发布"（真实浏览器验证发现：release 状态能一路
	// 走到 completed，但对应 process_instances 行永远 status=running/Activity_Schedule）。
	// approve 就是"该发布已排期"的权威决策时刻，这里直接把 Schedule 节点桥接掉，
	// 不再指望前端另有一次"提交计划"点击来补这个动作。
	if action == "approve" && s.approvalBridge != nil {
		if taskKey, ok := releaseStageTaskKey(targetStatus); ok {
			if _, bridgeErr := s.approvalBridge.CompleteBusinessStageTask(
				ctx, tenantID, 0, string(dto.BusinessTypeRelease), id, taskKey, nil,
			); bridgeErr != nil {
				return nil, fmt.Errorf("同步流程计划节点失败: %w", bridgeErr)
			}
		}
	}

	s.logger.Infow("Release approval applied",
		"release_id", id, "tenant_id", tenantID, "actor_id", actorID, "action", action, "status", targetStatus)
	return dto.ToReleaseResponse(updated), nil
}

// ApplyReleaseTechReview 提交技术评审意见：桥接完成 release_approval_flow 的
// Activity_TechReview 节点（注入 business_id + comment），评审意见由 release_task
// handler 的 tech_review 动作追加到 release_notes。
//
// 无关联运行中流程实例时回退为纯业务记录（直接追加评审意见）；存在待办任务但完成失败
// 则中止，避免评审记录与流程状态分叉。actorID 不强制 BPMN 任务 assignee 匹配
// （同 UpdateReleaseStatus 的说明）。
func (s *ReleaseService) ApplyReleaseTechReview(ctx context.Context, id, tenantID, actorID int, comment string) (*dto.ReleaseResponse, error) {
	releaseEntity, err := s.client.Release.Query().
		Where(release.IDEQ(id), release.TenantIDEQ(tenantID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		s.logger.Errorw("Failed to get release for tech review", "error", err, "release_id", id)
		return nil, fmt.Errorf("failed to get release: %w", err)
	}

	comment = strings.TrimSpace(comment)

	if s.approvalBridge != nil {
		handled, bridgeErr := s.approvalBridge.CompleteBusinessStageTask(
			ctx, tenantID, actorID, string(dto.BusinessTypeRelease), id, "Activity_TechReview",
			map[string]interface{}{
				"comment": comment,
				// Gateway_TechResult 按 tech_review_pass 路由到 Activity_Approval；
				// 此前无人写这个变量，评审完成后流程停在网关上。评审意见提交即视为通过，
				// 技术否决走发布驳回（ApplyReleaseApproval reject）。
				"tech_review_pass": true,
			},
		)
		if bridgeErr != nil {
			return nil, fmt.Errorf("同步技术评审任务失败: %w", bridgeErr)
		}
		if handled {
			// 评审意见已由 handler 写入 release_notes，重新读库返回最新实体
			updated, err := s.client.Release.Query().
				Where(release.IDEQ(id), release.TenantIDEQ(tenantID)).
				First(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to reload release: %w", err)
			}
			s.logger.Infow("Release tech review bridged to BPMN task",
				"release_id", id, "tenant_id", tenantID, "actor_id", actorID)
			return dto.ToReleaseResponse(updated), nil
		}
	}

	// 回退：无关联运行中流程实例时直接追加评审意见（与 handler 的 tech_review 格式一致）
	if comment != "" {
		notes := releaseEntity.ReleaseNotes
		if notes != "" {
			notes += "\n"
		}
		notes += fmt.Sprintf("[技术评审] %s", comment)
		updated, err := releaseEntity.Update().SetReleaseNotes(notes).Save(ctx)
		if err != nil {
			s.logger.Errorw("Failed to record tech review", "error", err, "release_id", id)
			return nil, fmt.Errorf("failed to record tech review: %w", err)
		}
		return dto.ToReleaseResponse(updated), nil
	}

	s.logger.Infow("Release tech review recorded (empty comment)",
		"release_id", id, "tenant_id", tenantID, "actor_id", actorID)
	return dto.ToReleaseResponse(releaseEntity), nil
}

// DeleteRelease 删除发布
func (s *ReleaseService) DeleteRelease(ctx context.Context, id, tenantID int) error {
	_, err := s.client.Release.Delete().
		Where(release.IDEQ(id), release.TenantIDEQ(tenantID)).
		Exec(ctx)
	if err != nil {
		s.logger.Errorw("Failed to delete release", "error", err, "release_id", id)
		return fmt.Errorf("failed to delete release: %w", err)
	}

	s.logger.Infow("Release deleted", "release_id", id)
	return nil
}

// GetReleaseStats 获取发布统计
func (s *ReleaseService) GetReleaseStats(ctx context.Context, tenantID int) (*dto.ReleaseStatsResponse, error) {
	stats := &dto.ReleaseStatsResponse{}

	total, err := s.client.Release.Query().Where(release.TenantIDEQ(tenantID)).Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count releases", "error", err)
		return nil, err
	}
	stats.Total = total

	// 统计各状态数量
	draft, _ := s.client.Release.Query().Where(release.TenantIDEQ(tenantID), release.StatusEQ(string(dto.ReleaseStatusDraft))).Count(ctx)
	scheduled, _ := s.client.Release.Query().Where(release.TenantIDEQ(tenantID), release.StatusEQ(string(dto.ReleaseStatusScheduled))).Count(ctx)
	inProgress, _ := s.client.Release.Query().Where(release.TenantIDEQ(tenantID), release.StatusEQ(string(dto.ReleaseStatusInProgress))).Count(ctx)
	completed, _ := s.client.Release.Query().Where(release.TenantIDEQ(tenantID), release.StatusEQ(string(dto.ReleaseStatusCompleted))).Count(ctx)
	cancelled, _ := s.client.Release.Query().Where(release.TenantIDEQ(tenantID), release.StatusEQ(string(dto.ReleaseStatusCancelled))).Count(ctx)
	failed, _ := s.client.Release.Query().Where(release.TenantIDEQ(tenantID), release.StatusEQ(string(dto.ReleaseStatusFailed))).Count(ctx)
	rolledBack, _ := s.client.Release.Query().Where(release.TenantIDEQ(tenantID), release.StatusEQ(string(dto.ReleaseStatusRolledBack))).Count(ctx)

	stats.Draft = draft
	stats.Scheduled = scheduled
	stats.InProgress = inProgress
	stats.Completed = completed
	stats.Cancelled = cancelled
	stats.Failed = failed
	stats.RolledBack = rolledBack

	return stats, nil
}
