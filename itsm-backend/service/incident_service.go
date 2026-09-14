package service

import (
	"context"
	"fmt"
	"itsm-backend/handlers/shared/workflowcallback"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/configurationitem"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/incidentalert"
	"itsm-backend/ent/incidentevent"
	"itsm-backend/ent/incidentmetric"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketcategory"
	"itsm-backend/ent/user"
	assignment "itsm-backend/handlers/common/workitemassignment"
	creation "itsm-backend/handlers/common/workitemcreation"

	entsql "entgo.io/ent/dialect/sql"
	"go.uber.org/zap"
)

type IncidentService struct {
	workflowAssignment    workflowcallback.AssignmentBoundary
	sessions              *authorization.SessionReader
	priorityMatrixService *PriorityMatrixService
	client                *ent.Client
	logger                *zap.SugaredLogger

	ruleEngine   *IncidentRuleEngine
	alertCreator IncidentAlertCreator
}

type IncidentAlertCreator interface {
	CreateIncidentAlert(context.Context, *dto.CreateIncidentAlertRequest, int) (*dto.IncidentAlertResponse, error)
}

func NewIncidentService(client *ent.Client, logger *zap.SugaredLogger) *IncidentService {
	incidentService := &IncidentService{
		client:                client,
		logger:                logger,
		priorityMatrixService: NewPriorityMatrixService(logger),
	}
	incidentService.ruleEngine = NewIncidentRuleEngine(client, logger)
	return incidentService
}

// RuleEngine returns the single authoritative rule engine owned by this service.
func (s *IncidentService) RuleEngine() *IncidentRuleEngine {
	return s.ruleEngine
}

func (s *IncidentService) SetAlertCreator(creator IncidentAlertCreator) {
	s.alertCreator = creator
	s.ruleEngine.alertCreator = creator
}

// SetPriorityMatrixService configures professional Incident priority rules.
func (s *IncidentService) SetPriorityMatrixService(pms *PriorityMatrixService) {
	s.priorityMatrixService = pms
}

// GetIncident 获取事件
func (s *IncidentService) GetIncident(ctx context.Context, id int, tenantID int) (*dto.IncidentResponse, error) {
	incidentEntity, err := s.getIncidentEntity(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	return s.toIncidentResponse(incidentEntity), nil
}

func (s *IncidentService) GetIncidentWithActions(ctx context.Context, id int, actor ActionActor) (*dto.IncidentResponse, error) {
	incidentEntity, err := s.getIncidentEntity(ctx, id, actor.TenantID)
	if err != nil {
		return nil, err
	}
	response := s.toIncidentResponse(incidentEntity)
	actor.Client = s.client
	response.Actions = BuildIncidentActions(ctx, actor, incidentEntity)
	return response, nil
}

func (s *IncidentService) getIncidentEntity(ctx context.Context, id int, tenantID int) (*ent.Incident, error) {
	incidentEntity, err := s.client.Incident.Query().
		Where(
			incident.IDEQ(id),
			incidentTenantScope(tenantID),
		).
		WithWorkItem(withIncidentWorkItemProjection).
		WithConfigurationItems().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found")
		}
		s.logger.Errorw("Failed to get incident", "error", err, "id", id)
		return nil, fmt.Errorf("failed to get incident: %w", err)
	}

	return incidentEntity, nil
}

// ListIncidents 获取事件列表
func (s *IncidentService) ListIncidents(ctx context.Context, tenantID int, page, size int, filters map[string]interface{}) ([]*dto.IncidentResponse, int, error) {
	query := s.client.Incident.Query().
		Where(incidentTenantScope(tenantID))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 200 {
		size = 200
	}

	// 应用过滤器
	if status, ok := filters["status"].(string); ok && status != "" {
		query = query.Where(incident.HasWorkItemWith(ticket.StatusEQ(status)))
	}
	if priority, ok := filters["priority"].(string); ok && priority != "" {
		query = query.Where(incident.HasWorkItemWith(ticket.PriorityEQ(priority)))
	}
	if severity, ok := filters["severity"].(string); ok && severity != "" {
		query = query.Where(incident.SeverityEQ(severity))
	}
	if category, ok := filters["category"].(string); ok && category != "" {
		query = query.Where(incident.HasWorkItemWith(ticket.HasCategoryWith(ticketcategory.NameEQ(category))))
	}
	if source, ok := filters["source"].(string); ok && source != "" {
		query = query.Where(incident.HasWorkItemWith(ticket.SourceEQ(source)))
	}
	if keyword, ok := filters["keyword"].(string); ok && keyword != "" {
		// 关键词搜索：标题、描述、事件编号
		query = query.Where(
			incident.Or(
				incident.HasWorkItemWith(ticket.Or(ticket.TitleContains(keyword), ticket.DescriptionContains(keyword))),
				incident.HasWorkItemWith(ticket.TicketNumberContains(keyword)),
			),
		)
	}
	if assigneeID, ok := filters["assignee_id"].(int); ok && assigneeID > 0 {
		query = query.Where(incident.HasWorkItemWith(ticket.AssigneeIDEQ(assigneeID)))
	}

	// 获取总数
	total, err := query.Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count incidents", "error", err)
		return nil, 0, fmt.Errorf("failed to count incidents: %w", err)
	}

	// 分页查询
	incidents, err := query.
		WithWorkItem(withIncidentWorkItemProjection).
		WithConfigurationItems().
		Offset((page - 1) * size).
		Limit(size).
		Order(incident.ByWorkItemField(ticket.FieldCreatedAt, entsql.OrderDesc())).
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to list incidents", "error", err)
		return nil, 0, fmt.Errorf("failed to list incidents: %w", err)
	}

	responses := make([]*dto.IncidentResponse, len(incidents))
	for i, incidentEntity := range incidents {
		responses[i] = s.toIncidentResponse(incidentEntity)
	}

	return responses, total, nil
}

// LinkIncidentCIs links configuration items to an incident.
func (s *IncidentService) LinkIncidentCIs(ctx context.Context, incidentID int, ciIDs []int, tenantID int) error {
	incidentEntity, err := s.client.Incident.Query().
		Where(incident.IDEQ(incidentID), incidentTenantScope(tenantID)).
		WithWorkItem().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("incident not found")
		}
		return fmt.Errorf("failed to get incident: %w", err)
	}
	if len(ciIDs) == 0 {
		return nil
	}

	count, err := s.client.ConfigurationItem.Query().
		Where(configurationitem.IDIn(ciIDs...), configurationitem.TenantIDEQ(incidentEntity.Edges.WorkItem.TenantID)).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate configuration items: %w", err)
	}
	if count != len(ciIDs) {
		return fmt.Errorf("one or more configuration items not found")
	}

	if _, err := s.client.Incident.UpdateOneID(incidentID).
		Where(incidentTenantScope(incidentEntity.Edges.WorkItem.TenantID)).
		AddConfigurationItemIDs(ciIDs...).
		Save(ctx); err != nil {
		return fmt.Errorf("failed to link configuration items: %w", err)
	}
	return nil
}

// GetIncidentCIs returns the configuration items linked to an incident.
func (s *IncidentService) GetIncidentCIs(ctx context.Context, incidentID int, tenantID int) ([]dto.CIInfo, error) {
	incidentEntity, err := s.client.Incident.Query().
		Where(incident.IDEQ(incidentID), incidentTenantScope(tenantID)).
		WithConfigurationItems().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found")
		}
		return nil, fmt.Errorf("failed to get incident configuration items: %w", err)
	}

	cis := make([]dto.CIInfo, 0, len(incidentEntity.Edges.ConfigurationItems))
	for _, ci := range incidentEntity.Edges.ConfigurationItems {
		cis = append(cis, dto.CIInfo{ID: ci.ID, Name: ci.Name})
	}
	return cis, nil
}

func (s *IncidentService) SetSessionReader(sessions *authorization.SessionReader) {
	s.sessions = sessions
}

type incidentAssignmentMutation struct {
	writer  *assignment.Writer
	command assignment.Command
}

