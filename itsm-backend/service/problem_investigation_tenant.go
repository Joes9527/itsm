package service

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database/rls"
)

type problemInvestigationDB interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// NewTenantScopedProblemInvestigationService enforces physical PostgreSQL
// connection tenant scope when RLS is enabled. The direct constructor remains
// available for deployments without RLS and for SQLite service tests.
func NewTenantScopedProblemInvestigationService(db *sql.DB, logger *zap.SugaredLogger) *ProblemInvestigationService {
	s := NewProblemInvestigationService(db, logger)
	s.tenantPool = db
	return s
}

func (s *ProblemInvestigationService) tenantScope(ctx context.Context, tenantID int) (*ProblemInvestigationService, func(), error) {
	if tenantID <= 0 {
		return nil, nil, fmt.Errorf("problem investigation requires a tenant")
	}
	if existing, ok := tenantctx.TenantID(ctx); ok && existing != tenantID {
		return nil, nil, fmt.Errorf("problem investigation tenant context mismatch")
	}
	if s.scopedTenant != 0 && s.scopedTenant != tenantID {
		return nil, nil, fmt.Errorf("problem investigation connection tenant mismatch")
	}
	if s.tenantPool == nil {
		return s, func() {}, nil
	}
	conn, err := rls.AcquireConn(tenantctx.WithTenantID(ctx, tenantID), s.tenantPool)
	if err != nil {
		return nil, nil, err
	}
	scoped := *s
	scoped.db, scoped.tenantPool, scoped.scopedTenant = conn, nil, tenantID
	return &scoped, func() {
		if err := rls.ReleaseConn(ctx, conn); err != nil {
			s.logger.Errorw("Failed to release problem investigation tenant connection", "error", err)
		}
	}, nil
}
