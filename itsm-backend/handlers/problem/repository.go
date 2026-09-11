package problem

import (
	"context"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/predicate"
	"time"
)

// Repository interface for Problem domain
type Repository interface {
	Get(ctx context.Context, id int, tenantID int) (*Problem, error)
	List(ctx context.Context, tenantID int, page, size int, filters map[string]interface{}, scope ...predicate.Ticket) ([]*Problem, int, error)
	GetStats(ctx context.Context, tenantID int) (*ProblemStats, error)
}

// The owning service controls one transaction; persistence never commits it.
type investigationTransactions interface {
	transactionClient() *ent.Client
	createInvestigationTx(context.Context, *ent.Tx, *dto.CreateProblemInvestigationRequest, int, time.Time) (int, error)
}

func (r *EntRepository) transactionClient() *ent.Client { return r.client }