// UpdateIncident keeps professional and shared edits in the same owning transaction.
func (s *IncidentService) UpdateIncident(ctx context.Context, id int, req *dto.UpdateIncidentRequest, tenantID int, identity creation.Identity) (*dto.IncidentResponse, error) {
	if req.AssigneeID != nil {
		if s.sessions == nil || identity.TenantID != tenantID {
			return nil, fmt.Errorf("verified Incident assignment session is required")
		}
		var result *dto.IncidentResponse
		err := s.sessions.Write(ctx, identity, func(session *authorization.SessionSnapshot) error {
			var err error
			current, loadErr := session.Tx.Incident.Query().Where(incident.ID(id), incidentTenantScope(tenantID)).Only(ctx)
			if loadErr != nil {
				return loadErr
			}
			if _, authErr := session.AuthorizeWorkItemAssignment(ctx, current.WorkItemID); authErr != nil {
				return authErr
			}
			mutation := &incidentAssignmentMutation{writer: NewWorkItemAssignmentWriter(session), command: assignment.Command{TenantID: tenantID, ActorID: session.Actor.ID, ActorTenantID: session.Actor.TenantID, Source: identity.Channel + ".incident.update"}}
			result, err = s.UpdateIncidentTx(ctx, session.Tx, id, req, tenantID, mutation)
			return err
		})
		return result, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := s.UpdateIncidentTx(ctx, tx, id, req, tenantID, nil)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateIncidentTx keeps validation, WorkItem/extension changes and timeline in the caller's transaction.
func (s *IncidentService) UpdateIncidentTx(ctx context.Context, tx *ent.Tx, id int, req *dto.UpdateIncidentRequest, tenantID int, mutation *incidentAssignmentMutation) (*dto.IncidentResponse, error) {
	owner := *s
	owner.client = tx.Client()
	return owner.updateIncident(ctx, id, req, tenantID, mutation)
}

func (s *IncidentService) updateIncident(ctx context.Context, id int, req *dto.UpdateIncidentRequest, tenantID int, mutation *incidentAssignmentMutation) (*dto.IncidentResponse, error) {
	s.logger.Infow("Updating incident", "id", id, "tenant_id", tenantID)

	// 获取当前事件实体
	currentIncident, err := s.client.Incident.Query().
		Where(incident.IDEQ(id), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found")
		}
		return nil, fmt.Errorf("failed to get incident: %w", err)
	}

	// 版本检查（乐观锁）- 除非明确强制更新
	if !req.Force && req.Version > 0 && currentIncident.Edges.WorkItem.Version != req.Version {
		return nil, common.NewVersionConflictError(
			"事件",
			id,
			req.Version,
			currentIncident.Edges.WorkItem.Version,
		)
	}

	// 如果要更新状态，验证状态转换是否合法
	if req.Status != nil {
		// 验证状态转换
		if !isValidIncidentStatusTransition(currentIncident.Edges.WorkItem.Status, *req.Status) {
			return nil, rejectIncidentAction("invalid status transition from '%s' to '%s'", currentIncident.Edges.WorkItem.Status, *req.Status)
		}
		// 解决与关闭必须走专用动作，确保解决说明、关闭备注和审计事件不可被通用更新绕过。
		if *req.Status == common.IncidentStatusResolved || *req.Status == common.IncidentStatusClosed {
			return nil, rejectIncidentAction("use the dedicated resolve or close action for this status transition")
		}
	}
	if req.AssigneeID != nil {
		if !canAssignIncidentStatus(currentIncident.Edges.WorkItem.Status) {
			return nil, rejectIncidentAction("resolved, closed, or cancelled incidents cannot be reassigned")
		}
		if mutation == nil || mutation.writer == nil {
			return nil, fmt.Errorf("verified Incident assignment writer is required")
		}
	}
	if req.CategoryID != nil && *req.CategoryID != 0 {
		_, err = s.client.TicketCategory.Query().Where(ticketcategory.IDEQ(*req.CategoryID), ticketcategory.TenantIDEQ(tenantID), ticketcategory.IsActiveEQ(true)).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("active ticket category not found in tenant: %w", err)
		}
	}

	// 计算优先级：如果用户没有显式传入Priority，但修改了Impact或Urgency，则自动重新计算
	priority := req.Priority
	if priority == nil && s.priorityMatrixService != nil && (req.Impact != nil || req.Urgency != nil) {
		// 使用新的Impact或现有Impact
		impact := currentIncident.Impact
		if req.Impact != nil {
			impact = *req.Impact
		}

		// 使用新的Urgency或现有Urgency
		urgency := currentIncident.Urgency
		if req.Urgency != nil {
			urgency = *req.Urgency
		}

		calculatedPriority, err := s.priorityMatrixService.CalculatePriority(tenantID, impact, urgency)
		if err != nil {
			s.logger.Warnw("Failed to calculate priority during update, keeping current value", "error", err)
		} else {
			priority = &calculatedPriority
		}
	}

	updateQuery := s.client.Incident.UpdateOneID(id).Where(incidentTenantScope(tenantID))
	if req.Severity != nil {
		updateQuery.SetSeverity(*req.Severity)
	}
	if req.Impact != nil {
		updateQuery.SetImpact(*req.Impact)
	}
	if req.Urgency != nil {
		updateQuery.SetUrgency(*req.Urgency)
	}
	if req.ImpactAnalysis != nil {
		updateQuery.SetImpactAnalysis(dto.StructToMap(req.ImpactAnalysis))
	}
	if req.RootCause != nil {
		updateQuery.SetRootCause(dto.StructToMap(req.RootCause))
	}
	if req.ResolutionSteps != nil {
		updateQuery.SetResolutionSteps(dto.StructSliceToMapSlice(req.ResolutionSteps))
	}
	if req.Metadata != nil {
		updateQuery.SetMetadata(req.Metadata)
	}

	expectedVersion := currentIncident.Edges.WorkItem.Version
	nextVersion := expectedVersion + 1
	if req.AssigneeID != nil {
		cmd := mutation.command
		cmd.WorkItemID = currentIncident.WorkItemID
		cmd.AssigneeID = *req.AssigneeID
		cmd.ExpectedVersion = expectedVersion
		updated, err := mutation.writer.Apply(ctx, s.client, cmd)
		if err != nil {
			return nil, err
		}
		expectedVersion = updated.Version
	}
	workItemUpdate := s.client.Ticket.UpdateOneID(currentIncident.WorkItemID).
		Where(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil(), ticket.VersionEQ(expectedVersion)).
		SetUpdatedAt(time.Now()).
		SetVersion(nextVersion)
	if req.Title != nil {
		workItemUpdate.SetTitle(*req.Title)
	}
	if req.Description != nil {
		workItemUpdate.SetDescription(*req.Description)
	}
	if req.Status != nil {
		workItemUpdate.SetStatus(*req.Status)
		if *req.Status == common.IncidentStatusInProgress && currentIncident.Edges.WorkItem.Status == common.IncidentStatusResolved {
			workItemUpdate.ClearResolvedAt().ClearClosedAt()
		}
	}
	if priority != nil {
		workItemUpdate.SetPriority(*priority)
	}
	if req.CategoryID != nil {
		if *req.CategoryID == 0 {
			workItemUpdate.ClearCategoryID()
		} else {
			workItemUpdate.SetCategoryID(*req.CategoryID)
		}
	}
	workItem, err := workItemUpdate.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) && !req.Force && req.Version > 0 {
			latest, lookupErr := s.client.Ticket.Query().Where(ticket.IDEQ(currentIncident.WorkItemID), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()).Only(ctx)
			if lookupErr == nil {
				return nil, common.NewVersionConflictError("事件", id, req.Version, latest.Version)
			}
		}
		return nil, fmt.Errorf("failed to update incident work item: %w", err)
	}
	incidentEntity, err := updateQuery.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to update incident: %w", err)
	}
	incidentEntity.Edges.WorkItem = workItem

	// 记录事件更新活动
	_, err = s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID:  id,
		EventType:   "update",
		EventName:   "事件更新",
		Description: "事件信息已更新",
		Status:      "active",
		Severity:    "info",
		Source:      "system",
	}, tenantID)
	if err != nil {
		return nil, err
	}

	s.logger.Infow("Incident updated successfully", "id", id)
	refreshed, err := s.getIncidentEntity(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	return s.toIncidentResponse(refreshed), nil
}

// AssignIncident 分配事件
func canAssignIncidentStatus(status string) bool {
	return status != common.IncidentStatusResolved && !common.IsIncidentFinalStatus(status)
}

// IsUnfinished uses the Incident owner's actionable/resolved/final boundary.
func (s *IncidentService) IsUnfinished(_ context.Context, _ *ent.Client, item *ent.Ticket) (bool, error) {
	if item == nil || item.RecordClass != "incident" {
		return false, fmt.Errorf("Incident WorkItem is required")
	}
	switch item.Status {
	case common.IncidentStatusNew, common.IncidentStatusAcknowledged, common.IncidentStatusAssigned,
		common.IncidentStatusInProgress, common.IncidentStatusTriaged, common.IncidentStatusEscalated,
		common.IncidentStatusOnHold, common.IncidentStatusResolved, common.IncidentStatusClosed, common.IncidentStatusCancelled:
		return canAssignIncidentStatus(item.Status), nil
	default:
		return false, fmt.Errorf("unsupported Incident status %q", item.Status)
	}
}

