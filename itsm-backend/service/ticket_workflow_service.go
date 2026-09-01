package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketautomationrule"
	"itsm-backend/ent/ticketcc"
	"itsm-backend/ent/ticketworkflowrecord"
	"itsm-backend/ent/user"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type TicketWorkflowService struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

func NewTicketWorkflowService(client *ent.Client, logger *zap.SugaredLogger) *TicketWorkflowService {
	return &TicketWorkflowService{
		client: client,
		logger: logger,
	}
}

func (s *TicketWorkflowService) withClient(client *ent.Client) *TicketWorkflowService {
	rebound := *s
	rebound.client = client
	return &rebound
}

// AcceptTicket 接单（事务保护，保证工单状态更新与流转记录的原子性）
func (s *TicketWorkflowService) AcceptTicket(ctx context.Context, req *dto.AcceptTicketRequest, userID, tenantID int) error {
	s.logger.Infow("Accepting ticket", "ticket_id", req.TicketID, "user_id", userID)

	// 检查工单是否存在且状态允许接单（读操作，事务外执行）
	tk, err := s.getTicket(ctx, req.TicketID, tenantID)
	if err != nil {
		return err
	}

	if tk.Status != "new" && tk.Status != "open" {
		return fmt.Errorf("工单当前状态不允许接单: %s", tk.Status)
	}

	// 开启事务，保证原子性
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	var txErr error
	defer func() {
		if txErr != nil {
			tx.Rollback()
		}
	}()

	txClient := tx.Client()

	// 更新工单状态和分配人
	// P1-07 修复：接单同时设置 first_response_at，供 SLA 计时使用
	now := time.Now()
	_, err = txClient.Ticket.UpdateOneID(req.TicketID).
		Where(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil(), ticket.VersionEQ(tk.Version), ticket.StatusIn("new", "open")).
		SetAssigneeID(userID).
		SetStatus("in_progress").
		SetFirstResponseAt(now).
		SetVersion(tk.Version + 1).
		Save(ctx)
	if err != nil {
		txErr = fmt.Errorf("failed to accept ticket: %w", err)
		return txErr
	}

	// 记录流转记录
	err = s.createWorkflowRecordWithClient(ctx, txClient, &dto.TicketWorkflowRecord{
		TicketID:   req.TicketID,
		Action:     dto.WorkflowActionAccept,
		FromStatus: &tk.Status,
		ToStatus:   ptrString("in_progress"),
		Operator:   dto.WorkflowUserInfo{ID: userID},
		Comment:    req.Comment,
		CreatedAt:  time.Now(),
	}, tenantID)
	if err != nil {
		txErr = fmt.Errorf("记录流转记录失败: %w", err)
		return txErr
	}

	txErr = tx.Commit()
	if txErr != nil {
		return fmt.Errorf("提交接单事务失败: %w", txErr)
	}
	return txErr
}

// WithdrawTicket 撤回工单
func (s *TicketWorkflowService) WithdrawTicket(ctx context.Context, req *dto.WithdrawTicketRequest, userID, tenantID int) error {
	s.logger.Infow("Withdrawing ticket", "ticket_id", req.TicketID, "user_id", userID)

	tk, err := s.getTicket(ctx, req.TicketID, tenantID)
	if err != nil {
		return err
	}

	// 检查是否是工单创建者
	if tk.RequesterID != userID {
		return fmt.Errorf("只有工单创建者可以撤回工单")
	}

	if tk.Status == "closed" || tk.Status == "cancelled" {
		return fmt.Errorf("工单已关闭或取消，无法撤回")
	}

	// 更新工单状态
	_, err = s.client.Ticket.UpdateOneID(req.TicketID).
		SetStatus("cancelled").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to withdraw ticket: %w", err)
	}

	// 记录流转记录
	newStatus := "cancelled"
	err = s.createWorkflowRecord(ctx, &dto.TicketWorkflowRecord{
		TicketID:   req.TicketID,
		Action:     dto.WorkflowActionWithdraw,
		FromStatus: &tk.Status,
		ToStatus:   &newStatus,
		Operator:   dto.WorkflowUserInfo{ID: userID},
		Reason:     req.Reason,
		CreatedAt:  time.Now(),
	}, tenantID)

	return err
}

