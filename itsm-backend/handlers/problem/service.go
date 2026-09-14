package problem

import (
	"context"
	"errors"
	"fmt"
	"itsm-backend/common"
	"itsm-backend/common/executionscope"
	"itsm-backend/database"
	"strings"

	"go.uber.org/zap"
	"itsm-backend/ent"
)

type Service struct {
	execution      *database.ExecutionPolicy
	directory      database.DirectorySnapshot
	client         *ent.Client
	investigations investigationTransactions
	repo           Repository
	logger         *zap.SugaredLogger
}

func NewService(repo Repository, logger *zap.SugaredLogger, execution *database.ExecutionPolicy) *Service {
	s := &Service{
		execution: execution,
		repo:      repo,
		logger:    logger,
	}
	if transactions, ok := repo.(investigationTransactions); ok {
		s.investigations = transactions
		s.client = transactions.transactionClient()
	}
	return s
}

func isValidProblemPriority(priority string) bool {
	switch priority {
	case "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

func isValidProblemStatusTransition(current, next string) bool {
	if current == next {
		return true
	}
	if next == "closed" {
		return canCloseProblemStatus(current)
	}
	transitions := map[string]map[string]struct{}{
		"open":          {"investigating": {}, "identified": {}, "resolved": {}},
		"investigating": {"identified": {}, "resolved": {}},
		"identified":    {"investigating": {}, "resolved": {}},
		"resolved":      {"investigating": {}},
		"closed":        {},
		// 兼容存量 in_progress 数据，仅允许进入规范状态。
		"in_progress": {"identified": {}, "resolved": {}},
	}
	allowed, ok := transitions[current]
	if !ok {
		return false
	}
	_, ok = allowed[next]
	return ok
}

func canCloseProblemStatus(status string) bool {
	return strings.TrimSpace(status) == "resolved"
}

func uniquePositiveIDs(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func (s *Service) GetStats(ctx context.Context, tenantID int) (*ProblemStats, error) {
	return s.repo.GetStats(ctx, tenantID)
}

func (s *Service) SetDirectorySnapshot(directory database.DirectorySnapshot) { s.directory = directory }

// requireExecutionTx is an additional write boundary, not business authorization.
func (s *Service) requireExecutionTx(ctx context.Context, tx *ent.Tx, tenantID, workItemID int) error {
	if err := s.execution.BindEnt(ctx, tx, tenantID); err != nil {
		return executionFailure(err)
	}
	return executionFailure(s.execution.RequireEntMembers(ctx, tx, tenantID, workItemID))
}

func executionFailure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, executionscope.ErrDenied) {
		return common.NewForbiddenError("problem execution scope denied")
	}
	return fmt.Errorf("problem execution scope: %w", err)
}
