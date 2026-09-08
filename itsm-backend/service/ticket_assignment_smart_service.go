package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

type TicketAssignmentSmartService struct {
	client            *ent.Client
	logger            *zap.SugaredLogger
	assignmentService *TicketAssignmentService
	ruleService       *TicketAssignmentRuleService
}

func NewTicketAssignmentSmartService(
	client *ent.Client,
	logger *zap.SugaredLogger,
	assignmentService *TicketAssignmentService,
	ruleService *TicketAssignmentRuleService,
) *TicketAssignmentSmartService {
	return &TicketAssignmentSmartService{
		client:            client,
		logger:            logger,
		assignmentService: assignmentService,
		ruleService:       ruleService,
	}
}

// AutoAssign 自动分配工单
func (s *TicketAssignmentSmartService) AutoAssign(
	ctx context.Context,
	ticketID, tenantID int,
) (*dto.AutoAssignResponse, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(ticketID), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.prepareCreation(ctx, tx, item)
	if err != nil {
		return nil, err
	}
	if target != nil {
		if err := tx.Ticket.UpdateOneID(item.ID).SetAssigneeID(*target).Exec(ctx); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &dto.AutoAssignResponse{TicketID: ticketID, AssignedTo: target, AssignmentType: "auto", Reason: "configured assignment policy"}, nil
}

// GetAssignRecommendations 获取分配推荐
func (s *TicketAssignmentSmartService) GetAssignRecommendations(
	ctx context.Context,
	ticketID, tenantID int,
) ([]*dto.AssignmentRecommendation, error) {
	s.logger.Infow("Getting assignment recommendations", "ticket_id", ticketID, "tenant_id", tenantID)

	// 获取工单信息
	ticketEntity, err := s.client.Ticket.Get(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	if ticketEntity.TenantID != tenantID {
		return nil, fmt.Errorf("ticket not found")
	}

	// 构建分配请求
	var categoryID *int
	if ticketEntity.CategoryID > 0 {
		categoryID = &ticketEntity.CategoryID
	}
	req := &AssignmentRequest{
		TicketID:   ticketID,
		CategoryID: categoryID,
		Priority:   ticketEntity.Priority,
		TenantID:   tenantID,
		AutoAssign: false, // 不实际分配，只获取推荐
	}

	// 获取可用用户
	availableUsers, err := s.assignmentService.getAvailableUsers(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get available users: %w", err)
	}

	// 计算每个用户的评分
	for i := range availableUsers {
		availableUsers[i].Score = s.assignmentService.calculateUserScore(ctx, &availableUsers[i], req)
	}

	// 按评分排序
	sort.Slice(availableUsers, func(i, j int) bool {
		return availableUsers[i].Score > availableUsers[j].Score
	})

	// 转换为推荐响应
	recommendations := make([]*dto.AssignmentRecommendation, 0, len(availableUsers))
	for _, user := range availableUsers {
		// 获取用户详细信息
		userEntity, err := s.client.User.Get(ctx, user.UserID)
		if err != nil {
			continue
		}

		// 生成推荐理由
		reason := s.generateRecommendationReason(&user, req)

		recommendations = append(recommendations, &dto.AssignmentRecommendation{
			UserID:     user.UserID,
			Username:   userEntity.Username,
			Name:       userEntity.Name,
			Email:      userEntity.Email,
			Score:      user.Score,
			Reason:     reason,
			Workload:   user.ActiveTickets,
			Skills:     user.Skills,
			Categories: user.Categories,
		})
	}

	return recommendations, nil
}

// generateRecommendationReason 生成推荐理由
func (s *TicketAssignmentSmartService) generateRecommendationReason(
	user *UserWorkload,
	req *AssignmentRequest,
) string {
	reasons := []string{}

	if len(user.Skills) > 0 {
		reasons = append(reasons, fmt.Sprintf("具备相关技能（%d项）", len(user.Skills)))
	}

	if user.ActiveTickets == 0 {
		reasons = append(reasons, "当前无活跃工单")
	} else if user.ActiveTickets < 3 {
		reasons = append(reasons, fmt.Sprintf("工作负载较轻（%d个活跃工单）", user.ActiveTickets))
	}

	if len(user.Categories) > 0 && req.CategoryID != nil {
		for _, catID := range user.Categories {
			if catID == *req.CategoryID {
				reasons = append(reasons, "有该分类的处理经验")
				break
			}
		}
	}

	if user.AvgResolution > 0 && user.AvgResolution < 24*time.Hour {
		reasons = append(reasons, "平均解决时间较短")
	}

	if len(reasons) == 0 {
		return "符合基本分配条件"
	}

	return fmt.Sprintf("推荐理由: %s", joinStrings(reasons, "，"))
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
