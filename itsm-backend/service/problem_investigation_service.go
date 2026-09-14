package service

import (
	"context"
	"database/sql"
	"fmt"

	"itsm-backend/dto"

	"go.uber.org/zap"
)

// ProblemInvestigationService 问题调查服务
type ProblemInvestigationService struct {
	db           problemInvestigationDB
	logger       *zap.SugaredLogger
	tenantPool   *sql.DB
	scopedTenant int
}

// NewProblemInvestigationService 创建问题调查服务
func NewProblemInvestigationService(db *sql.DB, logger *zap.SugaredLogger) *ProblemInvestigationService {
	return &ProblemInvestigationService{
		db:     db,
		logger: logger,
	}
}

func (s *ProblemInvestigationService) requireTenantUser(ctx context.Context, userID, tenantID int) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND tenant_id = $2)", userID, tenantID).Scan(&exists); err != nil {
		return fmt.Errorf("验证用户失败: %v", err)
	}
	if !exists {
		return fmt.Errorf("用户不存在")
	}
	return nil
}

// GetRootCauseAnalysis 获取根本原因分析
func (s *ProblemInvestigationService) GetRootCauseAnalysis(ctx context.Context, id int, tenantID int) (*dto.RootCauseAnalysisResponse, error) {
	scoped, release, scopeErr := s.tenantScope(ctx, tenantID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	defer release()
	s = scoped

	return getRootCauseAnalysis(ctx, s.db, id, tenantID)
}

// Both DB and transaction reads use the authoritative Problem root cause.
func getRootCauseAnalysis(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}, id, tenantID int) (*dto.RootCauseAnalysisResponse, error) {
	var analysis dto.RootCauseAnalysisResponse
	err := db.QueryRowContext(ctx, `
		SELECT prca.id, prca.problem_id, prca.analyst_id, u1.name, prca.analysis_method,
		       COALESCE(p.root_cause, ''), prca.contributing_factors, prca.evidence, prca.confidence_level,
		       prca.analysis_date, prca.reviewed_by, u2.name, prca.review_date,
		       prca.created_at, prca.updated_at
		FROM problem_root_cause_analyses prca
		JOIN problems p ON prca.problem_id = p.id
		JOIN users u1 ON prca.analyst_id = u1.id
		LEFT JOIN users u2 ON prca.reviewed_by = u2.id
		WHERE prca.id = $1
		  AND p.work_item_id IN (SELECT id FROM tickets WHERE tenant_id = $2 AND deleted_at IS NULL)
		  AND u1.tenant_id = (SELECT tenant_id FROM tickets WHERE id = p.work_item_id AND deleted_at IS NULL)
		  AND (u2.id IS NULL OR u2.tenant_id = (SELECT tenant_id FROM tickets WHERE id = p.work_item_id AND deleted_at IS NULL))
	`, id, tenantID).Scan(
		&analysis.ID, &analysis.ProblemID, &analysis.AnalystID, &analysis.AnalystName,
		&analysis.AnalysisMethod, &analysis.RootCauseDescription, &analysis.ContributingFactors,
		&analysis.Evidence, &analysis.ConfidenceLevel, &analysis.AnalysisDate,
		&analysis.ReviewedBy, &analysis.ReviewedByName, &analysis.ReviewDate,
		&analysis.CreatedAt, &analysis.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("根因分析不存在")
		}
		return nil, fmt.Errorf("查询根因分析失败: %v", err)
	}
	return &analysis, nil
}

// GetProblemSolution 获取解决方案
func (s *ProblemInvestigationService) GetProblemSolution(ctx context.Context, id int, tenantID int) (*dto.ProblemSolutionResponse, error) {
	scoped, release, scopeErr := s.tenantScope(ctx, tenantID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	defer release()
	s = scoped

	var solution dto.ProblemSolutionResponse
	err := s.db.QueryRowContext(ctx, `
		SELECT ps.id, ps.problem_id, ps.solution_type, ps.solution_description, ps.proposed_by, u1.name,
		       ps.proposed_date, ps.status, ps.priority, ps.estimated_effort_hours, ps.estimated_cost,
		       ps.risk_assessment, ps.approval_status, ps.approved_by, u2.name, ps.approval_date,
		       ps.created_at, ps.updated_at
		FROM problem_solutions ps
		JOIN problems p ON ps.problem_id = p.id
		JOIN users u1 ON ps.proposed_by = u1.id
		LEFT JOIN users u2 ON ps.approved_by = u2.id
		WHERE ps.id = $1
		  AND p.work_item_id IN (SELECT id FROM tickets WHERE tenant_id = $2 AND deleted_at IS NULL)
		  AND u1.tenant_id = (SELECT tenant_id FROM tickets WHERE id = p.work_item_id AND deleted_at IS NULL)
		  AND (u2.id IS NULL OR u2.tenant_id = (SELECT tenant_id FROM tickets WHERE id = p.work_item_id AND deleted_at IS NULL))
	`, id, tenantID).Scan(
		&solution.ID, &solution.ProblemID, &solution.SolutionType, &solution.SolutionDescription,
		&solution.ProposedBy, &solution.ProposedByName, &solution.ProposedDate, &solution.Status,
		&solution.Priority, &solution.EstimatedEffortHours, &solution.EstimatedCost,
		&solution.RiskAssessment, &solution.ApprovalStatus, &solution.ApprovedBy, &solution.ApprovedByName,
		&solution.ApprovalDate, &solution.CreatedAt, &solution.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("解决方案不存在")
		}
		return nil, fmt.Errorf("查询解决方案失败: %v", err)
	}
	return &solution, nil
}