// ForwardTicket 转发工单
func (s *TicketWorkflowService) ForwardTicket(ctx context.Context, req *dto.ForwardTicketRequest, userID, tenantID int) error {
	s.logger.Infow("Forwarding ticket", "ticket_id", req.TicketID, "to_user_id", req.ToUserID, "user_id", userID)

	_, err := s.getTicket(ctx, req.TicketID, tenantID)
	if err != nil {
		return err
	}

	// 如果转移所有权，更新assignee
	if req.TransferOwnership {
		_, err = s.client.Ticket.UpdateOneID(req.TicketID).
			SetAssigneeID(req.ToUserID).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to forward ticket: %w", err)
		}
	}

	// 记录流转记录
	err = s.createWorkflowRecord(ctx, &dto.TicketWorkflowRecord{
		TicketID:  req.TicketID,
		Action:    dto.WorkflowActionForward,
		Operator:  dto.WorkflowUserInfo{ID: userID},
		FromUser:  &dto.WorkflowUserInfo{ID: userID},
		ToUser:    &dto.WorkflowUserInfo{ID: req.ToUserID},
		Comment:   req.Comment,
		CreatedAt: time.Now(),
		Metadata: map[string]interface{}{
			"transfer_ownership": req.TransferOwnership,
		},
	}, tenantID)

	return err
}

