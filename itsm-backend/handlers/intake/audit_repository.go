package intake

import (
	"context"
	"fmt"

	"itsm-backend/ent"
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
		return NewInternalFailure("intake transaction is required", nil)
	}
	if input.TenantID <= 0 || input.UserID <= 0 || input.WorkItemID <= 0 || input.RequestID == "" || input.Path == "" || input.Method == "" {
		return NewInvalidCommand("invalid intake audit evidence", FieldError{Field: "audit", Message: "required audit context is missing"}, nil)
	}
	_, err := tx.AuditLog.Create().
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
		return NewInfrastructureUnavailable("could not record intake audit", err)
	}
	return nil
}
