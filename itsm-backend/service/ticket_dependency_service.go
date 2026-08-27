package service

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

type TicketDependencyService struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

func NewTicketDependencyService(client *ent.Client, logger *zap.SugaredLogger) *TicketDependencyService {
	return &TicketDependencyService{
		client: client,
		logger: logger,
	}
}

// AnalyzeDependencyImpact 分析依赖关系影响
func (s *TicketDependencyService) AnalyzeDependencyImpact(ctx context.Context, ticketID int, action string, newStatus *string, tenantID int) (*dto.RelationImpactAnalysis, error) {
	s.logger.Infow("Analyzing dependency impact", "ticket_id", ticketID, "action", action, "tenant_id", tenantID)

	// 获取工单信息
	ticketEntity, err := s.client.Ticket.Query().
		Where(
			ticket.IDEQ(ticketID),
			ticket.TenantIDEQ(tenantID),
		).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("ticket not found: %w", err)
	}

	// 获取所有相关工单（通过parent_ticket_id）
	relatedTickets, err := s.client.Ticket.Query().
		Where(
			ticket.ParentTicketIDEQ(ticketID),
			ticket.TenantIDEQ(tenantID),
		).
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get related tickets", "error", err)
		return nil, fmt.Errorf("failed to get related tickets: %w", err)
	}

	// 分析影响
	impact := &dto.RelationImpactAnalysis{
		TicketID:        ticketID,
		TicketNumber:    ticketEntity.TicketNumber,
		TicketTitle:     ticketEntity.Title,
		Action:          action,
		AffectedCount:   len(relatedTickets),
		Warnings:        []string{},
		Recommendations: []string{},
	}

	// 根据action分析影响
	switch action {
	case "close":
		// 检查是否有未完成的子工单
		for _, related := range relatedTickets {
			if related.Status != "closed" && related.Status != "resolved" {
				impact.Warnings = append(impact.Warnings,
					fmt.Sprintf("子工单 %s (%s) 尚未完成", related.TicketNumber, related.Title))
				impact.AffectedTickets = append(impact.AffectedTickets, dto.AffectedTicketInfo{
					ID:          related.ID,
					Number:      related.TicketNumber,
					Title:       related.Title,
					Status:      related.Status,
					ImpactType:  "blocked",
					Description: "父工单关闭可能导致此工单无法继续",
				})
			}
		}
		if len(impact.Warnings) > 0 {
			impact.Recommendations = append(impact.Recommendations,
				"建议先完成或取消所有子工单后再关闭父工单")
		}

	case "delete":
		// 删除操作影响更大
		for _, related := range relatedTickets {
			impact.Warnings = append(impact.Warnings,
				fmt.Sprintf("子工单 %s (%s) 将失去父工单关联", related.TicketNumber, related.Title))
			impact.AffectedTickets = append(impact.AffectedTickets, dto.AffectedTicketInfo{
				ID:          related.ID,
				Number:      related.TicketNumber,
				Title:       related.Title,
				Status:      related.Status,
				ImpactType:  "orphaned",
				Description: "父工单删除后此工单将变为孤立工单",
			})
		}
		impact.Recommendations = append(impact.Recommendations,
			"删除操作不可逆，请谨慎操作")
		impact.Recommendations = append(impact.Recommendations,
			"建议先处理所有相关工单后再删除")

	case "change_status":
		if newStatus != nil {
			// 检查状态变更对依赖工单的影响
			if *newStatus == "closed" || *newStatus == "resolved" {
				for _, related := range relatedTickets {
					if related.Status == "open" || related.Status == "in_progress" {
						impact.Warnings = append(impact.Warnings,
							fmt.Sprintf("子工单 %s (%s) 可能受到影响", related.TicketNumber, related.Title))
						impact.AffectedTickets = append(impact.AffectedTickets, dto.AffectedTicketInfo{
							ID:          related.ID,
							Number:      related.TicketNumber,
							Title:       related.Title,
							Status:      related.Status,
							ImpactType:  "status_change",
							Description: fmt.Sprintf("父工单状态变更为 %s 可能影响此工单", *newStatus),
						})
					}
				}
			}
		}
	}

	// 计算风险等级
	if len(impact.Warnings) == 0 {
		impact.RiskLevel = "low"
	} else if len(impact.Warnings) <= 2 {
		impact.RiskLevel = "medium"
	} else {
		impact.RiskLevel = "high"
	}

	return impact, nil
}

