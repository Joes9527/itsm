package intake

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"itsm-backend/handlers/common/workitemcreation"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/ticket"

	entsql "entgo.io/ent/dialect/sql"
)

const intakeCreateOperation = "create_work_item"

var errIdempotencyOwnerInProgress = errors.New("idempotency owner is still processing")

type ClaimOutcome string

const (
	ClaimInserted ClaimOutcome = "inserted"
	ClaimReplay   ClaimOutcome = "replay"
)

type IdempotencyRepository struct {
	now func() time.Time
}

func NewIdempotencyRepository() *IdempotencyRepository {
	return &IdempotencyRepository{now: time.Now}
}

func (r *IdempotencyRepository) Claim(
	ctx context.Context,
	tx *ent.Tx,
	identity workitemcreation.Identity,
	key string,
	digest string,
	digestVersion string,
) (*ent.IntakeRequest, ClaimOutcome, error) {
	if tx == nil {
		return nil, "", workitemcreation.NewInternalFailure("intake transaction is required", nil)
	}
	if identity.TenantID <= 0 || identity.ActorID <= 0 || identity.ActorTenantID <= 0 || identity.Channel == "" || key == "" || digest == "" || digestVersion == "" {
		return nil, "", workitemcreation.NewInvalidCommand("invalid idempotency claim", workitemcreation.FieldError{Field: "idempotencyKey", Message: "claim scope and digest are required"}, nil)
	}

	id, err := tx.IntakeRequest.Create().
		SetTenantID(identity.TenantID).
		SetActorID(identity.ActorID).
		SetActorTenantID(identity.ActorTenantID).
		SetRequesterID(identity.RequesterID).
		SetChannel(identity.Channel).
		SetOperation(intakeCreateOperation).
		SetIdempotencyKey(key).
		SetRequestDigest(digest).
		SetDigestVersion(digestVersion).
		SetStatus("processing").
		SetCreatedAt(r.now().UTC()).
		OnConflict(
			entsql.ConflictColumns(
				intakerequest.FieldTenantID,
				intakerequest.FieldActorID,
				intakerequest.FieldChannel,
				intakerequest.FieldOperation,
				intakerequest.FieldIdempotencyKey,
			),
			entsql.DoNothing(),
		).
		ID(ctx)
	inserted := err == nil && id > 0
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !ent.IsNotFound(err) {
		return nil, "", workitemcreation.NewInfrastructureUnavailable("could not claim idempotency key", err)
	}

	receipt, loadErr := tx.IntakeRequest.Query().
		Where(
			intakerequest.TenantIDEQ(identity.TenantID),
			intakerequest.ActorIDEQ(identity.ActorID),
			intakerequest.ChannelEQ(identity.Channel),
			intakerequest.OperationEQ(intakeCreateOperation),
			intakerequest.IdempotencyKeyEQ(key),
		).
		Only(ctx)
	if ent.IsNotFound(loadErr) || ent.IsNotSingular(loadErr) {
		return nil, "", workitemcreation.NewInternalFailure("claimed intake receipt is missing or ambiguous", loadErr)
	}
	if loadErr != nil {
		return nil, "", workitemcreation.NewInfrastructureUnavailable("could not load idempotency claim", loadErr)
	}
	if receipt.ActorTenantID != identity.ActorTenantID || receipt.RequesterID != identity.RequesterID || receipt.RequestDigest != digest || receipt.DigestVersion != digestVersion {
		return nil, "", workitemcreation.NewIdempotencyConflict("idempotency key was already used for a different command", nil)
	}
	if receipt.Status == "completed" {
		if receipt.WorkItemID == nil || *receipt.WorkItemID <= 0 {
			return nil, "", workitemcreation.NewInternalFailure("completed intake receipt is missing its work item", nil)
		}
		return receipt, ClaimReplay, nil
	}
	if receipt.Status != "processing" {
		return nil, "", workitemcreation.NewInternalFailure(fmt.Sprintf("unsupported intake receipt status %q", receipt.Status), nil)
	}
	if !inserted {
		return nil, "", workitemcreation.NewInfrastructureUnavailable("idempotency claim is still processing", errIdempotencyOwnerInProgress)
	}
	return receipt, ClaimInserted, nil
}

func (r *IdempotencyRepository) Complete(ctx context.Context, tx *ent.Tx, tenantID, receiptID, workItemID int) error {
	if tx == nil {
		return workitemcreation.NewInternalFailure("intake transaction is required", nil)
	}
	if workItemID <= 0 {
		return workitemcreation.NewInternalFailure("completed intake receipt requires a work item", nil)
	}
	ok, err := tx.Ticket.Query().Where(ticket.IDEQ(workItemID), ticket.TenantIDEQ(tenantID)).Exist(ctx)
	if err != nil {
		return workitemcreation.NewInfrastructureUnavailable("could not query intake reference", err)
	}
	if !ok {
		return workitemcreation.NewReferenceNotFound("receipt work item is outside tenant", err)
	}
	updated, err := tx.IntakeRequest.Update().
		Where(
			intakerequest.IDEQ(receiptID),
			intakerequest.TenantIDEQ(tenantID),
			intakerequest.StatusEQ("processing"),
		).
		SetStatus("completed").
		SetWorkItemID(workItemID).
		SetCompletedAt(r.now().UTC()).
		Save(ctx)
	if err != nil {
		return workitemcreation.NewInfrastructureUnavailable("could not complete intake receipt", err)
	}
	if updated != 1 {
		return workitemcreation.NewReferenceNotFound("intake receipt was not found", nil)
	}
	return nil
}

func (r *IdempotencyRepository) LoadCompleted(ctx context.Context, tx *ent.Tx, identity workitemcreation.Identity, key string) (*ent.IntakeRequest, error) {
	if tx == nil {
		return nil, workitemcreation.NewInternalFailure("intake transaction is required", nil)
	}
	receipt, err := tx.IntakeRequest.Query().
		Where(
			intakerequest.TenantIDEQ(identity.TenantID),
			intakerequest.ActorIDEQ(identity.ActorID),
			intakerequest.ChannelEQ(identity.Channel),
			intakerequest.OperationEQ(intakeCreateOperation),
			intakerequest.IdempotencyKeyEQ(key),
			intakerequest.StatusEQ("completed"),
		).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, workitemcreation.NewReferenceNotFound("completed intake receipt was not found", err)
	}
	if ent.IsNotSingular(err) {
		return nil, workitemcreation.NewInternalFailure("completed intake receipt is ambiguous", err)
	}
	if err != nil {
		return nil, workitemcreation.NewInfrastructureUnavailable("could not load completed intake receipt", err)
	}
	if receipt.WorkItemID == nil || *receipt.WorkItemID <= 0 {
		return nil, workitemcreation.NewInternalFailure("completed intake receipt is missing its work item", nil)
	}
	return receipt, nil
}
