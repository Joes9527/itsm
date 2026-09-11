package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/handlers/shared/workitemmutation"
	"strings"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/slaviolation"
	"itsm-backend/ent/ticket"

	"go.uber.org/zap"
)

// IncidentEscalationService 事件升级服务
type IncidentEscalationService struct {
	directory    database.DirectorySnapshot
	client       *ent.Client
	logger       *zap.SugaredLogger
	alertCreator IncidentAlertCreator
}

func (s *IncidentEscalationService) SetDirectorySnapshot(directory database.DirectorySnapshot) {
	s.directory = directory
}

func (s *IncidentEscalationService) SetAlertCreator(creator IncidentAlertCreator) {
	s.alertCreator = creator
}

// NewIncidentEscalationService 创建事件升级服务
func NewIncidentEscalationService(client *ent.Client) *IncidentEscalationService {
	return &IncidentEscalationService{
		client: client,
		logger: zap.L().Sugar(),
	}
}

// CreateEscalationRule 创建升级规则
func (s *IncidentEscalationService) CreateEscalationRule(ctx context.Context, input dto.CreateIncidentEscalationRuleRequest) (*ent.IncidentEscalationRule, error) {
	if err := validateIncidentNotificationConfig(input.NotificationConfig); err != nil {
		return nil, err
	}
	build := s.client.IncidentEscalationRule.Create().
		SetName(input.Name).
		SetDescription(input.Description).
		SetTriggerType(input.TriggerType).
		SetEscalationLevel(input.EscalationLevel).
		SetTriggerMinutes(input.TriggerMinutes).
		SetTargetAssigneeType(input.TargetAssigneeType).
		SetAutoEscalate(input.AutoEscalate).
		SetNotificationConfig(input.NotificationConfig).
		SetIsActive(input.IsActive).
		SetTenantID(input.TenantID)

	if input.FromStatus != nil && *input.FromStatus != "" {
		build.SetFromStatus(*input.FromStatus)
	}
	if input.ToStatus != nil && *input.ToStatus != "" {
		build.SetToStatus(*input.ToStatus)
	}
	if input.TargetAssigneeID != nil && *input.TargetAssigneeID > 0 {
		build.SetTargetAssigneeID(*input.TargetAssigneeID)
	}
	if input.TargetGroup != nil && *input.TargetGroup != "" {
		build.SetTargetGroup(*input.TargetGroup)
	}
	if input.PriorityMatch != nil && *input.PriorityMatch != "" {
		build.SetPriorityMatch(*input.PriorityMatch)
	}
	if input.CategoryMatch != nil && *input.CategoryMatch != "" {
		build.SetCategoryMatch(*input.CategoryMatch)
	}

	return build.Save(ctx)
}

// GetEscalationRuleByID 获取升级规则
func (s *IncidentEscalationService) GetEscalationRuleByID(ctx context.Context, id int) (*ent.IncidentEscalationRule, error) {
	return s.client.IncidentEscalationRule.Get(ctx, id)
}

// QueryEscalationRules 查询升级规则列表
func (s *IncidentEscalationService) QueryEscalationRules(ctx context.Context, tenantID int) ([]*ent.IncidentEscalationRule, error) {
	// Simple query - get all and filter
	all, err := s.client.IncidentEscalationRule.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	var result []*ent.IncidentEscalationRule
	for _, rule := range all {
		if rule.TenantID == tenantID {
			result = append(result, rule)
		}
	}
	return result, nil
}

