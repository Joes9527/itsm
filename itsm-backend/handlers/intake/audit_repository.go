package intake

import (
	"context"
	"encoding/json"
	"fmt"

	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/common/workitemcreation"
)

type CreatedAuditInput struct {
	Authorization   *authorization.CreationAuthorization
	IntakeRequestID int
	TenantID        int
	UserID          int
	WorkItemID      int
	RequestID       string
	Path            string
	Method          string
	StatusCode      int
}

type AuditRepository struct{}

func NewAuditRepository() *AuditRepository {
	return &AuditRepository{}
}

func (r *AuditRepository) RecordCreated(ctx context.Context, tx *ent.Tx, input CreatedAuditInput) error {
	if tx == nil {
		return workitemcreation.NewInternalFailure("intake transaction is required", nil)
	}
	if input.TenantID <= 0 || input.UserID <= 0 || input.WorkItemID <= 0 || input.IntakeRequestID <= 0 || input.RequestID == "" || input.Path == "" || input.Method == "" {
		return workitemcreation.NewInvalidCommand("invalid intake audit evidence", workitemcreation.FieldError{Field: "audit", Message: "required audit context is missing"}, nil)
	}
	identity := input.Authorization.Identity()
	if identity.ActorID != input.UserID || identity.TenantID != input.TenantID {
		return workitemcreation.NewPermissionDenied("audit identity differs from authorization", nil)
	}
	if err := input.Authorization.Validate(tx, identity); err != nil {
		return err
	}
	ok, err := tx.Ticket.Query().Where(ticket.IDEQ(input.WorkItemID), ticket.TenantIDEQ(input.TenantID)).Exist(ctx)
	if err != nil {
		return workitemcreation.NewInfrastructureUnavailable("could not query intake reference", err)
	}
	if !ok {
		return workitemcreation.NewReferenceNotFound("audit work item outside tenant", err)
	}
	evidence, err := json.Marshal(workitemcreation.ActorProvenance{ActorUserID: identity.ActorID, ActorTenantID: identity.ActorTenantID, TargetTenantID: identity.TenantID, IntakeRequestID: input.IntakeRequestID, WorkItemID: input.WorkItemID})
	if err != nil {
		return err
	}

	_, err = tx.AuditLog.Create().
		SetTenantID(input.TenantID).
		SetUserID(input.UserID).
		SetRequestID(input.RequestID).
		SetResource(fmt.Sprintf("work_item:%d", input.WorkItemID)).
		SetAction("intake.created").
		SetRequestBody(string(evidence)).
		SetPath(input.Path).
		SetMethod(input.Method).
		SetStatusCode(input.StatusCode).
		Save(ctx)
	if err != nil {
		return workitemcreation.NewInfrastructureUnavailable("could not record intake audit", err)
	}
	return nil
}
