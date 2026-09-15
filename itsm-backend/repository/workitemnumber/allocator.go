package workitemnumber

import (
	"context"
	"errors"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/workitemnumbersequence"
)

// Allocator issues the authoritative human-readable number for a WorkItem.
type Allocator interface {
	Allocate(ctx context.Context, client *ent.Client, tenantID int, issuedAt time.Time) (string, error)
}

// PostgreSQLAllocator delegates sequence authority and transaction semantics to PostgreSQL.
type PostgreSQLAllocator struct{}

// NewPostgreSQLAllocator constructs the stateless WorkItem number allocator.
func NewPostgreSQLAllocator() *PostgreSQLAllocator {
	return &PostgreSQLAllocator{}
}

// Allocate atomically advances the tenant/month sequence through the supplied client.
// Transaction-owning callers must pass tx.Client() so the allocation rolls back with
// the WorkItem write.
func (a *PostgreSQLAllocator) Allocate(
	ctx context.Context,
	client *ent.Client,
	tenantID int,
	issuedAt time.Time,
) (string, error) {
	if client == nil {
		return "", errors.New("work item number allocator requires an Ent client")
	}
	if tenantID <= 0 {
		return "", errors.New("work item number allocator requires a positive tenant id")
	}
	if issuedAt.IsZero() {
		return "", errors.New("work item number allocator requires issuedAt")
	}

	period := issuedAt.UTC().Format("200601")
	err := client.WorkItemNumberSequence.Create().
		SetTenantID(tenantID).
		SetPeriod(period).
		SetLastValue(0).
		OnConflictColumns(
			workitemnumbersequence.FieldTenantID,
			workitemnumbersequence.FieldPeriod,
		).
		Ignore().
		Exec(ctx)
	if err != nil {
		return "", fmt.Errorf("ensure work item number sequence: %w", err)
	}

	row, err := client.WorkItemNumberSequence.Query().
		Where(
			workitemnumbersequence.TenantID(tenantID),
			workitemnumbersequence.Period(period),
		).
		Only(ctx)
	if err != nil {
		return "", fmt.Errorf("load work item number sequence: %w", err)
	}

	row, err = client.WorkItemNumberSequence.UpdateOneID(row.ID).
		AddLastValue(1).
		Save(ctx)
	if err != nil {
		return "", fmt.Errorf("increment work item number sequence: %w", err)
	}

	return fmt.Sprintf("TKT-%s-%06d", period, row.LastValue), nil
}