// GetRelationStats 获取工单关联统计
func (s *TicketDependencyService) GetRelationStats(ctx context.Context, ticketID int, tenantID int) (*dto.TicketRelationStats, error) {
	// 先确认工单存在且属于当前租户——不存在或跨租户都必须 fail closed 返回错误，
	// 不能静默当成"没有关联"返回一份全零的成功统计，那样跟"自己的空工单"无法区分。
	currentTicket, err := s.client.Ticket.Query().
		Where(
			ticket.IDEQ(ticketID),
			ticket.TenantIDEQ(tenantID),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("ticket not found: %d", ticketID)
		}
		s.logger.Errorw("Failed to load ticket for relation stats", "error", err, "ticket_id", ticketID)
		return nil, fmt.Errorf("failed to load ticket: %w", err)
	}

	// 查询作为父工单拥有的子工单
	childrenCount, err := s.client.Ticket.Query().
		Where(
			ticket.ParentTicketIDEQ(ticketID),
			ticket.TenantIDEQ(tenantID),
		).
		Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count children tickets", "error", err, "ticket_id", ticketID)
		return nil, fmt.Errorf("failed to count children tickets: %w", err)
	}

	parentCount := 0
	if currentTicket.ParentTicketID > 0 {
		parentCount = 1
	}

	total := childrenCount + parentCount
	stats := &dto.TicketRelationStats{
		TotalRelations: total,
		RelationsByType: map[string]int{
			"parent_child": total,
		},
		InboundCount:   parentCount,
		OutboundCount:  childrenCount,
		ParentCount:    parentCount,
		ChildrenCount:  childrenCount,
		BlockedByCount: 0,
		BlockingCount:  0,
		RelatedCount:   0,
		DuplicateCount: 0,
	}

	return stats, nil
}

// GetTicketRelations 获取工单的关联列表。
//
// 当前数据模型里唯一真实存在的关联来源是 tickets.parent_ticket_id（父子关系），
// 没有独立的多类型关联表（阻塞/依赖/重复等——见 dto.TicketRelation 上的注释），
// 所以这里只返回父子关系，relationType 固定为 "parent_child"。语义：
//   - 如果本工单有父工单：一条记录，source=父工单，target=本工单
//     （父工单"指向"本工单，对调用方而言是"对方指向本单"）。
//   - 本工单的每个子工单各一条记录，source=本工单，target=子工单
//     （本工单"指向"每个子工单，对调用方而言是"本单指向对方"）。
func (s *TicketDependencyService) GetTicketRelations(ctx context.Context, ticketID int, tenantID int) ([]*dto.TicketRelation, error) {
	currentTicket, err := s.client.Ticket.Query().
		Where(
			ticket.IDEQ(ticketID),
			ticket.TenantIDEQ(tenantID),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("ticket not found: %d", ticketID)
		}
		s.logger.Errorw("Failed to load ticket for relations list", "error", err, "ticket_id", ticketID)
		return nil, fmt.Errorf("failed to load ticket: %w", err)
	}

	relations := make([]*dto.TicketRelation, 0)

	if currentTicket.ParentTicketID > 0 {
		parent, err := s.client.Ticket.Query().
			Where(
				ticket.IDEQ(currentTicket.ParentTicketID),
				ticket.TenantIDEQ(tenantID),
			).
			Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			s.logger.Errorw("Failed to load parent ticket", "error", err, "ticket_id", ticketID, "parent_id", currentTicket.ParentTicketID)
			return nil, fmt.Errorf("failed to load parent ticket: %w", err)
		}
		if err == nil {
			relations = append(relations, toParentChildRelation(parent, currentTicket))
		}
		// parent_ticket_id 指向的行不存在（数据被并发删除等）时静默跳过这一条关联，
		// 不影响其它关联的返回——已确认本工单自身存在且属于当前租户，这里不是
		// fail-closed 场景。
	}

	children, err := s.client.Ticket.Query().
		Where(
			ticket.ParentTicketIDEQ(ticketID),
			ticket.TenantIDEQ(tenantID),
		).
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to load children tickets", "error", err, "ticket_id", ticketID)
		return nil, fmt.Errorf("failed to load children tickets: %w", err)
	}
	for _, child := range children {
		relations = append(relations, toParentChildRelation(currentTicket, child))
	}

	return relations, nil
}

// toParentChildRelation 把一条父子工单对组装成 dto.TicketRelation，source=parent。
func toParentChildRelation(parent, child *ent.Ticket) *dto.TicketRelation {
	return &dto.TicketRelation{
		ID:                 fmt.Sprintf("parent_child_%d_%d", parent.ID, child.ID),
		SourceTicketID:     parent.ID,
		SourceTicketNumber: parent.TicketNumber,
		TargetTicketID:     child.ID,
		TargetTicketNumber: child.TicketNumber,
		RelationType:       "parent_child",
		Direction:          "bidirectional",
		CreatedAt:          child.CreatedAt.Format(time.RFC3339),
		SourceTicket: &dto.TicketRelationTicketRef{
			ID:           parent.ID,
			TicketNumber: parent.TicketNumber,
			Title:        parent.Title,
			Status:       parent.Status,
		},
		TargetTicket: &dto.TicketRelationTicketRef{
			ID:           child.ID,
			TicketNumber: child.TicketNumber,
			Title:        child.Title,
			Status:       child.Status,
		},
	}
}