func (s *IncidentService) AssignIncident(ctx context.Context, id, assigneeID, tenantID int, identity creation.Identity) (*dto.IncidentResponse, error) {
	if s.sessions == nil || identity.TenantID != tenantID {
		return nil, fmt.Errorf("verified Incident assignment session is required")
	}
	var result *dto.IncidentMutationOutcome
	err := s.sessions.Write(ctx, identity, func(session *authorization.SessionSnapshot) error {
		owner := *s
		owner.client = session.Tx.Client()
		current, err := owner.getIncidentEntity(ctx, id, tenantID)
		if err != nil {
			return err
		}
		if _, err := session.AuthorizeWorkItemAssignment(ctx, current.WorkItemID); err != nil {
			return err
		}
		result, err = owner.assignIncident(ctx, id, assigneeID, tenantID, false, &incidentAssignmentMutation{writer: NewWorkItemAssignmentWriter(session), command: assignment.Command{TenantID: tenantID, ActorID: session.Actor.ID, ActorTenantID: session.Actor.TenantID, Source: identity.Channel + ".incident.assign"}})
		return err
	})
	if err != nil {
		return nil, err
	}
	return result.Incident, nil
}

// AssignIncidentForWorkflow atomically applies the workflow assignment target:
// assignee and the Incident-owned assigned state. Returning the persisted
// mutation outcome lets the callback engine distinguish a retry from a first
// application without a race-prone read in the handler.
func (s *IncidentService) AssignIncidentForWorkflow(ctx context.Context, id int, assigneeID int, tenantID int) (*dto.IncidentMutationOutcome, error) {
	if s.workflowAssignment == nil {
		return nil, fmt.Errorf("verified callback assignment provenance is required")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	writer, command, err := s.workflowAssignment(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	owner := *s
	owner.client = tx.Client()
	result, err := owner.assignIncident(ctx, id, assigneeID, tenantID, true, &incidentAssignmentMutation{writer: writer, command: command})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *IncidentService) assignIncident(ctx context.Context, id int, assigneeID int, tenantID int, workflow bool, mutation *incidentAssignmentMutation) (*dto.IncidentMutationOutcome, error) {
	s.logger.Infow("Assigning incident", "id", id, "assignee_id", assigneeID, "tenant_id", tenantID)

	// 获取当前事件
	current, err := s.client.Incident.Query().
		Where(incident.IDEQ(id), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found")
		}
		return nil, fmt.Errorf("failed to get incident: %w", err)
	}
	if !canAssignIncidentStatus(current.Edges.WorkItem.Status) {
		return nil, rejectIncidentAction("resolved, closed, or cancelled incidents cannot be reassigned")
	}
	if mutation == nil || mutation.writer == nil {
		return nil, fmt.Errorf("verified Incident assignment writer is required")
	}
	cmd := mutation.command
	cmd.WorkItemID = current.WorkItemID
	cmd.AssigneeID = assigneeID
	cmd.ExpectedVersion = current.Edges.WorkItem.Version
	updated, err := mutation.writer.Apply(ctx, s.client, cmd)
	if err != nil {
		return nil, err
	}
	changed := updated.Version != current.Edges.WorkItem.Version
	if workflow && current.Edges.WorkItem.Status != common.IncidentStatusAssigned {
		write := s.client.Ticket.UpdateOneID(current.WorkItemID).SetStatus(common.IncidentStatusAssigned)
		if !changed {
			write.AddVersion(1)
		}
		if err := write.Exec(ctx); err != nil {
			return nil, err
		}
		changed = true
	}
	if !changed {
		return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(current), Applied: false}, nil
	}
	updatedIncident, err := s.client.Incident.Query().Where(incident.IDEQ(id), incidentTenantScope(tenantID)).WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("reload assigned incident: %w", err)
	}

	// 记录分配活动
	_, err = s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID:  id,
		EventType:   "assignment",
		EventName:   "事件分配",
		Description: fmt.Sprintf("事件已分配给用户 %d", assigneeID),
		Status:      "active",
		Severity:    "info",
		Source:      "user",
	}, tenantID)

	if err != nil {
		return nil, err
	}
	s.logger.Infow("Incident assigned successfully", "id", id, "assignee_id", assigneeID)
	return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(updatedIncident), Applied: true}, nil
}

func (s *IncidentService) validateIncidentAssignee(ctx context.Context, assigneeID, tenantID int) error {
	if assigneeID <= 0 {
		return rejectIncidentAction("invalid assignee id")
	}
	assigneeExists, err := s.client.User.Query().
		Where(user.IDEQ(assigneeID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate assignee: %w", err)
	}
	if !assigneeExists {
		return rejectIncidentAction("assignee not found or inactive")
	}
	return nil
}

func (s *IncidentService) ensureActiveIncident(ctx context.Context, incidentID, tenantID int) error {
	exists, err := s.client.Incident.Query().
		Where(incident.IDEQ(incidentID), incidentTenantScope(tenantID)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate incident: %w", err)
	}
	if !exists {
		return fmt.Errorf("incident not found")
	}
	return nil
}

// DeleteIncident 软删除事件，保留事件、活动、告警与指标用于审计。
func (s *IncidentService) DeleteIncident(ctx context.Context, id int, tenantID int) error {
	s.logger.Infow("Deleting incident", "id", id, "tenant_id", tenantID)

	entity, err := s.client.Incident.Query().Where(incident.IDEQ(id), incidentTenantScope(tenantID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("cross-tenant access denied: incident not found")
	}
	updated, err := s.client.Ticket.Update().
		Where(ticket.IDEQ(entity.WorkItemID), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()).
		SetDeletedAt(time.Now()).SetUpdatedAt(time.Now()).AddVersion(1).Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete incident: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("cross-tenant access denied: incident not found")
	}

	s.logger.Infow("Incident deleted successfully", "id", id)
	return nil
}

// CreateIncidentEvent 创建事件活动记录
func (s *IncidentService) CreateIncidentEvent(ctx context.Context, req *dto.CreateIncidentEventRequest, tenantID int) (*dto.IncidentEventResponse, error) {
	s.logger.Infow("Creating incident event", "incident_id", req.IncidentID, "type", req.EventType)
	if err := s.ensureActiveIncident(ctx, req.IncidentID, tenantID); err != nil {
		return nil, err
	}

	occurredAt := time.Now()
	if req.OccurredAt != nil {
		occurredAt = *req.OccurredAt
	}

	eventBuilder := s.client.IncidentEvent.Create().
		SetIncidentID(req.IncidentID).
		SetEventType(req.EventType).
		SetEventName(req.EventName).
		SetDescription(req.Description).
		SetStatus(req.Status).
		SetSeverity(req.Severity).
		SetData(req.Data).
		SetOccurredAt(occurredAt).
		SetSource(req.Source).
		SetMetadata(req.Metadata).
		SetTenantID(tenantID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now())

	if req.UserID != nil {
		eventBuilder.SetUserID(*req.UserID)
	}
	if actor, ok := ctx.Value(incidentAlertActorContextKey{}).(incidentAlertActor); ok {
		metadata := make(map[string]interface{}, len(req.Metadata)+1)
		for key, value := range req.Metadata {
			metadata[key] = value
		}
		metadata["correlationId"] = actor.CorrelationID
		eventBuilder.SetUserID(actor.ID).SetSource(actor.Source).SetMetadata(metadata)
	}

	event, err := eventBuilder.Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create incident event", "error", err)
		return nil, fmt.Errorf("failed to create incident event: %w", err)
	}

	s.logger.Infow("Incident event created successfully", "id", event.ID)
	return s.toIncidentEventResponse(event), nil
}

