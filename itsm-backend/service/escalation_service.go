package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/slaalerthistory"
	"itsm-backend/ent/slaalertrule"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/handlers/shared/workitemmutation"

	"go.uber.org/zap"
)

type EscalationService struct {
	client          *ent.Client
	execution       *database.ExecutionPolicy
	logger          *zap.SugaredLogger
	notificationSvc *TicketNotificationService
}

func NewEscalationService(client *ent.Client, logger *zap.SugaredLogger, execution *database.ExecutionPolicy) *EscalationService {
	return &EscalationService{
		client:    client,
		execution: execution,
		logger:    logger,
	}
}

// SetNotificationService 设置通知服务
func (e *EscalationService) SetNotificationService(notificationSvc *TicketNotificationService) {
	e.notificationSvc = notificationSvc
}

// ProcessEscalations 处理所有升级任务
func (e *EscalationService) ProcessEscalations(ctx context.Context, tenantID int) error {
	var failures []error
	e.logger.Infow("Processing escalations", "tenant_id", tenantID)

	// 1. 检查SLA预警规则中的升级配置
	if err := e.processSLAEscalations(ctx, tenantID); err != nil {
		failures = append(failures, err)
		e.logger.Errorw("Failed to process SLA escalations", "error", err)
	}

	// 2. 检查长时间未解决的工单
	if err := e.processLongPendingTickets(ctx, tenantID); err != nil {
		failures = append(failures, err)
		e.logger.Errorw("Failed to process long pending tickets", "error", err)
	}

	// 3. 检查未分配工单
	if err := e.processUnassignedTickets(ctx, tenantID); err != nil {
		failures = append(failures, err)
		e.logger.Errorw("Failed to process unassigned tickets", "error", err)
	}

	e.logger.Infow("Escalation processing completed", "tenant_id", tenantID)
	return errors.Join(failures...)
}

// processSLAEscalations discovers only admitted WorkItems; each alert then owns
// its write transaction, including all levels currently due for that alert.
func (e *EscalationService) processSLAEscalations(ctx context.Context, tenantID int) error {
	lastID := 0
	for {
		tx, err := e.client.Tx(ctx)
		if err != nil {
			return err
		}
		member, err := e.execution.TenantPredicate(ctx, tx, tenantID, slaalerthistory.FieldTenantID, slaalerthistory.FieldTicketID)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		ids, err := tx.SLAAlertHistory.Query().Where(slaalerthistory.TenantIDEQ(tenantID), slaalerthistory.IDGT(lastID), slaalerthistory.ResolvedAtIsNil(), member, slaalerthistory.HasAlertRuleWith(slaalertrule.TenantIDEQ(tenantID), slaalertrule.IsActiveEQ(true), slaalertrule.EscalationEnabledEQ(true))).Order(ent.Asc(slaalerthistory.FieldID)).Limit(100).IDs(ctx)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			lastID = id
			if err := e.escalateAlert(ctx, id, tenantID); err != nil {
				return err
			}
		}
	}
}