// GetProblemInvestigation 获取问题调查详情
func (s *ProblemInvestigationService) GetProblemInvestigation(ctx context.Context, investigationID, tenantID int) (*dto.ProblemInvestigationResponse, error) {
	scoped, release, scopeErr := s.tenantScope(ctx, tenantID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	defer release()
	s = scoped

	var investigation dto.ProblemInvestigationResponse
	err := s.db.QueryRowContext(ctx, `
		SELECT pi.id, pi.problem_id, pi.investigator_id, u.name, pi.status, pi.start_date, 
		       pi.estimated_completion_date, pi.actual_completion_date, pi.investigation_summary,
		       pi.created_at, pi.updated_at
		FROM problem_investigations pi
		JOIN users u ON pi.investigator_id = u.id AND u.tenant_id = $2
		JOIN problems p ON pi.problem_id = p.id
		WHERE pi.id = $1 AND p.work_item_id IN (SELECT id FROM tickets WHERE tenant_id = $2 AND deleted_at IS NULL)
	`, investigationID, tenantID).Scan(
		&investigation.ID, &investigation.ProblemID, &investigation.InvestigatorID, &investigation.InvestigatorName,
		&investigation.Status, &investigation.StartDate, &investigation.EstimatedCompletionDate,
		&investigation.ActualCompletionDate, &investigation.InvestigationSummary,
		&investigation.CreatedAt, &investigation.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("问题调查不存在")
		}
		return nil, fmt.Errorf("获取问题调查失败: %v", err)
	}

	return &investigation, nil
}

// UpdateProblemInvestigation 更新问题调查

func (s *ProblemInvestigationService) getInvestigationStep(ctx context.Context, stepID, tenantID int) (*dto.InvestigationStepResponse, error) {
	var step dto.InvestigationStepResponse
	err := s.db.QueryRowContext(ctx, `
		SELECT pis.id, pis.investigation_id, pis.step_number, pis.step_title, pis.step_description,
		       pis.status, pis.assigned_to, u.name, pis.start_date, pis.completion_date, pis.notes,
		       pis.created_at, pis.updated_at
		FROM problem_investigation_steps pis
		JOIN problem_investigations pi ON pis.investigation_id = pi.id
		JOIN problems p ON pi.problem_id = p.id
		LEFT JOIN users u ON pis.assigned_to = u.id AND u.tenant_id = (SELECT tenant_id FROM tickets WHERE id = p.work_item_id AND deleted_at IS NULL)
		WHERE pis.id = $1 AND p.work_item_id IN (SELECT id FROM tickets WHERE tenant_id = $2 AND deleted_at IS NULL)
	`, stepID, tenantID).Scan(
		&step.ID, &step.InvestigationID, &step.StepNumber, &step.StepTitle, &step.StepDescription,
		&step.Status, &step.AssignedTo, &step.AssignedToName, &step.StartDate, &step.CompletionDate, &step.Notes,
		&step.CreatedAt, &step.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("获取调查步骤失败: %v", err)
	}

	return &step, nil
}

// CreateProblemSolution 创建问题解决方案