// CreateIncidentMetric 创建事件指标
func (s *IncidentService) CreateIncidentMetric(ctx context.Context, req *dto.CreateIncidentMetricRequest, tenantID int) (*dto.IncidentMetricResponse, error) {
	s.logger.Infow("Creating incident metric", "incident_id", req.IncidentID, "type", req.MetricType)
	if err := s.ensureActiveIncident(ctx, req.IncidentID, tenantID); err != nil {
		return nil, err
	}

	measuredAt := time.Now()
	if req.MeasuredAt != nil {
		measuredAt = *req.MeasuredAt
	}

	metric, err := s.client.IncidentMetric.Create().
		SetIncidentID(req.IncidentID).
		SetMetricType(req.MetricType).
		SetMetricName(req.MetricName).
		SetMetricValue(req.MetricValue).
		SetUnit(req.Unit).
		SetMeasuredAt(measuredAt).
		SetTags(req.Tags).
		SetMetadata(req.Metadata).
		SetTenantID(tenantID).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create incident metric", "error", err)
		return nil, fmt.Errorf("failed to create incident metric: %w", err)
	}

	s.logger.Infow("Incident metric created successfully", "id", metric.ID)
	return s.toIncidentMetricResponse(metric), nil
}

// GetIncidentMonitoring 获取事件监控数据
func (s *IncidentService) GetIncidentMonitoring(ctx context.Context, req *dto.IncidentMonitoringRequest, tenantID int) (*dto.IncidentMonitoringResponse, error) {
	s.logger.Infow("Getting incident monitoring data", "tenant_id", tenantID)

	// 解析时间范围
	startTime, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		return nil, fmt.Errorf("invalid start_time format: %w", err)
	}
	endTime, err := time.Parse(time.RFC3339, req.EndTime)
	if err != nil {
		return nil, fmt.Errorf("invalid end_time format: %w", err)
	}

	query := s.client.Incident.Query().
		Where(
			incidentTenantScope(tenantID, ticket.CreatedAtGTE(startTime), ticket.CreatedAtLTE(endTime)),
		)

	// 应用过滤器
	if req.IncidentID != nil {
		query = query.Where(incident.IDEQ(*req.IncidentID))
	}
	if req.Category != nil {
		query = query.Where(incident.HasWorkItemWith(ticket.HasCategoryWith(ticketcategory.NameEQ(*req.Category))))
	}
	if req.Priority != nil {
		query = query.Where(incident.HasWorkItemWith(ticket.PriorityEQ(*req.Priority)))
	}
	if req.Status != nil {
		query = query.Where(incident.HasWorkItemWith(ticket.StatusEQ(*req.Status)))
	}

	// 获取事件列表
	incidents, err := query.WithWorkItem(withIncidentWorkItemProjection).All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get incidents", "error", err)
		return nil, fmt.Errorf("failed to get incidents: %w", err)
	}

	// 计算统计数据
	totalIncidents := len(incidents)
	var openIncidents, resolvedIncidents, closedIncidents, criticalIncidents, highPriorityIncidents int
	var totalResolutionTime float64
	var resolvedCount int

	for _, incidentEntity := range incidents {
		switch incidentEntity.Edges.WorkItem.Status {
		case "new", "in_progress":
			openIncidents++
		case "resolved":
			resolvedIncidents++
			if !incidentEntity.Edges.WorkItem.ResolvedAt.IsZero() {
				resolutionTime := incidentEntity.Edges.WorkItem.ResolvedAt.Sub(incidentEntity.Edges.WorkItem.CreatedAt).Hours()
				totalResolutionTime += resolutionTime
				resolvedCount++
			}
		case "closed":
			closedIncidents++
		}

		if incidentEntity.Severity == "critical" {
			criticalIncidents++
		}
		if incidentEntity.Edges.WorkItem.Priority == "high" || incidentEntity.Edges.WorkItem.Priority == "urgent" {
			highPriorityIncidents++
		}
	}

	// 计算平均解决时间
	var averageResolutionTime float64
	if resolvedCount > 0 {
		averageResolutionTime = totalResolutionTime / float64(resolvedCount)
	}

	// 计算解决率
	var resolutionRate float64
	if totalIncidents > 0 {
		resolutionRate = float64(resolvedIncidents+closedIncidents) / float64(totalIncidents) * 100
	}

	// 计算升级率
	var escalationRate float64
	if totalIncidents > 0 {
		var escalatedCount int
		for _, incidentEntity := range incidents {
			if incidentEntity.EscalationLevel > 0 {
				escalatedCount++
			}
		}
		escalationRate = float64(escalatedCount) / float64(totalIncidents) * 100
	}

	// 构建响应
	response := &dto.IncidentMonitoringResponse{
		TotalIncidents:        totalIncidents,
		OpenIncidents:         openIncidents,
		ResolvedIncidents:     resolvedIncidents,
		ClosedIncidents:       closedIncidents,
		CriticalIncidents:     criticalIncidents,
		HighPriorityIncidents: highPriorityIncidents,
		AverageResolutionTime: averageResolutionTime,
		ResolutionRate:        resolutionRate,
		EscalationRate:        escalationRate,
	}

	// 转换事件列表
	response.Incidents = make([]dto.IncidentResponse, len(incidents))
	for i, incidentEntity := range incidents {
		response.Incidents[i] = *s.toIncidentResponse(incidentEntity)
	}

	return response, nil
}

// EscalateIncident 升级事件
func (s *IncidentService) EscalateIncident(ctx context.Context, req *dto.IncidentEscalationRequest, tenantID int) (*dto.IncidentEscalationResponse, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := s.EscalateIncidentTx(ctx, tx, req, tenantID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *IncidentService) EscalateIncidentTx(ctx context.Context, tx *ent.Tx, req *dto.IncidentEscalationRequest, tenantID int) (*dto.IncidentEscalationResponse, error) {
	owner := *s
	owner.client = tx.Client()
	return owner.escalateIncident(ctx, tx, req, tenantID)
}

func (s *IncidentService) escalateIncident(ctx context.Context, tx *ent.Tx, req *dto.IncidentEscalationRequest, tenantID int) (*dto.IncidentEscalationResponse, error) {
	if req.AutoAssign {
		return nil, rejectIncidentAction("automatic escalation assignment is unsupported; configure an explicit assign action")
	}
	recipients, err := incidentRuleUserRecipients(ctx, s.client, req.NotifyUsers, tenantID)
	if err != nil {
		return nil, err
	}
	s.logger.Infow("Escalating incident", "incident_id", req.IncidentID, "level", req.EscalationLevel)
	if req.EscalationLevel < 1 || req.EscalationLevel > 5 {
		return nil, rejectIncidentAction("escalation level must be between 1 and 5")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return nil, rejectIncidentAction("escalation reason is required")
	}

	// 获取事件
	current, err := s.client.Incident.Query().
		Where(
			incident.IDEQ(req.IncidentID),
			incidentTenantScope(tenantID),
		).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found")
		}
		return nil, fmt.Errorf("failed to get incident: %w", err)
	}
	if current.Edges.WorkItem.Status == common.IncidentStatusClosed || current.Edges.WorkItem.Status == common.IncidentStatusCancelled {
		return nil, rejectIncidentAction("terminal incident cannot be escalated")
	}
	if req.EscalationLevel <= current.EscalationLevel {
		return nil, rejectIncidentAction("escalation level must be greater than current level %d", current.EscalationLevel)
	}

	// 更新事件升级信息
	now := time.Now()
	workItem, err := tx.Ticket.UpdateOneID(current.WorkItemID).
		Where(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil(), ticket.VersionEQ(current.Edges.WorkItem.Version)).
		SetUpdatedAt(now).AddVersion(1).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("advance incident WorkItem version: %w", err)
	}
	incidentEntity, err := tx.Incident.UpdateOneID(req.IncidentID).
		Where(incidentTenantScope(tenantID)).
		SetEscalationLevel(req.EscalationLevel).
		SetEscalatedAt(now).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to escalate incident", "error", err)
		return nil, fmt.Errorf("failed to escalate incident: %w", err)
	}
	incidentEntity.Edges.WorkItem = workItem

	// 记录升级活动
	_, err = s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID:  req.IncidentID,
		EventType:   "escalation",
		EventName:   "事件升级",
		Description: fmt.Sprintf("事件升级到级别 %d: %s", req.EscalationLevel, req.Reason),
		Status:      "active",
		Severity:    "high",
		Data: map[string]interface{}{
			"escalation_level": req.EscalationLevel,
			"reason":           req.Reason,
		},
		Source: "system",
	}, tenantID)
	if err != nil {
		return nil, err
	}

	if len(recipients) > 0 {
		creator, ok := s.alertCreator.(IncidentAlertTransactionCreator)
		if !ok {
			return nil, fmt.Errorf("transactional incident alerting service is not configured")
		}
		_, err = creator.CreateIncidentAlertTx(ctx, tx, &dto.CreateIncidentAlertRequest{IncidentID: req.IncidentID, AlertType: "escalation", AlertName: "事件升级告警", Message: fmt.Sprintf("事件 %s 已升级到级别 %d", workItem.TicketNumber, req.EscalationLevel), Severity: "high", Channels: []string{"email", "in_app"}, Recipients: recipients}, tenantID)
		if err != nil {
			return nil, fmt.Errorf("create escalation alert: %w", err)
		}
	}

	// 构建响应
	response := &dto.IncidentEscalationResponse{
		ID:              incidentEntity.ID,
		IncidentID:      req.IncidentID,
		EscalationLevel: req.EscalationLevel,
		Reason:          req.Reason,
		Status:          "active",
		NotifiedUsers:   req.NotifyUsers,
		AutoAssigned:    req.AutoAssign,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	s.logger.Infow("Incident escalated successfully", "incident_id", req.IncidentID, "level", req.EscalationLevel)
	return response, nil
}

