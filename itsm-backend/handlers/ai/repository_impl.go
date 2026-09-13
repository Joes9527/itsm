package ai

import (
	"context"
	"errors"
	"fmt"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/conversation"
	"itsm-backend/ent/message"
	"itsm-backend/ent/rootcauseanalysis"
	"itsm-backend/ent/toolinvocation"
)

type EntRepository struct {
	execution *database.ExecutionPolicy
	client    *ent.Client
}

func NewEntRepository(client *ent.Client, execution *database.ExecutionPolicy) *EntRepository {
	if client == nil || execution == nil {
		panic("AI repository requires tenant client and execution policy")
	}
	return &EntRepository{client: client, execution: execution}
}

// Conversations

func toConversationDomain(e *ent.Conversation) *Conversation {
	if e == nil {
		return nil
	}
	return &Conversation{
		ID:        e.ID,
		Title:     e.Title,
		UserID:    e.UserID,
		TenantID:  e.TenantID,
		CreatedAt: e.CreatedAt,
	}
}

func (r *EntRepository) CreateConversation(ctx context.Context, c *Conversation) (*Conversation, error) {
	e, err := r.client.Conversation.Create().
		SetTitle(c.Title).
		SetUserID(c.UserID).
		SetTenantID(c.TenantID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toConversationDomain(e), nil
}

func (r *EntRepository) GetConversation(ctx context.Context, id int, tenantID int) (*Conversation, error) {
	e, err := r.client.Conversation.Query().
		Where(conversation.ID(id), conversation.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return toConversationDomain(e), nil
}

func (r *EntRepository) ListConversations(ctx context.Context, tenantID int, userID int) ([]*Conversation, error) {
	es, err := r.client.Conversation.Query().
		Where(conversation.TenantID(tenantID), conversation.UserID(userID)).
		Order(ent.Desc(conversation.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	var res []*Conversation
	for _, e := range es {
		res = append(res, toConversationDomain(e))
	}
	return res, nil
}

// Messages

func toMessageDomain(e *ent.Message) *Message {
	if e == nil {
		return nil
	}
	return &Message{
		ID:             e.ID,
		ConversationID: e.ConversationID,
		Role:           e.Role,
		Content:        e.Content,
		RequestID:      e.RequestID,
		CreatedAt:      e.CreatedAt,
	}
}

func (r *EntRepository) CreateMessage(ctx context.Context, m *Message) (*Message, error) {
	e, err := r.client.Message.Create().
		SetConversationID(m.ConversationID).
		SetRole(m.Role).
		SetContent(m.Content).
		SetRequestID(m.RequestID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toMessageDomain(e), nil
}

func (r *EntRepository) GetMessages(ctx context.Context, conversationID int) ([]*Message, error) {
	es, err := r.client.Message.Query().
		Where(message.ConversationID(conversationID)).
		Order(ent.Asc(message.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	var res []*Message
	for _, e := range es {
		res = append(res, toMessageDomain(e))
	}
	return res, nil
}

// Tool Invocations

func toToolInvocationDomain(e *ent.ToolInvocation) *ToolInvocation {
	if e == nil {
		return nil
	}
	var approvedAt *time.Time
	if !e.ApprovedAt.IsZero() {
		t := e.ApprovedAt
		approvedAt = &t
	}
	return &ToolInvocation{
		ID:               e.ID,
		TenantID:         e.TenantID,
		ConversationID:   e.ConversationID,
		ToolName:         e.ToolName,
		Arguments:        e.Arguments,
		Status:           e.Status,
		Result:           e.Result,
		Error:            e.Error,
		NeedsApproval:    e.NeedsApproval,
		ApprovalState:    e.ApprovalState,
		ApprovedBy:       e.ApprovedBy,
		ApprovalReason:   e.ApprovalReason,
		ApprovedAt:       approvedAt,
		RequestID:        e.RequestID,
		CreatedAt:        e.CreatedAt,
		UserID:           e.UserID,
		PermissionCheck:  e.PermissionCheck,
		PermissionReason: e.PermissionReason,
		RoleSnapshot:     e.RoleSnapshot,
	}
}

func (r *EntRepository) CreateToolInvocation(ctx context.Context, i *ToolInvocation) (*ToolInvocation, error) {
	if i == nil || i.TenantID <= 0 || ctx == nil || tenantctx.IsSystemBypass(ctx) {
		return nil, fmt.Errorf("explicit tool invocation tenant required")
	}
	if tenantID, ok := tenantctx.TenantID(ctx); ok && tenantID != i.TenantID {
		return nil, fmt.Errorf("tool invocation tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, i.TenantID)
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := r.execution.BindEnt(ctx, tx, i.TenantID); err != nil {
		return nil, err
	}
	e, err := tx.ToolInvocation.Create().
		SetTenantID(i.TenantID).
		SetToolName(i.ToolName).
		SetArguments(i.Arguments).
		SetStatus(i.Status).
		SetNeedsApproval(i.NeedsApproval).
		SetApprovalState(i.ApprovalState).
		SetRequestID(i.RequestID).
		SetUserID(i.UserID).
		SetPermissionCheck(i.PermissionCheck).
		SetPermissionReason(i.PermissionReason).
		SetRoleSnapshot(i.RoleSnapshot).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toToolInvocationDomain(e), nil
}

func (r *EntRepository) GetToolInvocation(ctx context.Context, id int, tenantID int) (*ToolInvocation, error) {
	e, err := r.client.ToolInvocation.Query().
		Where(toolinvocation.ID(id), toolinvocation.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return toToolInvocationDomain(e), nil
}

var ErrToolApprovalConflict = errors.New("tool approval decision conflicts with current state")

// DecideToolInvocation is the sole transactional write of a tool approval.
func (r *EntRepository) DecideToolInvocation(ctx context.Context, id, tenantID, actorID int, approve bool, reason string) (*ToolInvocation, error) {
	if ctx == nil || id <= 0 || tenantID <= 0 || actorID <= 0 || tenantctx.IsSystemBypass(ctx) {
		return nil, fmt.Errorf("explicit tool approval identity required")
	}
	if current, ok := tenantctx.TenantID(ctx); ok && current != tenantID {
		return nil, fmt.Errorf("tool approval tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = r.execution.BindEnt(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	if err = r.execution.RequireEntToolInvocation(ctx, tx, tenantID, id); err != nil {
		return nil, err
	}
	actor, err := tx.User.Query().Where(user.IDEQ(actorID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, creation.NewPermissionDenied("active tool approver required", err)
	}
	if err != nil {
		return nil, err
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: tenantID, ActorID: actorID, RequesterID: actorID, Role: actor.Role}, "ai", "write"); err != nil {
		return nil, err
	}
	current, err := tx.ToolInvocation.Query().Where(toolinvocation.IDEQ(id), toolinvocation.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	target := "rejected"
	if approve {
		target = "approved"
	}
	if !current.NeedsApproval || current.DryRun {
		return nil, ErrToolApprovalConflict
	}
	if current.ApprovalState == target && current.ApprovedBy == actorID && current.ApprovalReason == reason && !current.ApprovedAt.IsZero() {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return toToolInvocationDomain(current), nil
	}
	if current.ApprovalState != "pending" || current.Status != "pending" {
		return nil, ErrToolApprovalConflict
	}
	count, err := tx.ToolInvocation.Update().Where(toolinvocation.IDEQ(id), toolinvocation.TenantIDEQ(tenantID), toolinvocation.ApprovalStateEQ("pending"), toolinvocation.StatusEQ("pending"), toolinvocation.NeedsApprovalEQ(true), toolinvocation.DryRunEQ(false)).SetApprovalState(target).SetApprovalReason(reason).SetApprovedBy(actorID).SetApprovedAt(time.Now().UTC()).Save(ctx)
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrToolApprovalConflict
	}
	saved, err := tx.ToolInvocation.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return toToolInvocationDomain(saved), nil
}

// Root Cause Analysis

func toRCADomain(e *ent.RootCauseAnalysis) *RootCauseAnalysis {
	if e == nil {
		return nil
	}
	return &RootCauseAnalysis{
		ID:              e.ID,
		TicketID:        e.TicketID,
		TicketNumber:    e.TicketNumber,
		TicketTitle:     e.TicketTitle,
		AnalysisDate:    e.AnalysisDate,
		RootCauses:      e.RootCauses,
		AnalysisSummary: e.AnalysisSummary,
		ConfidenceScore: e.ConfidenceScore,
		AnalysisMethod:  e.AnalysisMethod,
		TenantID:        e.TenantID,
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

func (r *EntRepository) CreateRCA(ctx context.Context, rca *RootCauseAnalysis) (*RootCauseAnalysis, error) {
	e, err := r.client.RootCauseAnalysis.Create().
		SetTicketID(rca.TicketID).
		SetTicketNumber(rca.TicketNumber).
		SetTicketTitle(rca.TicketTitle).
		SetAnalysisDate(rca.AnalysisDate).
		SetRootCauses(rca.RootCauses).
		SetAnalysisSummary(rca.AnalysisSummary).
		SetConfidenceScore(rca.ConfidenceScore).
		SetAnalysisMethod(rca.AnalysisMethod).
		SetTenantID(rca.TenantID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toRCADomain(e), nil
}

func (r *EntRepository) GetRCAByTicket(ctx context.Context, ticketID int, tenantID int) (*RootCauseAnalysis, error) {
	e, err := r.client.RootCauseAnalysis.Query().
		Where(rootcauseanalysis.TicketID(ticketID), rootcauseanalysis.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return toRCADomain(e), nil
}

func (r *EntRepository) UpdateRCA(ctx context.Context, rca *RootCauseAnalysis) (*RootCauseAnalysis, error) {
	e, err := r.client.RootCauseAnalysis.UpdateOneID(rca.ID).
		SetRootCauses(rca.RootCauses).
		SetAnalysisSummary(rca.AnalysisSummary).
		SetConfidenceScore(rca.ConfidenceScore).
		SetAnalysisMethod(rca.AnalysisMethod).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toRCADomain(e), nil
}