// UpdateEscalationRule 更新升级规则
func (s *IncidentEscalationService) UpdateEscalationRule(ctx context.Context, id int, input dto.UpdateIncidentEscalationRuleRequest) (*ent.IncidentEscalationRule, error) {
	update := s.client.IncidentEscalationRule.UpdateOneID(id)
	if input.Name != nil {
		update.SetName(*input.Name)
	}
	if input.Description != nil {
		update.SetDescription(*input.Description)
	}
	if input.TriggerType != nil {
		update.SetTriggerType(*input.TriggerType)
	}
	if input.EscalationLevel != nil {
		update.SetEscalationLevel(*input.EscalationLevel)
	}
	if input.TriggerMinutes != nil {
		update.SetTriggerMinutes(*input.TriggerMinutes)
	}
	if input.FromStatus != nil {
		update.SetFromStatus(*input.FromStatus)
	}
	if input.ToStatus != nil {
		update.SetToStatus(*input.ToStatus)
	}
	if input.TargetAssigneeType != nil {
		update.SetTargetAssigneeType(*input.TargetAssigneeType)
	}
	if input.TargetAssigneeID != nil {
		update.SetTargetAssigneeID(*input.TargetAssigneeID)
	}
	if input.TargetGroup != nil {
		update.SetTargetGroup(*input.TargetGroup)
	}
	if input.AutoEscalate != nil {
		update.SetAutoEscalate(*input.AutoEscalate)
	}
	if input.NotificationConfig != nil {
		if err := validateIncidentNotificationConfig(input.NotificationConfig); err != nil {
			return nil, err
		}
		update.SetNotificationConfig(input.NotificationConfig)
	}
	if input.IsActive != nil {
		update.SetIsActive(*input.IsActive)
	}
	if input.PriorityMatch != nil {
		update.SetPriorityMatch(*input.PriorityMatch)
	}
	if input.CategoryMatch != nil {
		update.SetCategoryMatch(*input.CategoryMatch)
	}
	return update.Save(ctx)
}

// DeleteEscalationRule 删除升级规则
func (s *IncidentEscalationService) DeleteEscalationRule(ctx context.Context, id int) error {
	return s.client.IncidentEscalationRule.DeleteOneID(id).Exec(ctx)
}