// isValidIncidentStatusTransition 检查事件状态转换是否合法。
// 阻断6 修复：委托给 common.IsValidIncidentStatusTransition，保持单一事实来源，
// 避免 service 层与 handlers/incident 层两套白名单漂移。
func isValidIncidentStatusTransition(currentStatus, newStatus string) bool {
	return common.IsValidIncidentStatusTransition(currentStatus, newStatus)
}

// 转换为响应DTO
func (s *IncidentService) toIncidentResponse(incident *ent.Incident) *dto.IncidentResponse {
	if incident == nil {
		return nil
	}
	return dto.ToIncidentResponse(incident, incident.Edges.WorkItem)
}

func (s *IncidentService) toIncidentEventResponse(event *ent.IncidentEvent) *dto.IncidentEventResponse {
	return &dto.IncidentEventResponse{
		ID:          event.ID,
		IncidentID:  event.IncidentID,
		EventType:   event.EventType,
		EventName:   event.EventName,
		Description: event.Description,
		Status:      event.Status,
		Severity:    event.Severity,
		Data:        event.Data,
		OccurredAt:  event.OccurredAt,
		UserID:      &event.UserID,
		Source:      event.Source,
		Metadata:    event.Metadata,
		TenantID:    event.TenantID,
		CreatedAt:   event.CreatedAt,
		UpdatedAt:   event.UpdatedAt,
	}
}

func (s *IncidentService) toIncidentAlertResponse(alert *ent.IncidentAlert) *dto.IncidentAlertResponse {
	return &dto.IncidentAlertResponse{
		ID:             alert.ID,
		IncidentID:     alert.IncidentID,
		AlertType:      alert.AlertType,
		AlertName:      alert.AlertName,
		Message:        alert.Message,
		Severity:       alert.Severity,
		Status:         alert.Status,
		Channels:       alert.Channels,
		Recipients:     alert.Recipients,
		TriggeredAt:    alert.TriggeredAt,
		AcknowledgedAt: &alert.AcknowledgedAt,
		ResolvedAt:     &alert.ResolvedAt,
		AcknowledgedBy: &alert.AcknowledgedBy,
		Metadata:       alert.Metadata,
		TenantID:       alert.TenantID,
		CreatedAt:      alert.CreatedAt,
		UpdatedAt:      alert.UpdatedAt,
	}
}

func (s *IncidentService) toIncidentMetricResponse(metric *ent.IncidentMetric) *dto.IncidentMetricResponse {
	return &dto.IncidentMetricResponse{
		ID:          metric.ID,
		IncidentID:  metric.IncidentID,
		MetricType:  metric.MetricType,
		MetricName:  metric.MetricName,
		MetricValue: metric.MetricValue,
		Unit:        metric.Unit,
		MeasuredAt:  metric.MeasuredAt,
		Tags:        metric.Tags,
		Metadata:    metric.Metadata,
		TenantID:    metric.TenantID,
		CreatedAt:   metric.CreatedAt,
		UpdatedAt:   metric.UpdatedAt,
	}
}

// GetIncidentStats 获取事件统计信息

// AcknowledgeIncident 流转事件状态到 acknowledged
func (s *IncidentService) AcknowledgeIncident(ctx context.Context, id, userID, tenantID int) error {
	// 获取当前事件状态进行验证
	incidentEntity, err := s.client.Incident.Query().
		Where(incident.IDEQ(id), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return err
	}

	// 验证状态转换是否合法
	status := incidentEntity.Edges.WorkItem.Status
	if !isValidIncidentStatusTransition(status, common.IncidentStatusAcknowledged) {
		return fmt.Errorf("invalid status transition from '%s' to '%s'", status, common.IncidentStatusAcknowledged)
	}

	_, err = s.transitionIncident(ctx, incidentEntity, tenantID, common.IncidentStatusAcknowledged, nil)
	if err != nil {
		return err
	}
	_, eventErr := s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "acknowledgement", EventName: "事件确认",
		Description: fmt.Sprintf("事件由用户 %d 确认", userID), Status: "active", Severity: "info",
		UserID: &userID, Source: "user",
	}, tenantID)
	return eventErr
}

// ResolveIncident 流转事件状态到 resolved
func (s *IncidentService) ResolveIncident(ctx context.Context, id, userID, tenantID int, resolution, rootCause string) error {
	if strings.TrimSpace(resolution) == "" {
		return fmt.Errorf("resolution is required")
	}
	// 获取当前事件状态进行验证
	incidentEntity, err := s.client.Incident.Query().
		Where(incident.IDEQ(id), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return err
	}

	// 验证状态转换是否合法
	status := incidentEntity.Edges.WorkItem.Status
	if !isValidIncidentStatusTransition(status, common.IncidentStatusResolved) {
		return fmt.Errorf("invalid status transition from '%s' to '%s'", status, common.IncidentStatusResolved)
	}

	now := time.Now()
	rootCauseData := incidentEntity.RootCause
	if rootCauseData == nil {
		rootCauseData = make(map[string]interface{})
	}
	if strings.TrimSpace(rootCause) != "" {
		rootCauseData["rootCause"] = strings.TrimSpace(rootCause)
		rootCauseData["status"] = "confirmed"
	}
	resolutionSteps := incidentEntity.ResolutionSteps
	resolutionSteps = append(resolutionSteps, map[string]interface{}{
		"step": len(resolutionSteps) + 1, "description": strings.TrimSpace(resolution),
		"executedBy": fmt.Sprintf("%d", userID), "executedAt": now, "status": "completed",
	})
	_, err = s.transitionIncident(ctx, incidentEntity, tenantID, common.IncidentStatusResolved, func(update *ent.IncidentUpdateOne) {
		update.
			SetRootCause(rootCauseData).
			SetResolutionSteps(resolutionSteps)
	})
	if err != nil {
		return err
	}
	_, eventErr := s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "resolution", EventName: "事件解决",
		Description: strings.TrimSpace(resolution), Status: "active", Severity: "info",
		Data:   map[string]interface{}{"rootCause": strings.TrimSpace(rootCause)},
		UserID: &userID, Source: "user",
	}, tenantID)
	return eventErr
}

// CloseIncident 流转事件状态到 closed
func (s *IncidentService) CloseIncident(ctx context.Context, id, userID, tenantID int, closeNotes string) error {
	if strings.TrimSpace(closeNotes) == "" {
		return fmt.Errorf("close notes are required")
	}
	// 获取当前事件状态进行验证
	incidentEntity, err := s.client.Incident.Query().
		Where(incident.IDEQ(id), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return err
	}

	// 验证状态转换是否合法
	status := incidentEntity.Edges.WorkItem.Status
	if !isValidIncidentStatusTransition(status, common.IncidentStatusClosed) {
		return fmt.Errorf("invalid status transition from '%s' to '%s'", status, common.IncidentStatusClosed)
	}

	_, err = s.transitionIncident(ctx, incidentEntity, tenantID, common.IncidentStatusClosed, nil)
	if err != nil {
		return err
	}
	_, eventErr := s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "closure", EventName: "事件关闭",
		Description: strings.TrimSpace(closeNotes), Status: "active", Severity: "info",
		UserID: &userID, Source: "user",
	}, tenantID)
	return eventErr
}

