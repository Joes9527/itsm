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
	"itsm-backend/service/bpmn"

	"go.uber.org/zap"
)

// ReleaseService 发布管理服务
type ReleaseService struct {
	client            *ent.Client
	logger            *zap.SugaredLogger
	processEngine     ProcessEngine
	processTriggerSvc ProcessTriggerServiceInterface
}

// NewReleaseService 创建发布管理服务
func NewReleaseService(client *ent.Client, logger *zap.SugaredLogger) *ReleaseService {
	return &ReleaseService{
		client: client,
		logger: logger,
	}
}

// SetProcessEngine 注入全局流程引擎（已完成 CallbackRegistry 依赖装配的那一个），
// 由 bootstrap 调用，理由同 TicketWorkflowService.SetProcessEngine。
func (s *ReleaseService) SetProcessEngine(engine ProcessEngine) {
	s.processEngine = engine
}

// SetProcessTriggerService 注入流程触发服务（创建发布后自动启动 release_approval_flow）。
// 与 TicketService.SetProcessTriggerService 同一模式：由 bootstrap 装配。
func (s *ReleaseService) SetProcessTriggerService(p ProcessTriggerServiceInterface) {
	s.processTriggerSvc = p
}

// CreateRelease 创建发布
func (s *ReleaseService) CreateRelease(ctx context.Context, req *dto.CreateReleaseRequest, createdBy, tenantID int) (*dto.ReleaseResponse, error) {
	if s.processEngine == nil {
		return nil, fmt.Errorf("release workflow engine is unavailable")
	}
	if s.processTriggerSvc == nil {
		return nil, fmt.Errorf("release workflow trigger is unavailable")
	}
	transactionalTrigger, ok := s.processTriggerSvc.(TransactionalProcessTrigger)
	if !ok {
		return nil, fmt.Errorf("release workflow trigger does not support atomic start")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start release creation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txClient := tx.Client()

	releaseEntity, err := txClient.Release.Create().
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
	creator, _ := txClient.User.Get(ctx, createdBy)
	creatorName := ""
	if creator != nil {
		creatorName = creator.Name
	}

	triggerCtx := WithTrustedBPMNTenantContext(ctx, tenantID)
	processStart, err := transactionalTrigger.TriggerByBusinessTypeWithClient(
		triggerCtx, txClient, dto.BusinessTypeRelease, releaseEntity.ID,
		nil,
		strconv.Itoa(createdBy), tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("start release workflow atomically: %w", err)
	}
	if err := processStart.validateIdentity(dto.BusinessTypeRelease, releaseEntity.ID, tenantID); err != nil {
		return nil, fmt.Errorf("invalid workflow start: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit release and workflow: %w", err)
	}
	releaseEntity.Unwrap()
	processStart.DeliverCommittedCallbacks(ctx)

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
func (s *ReleaseService) UpdateReleaseStatus(ctx context.Context, id, tenantID, actorID int, status string) (*dto.ReleaseResponse, error) {
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

	if taskKey, workflowOwned := releaseStageTaskKey(status); workflowOwned {
		if err := s.completeReleaseWorkflowTask(ctx, tenantID, actorID, id, taskKey, nil); err != nil {
			return nil, fmt.Errorf("完成发布流程阶段失败: %w", err)
		}
		updated, err := s.client.Release.Query().Where(release.IDEQ(id), release.TenantIDEQ(tenantID)).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("重新加载发布状态失败: %w", err)
		}
		if updated.Status != status {
			return nil, fmt.Errorf("发布流程回调未应用目标状态: expected %s, got %s", status, updated.Status)
		}
		s.logger.Infow("Release status updated through BPMN", "release_id", id, "status", status)
		return dto.ToReleaseResponse(updated), nil
	}

	update := releaseEntity.Update().SetStatus(status)
	if status == string(dto.ReleaseStatusCompleted) {
		update.SetActualReleaseDate(time.Now())
	}
	updated, err := update.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to update release status: %w", err)
	}

	s.logger.Infow("Release status updated", "release_id", id, "status", status)
	return dto.ToReleaseResponse(updated), nil
}

// releaseStageTaskKey returns the authoritative ProcessTask for lifecycle
// stages modeled in release_approval_flow. Failure/rollback/cancellation remain
// explicit professional commands because the BPMN definition has no such task.
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