// CheckAndEscalate 检查事件是否需要升级
func (s *IncidentEscalationService) CheckAndEscalate(ctx context.Context, incidentID int, meta workitemmutation.Meta) (*ent.Incident, error) {
	if meta.ActorID <= 0 || meta.TenantID <= 0 || meta.ExpectedVersion <= 0 || strings.TrimSpace(meta.Source) == "" || strings.TrimSpace(meta.OperationID) == "" {
		return nil, fmt.Errorf("trusted escalation actor, tenant, version, source and operationId required")
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != meta.TenantID {
		return nil, fmt.Errorf("tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, meta.TenantID)
	ctx = WithIncidentAlertActor(ctx, meta.ActorID, meta.Source, meta.OperationID)
	incidentEnt, err := s.client.Incident.Query().Where(incident.IDEQ(incidentID), incidentTenantScope(meta.TenantID)).WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return nil, err
	}

	incidentEnt.Edges.WorkItem.Version = meta.ExpectedVersion

	// 获取匹配的升级规则
	rules, err := s.getMatchingRules(ctx, incidentEnt)
	if err != nil {
		return nil, err
	}

	// 检查是否需要升级
	for _, rule := range rules {
		shouldEscalate, err := s.shouldEscalate(ctx, incidentEnt, rule)
		if err != nil {
			return nil, err
		}

		if shouldEscalate {
			return s.escalateIncident(ctx, incidentEnt, rule)
		}
	}

	return incidentEnt, nil
}

// getMatchingRules 获取匹配的升级规则
func (s *IncidentEscalationService) getMatchingRules(ctx context.Context, incidentEnt *ent.Incident) ([]*ent.IncidentEscalationRule, error) {
	all, err := s.client.IncidentEscalationRule.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	var matchedRules []*ent.IncidentEscalationRule
	for _, rule := range all {
		if incidentEnt.Edges.WorkItem == nil || rule.TenantID != incidentEnt.Edges.WorkItem.TenantID || !rule.IsActive {
			continue
		}

		// 匹配优先级
		if rule.PriorityMatch != "" && (incidentEnt.Edges.WorkItem == nil || rule.PriorityMatch != incidentEnt.Edges.WorkItem.Priority) {
			continue
		}
		// 匹配分类
		categoryName := ""
		if category := incidentEnt.Edges.WorkItem.Edges.Category; category != nil {
			categoryName = category.Name
			if parent := category.Edges.Parent; parent != nil {
				categoryName = parent.Name
			}
		}
		if rule.CategoryMatch != "" && rule.CategoryMatch != categoryName {
			continue
		}
		matchedRules = append(matchedRules, rule)
	}

	return matchedRules, nil
}

// shouldEscalate 检查是否应该升级
func (s *IncidentEscalationService) shouldEscalate(ctx context.Context, incidentEnt *ent.Incident, rule *ent.IncidentEscalationRule) (bool, error) {
	switch rule.TriggerType {
	case "time_based":
		// 基于时间升级
		elapsedMinutes := time.Since(incidentEnt.DetectedAt).Minutes()
		return elapsedMinutes >= float64(rule.TriggerMinutes), nil
	case "sla_breach":
		// SLAViolation.ticket_id references the WorkItem, not the professional Incident ID.
		// An unresolved violation on another item must never trigger this Incident.
		if incidentEnt.Edges.WorkItem == nil || incidentEnt.WorkItemID <= 0 {
			return false, fmt.Errorf("SLA escalation requires an Incident WorkItem")
		}
		item := incidentEnt.Edges.WorkItem
		current := projectSLACycle(item, time.Now())
		// A delayed monitor snapshot can be written after reopen. Require the
		// corresponding current clock to be breached, not just a recent insert.
		var breaches []predicate.SLAViolation
		for _, clock := range []struct {
			kind     string
			breached bool
			deadline time.Time
		}{
			{"response_time", current.ResponseBreached, item.SLAResponseDeadline},
			{"resolution_time", current.ResolutionBreached, item.SLAResolutionDeadline},
		} {
			if !clock.breached {
				continue
			}
			start := slaCycleStart(item)
			if clock.deadline.After(start) {
				start = clock.deadline
			}
			breaches = append(breaches, slaviolation.And(
				slaviolation.ViolationTypeEQ(clock.kind),
				slaviolation.ViolationTimeGTE(start),
				slaviolation.ViolationOccurredAtGTE(start),
			))
		}
		if len(breaches) == 0 {
			return false, nil
		}
		exists, err := s.client.SLAViolation.Query().Where(
			slaviolation.TenantIDEQ(item.TenantID),
			slaviolation.TicketIDEQ(incidentEnt.WorkItemID),
			slaviolation.IsResolved(false),
			slaviolation.Or(breaches...),
		).Exist(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to query SLA violations: %w", err)
		}
		return exists, nil
	case "manual":
		// 手动升级
		return false, nil
	}
	return false, nil
}

// escalateIncident 执行事件升级
func (s *IncidentEscalationService) escalateIncident(ctx context.Context, incidentEnt *ent.Incident, rule *ent.IncidentEscalationRule) (*ent.Incident, error) {
	actor, ok := ctx.Value(incidentAlertActorContextKey{}).(incidentAlertActor)
	if !ok || actor.ID <= 0 || actor.Source == "" || actor.CorrelationID == "" || incidentEnt.Edges.WorkItem == nil {
		return nil, fmt.Errorf("escalation requires trusted actor, source, stable operation identity and WorkItem")
	}
	item := incidentEnt.Edges.WorkItem
	tenantID := item.TenantID
	if rule.TenantID != tenantID || (rule.ToStatus != "" && rule.ToStatus != "escalated") {
		return nil, fmt.Errorf("invalid escalation tenant or lifecycle target")
	}
	if rule.TargetAssigneeID > 0 && rule.TargetAssigneeType != "user" {
		return nil, fmt.Errorf("unsupported escalation assignee type")
	}
	if rule.TargetGroup != "" {
		return nil, fmt.Errorf("unsupported escalation group assignment")
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	owner := NewIncidentService(s.client, s.logger)
	owner.SetDirectorySnapshot(s.directory)
	reason := fmt.Sprintf("escalation rule %d (%s), trigger %s after %d minutes, level %d", rule.ID, rule.Name, rule.TriggerType, rule.TriggerMinutes, rule.EscalationLevel)
	cmd := dto.IncidentCommand{IncidentID: incidentEnt.ID, Action: "escalate", EscalationLevel: rule.EscalationLevel, AssigneeID: rule.TargetAssigneeID, Reason: reason, Meta: workitemmutation.Meta{TenantID: tenantID, ActorID: actor.ID, ExpectedVersion: item.Version, Source: actor.Source, OperationID: actor.CorrelationID + ":escalate", CorrelationID: actor.CorrelationID}}
	digest, err := incidentCommandDigest(cmd)
	if err != nil {
		return nil, err
	}
	// The escalation receipt binds the complete composition, including durable notification intent.
	digest, err = workitemmutation.Digest([]any{digest, rule.NotificationConfig})
	if err != nil {
		return nil, err
	}
	result, err := owner.applyIncidentCommandTx(ctx, tx, cmd, digest)
	if err != nil {
		return nil, err
	}
	if !result.Replayed && rule.TargetAssigneeID > 0 && rule.TargetAssigneeID != item.AssigneeID {
		cmd.Action = "assign"
		cmd.Meta.ExpectedVersion = result.Version
		cmd.Meta.OperationID = actor.CorrelationID + ":assign"
		digest, err = incidentCommandDigest(cmd)
		if err != nil {
			return nil, err
		}
		if _, err = owner.applyIncidentCommandTx(ctx, tx, cmd, digest); err != nil {
			return nil, err
		}
	}
	updatedIncident, err := tx.Incident.Query().Where(incident.ID(incidentEnt.ID), incidentTenantScope(tenantID)).WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return nil, err
	}
	if result.Replayed {
		return updatedIncident, nil
	}
	channels, err := channelsFromNotificationConfig(rule.NotificationConfig)
	if err != nil {
		return nil, err
	}
	if len(channels) > 0 {
		creator, ok := s.alertCreator.(IncidentAlertTransactionCreator)
		if !ok {
			return nil, fmt.Errorf("transactional incident alerting service is not configured")
		}
		recipients, err := recipientsFromNotificationConfig(rule.NotificationConfig)
		if err != nil {
			return nil, err
		}
		if _, err := creator.CreateIncidentAlertTx(ctx, tx, &dto.CreateIncidentAlertRequest{
			IncidentID: incidentEnt.ID, AlertType: "escalation", AlertName: "事件升级告警",
			Message:  fmt.Sprintf("事件 #%d 已升级到 L%d: %s", incidentEnt.ID, rule.EscalationLevel, rule.Name),
			Severity: "high", Channels: channels, Recipients: recipients,
		}, tenantID); err != nil {
			return nil, fmt.Errorf("create escalation alert: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return updatedIncident, nil
}

// ProcessEscalations 批量处理升级检查
// 由定时任务调用
func (s *IncidentEscalationService) ProcessEscalations(ctx context.Context, tenantID int) error {
	actor, ok := ctx.Value(incidentAlertActorContextKey{}).(incidentAlertActor)
	if !ok || actor.ID <= 0 || actor.Source == "" || actor.CorrelationID == "" {
		return fmt.Errorf("batch escalation requires trusted actor and stable run identity")
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != tenantID {
		return fmt.Errorf("tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	incidents, err := s.client.Incident.Query().Where(incidentTenantScope(tenantID, ticket.DeletedAtIsNil())).WithWorkItem(withIncidentWorkItemProjection).All(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, inc := range incidents {
		if !common.IsValidIncidentStatusTransition(inc.Edges.WorkItem.Status, common.IncidentStatusEscalated) {
			continue
		}
		rules, err := s.getMatchingRules(ctx, inc)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, rule := range rules {
			if !rule.AutoEscalate || rule.EscalationLevel <= inc.EscalationLevel {
				continue
			}
			eligible, err := s.shouldEscalate(ctx, inc, rule)
			if err != nil {
				failures = append(failures, err)
				break
			}
			if !eligible {
				continue
			}
			// Each candidate is an observation, not a replay with a silently refreshed version.
			key := fmt.Sprintf("%s:rule:%d:incident:%d:version:%d", actor.CorrelationID, rule.ID, inc.ID, inc.Edges.WorkItem.Version)
			_, err = s.escalateIncident(WithIncidentAlertActor(ctx, actor.ID, actor.Source, key), inc, rule)
			if err != nil {
				failures = append(failures, fmt.Errorf("incident %d: %w", inc.ID, err))
			}
			break
		}
	}

	return errors.Join(failures...)
}

// GetEscalationHistory 获取事件升级历史
func (s *IncidentEscalationService) GetEscalationHistory(ctx context.Context, incidentID int) ([]*ent.IncidentEvent, error) {
	all, err := s.client.IncidentEvent.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	var result []*ent.IncidentEvent
	for _, event := range all {
		if event.IncidentID == incidentID && (event.EventName == "escalate" || event.EventType == "escalation") {
			result = append(result, event)
		}
	}
	return result, nil
}