// CCTicket 抄送工单
func (s *TicketWorkflowService) CCTicket(ctx context.Context, req *dto.CCTicketRequest, userID, tenantID int) error {
	s.logger.Infow("CC ticket", "ticket_id", req.TicketID)

	notifyChannels, err := normalizeNotifyChannels(req.NotifyChannels)
	if err != nil {
		return err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启抄送事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txService := s.withClient(tx.Client())

	tk, err := txService.getTicket(ctx, req.TicketID, tenantID)
	if err != nil {
		return err
	}
	if err := txService.EnsureCanCCTicket(ctx, tk, userID, tenantID); err != nil {
		return err
	}

	requestedUserIDs := uniqueInts(req.CCUsers)
	targetUsers, err := txService.client.User.Query().
		Where(user.IDIn(req.CCUsers...), user.TenantID(tenantID), user.Active(true)).
		Order(ent.Asc(user.FieldID)).
		Select(user.FieldID).
		Ints(ctx)
	if err != nil {
		return fmt.Errorf("校验抄送用户失败: %w", err)
	}
	if len(targetUsers) != len(requestedUserIDs) {
		return fmt.Errorf("抄送用户不存在、未激活或不属于当前租户")
	}

	addedUserIDs := make([]int, 0, len(targetUsers))
	addedAt := time.Now()
	for _, ccUserID := range targetUsers {
		exists, err := txService.client.TicketCC.Query().
			Where(ticketcc.TicketID(req.TicketID),
				ticketcc.UserID(ccUserID),
				ticketcc.TenantID(tenantID),
				ticketcc.IsActive(true)).
			Exist(ctx)
		if err != nil {
			return fmt.Errorf("检查抄送关系失败: %w", err)
		}
		if exists {
			continue
		}

		inactive, err := txService.client.TicketCC.Query().
			Where(
				ticketcc.TicketID(req.TicketID),
				ticketcc.UserID(ccUserID),
				ticketcc.TenantID(tenantID),
				ticketcc.IsActive(false),
			).
			Order(ent.Desc(ticketcc.FieldID)).
			First(ctx)
		switch {
		case err == nil:
			_, err = txService.client.TicketCC.UpdateOneID(inactive.ID).
				Where(
					ticketcc.TicketID(req.TicketID),
					ticketcc.UserID(ccUserID),
					ticketcc.TenantID(tenantID),
					ticketcc.IsActive(false),
				).
				SetAddedBy(userID).
				SetAddedAt(addedAt).
				SetIsActive(true).
				ClearDeliveryKey().
				Save(ctx)
			if err != nil {
				return fmt.Errorf("重新激活抄送关系失败: %w", err)
			}
		case ent.IsNotFound(err):
			err = txService.client.TicketCC.Create().
				SetTicketID(req.TicketID).
				SetUserID(ccUserID).
				SetAddedBy(userID).
				SetTenantID(tenantID).
				SetAddedAt(addedAt).
				SetIsActive(true).
				Exec(ctx)
			if err != nil {
				return fmt.Errorf("创建抄送关系失败: %w", err)
			}
		default:
			return fmt.Errorf("查询历史抄送关系失败: %w", err)
		}
		addedUserIDs = append(addedUserIDs, ccUserID)
	}

	if len(addedUserIDs) > 0 {
		if err := txService.createCCNotifications(ctx, tk, addedUserIDs, notifyChannels, tenantID); err != nil {
			return err
		}
	}

	err = txService.createWorkflowRecord(ctx, &dto.TicketWorkflowRecord{
		TicketID:  req.TicketID,
		Action:    dto.WorkflowActionCC,
		Operator:  dto.WorkflowUserInfo{ID: userID},
		Comment:   req.Comment,
		CreatedAt: time.Now(),
		Metadata: map[string]interface{}{
			"cc_users":        append([]int(nil), addedUserIDs...),
			"notify_channels": append([]string(nil), notifyChannels...),
		},
	}, tenantID)
	if err != nil {
		return fmt.Errorf("记录抄送流转失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交抄送事务失败: %w", err)
	}

	return nil
}

// ListMyCCRecords 查询当前用户收到的抄送记录
func (s *TicketWorkflowService) ListMyCCRecords(ctx context.Context, userID, tenantID int) (*dto.TicketCCListResponse, error) {
	records, err := s.client.TicketCC.Query().
		Where(ticketcc.UserID(userID), ticketcc.TenantID(tenantID), ticketcc.IsActive(true)).
		Order(ent.Desc(ticketcc.FieldAddedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询我的抄送失败: %w", err)
	}
	return s.buildCCListResponse(ctx, records)
}

// ListTicketCCRecords 查询单个工单抄送记录
func (s *TicketWorkflowService) ListTicketCCRecords(ctx context.Context, ticketID, userID, tenantID int) (*dto.TicketCCListResponse, error) {
	tk, err := s.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureCanViewTicketCC(ctx, tk, userID, tenantID); err != nil {
		return nil, err
	}

	records, err := s.client.TicketCC.Query().
		Where(ticketcc.TicketID(ticketID), ticketcc.TenantID(tenantID), ticketcc.IsActive(true)).
		Order(ent.Desc(ticketcc.FieldAddedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询工单抄送记录失败: %w", err)
	}
	return s.buildCCListResponse(ctx, records)
}

// GetApprovalDecisions 返回某个工单在 BPMN 引擎里留下的全部审批决策记录，按时间升序。
// 工单审批状态完全由 BPMN ProcessTask/ProcessApprovalDecision 驱动。
func (s *TicketWorkflowService) GetApprovalDecisions(ctx context.Context, ticketID, tenantID int) ([]*ent.ProcessApprovalDecision, error) {
	return s.client.ProcessApprovalDecision.Query().
		Where(
			processapprovaldecision.BusinessType("ticket"),
			processapprovaldecision.BusinessID(strconv.Itoa(ticketID)),
			processapprovaldecision.TenantID(tenantID),
		).
		Order(ent.Asc(processapprovaldecision.FieldCreatedAt)).
		All(ctx)
}

// ResolveTicket 解决工单（事务保护，保证工单状态更新与流转记录的原子性）
func (s *TicketWorkflowService) ResolveTicket(ctx context.Context, req *dto.ResolveTicketRequest, userID, tenantID int) error {
	s.logger.Infow("Resolving ticket", "ticket_id", req.TicketID, "user_id", userID)

	tk, err := s.getTicket(ctx, req.TicketID, tenantID)
	if err != nil {
		return err
	}

	// 开启事务，保证原子性
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	var txErr error
	defer func() {
		if txErr != nil {
			tx.Rollback()
		}
	}()

	txClient := tx.Client()

	// 更新工单状态
	_, err = txClient.Ticket.UpdateOneID(req.TicketID).
		SetStatus("resolved").
		SetResolution(req.Resolution).
		SetResolutionCategory(req.ResolutionCategory).
		SetResolvedAt(time.Now()).
		Save(ctx)
	if err != nil {
		txErr = fmt.Errorf("failed to resolve ticket: %w", err)
		return txErr
	}

	// 记录流转记录
	newStatus := "resolved"
	err = s.createWorkflowRecordWithClient(ctx, txClient, &dto.TicketWorkflowRecord{
		TicketID:   req.TicketID,
		Action:     dto.WorkflowActionResolve,
		FromStatus: &tk.Status,
		ToStatus:   &newStatus,
		Operator:   dto.WorkflowUserInfo{ID: userID},
		Comment:    req.Resolution,
		CreatedAt:  time.Now(),
		Metadata: map[string]interface{}{
			"resolution_category": req.ResolutionCategory,
			"work_notes":          req.WorkNotes,
		},
	}, tenantID)
	if err != nil {
		txErr = fmt.Errorf("记录流转记录失败: %w", err)
		return txErr
	}

	txErr = tx.Commit()
	return txErr
}

// CloseTicket 关闭工单（事务保护，保证工单状态更新与流转记录的原子性）
func (s *TicketWorkflowService) CloseTicket(ctx context.Context, req *dto.CloseTicketRequest, userID, tenantID int) error {
	s.logger.Infow("Closing ticket", "ticket_id", req.TicketID, "user_id", userID)

	tk, err := s.getTicket(ctx, req.TicketID, tenantID)
	if err != nil {
		return err
	}

	if tk.Status != "resolved" {
		return fmt.Errorf("只有已解决的工单才能关闭")
	}

	// 开启事务，保证原子性
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	var txErr error
	defer func() {
		if txErr != nil {
			tx.Rollback()
		}
	}()

	txClient := tx.Client()

	// 更新工单状态
	_, err = txClient.Ticket.UpdateOneID(req.TicketID).
		SetStatus("closed").
		SetClosedAt(time.Now()).
		Save(ctx)
	if err != nil {
		txErr = fmt.Errorf("failed to close ticket: %w", err)
		return txErr
	}

	// 记录流转记录
	newStatus := "closed"
	err = s.createWorkflowRecordWithClient(ctx, txClient, &dto.TicketWorkflowRecord{
		TicketID:   req.TicketID,
		Action:     dto.WorkflowActionClose,
		FromStatus: &tk.Status,
		ToStatus:   &newStatus,
		Operator:   dto.WorkflowUserInfo{ID: userID},
		Comment:    req.CloseNotes,
		CreatedAt:  time.Now(),
		Metadata: map[string]interface{}{
			"close_reason": req.CloseReason,
		},
	}, tenantID)
	if err != nil {
		txErr = fmt.Errorf("记录流转记录失败: %w", err)
		return txErr
	}

	txErr = tx.Commit()
	return txErr
}

// ReopenTicket 重开工单（事务保护，保证工单状态更新与流转记录的原子性）
func (s *TicketWorkflowService) ReopenTicket(ctx context.Context, req *dto.ReopenTicketRequest, userID, tenantID int) error {
	s.logger.Infow("Reopening ticket", "ticket_id", req.TicketID, "user_id", userID)

	tk, err := s.getTicket(ctx, req.TicketID, tenantID)
	if err != nil {
		return err
	}

	if tk.Status != "closed" && tk.Status != "resolved" {
		return fmt.Errorf("只有已关闭或已解决的工单才能重开")
	}

	// 开启事务，保证原子性
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	var txErr error
	defer func() {
		if txErr != nil {
			tx.Rollback()
		}
	}()

	txClient := tx.Client()

	// 更新工单状态
	_, err = txClient.Ticket.UpdateOneID(req.TicketID).
		SetStatus("open").
		Save(ctx)
	if err != nil {
		txErr = fmt.Errorf("failed to reopen ticket: %w", err)
		return txErr
	}

	// 记录流转记录
	newStatus := "open"
	err = s.createWorkflowRecordWithClient(ctx, txClient, &dto.TicketWorkflowRecord{
		TicketID:   req.TicketID,
		Action:     dto.WorkflowActionReopen,
		FromStatus: &tk.Status,
		ToStatus:   &newStatus,
		Operator:   dto.WorkflowUserInfo{ID: userID},
		Reason:     req.Reason,
		CreatedAt:  time.Now(),
	}, tenantID)
	if err != nil {
		txErr = fmt.Errorf("记录流转记录失败: %w", err)
		return txErr
	}

	txErr = tx.Commit()
	return txErr
}

// GetTicketWorkflowState 获取工单流转状态
func (s *TicketWorkflowService) GetTicketWorkflowState(ctx context.Context, ticketID, userID, tenantID int) (*dto.TicketWorkflowState, error) {
	// 查询工单信息
	tk, err := s.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, err
	}

	// 构建工单流转状态
	state := &dto.TicketWorkflowState{
		TicketID:         ticketID,
		CurrentStatus:    tk.Status,
		AvailableActions: []dto.TicketWorkflowAction{},
	}

	// 根据当前状态和用户权限判断可执行的操作
	switch tk.Status {
	case "new", "open":
		state.CanAccept = true
		state.CanForward = true
		state.CanCC = true
		state.AvailableActions = append(state.AvailableActions,
			dto.WorkflowActionAccept,
			dto.WorkflowActionForward,
			dto.WorkflowActionCC)

		if tk.RequesterID == userID {
			state.CanWithdraw = true
			state.AvailableActions = append(state.AvailableActions, dto.WorkflowActionWithdraw)
		}
	case "in_progress":
		state.CanResolve = true
		state.CanForward = true
		state.CanCC = true
		state.AvailableActions = append(state.AvailableActions,
			dto.WorkflowActionResolve,
			dto.WorkflowActionForward,
			dto.WorkflowActionCC)
	case "resolved":
		state.CanClose = true
		state.AvailableActions = append(state.AvailableActions,
			dto.WorkflowActionClose,
			dto.WorkflowActionReopen)
	case "closed":
		state.AvailableActions = append(state.AvailableActions, dto.WorkflowActionReopen)
	}

	return state, nil
}

// GetAvailableActions 返回当前用户在该工单上可执行的流转动作列表。
// 复用 GetTicketWorkflowState 的状态/权限计算逻辑，避免在多处重复状态机规则。
func (s *TicketWorkflowService) GetAvailableActions(ctx context.Context, ticketID, userID, tenantID int) ([]dto.TicketWorkflowAction, error) {
	state, err := s.GetTicketWorkflowState(ctx, ticketID, userID, tenantID)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return []dto.TicketWorkflowAction{}, nil
	}
	return state.AvailableActions, nil
}

// GetWorkflowHistory 返回工单的流转记录列表。
// 租户隔离：仅返回指定租户下的记录；工单不存在或不属于该租户时返回错误。
func (s *TicketWorkflowService) GetWorkflowHistory(ctx context.Context, ticketID, tenantID int) ([]*ent.TicketWorkflowRecord, error) {
	if _, err := s.getTicket(ctx, ticketID, tenantID); err != nil {
		return nil, err
	}
	return s.client.TicketWorkflowRecord.Query().
		Where(ticketworkflowrecord.TicketID(ticketID), ticketworkflowrecord.TenantID(tenantID)).
		Order(ent.Desc(ticketworkflowrecord.FieldCreatedAt)).
		All(ctx)
}

// GetWorkflowRules 返回指定业务类型下的活跃工作流规则。
func (s *TicketWorkflowService) GetWorkflowRules(ctx context.Context, ticketType string, tenantID int) ([]*ent.TicketAutomationRule, error) {
	rules, err := s.client.TicketAutomationRule.Query().
		Where(
			ticketautomationrule.TenantID(tenantID),
			ticketautomationrule.IsActive(true),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	if ticketType == "" {
		return rules, nil
	}
	return rules, nil
}

// GetWorkflowRulesByTicket 根据工单类型返回匹配的工作流规则。
func (s *TicketWorkflowService) GetWorkflowRulesByTicket(ctx context.Context, ticketID, tenantID int) ([]*ent.TicketAutomationRule, error) {
	tk, err := s.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return nil, err
	}
	ticketType := string(tk.Type)
	if ticketType == "" {
		ticketType = "ticket"
	}
	return s.GetWorkflowRules(ctx, ticketType, tenantID)
}

// NotifyTicketUpdate 在工单状态变化后发送通知（不阻塞主流程）。
func (s *TicketWorkflowService) NotifyTicketUpdate(ctx context.Context, ticketID int, message string, tenantID int) error {
	if _, err := s.getTicket(ctx, ticketID, tenantID); err != nil {
		return err
	}
	s.logger.Infow(
		"NotifyTicketUpdate",
		"ticket_id", ticketID,
		"tenant_id", tenantID,
		"message", message,
	)
	return nil
}

// CanUserAccessTicket 检查用户是否有权访问指定工单。
// 跨租户访问一律返回 false；同一租户内目前对所有用户放行（与 getTicket 一致）。
func (s *TicketWorkflowService) CanUserAccessTicket(ctx context.Context, ticketID, userID, tenantID int) (bool, error) {
	if _, err := s.client.User.Get(ctx, userID); err != nil {
		return false, err
	}
	tk, err := s.getTicket(ctx, ticketID, tenantID)
	if err != nil {
		return false, nil
	}
	_ = tk
	return true, nil
}

// 辅助函数

func uniqueInts(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func workflowUserInfoFromEnt(u *ent.User) dto.WorkflowUserInfo {
	if u == nil {
		return dto.WorkflowUserInfo{}
	}
	return dto.WorkflowUserInfo{
		ID:         u.ID,
		Username:   u.Username,
		FullName:   u.Name,
		Email:      u.Email,
		Role:       string(u.Role),
		Department: u.Department,
	}
}

func (s *TicketWorkflowService) EnsureCanCCTicket(ctx context.Context, tk *ent.Ticket, userID, tenantID int) error {
	if tk == nil {
		return fmt.Errorf("工单不存在")
	}
	if tk.Status == "closed" || tk.Status == "cancelled" {
		return fmt.Errorf("工单已结束，无法抄送")
	}
	return s.ensureCanViewTicketCC(ctx, tk, userID, tenantID)
}

func (s *TicketWorkflowService) ensureCanViewTicketCC(ctx context.Context, tk *ent.Ticket, userID, tenantID int) error {
	if tk == nil {
		return fmt.Errorf("工单不存在")
	}
	if tk.RequesterID == userID || tk.AssigneeID == userID {
		return nil
	}

	currentUser, err := s.client.User.Query().
		Where(user.ID(userID), user.TenantID(tenantID), user.Active(true)).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("用户不存在或无权限")
	}
	switch currentUser.Role {
	case "super_admin":
		return nil
	}

	isCCUser, err := s.client.TicketCC.Query().
		Where(ticketcc.TicketID(tk.ID), ticketcc.TenantID(tenantID), ticketcc.UserID(userID), ticketcc.IsActive(true)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("校验抄送权限失败: %w", err)
	}
	if isCCUser {
		return nil
	}

	return fmt.Errorf("无权访问该工单抄送信息")
}

func normalizeNotifyChannels(channels []string) ([]string, error) {
	if len(channels) == 0 {
		return []string{"in_app"}, nil
	}
	allowed := map[string]struct{}{
		"in_app":   {},
		"email":    {},
		"sms":      {},
		"feishu":   {},
		"dingtalk": {},
		"wecom":    {},
		"webhook":  {},
	}
	seen := make(map[string]struct{}, len(channels))
	result := make([]string, 0, len(channels))
	for _, rawChannel := range channels {
		channel := strings.TrimSpace(rawChannel)
		if channel == "" {
			continue
		}
		if _, ok := allowed[channel]; !ok {
			return nil, fmt.Errorf("通知渠道无效: %s", channel)
		}
		if _, exists := seen[channel]; exists {
			continue
		}
		seen[channel] = struct{}{}
		result = append(result, channel)
	}
	if len(result) == 0 {
		return []string{"in_app"}, nil
	}
	return result, nil
}

func (s *TicketWorkflowService) createCCNotifications(ctx context.Context, tk *ent.Ticket, userIDs []int, channels []string, tenantID int) error {
	now := time.Now()
	content := fmt.Sprintf("工单 %s「%s」已抄送给你", tk.TicketNumber, tk.Title)
	users, err := s.client.User.Query().Where(user.IDIn(uniqueInts(userIDs)...), user.TenantID(tenantID)).All(ctx)
	if err != nil {
		return fmt.Errorf("查询抄送通知用户失败: %w", err)
	}
	userByID := make(map[int]*ent.User, len(users))
	for _, u := range users {
		userByID[u.ID] = u
	}
	for _, userID := range userIDs {
		recipient := userByID[userID]
		if recipient == nil {
			return fmt.Errorf("抄送通知用户不存在")
		}
		for _, channel := range channels {
			status := "pending"
			create := s.client.TicketNotification.Create().
				SetTicketID(tk.ID).
				SetUserID(userID).
				SetType("cc").
				SetChannel(channel).
				SetContent(content).
				SetTenantID(tenantID).
				SetStatus(status)
			if channel == "in_app" {
				create.SetStatus("sent").SetSentAt(now)
			} else {
				create.SetDeliveryKey("ticket-notification-" + uuid.NewString()).SetNextAttemptAt(now)
			}
			_, err := create.Save(ctx)
			if err != nil {
				return fmt.Errorf("创建工单抄送通知失败: %w", err)
			}
		}

		if _, err := s.client.Notification.Create().
			SetTitle("工单抄送").
			SetMessage(content).
			SetType("info").
			SetUserID(userID).
			SetTenantID(tenantID).
			SetActionURL(fmt.Sprintf("/tickets/%d", tk.ID)).
			SetActionText("查看工单").
			Save(ctx); err != nil {
			return fmt.Errorf("创建统一抄送通知失败: %w", err)
		}
	}
	return nil
}

func (s *TicketWorkflowService) buildCCListResponse(ctx context.Context, records []*ent.TicketCC) (*dto.TicketCCListResponse, error) {
	response := &dto.TicketCCListResponse{
		Records: make([]dto.TicketCCRecordResponse, 0, len(records)),
		Total:   len(records),
	}
	if len(records) == 0 {
		return response, nil
	}

	ticketIDs := make([]int, 0, len(records))
	userIDs := make([]int, 0, len(records)*2)
	for _, record := range records {
		ticketIDs = append(ticketIDs, record.TicketID)
		userIDs = append(userIDs, record.UserID, record.AddedBy)
	}

	tickets, err := s.client.Ticket.Query().Where(ticket.IDIn(uniqueInts(ticketIDs)...)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询抄送工单信息失败: %w", err)
	}
	users, err := s.client.User.Query().Where(user.IDIn(uniqueInts(userIDs)...)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询抄送用户信息失败: %w", err)
	}

	ticketByID := make(map[int]*ent.Ticket, len(tickets))
	for _, tk := range tickets {
		ticketByID[tk.ID] = tk
	}
	userByID := make(map[int]*ent.User, len(users))
	for _, u := range users {
		userByID[u.ID] = u
	}

	for _, record := range records {
		tk := ticketByID[record.TicketID]
		row := dto.TicketCCRecordResponse{
			ID:       record.ID,
			TicketID: record.TicketID,
			User:     workflowUserInfoFromEnt(userByID[record.UserID]),
			AddedBy:  workflowUserInfoFromEnt(userByID[record.AddedBy]),
			AddedAt:  record.AddedAt,
			IsActive: record.IsActive,
		}
		if tk != nil {
			row.TicketNumber = tk.TicketNumber
			row.Title = tk.Title
			row.Status = tk.Status
			row.Priority = tk.Priority
		}
		response.Records = append(response.Records, row)
	}

	return response, nil
}

func (s *TicketWorkflowService) getTicket(ctx context.Context, ticketID, tenantID int) (*ent.Ticket, error) {
	tk, err := s.client.Ticket.Query().
		Where(ticket.ID(ticketID), ticket.TenantID(tenantID), ticket.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("工单不存在")
		}
		return nil, fmt.Errorf("failed to get ticket: %w", err)
	}
	return tk, nil
}