func (s *ProblemInvestigationService) GetProblemInvestigationSummary(ctx context.Context, problemID, tenantID int) (*dto.ProblemInvestigationSummaryResponse, error) {
	scoped, release, scopeErr := s.tenantScope(ctx, tenantID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	defer release()
	s = scoped

	// 检查问题是否存在
	var problemTitle string
	err := s.db.QueryRowContext(ctx, `SELECT t.title FROM problems p JOIN tickets t ON t.id = p.work_item_id WHERE p.id = $1 AND t.tenant_id = $2 AND t.deleted_at IS NULL`, problemID, tenantID).Scan(&problemTitle)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("问题不存在")
		}
		return nil, fmt.Errorf("查询问题失败: %v", err)
	}

	summary := &dto.ProblemInvestigationSummaryResponse{}

	// 获取调查记录
	var investigationID int
	err = s.db.QueryRowContext(ctx, "SELECT pi.id FROM problem_investigations pi JOIN problems p ON pi.problem_id = p.id WHERE pi.problem_id = $1 AND p.work_item_id IN (SELECT id FROM tickets WHERE tenant_id = $2 AND deleted_at IS NULL)", problemID, tenantID).Scan(&investigationID)
	if err == nil {
		investigation, err := s.GetProblemInvestigation(ctx, investigationID, tenantID)
		if err == nil {
			summary.Investigation = investigation
		}
	}

	// 获取调查步骤
	if summary.Investigation != nil {
		rows, err := s.db.QueryContext(ctx, `
			SELECT pis.id, pis.investigation_id, pis.step_number, pis.step_title, pis.step_description,
			       pis.status, pis.assigned_to, u.name, pis.start_date, pis.completion_date, pis.notes,
			       pis.created_at, pis.updated_at
			FROM problem_investigation_steps pis
			JOIN problem_investigations pi ON pis.investigation_id = pi.id
			JOIN problems p ON pi.problem_id = p.id
			LEFT JOIN users u ON pis.assigned_to = u.id AND u.tenant_id = (SELECT tenant_id FROM tickets WHERE id = p.work_item_id AND deleted_at IS NULL)
			WHERE pis.investigation_id = $1 AND p.work_item_id IN (SELECT id FROM tickets WHERE tenant_id = $2 AND deleted_at IS NULL)
			ORDER BY pis.step_number
		`, investigationID, tenantID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var step dto.InvestigationStepResponse
				err := rows.Scan(
					&step.ID, &step.InvestigationID, &step.StepNumber, &step.StepTitle, &step.StepDescription,
					&step.Status, &step.AssignedTo, &step.AssignedToName, &step.StartDate, &step.CompletionDate, &step.Notes,
					&step.CreatedAt, &step.UpdatedAt,
				)
				if err == nil {
					summary.Steps = append(summary.Steps, &step)
				}
			}
		}
	}

	// RCA正文仅投影 Problem；QueryRow 会在下一条查询前释放游标。
	var analysisID int
	err = s.db.QueryRowContext(ctx, "SELECT rca.id FROM problem_root_cause_analyses rca JOIN problems p ON rca.problem_id = p.id WHERE rca.problem_id = $1 AND p.work_item_id IN (SELECT id FROM tickets WHERE tenant_id = $2 AND deleted_at IS NULL)", problemID, tenantID).Scan(&analysisID)
	if err == nil {
		summary.RootCauseAnalysis, err = getRootCauseAnalysis(ctx, s.db, analysisID, tenantID)
		if err != nil {
			return nil, err
		}
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("查询根因分析失败: %w", err)
	}

	// 获取解决方案
	rows, err := s.db.QueryContext(ctx, `
		SELECT ps.id, ps.problem_id, ps.solution_type, ps.solution_description, ps.proposed_by, u1.name,
		       ps.proposed_date, ps.status, ps.priority, ps.estimated_effort_hours, ps.estimated_cost,
		       ps.risk_assessment, ps.approval_status, ps.approved_by, u2.name, ps.approval_date,
		       ps.created_at, ps.updated_at
		FROM problem_solutions ps
		JOIN problems p ON ps.problem_id = p.id
		JOIN users u1 ON ps.proposed_by = u1.id AND u1.tenant_id = (SELECT tenant_id FROM tickets WHERE id = p.work_item_id AND deleted_at IS NULL)
		LEFT JOIN users u2 ON ps.approved_by = u2.id AND (u2.id IS NULL OR u2.tenant_id = (SELECT tenant_id FROM tickets WHERE id = p.work_item_id AND deleted_at IS NULL))
		WHERE ps.problem_id = $1 AND p.work_item_id IN (SELECT id FROM tickets WHERE tenant_id = $2 AND deleted_at IS NULL)
		ORDER BY ps.created_at DESC
	`, problemID, tenantID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var solution dto.ProblemSolutionResponse
			err := rows.Scan(
				&solution.ID, &solution.ProblemID, &solution.SolutionType, &solution.SolutionDescription,
				&solution.ProposedBy, &solution.ProposedByName, &solution.ProposedDate, &solution.Status,
				&solution.Priority, &solution.EstimatedEffortHours, &solution.EstimatedCost,
				&solution.RiskAssessment, &solution.ApprovalStatus, &solution.ApprovedBy, &solution.ApprovedByName,
				&solution.ApprovalDate, &solution.CreatedAt, &solution.UpdatedAt,
			)
			if err == nil {
				summary.Solutions = append(summary.Solutions, &solution)
			}
		}
	}

	return summary, nil
}

// UpdateProblemSolution 更新解决方案
