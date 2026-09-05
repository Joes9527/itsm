package intake

import (
	"context"
	"fmt"
	"itsm-backend/handlers/common/workitemcreation"

	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
)

type CreatedAuditInput struct {
	TenantID   int
	UserID     int
	WorkItemID int
	RequestID  string
	Path       string
	Method     string
	StatusCode int
}

type AuditRepository struct{}

func NewAuditRepository() *AuditRepository {
	return &AuditRepository{}
}

func (r *AuditRepository) RecordCreated(ctx context.Context, tx *ent.Tx, input CreatedAuditInput) error {
	if tx == nil {
		return workitemcreation.NewInternalFailure("intake transaction is required", nil)
	}
	if input.TenantID <= 0 || input.UserID <= 0 || input.WorkItemID <= 0 || input.RequestID == "" || input.Path == "" || input.Method == "" {
		return workitemcreation.NewInvalidCommand("invalid intake audit evidence", workitemcreation.FieldError{Field: "audit", Message: "required audit context is missing"}, nil)
	}
	ok, err := tx.Ticket.Query().Where(ticket.IDEQ(input.WorkItemID), ticket.TenantIDEQ(input.TenantID)).Exist(ctx)
	if err != nil || !ok {
		return workitemcreation.NewReferenceNotFound("audit work item outside tenant", err)
	}
	ok, err = tx.User.Query().Where(user.IDEQ(input.UserID), user.TenantIDEQ(input.TenantID)).Exist(ctx)
	if err != nil || !ok {
		return workitemcreation.NewReferenceNotFound("audit actor outside tenant", err)
	}
	_, err = tx.AuditLog.Create().
		SetTenantID(input.TenantID).
		SetUserID(input.UserID).
		SetRequestID(input.RequestID).
		SetResource(fmt.Sprintf("work_item:%d", input.WorkItemID)).
		SetAction("intake.created").
		SetPath(input.Path).
		SetMethod(input.Method).
		SetStatusCode(input.StatusCode).
		Save(ctx)
	if err != nil {
		return workitemcreation.NewInfrastructureUnavailable("could not record intake audit", err)
	}
	return nil
}