// ==================== BPMN 工作流专用写入方法 ====================
//
// 以下方法专供 service/bpmn.IncidentServiceTaskHandler 使用（通过 IncidentDomainServiceInterface
// 注入），把 incident_emergency_flow.bpmn 里 escalate_incident/resolve_incident/close_incident/
// update_incident/acknowledge_incident/categorize_incident 几个节点原来直接写 Ent 的代码收回到
// 领域服务里（AGENTS.md：Handler 不能绕过专业服务直接修改状态）。
//
// 不复用上面 EscalateIncident/ResolveIncident/CloseIncident/AcknowledgeIncident 这几个面向
// 人工/API 调用的方法，是因为它们要求非空 reason/resolution/closeNotes，并且（Resolve/Close/
// Acknowledge）会用 isValidIncidentStatusTransition 校验状态机合法性。incident_emergency_flow
// 是已经在生产跑的流程：自动分配节点在没有处理人时不设置状态（保持 new），后续经过
// 主管审批（userTask，当前未接线）、初步诊断（update_incident，是否设置 status 取决于
// 提交的表单变量）才到达 resolve/escalate 节点——不能保证状态机走到这几个 BPMN 节点时
// current.Status 已经流转到合法的前置状态。给这几个 BPMN 动作补上完整状态机校验和必填
// 字段校验是有价值的后续工作，但一次性引入会有直接打断现网流程的风险，超出本次
// "把裸 Ent 写收回领域服务"的任务边界，留作独立后续项。这里只做等价迁移：保留原来的
// 字段写入语义，同时补上审计事件（原直接写 Ent 的版本完全没有审计记录，这是真实的
// 审计缺口，AGENTS.md 要求状态变更必须审计，顺带修掉）。

// EscalateIncidentLevel 供 BPMN 自动升级节点使用。level<=0 的稳定目标是一级，
// 而不是依赖当前值递增；这样同一 durable callback 的重试不会重复升级。
func (s *IncidentService) EscalateIncidentLevel(ctx context.Context, id, tenantID, level int) (*dto.IncidentMutationOutcome, error) {
	current, err := s.client.Incident.Query().
		Where(incident.IDEQ(id), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found")
		}
		return nil, fmt.Errorf("failed to get incident: %w", err)
	}
	if level <= 0 {
		level = 1
	}
	if current.Edges.WorkItem.Status == common.IncidentStatusEscalated && current.EscalationLevel >= level {
		return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(current), Applied: false}, nil
	}

	now := time.Now()
	updated, err := s.transitionIncident(ctx, current, tenantID, common.IncidentStatusEscalated, func(update *ent.IncidentUpdateOne) {
		update.SetEscalationLevel(level).
			SetEscalatedAt(now)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to escalate incident: %w", err)
	}
	s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "escalation", EventName: "事件升级",
		Description: fmt.Sprintf("事件升级到级别 %d（工作流自动触发）", level),
		Status:      "active", Severity: "high", Source: "system",
	}, tenantID)
	return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(updated), Applied: true}, nil
}

// ResolveIncidentForWorkflow 供 BPMN resolve_incident 节点使用，同旧的裸 Ent 实现语义
// （只设置 status=resolved），补上 ResolvedAt（旧实现遗漏）和审计事件。
func (s *IncidentService) ResolveIncidentForWorkflow(ctx context.Context, id, tenantID int, resolution string) (*dto.IncidentMutationOutcome, error) {
	current, err := s.workflowIncident(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if current.Edges.WorkItem.Status == common.IncidentStatusResolved && !current.Edges.WorkItem.ResolvedAt.IsZero() {
		return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(current), Applied: false}, nil
	}
	updated, err := s.transitionIncident(ctx, current, tenantID, common.IncidentStatusResolved, nil)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", id)
		}
		return nil, fmt.Errorf("failed to resolve incident: %w", err)
	}
	s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "resolution", EventName: "事件解决",
		Description: strings.TrimSpace(resolution), Status: "active", Severity: "info", Source: "system",
	}, tenantID)
	return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(updated), Applied: true}, nil
}

// CloseIncidentForWorkflow 供 BPMN close_incident 节点使用，同旧的裸 Ent 实现语义
// （status=closed + closed_at），补上审计事件。
func (s *IncidentService) CloseIncidentForWorkflow(ctx context.Context, id, tenantID int, feedback string) (*dto.IncidentMutationOutcome, error) {
	current, err := s.workflowIncident(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if current.Edges.WorkItem.Status == common.IncidentStatusClosed && current.Edges.WorkItem.ClosedAt != nil {
		return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(current), Applied: false}, nil
	}
	updated, err := s.transitionIncident(ctx, current, tenantID, common.IncidentStatusClosed, nil)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", id)
		}
		return nil, fmt.Errorf("failed to close incident: %w", err)
	}
	s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "closure", EventName: "事件关闭",
		Description: strings.TrimSpace(feedback), Status: "active", Severity: "info", Source: "system",
	}, tenantID)
	return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(updated), Applied: true}, nil
}

// AcknowledgeIncidentForWorkflow 供 BPMN acknowledge_incident 节点使用，同旧的裸 Ent 实现
// 语义（status=acknowledged），补上审计事件。
func (s *IncidentService) AcknowledgeIncidentForWorkflow(ctx context.Context, id, tenantID int) (*dto.IncidentMutationOutcome, error) {
	current, err := s.workflowIncident(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if current.Edges.WorkItem.Status == common.IncidentStatusAcknowledged {
		return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(current), Applied: false}, nil
	}
	updated, err := s.transitionIncident(ctx, current, tenantID, common.IncidentStatusAcknowledged, nil)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", id)
		}
		return nil, fmt.Errorf("failed to acknowledge incident: %w", err)
	}
	s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "acknowledgement", EventName: "事件确认",
		Description: "事件由工作流自动确认", Status: "active", Severity: "info", Source: "system",
	}, tenantID)
	return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(updated), Applied: true}, nil
}

// UpdateIncidentForWorkflow 供 BPMN update_incident 节点使用（如初步诊断步骤），按提供的
// 字段做部分更新，空字符串表示"不修改该字段"——同旧的裸 Ent 实现语义，补上审计事件。
func (s *IncidentService) UpdateIncidentForWorkflow(ctx context.Context, id, tenantID int, title, description, priority, severity, status string) (*dto.IncidentMutationOutcome, error) {
	current, err := s.workflowIncident(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	workItem := current.Edges.WorkItem
	unchanged := (title == "" || title == workItem.Title) &&
		(description == "" || description == workItem.Description) &&
		(priority == "" || priority == workItem.Priority) &&
		(severity == "" || severity == current.Severity) &&
		(status == "" || status == workItem.Status)
	if unchanged {
		return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(current), Applied: false}, nil
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*dto.IncidentMutationOutcome, error) { _ = tx.Rollback(); return nil, cause }
	updateQuery := tx.Incident.UpdateOneID(id).
		Where(incidentTenantScope(tenantID))
	if severity != "" {
		updateQuery.SetSeverity(severity)
	}
	updated, err := updateQuery.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fail(fmt.Errorf("事件 %d 不存在或不属于当前租户", id))
		}
		return fail(fmt.Errorf("failed to update incident: %w", err))
	}
	workItemUpdate := tx.Ticket.UpdateOneID(current.WorkItemID).
		Where(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil(), ticket.VersionEQ(current.Edges.WorkItem.Version)).
		SetUpdatedAt(time.Now()).AddVersion(1)
	if title != "" {
		workItemUpdate.SetTitle(title)
	}
	if description != "" {
		workItemUpdate.SetDescription(description)
	}
	if priority != "" {
		workItemUpdate.SetPriority(priority)
	}
	if status != "" {
		workItemUpdate.SetStatus(status)
	}
	updatedWorkItem, err := workItemUpdate.Save(ctx)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	updated.Edges.WorkItem = updatedWorkItem
	s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "update", EventName: "事件更新",
		Description: "事件信息已更新（工作流）", Status: "active", Severity: "info", Source: "system",
	}, tenantID)
	return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(updated), Applied: true}, nil
}

// CategorizeIncidentForWorkflow 供 BPMN categorize_incident 节点使用，同旧的裸 Ent 实现语义
// （status=triaged + category/subcategory），补上审计事件。
func (s *IncidentService) CategorizeIncidentForWorkflow(ctx context.Context, id, tenantID int, category, subcategory string) (*dto.IncidentMutationOutcome, error) {
	current, err := s.workflowIncident(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	currentResponse := s.toIncidentResponse(current)
	if current.Edges.WorkItem.Status == common.IncidentStatusTriaged &&
		(category == "" || category == currentResponse.Category) &&
		(subcategory == "" || subcategory == currentResponse.Subcategory) {
		return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(current), Applied: false}, nil
	}
	if category == "" {
		category = currentResponse.Category
	}
	if subcategory == "" {
		subcategory = currentResponse.Subcategory
	}
	categoryID, err := resolveIncidentCategory(ctx, s.client, tenantID, category, subcategory)
	if err != nil {
		return nil, err
	}
	updated, err := s.transitionIncidentWithCategory(ctx, current, tenantID, common.IncidentStatusTriaged, categoryID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", id)
		}
		return nil, fmt.Errorf("failed to categorize incident: %w", err)
	}
	s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "categorization", EventName: "事件分类",
		Description: fmt.Sprintf("事件已分类: %s/%s", category, subcategory), Status: "active", Severity: "info", Source: "system",
	}, tenantID)
	return &dto.IncidentMutationOutcome{Incident: s.toIncidentResponse(updated), Applied: true}, nil
}