// ApplyReleaseWorkflowCallback is the single professional persistence boundary
// for release BPMN callbacks. Updates use compare-and-swap predicates so a
// retry is idempotent and competing callbacks cannot both commit from the same
// observed state.
func (s *ReleaseService) ApplyReleaseWorkflowCallback(ctx context.Context, command bpmn.ReleaseWorkflowCommand) (*bpmn.ReleaseWorkflowMutation, error) {
	if command.ReleaseID <= 0 || command.TenantID <= 0 {
		return nil, fmt.Errorf("release workflow callback identity is invalid")
	}
	current, err := s.client.Release.Query().Where(
		release.ID(command.ReleaseID),
		release.TenantID(command.TenantID),
	).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load release workflow target: %w", err)
	}

	switch command.Action {
	case bpmn.ReleaseWorkflowActionTechReview:
		comment := strings.TrimSpace(command.Comment)
		entry := fmt.Sprintf("[技术评审] %s", comment)
		if comment == "" || strings.Contains(current.ReleaseNotes, entry) {
			return &bpmn.ReleaseWorkflowMutation{Message: "技术评审意见已记录"}, nil
		}
		notes := entry
		if current.ReleaseNotes != "" {
			notes = current.ReleaseNotes + "\n" + entry
		}
		update := current.Update().Where(release.TenantID(command.TenantID))
		if current.ReleaseNotes == "" {
			update = update.Where(release.Or(release.ReleaseNotesEQ(""), release.ReleaseNotesIsNil()))
		} else {
			update = update.Where(release.ReleaseNotesEQ(current.ReleaseNotes))
		}
		_, err = update.SetReleaseNotes(notes).Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("record release technical review with compare-and-swap: %w", err)
		}
		return &bpmn.ReleaseWorkflowMutation{Changed: true, Message: "技术评审意见已记录"}, nil

	case bpmn.ReleaseWorkflowActionReject:
		command.TargetStatus = string(dto.ReleaseStatusCancelled)
	case bpmn.ReleaseWorkflowActionStatus:
		if strings.TrimSpace(command.TargetStatus) == "" {
			return nil, fmt.Errorf("release workflow target status is required")
		}
	default:
		return nil, fmt.Errorf("unsupported release workflow callback action %q", command.Action)
	}

	if current.Status == command.TargetStatus {
		return &bpmn.ReleaseWorkflowMutation{Message: fmt.Sprintf("发布 %d 已处于 %s", command.ReleaseID, command.TargetStatus)}, nil
	}
	if !isValidReleaseStatusTransition(current.Status, command.TargetStatus) {
		return nil, fmt.Errorf("非法的发布状态转换: %s -> %s", current.Status, command.TargetStatus)
	}
	update := current.Update().Where(
		release.TenantID(command.TenantID),
		release.StatusEQ(current.Status),
	).SetStatus(command.TargetStatus)
	if command.TargetStatus == string(dto.ReleaseStatusCompleted) {
		update = update.SetActualReleaseDate(time.Now())
	}
	if _, err := update.Save(ctx); err != nil {
		return nil, fmt.Errorf("apply release workflow callback with compare-and-swap: %w", err)
	}
	return &bpmn.ReleaseWorkflowMutation{
		Changed: true,
		Message: fmt.Sprintf("发布 %d 状态已更新为 %s", command.ReleaseID, command.TargetStatus),
	}, nil
}

// ApplyReleaseTechReview completes the authoritative Activity_TechReview
// ProcessTask. Missing engine, instance, or task fails closed.
func (s *ReleaseService) ApplyReleaseTechReview(ctx context.Context, id, tenantID, actorID int, comment string) (*dto.ReleaseResponse, error) {
	_, err := s.client.Release.Query().
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

	if err := s.completeReleaseWorkflowTask(
		ctx, tenantID, actorID, id, "Activity_TechReview",
		map[string]interface{}{
			"comment": comment,
			// Gateway_TechResult 按 tech_review_pass 路由到 Activity_Approval；
			// 此前无人写这个变量，评审完成后流程停在网关上。评审意见提交即视为通过，
			// 审批结果由后续唯一 ProcessTask decision 命令提交。
			"tech_review_pass": true,
		},
	); err != nil {
		return nil, fmt.Errorf("完成技术评审流程任务失败: %w", err)
	}
	updated, err := s.client.Release.Query().Where(release.IDEQ(id), release.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to reload release: %w", err)
	}
	return dto.ToReleaseResponse(updated), nil
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