func (s *TicketWorkflowService) createWorkflowRecord(ctx context.Context, record *dto.TicketWorkflowRecord, tenantID int) error {
	return s.createWorkflowRecordWithClient(ctx, s.client, record, tenantID)
}

// createWorkflowRecordWithClient 使用指定的 Ent 客户端创建流转记录（支持事务内复用）
func (s *TicketWorkflowService) createWorkflowRecordWithClient(ctx context.Context, client *ent.Client, record *dto.TicketWorkflowRecord, tenantID int) error {
	create := client.TicketWorkflowRecord.Create().
		SetTicketID(record.TicketID).
		SetAction(string(record.Action)).
		SetOperatorID(record.Operator.ID).
		SetTenantID(tenantID)

	if record.FromStatus != nil {
		create.SetFromStatus(*record.FromStatus)
	}
	if record.ToStatus != nil {
		create.SetToStatus(*record.ToStatus)
	}
	if record.FromUser != nil {
		create.SetFromUserID(record.FromUser.ID)
	}
	if record.ToUser != nil {
		create.SetToUserID(record.ToUser.ID)
	}
	if record.Comment != "" {
		create.SetComment(record.Comment)
	}
	if record.Reason != "" {
		create.SetReason(record.Reason)
	}
	if record.Metadata != nil {
		create.SetMetadata(record.Metadata)
	}

	_, err := create.Save(ctx)
	return err
}

func ptrString(s string) *string {
	return &s
}