func (s *IncidentService) workflowIncident(ctx context.Context, id, tenantID int) (*ent.Incident, error) {
	entity, err := s.client.Incident.Query().
		Where(incident.IDEQ(id), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("事件 %d 不存在或不属于当前租户", id)
		}
		return nil, fmt.Errorf("failed to get incident: %w", err)
	}
	return entity, nil
}

func (s *IncidentService) transitionIncident(ctx context.Context, current *ent.Incident, tenantID int, status string, mutate func(*ent.IncidentUpdateOne)) (*ent.Incident, error) {
	return s.transitionIncidentWithCategoryAndMutation(ctx, current, tenantID, status, nil, mutate)
}

func (s *IncidentService) transitionIncidentWithCategory(ctx context.Context, current *ent.Incident, tenantID int, status string, categoryID *int) (*ent.Incident, error) {
	return s.transitionIncidentWithCategoryAndMutation(ctx, current, tenantID, status, categoryID, nil)
}

func (s *IncidentService) transitionIncidentWithCategoryAndMutation(ctx context.Context, current *ent.Incident, tenantID int, status string, categoryID *int, mutate func(*ent.IncidentUpdateOne)) (*ent.Incident, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*ent.Incident, error) { _ = tx.Rollback(); return nil, cause }
	now := time.Now()
	workItemUpdate := tx.Ticket.UpdateOneID(current.WorkItemID).
		Where(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil(), ticket.VersionEQ(current.Edges.WorkItem.Version)).
		SetStatus(status).SetUpdatedAt(now).AddVersion(1)
	switch status {
	case common.IncidentStatusResolved:
		workItemUpdate.SetResolvedAt(now).ClearClosedAt()
	case common.IncidentStatusClosed:
		workItemUpdate.SetClosedAt(now)
	case common.IncidentStatusInProgress:
		workItemUpdate.ClearResolvedAt().ClearClosedAt()
	}
	if categoryID != nil {
		workItemUpdate.SetCategoryID(*categoryID)
	}
	workItem, err := workItemUpdate.Save(ctx)
	if err != nil {
		return fail(err)
	}
	update := tx.Incident.UpdateOneID(current.ID).Where(incidentTenantScope(tenantID))
	if mutate != nil {
		mutate(update)
	}
	updated, err := update.Save(ctx)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	updated.Edges.WorkItem = workItem
	updated.Edges.WorkItem.Edges.Category = current.Edges.WorkItem.Edges.Category
	return updated, nil
}

// ReopenIncident 将已解决或已关闭的事件重新流转到 in_progress
func (s *IncidentService) ReopenIncident(ctx context.Context, id, userID, tenantID int) error {
	incidentEntity, err := s.client.Incident.Query().
		Where(incident.IDEQ(id), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return err
	}

	if incidentEntity.Edges.WorkItem.Status != common.IncidentStatusResolved && incidentEntity.Edges.WorkItem.Status != common.IncidentStatusClosed {
		return fmt.Errorf("only resolved or closed incidents can be reopened")
	}

	_, err = s.transitionIncident(ctx, incidentEntity, tenantID, common.IncidentStatusInProgress, nil)
	if err != nil {
		return err
	}
	_, eventErr := s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "reopen", EventName: "事件重新打开",
		Description: fmt.Sprintf("事件由用户 %d 重新打开", userID), Status: "active", Severity: "info",
		UserID: &userID, Source: "user",
	}, tenantID)
	return eventErr
}

// EscalateToMajorIncident 将事件升级为重大事件（Major Incident）
// 写入影响评估信息，提升严重程度，并记录审计事件
func (s *IncidentService) EscalateToMajorIncident(ctx context.Context, id, userID, tenantID int, req *dto.EscalateMajorIncidentRequest) error {
	incidentEntity, err := s.client.Incident.Query().
		Where(incident.IDEQ(id), incidentTenantScope(tenantID)).
		WithWorkItem(withIncidentWorkItemProjection).Only(ctx)
	if err != nil {
		return err
	}

	if incidentEntity.IsMajorIncident {
		return fmt.Errorf("incident is already a major incident")
	}
	if incidentEntity.Edges.WorkItem.Status == common.IncidentStatusResolved || incidentEntity.Edges.WorkItem.Status == common.IncidentStatusClosed {
		return fmt.Errorf("resolved or closed incidents cannot be escalated to major incident")
	}

	now := time.Now()
	impactAnalysis := incidentEntity.ImpactAnalysis
	if impactAnalysis == nil {
		impactAnalysis = make(map[string]interface{})
	}
	impactAnalysis["majorIncident"] = map[string]interface{}{
		"impactScope":       req.ImpactScope,
		"businessImpact":    strings.TrimSpace(req.BusinessImpact),
		"communicationPlan": strings.TrimSpace(req.CommunicationPlan),
		"escalatedBy":       userID,
		"escalatedAt":       now,
	}

	_, err = s.transitionIncident(ctx, incidentEntity, tenantID, incidentEntity.Edges.WorkItem.Status, func(update *ent.IncidentUpdateOne) {
		update.
			SetIsMajorIncident(true).
			SetSeverity("critical").
			SetImpactAnalysis(impactAnalysis).
			SetEscalatedAt(now).
			AddEscalationLevel(1)
	})
	if err != nil {
		return err
	}
	_, eventErr := s.CreateIncidentEvent(ctx, &dto.CreateIncidentEventRequest{
		IncidentID: id, EventType: "major_incident_escalation", EventName: "升级为重大事件",
		Description: strings.TrimSpace(req.BusinessImpact), Status: "active", Severity: "critical",
		Data: map[string]interface{}{
			"impactScope":       req.ImpactScope,
			"communicationPlan": strings.TrimSpace(req.CommunicationPlan),
		},
		UserID: &userID, Source: "user",
	}, tenantID)
	return eventErr
}

func (s *IncidentService) GetIncidentStats(ctx context.Context, tenantID int) (*dto.IncidentStatsResponse, error) {
	s.logger.Infow("Getting incident stats", "tenant_id", tenantID)

	// 获取总事件数
	totalIncidents, err := s.client.Incident.Query().
		Where(incidentTenantScope(tenantID)).
		Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count total incidents", "error", err)
		return nil, fmt.Errorf("failed to count total incidents: %w", err)
	}

	// 获取开放事件数（new, in_progress）
	openIncidents, err := s.client.Incident.Query().
		Where(
			incidentTenantScope(tenantID, ticket.StatusIn("new", "acknowledged", "assigned", "triaged", "in_progress", "on_hold", "escalated")),
		).
		Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count open incidents", "error", err)
		return nil, fmt.Errorf("failed to count open incidents: %w", err)
	}

	// 获取关键事件数（severity = critical）
	criticalIncidents, err := s.client.Incident.Query().
		Where(incidentTenantScope(tenantID), incident.SeverityEQ("critical")).
		Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count critical incidents", "error", err)
		return nil, fmt.Errorf("failed to count critical incidents: %w", err)
	}

	// 获取主要事件数（使用 severity = critical 或 priority = high/urgent 作为主要事件）
	majorIncidents, err := s.client.Incident.Query().
		Where(
			incidentTenantScope(tenantID),
			incident.Or(
				incident.SeverityEQ("critical"),
				incident.HasWorkItemWith(ticket.PriorityIn("high", "urgent")),
			),
		).
		Count(ctx)
	if err != nil {
		s.logger.Errorw("Failed to count major incidents", "error", err)
		return nil, fmt.Errorf("failed to count major incidents: %w", err)
	}

	// 获取已解决的事件，计算平均解决时间
	resolvedIncidents, err := s.client.Incident.Query().
		Where(
			incidentTenantScope(tenantID, ticket.StatusEQ("resolved"), ticket.ResolvedAtNotNil()),
		).
		WithWorkItem().
		All(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get resolved incidents", "error", err)
		return nil, fmt.Errorf("failed to get resolved incidents: %w", err)
	}

	var totalResolutionTime float64
	var totalAcknowledgeTime float64
	resolvedCount := len(resolvedIncidents)
	acknowledgedCount := 0

	for _, inc := range resolvedIncidents {
		if !inc.Edges.WorkItem.ResolvedAt.IsZero() && !inc.DetectedAt.IsZero() {
			resolutionTime := inc.Edges.WorkItem.ResolvedAt.Sub(inc.DetectedAt).Hours()
			totalResolutionTime += resolutionTime
		}
		// 使用 detected_at 到 created_at 的时间差作为确认时间（简化实现）
		if !inc.DetectedAt.IsZero() && !inc.Edges.WorkItem.CreatedAt.IsZero() {
			acknowledgeTime := inc.DetectedAt.Sub(inc.Edges.WorkItem.CreatedAt).Hours()
			if acknowledgeTime > 0 {
				totalAcknowledgeTime += acknowledgeTime
				acknowledgedCount++
			}
		}
	}

	var avgResolutionTime float64
	if resolvedCount > 0 {
		avgResolutionTime = totalResolutionTime / float64(resolvedCount)
	}

	var mtta float64
	if acknowledgedCount > 0 {
		mtta = totalAcknowledgeTime / float64(acknowledgedCount)
	}

	var mttr float64 = avgResolutionTime

	return &dto.IncidentStatsResponse{
		TotalIncidents:    totalIncidents,
		OpenIncidents:     openIncidents,
		CriticalIncidents: criticalIncidents,
		MajorIncidents:    majorIncidents,
		AvgResolutionTime: avgResolutionTime,
		MTTA:              mtta,
		MTTR:              mttr,
	}, nil
}

