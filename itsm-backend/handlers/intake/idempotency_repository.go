package intake

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"

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
	identity Identity,
	key string,
	digest string,
	digestVersion string,
) (*ent.IntakeRequest, ClaimOutcome, error) {
	if tx == nil {
		return nil, "", NewInternalFailure("intake transaction is required", nil)
	}
	if identity.TenantID <= 0 || identity.ActorID <= 0 || identity.Channel == "" || key == "" || digest == "" || digestVersion == "" {
		return nil, "", NewInvalidCommand("invalid idempotency claim", FieldError{Field: "idempotencyKey", Message: "claim scope and digest are required"}, nil)
	}

	id, err := tx.IntakeRequest.Create().
		SetTenantID(identity.TenantID).
		SetActorID(identity.ActorID).
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
		return nil, "", NewInfrastructureUnavailable("could not claim idempotency key", err)
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
	if loadErr != nil {
		return nil, "", NewInfrastructureUnavailable("could not load idempotency claim", loadErr)
	}
	if receipt.RequestDigest != digest || receipt.DigestVersion != digestVersion {
		return nil, "", NewIdempotencyConflict("idempotency key was already used for a different command", nil)
	}
	if receipt.Status == "completed" {
		if receipt.WorkItemID == nil || *receipt.WorkItemID <= 0 {
			return nil, "", NewInternalFailure("completed intake receipt is missing its work item", nil)
		}
		return receipt, ClaimReplay, nil
	}
	if receipt.Status != "processing" {
		return nil, "", NewInternalFailure(fmt.Sprintf("unsupported intake receipt status %q", receipt.Status), nil)
	}
	if !inserted {
		return nil, "", NewInfrastructureUnavailable("idempotency claim is still processing", errIdempotencyOwnerInProgress)
	}
	return receipt, ClaimInserted, nil
}

func (r *IdempotencyRepository) Complete(ctx context.Context, tx *ent.Tx, tenantID, receiptID, workItemID int) error {
	if tx == nil {
		return NewInternalFailure("intake transaction is required", nil)
	}
	if workItemID <= 0 {
		return NewInternalFailure("completed intake receipt requires a work item", nil)
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
		return NewInfrastructureUnavailable("could not complete intake receipt", err)
	}
	if updated != 1 {
		return NewReferenceNotFound("intake receipt was not found", nil)
	}
	return nil
}

func (r *IdempotencyRepository) LoadCompleted(ctx context.Context, tx *ent.Tx, identity Identity, key string) (*ent.IntakeRequest, error) {
	if tx == nil {
		return nil, NewInternalFailure("intake transaction is required", nil)
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
		return nil, NewReferenceNotFound("completed intake receipt was not found", err)
	}
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not load completed intake receipt", err)
	}
	if receipt.WorkItemID == nil || *receipt.WorkItemID <= 0 {
		return nil, NewInternalFailure("completed intake receipt is missing its work item", nil)
	}
	return receipt, nil
}
