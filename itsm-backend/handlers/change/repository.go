package change

import (
	"context"
)

// Repository interface for Change domain
type Repository interface {
	// Change CRUD
	Get(ctx context.Context, id int, tenantID int) (*Change, error)
	List(ctx context.Context, tenantID int, page, size int, status, search, riskLevel string) ([]*Change, int, error)
	GetStats(ctx context.Context, tenantID int) (*Stats, error)

	// Approvals
	GetApprovalHistory(ctx context.Context, changeID int, tenantID int) ([]*ApprovalRecord, error)

	// Risk Assessment

	// Calendar view
	ListByDateRange(ctx context.Context, tenantID int, startDate, endDate, status string) ([]*Change, error)
}
