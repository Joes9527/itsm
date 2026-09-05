package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	feishuConn "itsm-backend/connector/builtin/feishu"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/feishuticketsync"
	entTicket "itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"

	"go.uber.org/zap"
)

type FeishuSyncService struct {
	client      *ent.Client
	logger      *zap.SugaredLogger
	creationApp creation.Application
}

func NewFeishuSyncService(
	client *ent.Client,
	logger *zap.SugaredLogger,
	app creation.Application,
) *FeishuSyncService {
	if app == nil {
		panic("intake application is required")
	}
	return &FeishuSyncService{client: client, logger: logger, creationApp: app}
}

func (s *FeishuSyncService) SyncTicketToFeishu(ctx context.Context, tenantID, ticketID int, fc *feishuConn.Feishu) (*dto.FeishuTicketSyncResponse, error) {
	if s.client == nil {
		return nil, fmt.Errorf("ent client not configured")
	}
	ticket, err := s.client.Ticket.Query().
		Where(entTicket.ID(ticketID), entTicket.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("ticket not found")
		}
		return nil, fmt.Errorf("query ticket: %w", err)
	}

	taskPayload, err := prepareFeishuTask(ctx, s.client, ticket)
	if err != nil {
		return nil, err
	}
	syncRecord, err := s.client.FeishuTicketSync.Query().
		Where(feishuticketsync.TenantID(tenantID), feishuticketsync.TicketID(ticketID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, fmt.Errorf("query feishu sync record: %w", err)
	}

	var task *feishuConn.FeishuTask
	if syncRecord != nil && syncRecord.FeishuTaskGUID != "" {
		task, err = fc.UpdateTask(ctx, syncRecord.FeishuTaskGUID, taskPayload)
		if err != nil {
			_ = s.markSyncFailed(ctx, syncRecord, err)
			return nil, fmt.Errorf("update feishu task: %w", err)
		}
		syncRecord, err = syncRecord.Update().
			SetFeishuTaskID(firstNonEmptyString(task.GUID, syncRecord.FeishuTaskID)).
			SetFeishuTaskGUID(firstNonEmptyString(task.GUID, syncRecord.FeishuTaskGUID)).
			SetSyncStatus("synced").
			SetLastSyncDirection("itsm_to_feishu").
			SetLastSyncedAt(time.Now()).
			ClearErrorMessage().
			Save(ctx)
	} else {
		task, err = fc.CreateTask(ctx, taskPayload)
		if err != nil {
			return nil, fmt.Errorf("create feishu task: %w", err)
		}
		if task == nil || task.GUID == "" {
			return nil, fmt.Errorf("create feishu task: empty task guid")
		}
		syncRecord, err = s.client.FeishuTicketSync.Create().
			SetTenantID(tenantID).
			SetTicketID(ticket.ID).
			SetFeishuTaskID(task.GUID).
			SetFeishuTaskGUID(task.GUID).
			SetSyncStatus("synced").
			SetLastSyncDirection("itsm_to_feishu").
			SetLastSyncedAt(time.Now()).
			Save(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("save feishu sync record: %w", err)
	}
	return toFeishuSyncResponse(syncRecord, ticket, task), nil
}

func (s *FeishuSyncService) HandleTaskEvent(ctx context.Context, tenantID int, fc *feishuConn.Feishu, eventType string, eventData map[string]interface{}) (*dto.FeishuWebhookResponse, error) {
	taskGUID := extractFeishuTaskGUID(eventData)
	if taskGUID == "" {
		return nil, fmt.Errorf("missing feishu task guid")
	}

	if eventType == "task.deleted" {
		resp, err := s.markTaskDeleted(ctx, tenantID, taskGUID)
		if err != nil {
			return nil, err
		}
		return &dto.FeishuWebhookResponse{EventType: eventType, Action: "ticket_closed", Sync: resp}, nil
	}

	task, err := fc.GetTask(ctx, taskGUID)
	if err != nil {
		return nil, fmt.Errorf("get feishu task: %w", err)
	}
	resp, action, err := s.SyncFeishuTaskToTicket(ctx, tenantID, task)
	if err != nil {
		return nil, err
	}
	return &dto.FeishuWebhookResponse{EventType: eventType, Action: action, Sync: resp}, nil
}

func (s *FeishuSyncService) SyncFeishuTaskToTicket(ctx context.Context, tenantID int, task *feishuConn.FeishuTask) (*dto.FeishuTicketSyncResponse, string, error) {
	if task == nil || task.GUID == "" || task.CreatorID == "" {
		return nil, "", creation.NewDomainValidationFailed("Feishu task GUID and mapped creator are required", nil)
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	actor, err := tx.User.Query().Where(user.TenantIDEQ(tenantID), user.FeishuOpenIDEQ(task.CreatorID), user.ActiveEQ(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, "", creation.NewAuthenticationRequired("Feishu creator has no active tenant mapping", err)
	}
	if err != nil {
		return nil, "", err
	}
	identity := creation.Identity{TenantID: tenantID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "feishu", Provider: "feishu"}
	command := creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", IdempotencyKey: "feishu:" + task.GUID + ":create", Title: stripTicketNumberPrefix(task.Name), Description: task.Description, Priority: mapFeishuPriorityToTicket(task.Priority), SourceReference: &creation.SourceReference{Provider: "feishu", EventID: task.GUID}, FeishuTask: &creation.FeishuTaskInput{TaskGUID: task.GUID, CreatorOpenID: task.CreatorID, Status: task.Status, Completed: task.Completed}}
	if err := authorization.AuthorizeWorkItemCreation(ctx, tx, identity, command); err != nil {
		return nil, "", err
	}
	record, err := tx.FeishuTicketSync.Query().Where(feishuticketsync.TenantIDEQ(tenantID), feishuticketsync.FeishuTaskIDEQ(task.GUID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, "", err
	}
	if record != nil {
		item, err := s.updateTicketFromFeishuTask(ctx, tx, tenantID, record.TicketID, task)
		if err != nil {
			return nil, "", err
		}
		record, err = record.Update().SetSyncStatus("synced").SetLastSyncDirection("feishu_to_itsm").SetLastSyncedAt(time.Now()).ClearErrorMessage().Save(ctx)
		if err != nil {
			return nil, "", err
		}
		if err := tx.Commit(); err != nil {
			return nil, "", err
		}
		return toFeishuSyncResponse(record, item, task), "updated", nil
	}
	if err := tx.Rollback(); err != nil {
		return nil, "", err
	}
	result, err := s.creationApp.Create(ctx, identity, command)
	if err != nil {
		return nil, "", err
	}
	record, err = s.client.FeishuTicketSync.Query().Where(feishuticketsync.TenantIDEQ(tenantID), feishuticketsync.TicketIDEQ(result.WorkItemID), feishuticketsync.FeishuTaskIDEQ(task.GUID)).Only(ctx)
	if err != nil {
		return nil, "", err
	}
	item, err := s.client.Ticket.Query().Where(entTicket.IDEQ(result.WorkItemID), entTicket.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return nil, "", err
	}
	return toFeishuSyncResponse(record, item, task), "created", nil
}

func prepareFeishuTask(ctx context.Context, client *ent.Client, ticket *ent.Ticket) (*feishuConn.FeishuTask, error) {
	task := &feishuConn.FeishuTask{
		Name:        fmt.Sprintf("%s %s", ticket.TicketNumber, ticket.Title),
		Description: ticket.Description,
		StartTime:   ticket.CreatedAt.Unix(),
		Status:      mapTicketStatusToFeishu(ticket.Status),
		Priority:    mapTicketPriorityToFeishu(ticket.Priority),
		Extra: map[string]interface{}{
			"itsm_ticket_id":     ticket.ID,
			"itsm_ticket_number": ticket.TicketNumber,
			"tenant_id":          ticket.TenantID,
		},
	}
	if ticket.AssigneeID > 0 {
		assignee, err := client.User.Query().Where(user.IDEQ(ticket.AssigneeID), user.TenantIDEQ(ticket.TenantID), user.ActiveEQ(true)).Only(ctx)
		if err != nil {
			return nil, err
		}
		if assignee.FeishuOpenID != "" {
			task.Assignees = []string{assignee.FeishuOpenID}
		}
	}
	return task, nil
}

func (s *FeishuSyncService) updateTicketFromFeishuTask(ctx context.Context, tx *ent.Tx, tenantID, ticketID int, task *feishuConn.FeishuTask) (*ent.Ticket, error) {
	current, err := tx.Ticket.Query().
		Where(entTicket.ID(ticketID), entTicket.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("query ticket for feishu sync: %w", err)
	}
	if current.RecordClass != "generic" {
		return nil, creation.NewDomainValidationFailed("professional WorkItem updates require the owning domain", nil)
	}
	update := tx.Ticket.UpdateOneID(ticketID).
		Where(entTicket.TenantID(tenantID)).
		SetVersion(current.Version + 1)
	if task.Name != "" {
		update.SetTitle(stripTicketNumberPrefix(task.Name))
	}
	update.SetDescription(task.Description)
	if task.Priority != "" {
		update.SetPriority(mapFeishuPriorityToTicket(task.Priority))
	}
	if task.Status != "" || task.Completed {
		update.SetStatus(mapFeishuStatusToTicket(task.Status, task.Completed))
	}
	return update.Save(ctx)
}

func writeFeishuCreationSource(ctx context.Context, tx *ent.Tx, item *ent.Ticket, source *creation.FeishuTaskInput) error {
	_, err := tx.FeishuTicketSync.Create().SetTenantID(item.TenantID).SetTicketID(item.ID).SetFeishuTaskID(source.TaskGUID).SetFeishuTaskGUID(source.TaskGUID).SetSyncStatus("synced").SetLastSyncDirection("feishu_to_itsm").SetLastSyncedAt(item.CreatedAt).Save(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not persist Feishu source mapping", err)
	}
	return nil
}

func (s *FeishuSyncService) markTaskDeleted(ctx context.Context, tenantID int, taskGUID string) (*dto.FeishuTicketSyncResponse, error) {
	syncRecord, err := s.client.FeishuTicketSync.Query().
		Where(feishuticketsync.TenantID(tenantID), feishuticketsync.FeishuTaskID(taskGUID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("query feishu sync record: %w", err)
	}
	ticket, err := s.client.Ticket.UpdateOneID(syncRecord.TicketID).
		Where(entTicket.TenantID(tenantID)).
		SetStatus("closed").
		Save(ctx)
	if err != nil {
		_ = s.markSyncFailed(ctx, syncRecord, err)
		return nil, fmt.Errorf("close ticket after feishu task deletion: %w", err)
	}
	syncRecord, err = syncRecord.Update().
		SetSyncStatus("deleted").
		SetLastSyncDirection("feishu_to_itsm").
		SetLastSyncedAt(time.Now()).
		ClearErrorMessage().
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("update feishu sync deletion status: %w", err)
	}
	return toFeishuSyncResponse(syncRecord, ticket, &feishuConn.FeishuTask{GUID: taskGUID}), nil
}

func (s *FeishuSyncService) markSyncFailed(ctx context.Context, syncRecord *ent.FeishuTicketSync, err error) error {
	_, saveErr := syncRecord.Update().
		SetSyncStatus("failed").
		SetErrorMessage(err.Error()).
		Save(ctx)
	return saveErr
}

func mapTicketPriorityToFeishu(priority string) string {
	switch strings.ToLower(priority) {
	case "low":
		return "low"
	case "high":
		return "high"
	case "critical", "urgent":
		return "urgent"
	default:
		return "medium"
	}
}

func mapFeishuPriorityToTicket(priority string) string {
	switch strings.ToLower(priority) {
	case "low":
		return "low"
	case "high":
		return "high"
	case "urgent":
		return "critical"
	default:
		return "medium"
	}
}

func mapTicketStatusToFeishu(status string) string {
	switch strings.ToLower(status) {
	case "in_progress":
		return "in_progress"
	case "resolved", "closed":
		return "completed"
	case "cancelled", "canceled":
		return "canceled"
	default:
		return "not_started"
	}
}

func mapFeishuStatusToTicket(status string, completed bool) string {
	if completed {
		return "resolved"
	}
	switch strings.ToLower(status) {
	case "in_progress":
		return "in_progress"
	case "completed":
		return "resolved"
	case "canceled", "cancelled":
		return "closed"
	default:
		return "open"
	}
}

func extractFeishuTaskGUID(event map[string]interface{}) string {
	for _, key := range []string{"task_guid", "guid", "task_id"} {
		if v, ok := event[key].(string); ok && v != "" {
			return v
		}
	}
	for _, key := range []string{"task", "task_data"} {
		if nested, ok := event[key].(map[string]interface{}); ok {
			if guid := extractFeishuTaskGUID(nested); guid != "" {
				return guid
			}
		}
	}
	return ""
}

func stripTicketNumberPrefix(name string) string {
	parts := strings.SplitN(name, " ", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[0], "TKT-") {
		return parts[1]
	}
	return name
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func toFeishuSyncResponse(syncRecord *ent.FeishuTicketSync, ticket *ent.Ticket, task *feishuConn.FeishuTask) *dto.FeishuTicketSyncResponse {
	if syncRecord == nil {
		return nil
	}
	resp := &dto.FeishuTicketSyncResponse{
		ID:                syncRecord.ID,
		TenantID:          syncRecord.TenantID,
		TicketID:          syncRecord.TicketID,
		FeishuTaskID:      syncRecord.FeishuTaskID,
		FeishuTaskGUID:    syncRecord.FeishuTaskGUID,
		SyncStatus:        syncRecord.SyncStatus,
		LastSyncDirection: syncRecord.LastSyncDirection,
		ErrorMessage:      syncRecord.ErrorMessage,
	}
	if !syncRecord.LastSyncedAt.IsZero() {
		resp.LastSyncedAt = &syncRecord.LastSyncedAt
	}
	if ticket != nil {
		resp.TicketNumber = ticket.TicketNumber
	}
	if task != nil {
		resp.Task = &dto.FeishuTaskResponse{
			GUID:        task.GUID,
			Name:        task.Name,
			Description: task.Description,
			Status:      task.Status,
			Priority:    task.Priority,
			Completed:   task.Completed,
			CreatorID:   task.CreatorID,
			Assignees:   task.Assignees,
			Extra:       task.Extra,
		}
	}
	return resp
}
