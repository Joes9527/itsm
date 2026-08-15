package change

import (
	"context"
)

// Repository interface for Change domain
type Repository interface {
	// Change CRUD
	Create(ctx context.Context, c *Change) (*Change, error)
	Get(ctx context.Context, id int, tenantID int) (*Change, error)
	List(ctx context.Context, tenantID int, page, size int, status, search, riskLevel string) ([]*Change, int, error)
	Update(ctx context.Context, c *Change) (*Change, error)
	Delete(ctx context.Context, id int, tenantID int) error
	GetStats(ctx context.Context, tenantID int) (*Stats, error)
	MarkSubmittedForApproval(ctx context.Context, changeID, tenantID int) error

	// Approvals
	GetApprovalHistory(ctx context.Context, changeID int, tenantID int) ([]*ApprovalRecord, error)

	// Risk Assessment
	CreateRiskAssessment(ctx context.Context, ra *RiskAssessment) (*RiskAssessment, error)
	GetRiskAssessment(ctx context.Context, changeID int, tenantID int) (*RiskAssessment, error)
	UpdateRiskAssessment(ctx context.Context, ra *RiskAssessment) (*RiskAssessment, error)

	// Tenant validation
	ValidateApproverBelongsToTenant(ctx context.Context, approverID, tenantID int) (bool, error)

	// Calendar view
	ListByDateRange(ctx context.Context, tenantID int, startDate, endDate, status string) ([]*Change, error)
}