func (e *EscalationService) escalateAlert(ctx context.Context, alertID, tenantID int) error {
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = e.execution.BindEnt(ctx, tx, tenantID); err != nil {
		return err
	}
	alert, err := tx.SLAAlertHistory.Query().Where(slaalerthistory.IDEQ(alertID), slaalerthistory.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if err = e.execution.RequireEntMembers(ctx, tx, tenantID, alert.TicketID); err != nil {
		return err
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(alert.TicketID), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return err
	}
	if !alert.ResolvedAt.IsZero() || item.ClosedAt != nil || !item.ResolvedAt.IsZero() || alert.CreatedAt.Before(slaCycleStart(item)) {
		return tx.Commit()
	}
	rule, err := tx.SLAAlertRule.Query().Where(slaalertrule.IDEQ(alert.AlertRuleID), slaalertrule.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if !rule.IsActive || !rule.EscalationEnabled {
		return tx.Commit()
	}
	if rule.SLADefinitionID != item.SLADefinitionID {
		return fmt.Errorf("SLA escalation rule no longer matches WorkItem")
	}
	matrix, err := loadSLAEscalationMatrix(ctx, tx.Client(), tenantID, item.SLADefinitionID)
	if err != nil {
		return err
	}
	elapsed := int(time.Since(alert.CreatedAt).Minutes())
	current := alert.EscalationLevel
	fenced := false
	for {
		next, err := nextEscalationLevel(matrix, string(item.Priority), elapsed, current)
		if err != nil {
			return err
		}
		if next == nil {
			break
		}
		recipients, err := e.resolveNotifyUsersTx(ctx, tx, next, tenantID)
		if err != nil {
			return err
		}
		if len(recipients) > 0 && e.notificationSvc == nil {
			return fmt.Errorf("SLA escalation notification service is required")
		}
		if !fenced {
			if err = e.fenceWorkItem(ctx, tx, item); err != nil {
				return err
			}
			fenced = true
		}
		member, err := e.execution.TenantPredicate(ctx, tx, tenantID, slaalerthistory.FieldTenantID, slaalerthistory.FieldTicketID)
		if err != nil {
			return err
		}
		count, err := tx.SLAAlertHistory.Update().Where(slaalerthistory.IDEQ(alert.ID), slaalerthistory.TenantIDEQ(tenantID), slaalerthistory.EscalationLevelEQ(current), slaalerthistory.ResolvedAtIsNil(), member).SetEscalationLevel(next.Level).Save(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("SLA escalation level changed concurrently")
		}
		operation := fmt.Sprintf("escalation:matrix:%d:level:%d", alert.ID, next.Level)
		if len(recipients) > 0 {
			request := &dto.SendTicketNotificationRequest{UserIDs: recipients, EventType: "sla_violated", DeliveryKey: operation, Content: fmt.Sprintf("【SLA矩阵升级】工单 #%s (%s) [优先级 %s] 已升级至 L%d：%s", item.TicketNumber, item.Title, item.Priority, next.Level, next.Description)}
			if alert.NotificationTrackingVersion != nil && *alert.NotificationTrackingVersion == 1 {
				request.SLAAlertHistoryID = &alert.ID
			}
			if err = e.notificationSvc.EnqueueNotificationTx(ctx, tx, item.ID, tenantID, request); err != nil {
				return err
			}
		}
		digest, err := workitemmutation.Digest(struct{ AlertID, Level int }{alert.ID, next.Level})
		if err != nil {
			return err
		}
		meta := workitemmutation.Meta{TenantID: tenantID, ActorID: 0, Source: "scheduler", OperationID: operation, CorrelationID: operation}
		if err = workitemmutation.RecordTx(ctx, tx, meta, workitemmutation.Result{WorkItemID: item.ID, Version: item.Version + 1, Status: item.Status}, "work_item.escalation.matrix", digest, map[string]interface{}{"alertId": alert.ID, "previousLevel": current, "level": next.Level, "recipientIds": recipients}); err != nil {
			return err
		}
		current = next.Level
	}
	return tx.Commit()
}

func (e *EscalationService) fenceWorkItem(ctx context.Context, tx *ent.Tx, item *ent.Ticket) error {
	member, err := e.execution.TenantPredicate(ctx, tx, item.TenantID, ticket.FieldTenantID, ticket.FieldID)
	if err != nil {
		return err
	}
	n, err := tx.Ticket.Update().Where(ticket.IDEQ(item.ID), ticket.TenantIDEQ(item.TenantID), ticket.VersionEQ(item.Version), ticket.DeletedAtIsNil(), member).AddVersion(1).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("escalation WorkItem changed concurrently")
	}
	return nil
}

func (e *EscalationService) resolveNotifyUsersTx(ctx context.Context, tx *ent.Tx, level *EscalationLevel, tenantID int) ([]int, error) {
	seen := map[int]bool{}
	var ids []int
	for _, id := range level.NotifyUserIDs {
		if _, err := tx.User.Query().Where(user.IDEQ(id), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).Only(ctx); err != nil {
			return nil, fmt.Errorf("escalation recipient: %w", err)
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	for _, role := range level.NotifyRoles {
		matches, err := tx.User.Query().Where(user.TenantIDEQ(tenantID), user.ActiveEQ(true), user.RoleEQ(role)).Order(ent.Asc(user.FieldID)).IDs(ctx)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("escalation role has no active recipient: %s", role)
		}
		for _, id := range matches {
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}
	return ids, nil
}

func (e *EscalationService) processLongPendingTickets(ctx context.Context, tenantID int) error {
	return e.processReminders(ctx, tenantID, "long_pending", 24*time.Hour)
}
func (e *EscalationService) processUnassignedTickets(ctx context.Context, tenantID int) error {
	return e.processReminders(ctx, tenantID, "unassigned", 2*time.Hour)
}

// A reminder is WorkItem activity, not an SLA rule result. Its existing operation
// receipt records one occurrence per WorkItem SLA cycle, including notification intent.
func (e *EscalationService) processReminders(ctx context.Context, tenantID int, kind string, age time.Duration) error {
	if kind != "long_pending" && kind != "unassigned" {
		return fmt.Errorf("unsupported escalation reminder kind")
	}
	lastID := 0
	for {
		tx, err := e.client.Tx(ctx)
		if err != nil {
			return err
		}
		member, err := e.execution.TenantPredicate(ctx, tx, tenantID, ticket.FieldTenantID, ticket.FieldID)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		query := tx.Ticket.Query().Where(ticket.TenantIDEQ(tenantID), ticket.IDGT(lastID), ticket.DeletedAtIsNil(), ticket.ClosedAtIsNil(), ticket.ResolvedAtIsNil(), ticket.CreatedAtLTE(time.Now().Add(-age)), member)
		if kind == "unassigned" {
			query.Where(ticket.AssigneeIDIsNil())
		}
		ids, err := query.Order(ent.Asc(ticket.FieldID)).Limit(100).IDs(ctx)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			lastID = id
			if err := e.recordReminder(ctx, id, tenantID, kind, age); err != nil {
				return err
			}
		}
	}
}

func (e *EscalationService) recordReminder(ctx context.Context, id, tenantID int, kind string, age time.Duration) error {
	tx, err := e.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = e.execution.BindEnt(ctx, tx, tenantID); err != nil {
		return err
	}
	if err = e.execution.RequireEntMembers(ctx, tx, tenantID, id); err != nil {
		return err
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(id), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return err
	}
	if item.ClosedAt != nil || !item.ResolvedAt.IsZero() || time.Since(slaCycleStart(item)) < age || kind == "unassigned" && item.AssigneeID > 0 {
		return tx.Commit()
	}
	operation := fmt.Sprintf("escalation:%s:%d:cycle:%d", kind, id, item.SLACycleNumber)
	meta := workitemmutation.Meta{TenantID: tenantID, ActorID: 0, Source: "scheduler", OperationID: operation, CorrelationID: operation}
	digest, err := workitemmutation.Digest(struct {
		Kind              string
		WorkItemID, Cycle int
	}{kind, id, item.SLACycleNumber})
	if err != nil {
		return err
	}
	if _, replayed, err := workitemmutation.Replay(ctx, tx.Client(), meta, id, digest); err != nil {
		return err
	} else if replayed {
		return tx.Commit()
	}
	if e.notificationSvc == nil {
		return fmt.Errorf("escalation reminder notification service is required")
	}
	recipients, err := tx.User.Query().Where(user.TenantIDEQ(tenantID), user.ActiveEQ(true), user.RoleNEQ("end_user"), user.RoleNEQ("guest")).Order(ent.Asc(user.FieldID)).IDs(ctx)
	if err != nil {
		return err
	}
	if kind == "long_pending" {
		recipients = append(recipients, item.RequesterID)
		if item.AssigneeID > 0 {
			recipients = append(recipients, item.AssigneeID)
		}
	}
	if len(recipients) == 0 {
		return fmt.Errorf("escalation reminder has no active recipients")
	}
	if err = e.fenceWorkItem(ctx, tx, item); err != nil {
		return err
	}
	message := fmt.Sprintf("【超时提醒】工单 #%s (%s) 已超过24小时未解决，请及时处理！", item.TicketNumber, item.Title)
	if kind == "unassigned" {
		message = fmt.Sprintf("【未分配提醒】工单 #%s (%s) 已超过2小时未分配，请及时处理！", item.TicketNumber, item.Title)
	}
	if err = e.notificationSvc.EnqueueNotificationTx(ctx, tx, id, tenantID, &dto.SendTicketNotificationRequest{UserIDs: recipients, EventType: "ticket_updated", Content: message, DeliveryKey: operation}); err != nil {
		return err
	}
	if err = workitemmutation.RecordTx(ctx, tx, meta, workitemmutation.Result{WorkItemID: id, Version: item.Version + 1, Status: item.Status}, "work_item.escalation."+kind, digest, map[string]interface{}{"kind": kind, "cycle": item.SLACycleNumber, "recipientIds": recipients}); err != nil {
		return err
	}
	return tx.Commit()
}