// GetIncidentEvents 获取指定事件的活动记录
func (s *IncidentService) GetIncidentEvents(ctx context.Context, incidentID int, tenantID int) ([]dto.IncidentEventResponse, error) {
	s.logger.Infow("Getting incident events", "incident_id", incidentID, "tenant_id", tenantID)

	// 验证事件是否存在且属于该租户
	incident, err := s.client.Incident.Query().
		Where(
			incident.ID(incidentID),
			incidentTenantScope(tenantID),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found or not accessible")
		}
		return nil, fmt.Errorf("failed to verify incident: %w", err)
	}

	// 获取事件的活动记录
	events, err := s.client.IncidentEvent.Query().
		Where(
			incidentevent.IncidentIDEQ(incident.ID),
			incidentevent.TenantIDEQ(tenantID),
		).
		Order(ent.Desc("created_at")).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query incident events: %w", err)
	}

	responses := make([]dto.IncidentEventResponse, len(events))
	for i, event := range events {
		responses[i] = *s.toIncidentEventResponse(event)
	}

	return responses, nil
}

// GetIncidentAlerts 获取指定事件的告警
func (s *IncidentService) GetIncidentAlerts(ctx context.Context, incidentID int, tenantID int) ([]dto.IncidentAlertResponse, error) {
	s.logger.Infow("Getting incident alerts", "incident_id", incidentID, "tenant_id", tenantID)

	// 验证事件是否存在且属于该租户
	incident, err := s.client.Incident.Query().
		Where(
			incident.ID(incidentID),
			incidentTenantScope(tenantID),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found or not accessible")
		}
		return nil, fmt.Errorf("failed to verify incident: %w", err)
	}

	// 获取事件的告警
	alerts, err := s.client.IncidentAlert.Query().
		Where(
			incidentalert.IncidentIDEQ(incident.ID),
			incidentalert.TenantIDEQ(tenantID),
		).
		Order(ent.Desc("created_at")).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query incident alerts: %w", err)
	}

	responses := make([]dto.IncidentAlertResponse, len(alerts))
	for i, alert := range alerts {
		responses[i] = *s.toIncidentAlertResponse(alert)
	}

	return responses, nil
}

// GetIncidentMetrics 获取指定事件的指标
func (s *IncidentService) GetIncidentMetrics(ctx context.Context, incidentID int, tenantID int) ([]dto.IncidentMetricResponse, error) {
	s.logger.Infow("Getting incident metrics", "incident_id", incidentID, "tenant_id", tenantID)

	// 验证事件是否存在且属于该租户
	incident, err := s.client.Incident.Query().
		Where(
			incident.ID(incidentID),
			incidentTenantScope(tenantID),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found or not accessible")
		}
		return nil, fmt.Errorf("failed to verify incident: %w", err)
	}

	// 获取事件的指标
	metrics, err := s.client.IncidentMetric.Query().
		Where(
			incidentmetric.IncidentIDEQ(incident.ID),
			incidentmetric.TenantIDEQ(tenantID),
		).
		Order(ent.Desc("created_at")).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query incident metrics: %w", err)
	}

	responses := make([]dto.IncidentMetricResponse, len(metrics))
	for i, metric := range metrics {
		responses[i] = *s.toIncidentMetricResponse(metric)
	}

	return responses, nil
}

// GetWorkflowStatus 获取事件关联的流程状态
func (s *IncidentService) GetWorkflowStatus(ctx context.Context, incidentID int, tenantID int) (*dto.ProcessTriggerResponse, error) {
	inc, err := s.client.Incident.Query().
		Where(incident.IDEQ(incidentID), incidentTenantScope(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("incident not found")
		}
		return nil, fmt.Errorf("failed to get incident: %w", err)
	}
	// businessKey 的业务身份自 Wave 2 起是 WorkItem.ID，同 triggerWorkflowForIncident；
	// 缺失 work_item_id 表示开发数据违反领域不变量；fail closed 而不是用 Incident 自己
	// 的主键猜测旧格式 key。
	if inc.WorkItemID <= 0 {
		return nil, fmt.Errorf("事件 %d 违反 WorkItem 创建不变量：缺少 work_item_id，无法查询流程状态", incidentID)
	}
	businessKey := fmt.Sprintf("incident:%d", inc.WorkItemID)

	// 直接查询流程实例
	processInstance, err := s.client.ProcessInstance.Query().
		Where(
			processinstance.BusinessKey(businessKey),
			processinstance.TenantID(tenantID),
		).
		WithDefinition().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("未找到事件关联的流程实例")
		}
		return nil, fmt.Errorf("查询流程实例失败: %w", err)
	}

	processDefName := ""
	if processInstance.Edges.Definition != nil {
		processDefName = processInstance.Edges.Definition.Name
	}

	return &dto.ProcessTriggerResponse{
		ProcessInstanceID:     processInstance.ID,
		ProcessDefinitionKey:  processInstance.ProcessDefinitionKey,
		ProcessDefinitionName: processDefName,
		BusinessKey:           processInstance.BusinessKey,
		Status:                s.mapProcessStatus(processInstance.Status),
		CurrentActivityID:     processInstance.CurrentActivityID,
		CurrentActivityName:   processInstance.CurrentActivityName,
		StartTime:             processInstance.StartTime,
		EndTime:               &processInstance.EndTime,
	}, nil
}

// mapProcessStatus 映射流程状态
func (s *IncidentService) mapProcessStatus(status string) dto.ProcessStatus {
	switch status {
	case "running", "active":
		return dto.ProcessStatusRunning
	case "completed":
		return dto.ProcessStatusCompleted
	case "suspended":
		return dto.ProcessStatusSuspended
	case "terminated", "cancelled":
		return dto.ProcessStatusTerminated
	default:
		return dto.ProcessStatusPending
	}
}

func (s *IncidentService) SetWorkflowAssignmentBoundary(boundary workflowcallback.AssignmentBoundary) {
	s.workflowAssignment = boundary
}

func (s *IncidentService) AssignWorkItem(ctx context.Context, session *authorization.SessionSnapshot, item *ent.Ticket, target int, source string) (*ent.Ticket, error) {
	owner := *s
	owner.client = session.Tx.Client()
	current, err := owner.client.Incident.Query().Where(incident.WorkItemID(item.ID), incidentTenantScope(item.TenantID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	_, err = owner.assignIncident(ctx, current.ID, target, item.TenantID, false, &incidentAssignmentMutation{writer: NewWorkItemAssignmentWriter(session), command: assignment.Command{TenantID: item.TenantID, ActorID: session.Actor.ID, ActorTenantID: session.Actor.TenantID, Source: source}})
	if err != nil {
		return nil, err
	}
	return owner.client.Ticket.Get(ctx, item.ID)
}
