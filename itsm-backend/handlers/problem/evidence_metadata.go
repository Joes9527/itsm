package problem

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// EvidenceMetadata edits investigation records and candidate solutions. It never
// implicitly selects a candidate or changes the authoritative root cause/resolution.
type EvidenceMetadata struct {
	ID             int
	Investigation  *dto.UpdateProblemInvestigationRequest
	CreateStep     *dto.CreateInvestigationStepRequest
	UpdateStep     *dto.UpdateInvestigationStepRequest
	CreateSolution *dto.CreateProblemSolutionRequest
	UpdateSolution *dto.UpdateProblemSolutionRequest
	DeleteSolution bool
}

func (e *EvidenceMetadata) validate(problemID int) error {
	count := 0
	for _, yes := range []bool{e.Investigation != nil, e.CreateStep != nil, e.UpdateStep != nil, e.CreateSolution != nil, e.UpdateSolution != nil, e.DeleteSolution} {
		if yes {
			count++
		}
	}
	if count != 1 || problemID <= 0 {
		return common.NewValidationError("one evidence mutation required", nil)
	}
	if e.CreateStep == nil && e.CreateSolution == nil && e.ID <= 0 {
		return common.NewValidationError("evidence ID required", nil)
	}
	if e.CreateStep != nil {
		p := e.CreateStep
		if p.ProblemID != problemID || p.InvestigationID <= 0 || p.StepNumber <= 0 || strings.TrimSpace(p.StepTitle) == "" || strings.TrimSpace(p.StepDescription) == "" {
			return common.NewValidationError("invalid investigation step", nil)
		}
	}
	if e.CreateSolution != nil {
		p := e.CreateSolution
		if p.ProblemID != problemID || strings.TrimSpace(p.SolutionDescription) == "" {
			return common.NewValidationError("candidate description required", nil)
		}
	}
	if e.UpdateSolution != nil {
		p := e.UpdateSolution
		if p.ApprovalStatus != nil || p.ApprovedBy != nil || p.ApprovalDate != nil {
			return common.NewValidationError("approval requires the owning workflow", nil)
		}
	}
	return nil
}

func (e *EvidenceMetadata) applyTx(ctx context.Context, tx *ent.Tx, problemID, tenantID int) error {
	activeUser := func(id *int) error {
		if id == nil {
			return nil
		}
		ok, err := tx.User.Query().Where(user.ID(*id), user.TenantID(tenantID), user.Active(true)).Exist(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return common.NewValidationError("active target-tenant user required", nil)
		}
		return nil
	}
	now := time.Now().UTC()
	exec := func(query string, args ...any) error {
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return common.NewNotFoundError("Problem evidence")
		}
		return nil
	}
	if p := e.CreateStep; p != nil {
		if err := activeUser(p.AssignedTo); err != nil {
			return err
		}
		return exec(`INSERT INTO problem_investigation_steps(investigation_id,step_number,step_title,step_description,status,assigned_to,notes,created_at,updated_at) SELECT $1,$2,$3,$4,'pending',$5,$6,$7,$7 FROM problem_investigations WHERE id=$1 AND problem_id=$8`, p.InvestigationID, p.StepNumber, strings.TrimSpace(p.StepTitle), strings.TrimSpace(p.StepDescription), p.AssignedTo, p.Notes, now, problemID)
	}
	if p := e.CreateSolution; p != nil {
		if err := activeUser(&p.ProposedBy); err != nil {
			return err
		}
		if !validSolutionType(p.SolutionType) || !isValidProblemPriority(p.Priority) {
			return common.NewValidationError("invalid candidate type or priority", nil)
		}
		return exec(`INSERT INTO problem_solutions(problem_id,solution_type,solution_description,proposed_by,proposed_date,status,priority,estimated_effort_hours,estimated_cost,risk_assessment,approval_status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'proposed',$6,$7,$8,$9,'pending',$5,$5)`, problemID, p.SolutionType, strings.TrimSpace(p.SolutionDescription), p.ProposedBy, now, p.Priority, p.EstimatedEffortHours, p.EstimatedCost, p.RiskAssessment)
	}
	if e.DeleteSolution {
		return exec("DELETE FROM problem_solutions WHERE id=$1 AND problem_id=$2", e.ID, problemID)
	}
	table := ""
	relation := "problem_id=$%d"
	args := []any{now}
	changes := []string{"updated_at=$1"}
	add := func(col string, v any) {
		args = append(args, v)
		changes = append(changes, fmt.Sprintf("%s=$%d", col, len(args)))
	}
	if p := e.Investigation; p != nil {
		table = "problem_investigations"
		if p.Status != nil {
			switch *p.Status {
			case dto.InvestigationStatusNotStarted, dto.InvestigationStatusInProgress, dto.InvestigationStatusOnHold, dto.InvestigationStatusCompleted, dto.InvestigationStatusCancelled:
			default:
				return common.NewValidationError("invalid investigation status", nil)
			}
			add("status", *p.Status)
		}
		if p.EstimatedCompletionDate != nil {
			add("estimated_completion_date", *p.EstimatedCompletionDate)
		}
		if p.ActualCompletionDate != nil {
			add("actual_completion_date", *p.ActualCompletionDate)
		}
		if p.InvestigationSummary != nil {
			add("investigation_summary", *p.InvestigationSummary)
		}
	}
	if p := e.UpdateStep; p != nil {
		table = "problem_investigation_steps"
		relation = "investigation_id IN (SELECT id FROM problem_investigations WHERE problem_id=$%d)"
		if err := activeUser(p.AssignedTo); err != nil {
			return err
		}
		if p.StepTitle != nil {
			if strings.TrimSpace(*p.StepTitle) == "" {
				return common.NewValidationError("step title required", nil)
			}
			add("step_title", *p.StepTitle)
		}
		if p.StepDescription != nil {
			add("step_description", *p.StepDescription)
		}
		if p.Status != nil {
			switch *p.Status {
			case dto.StepStatusPending, dto.StepStatusInProgress, dto.StepStatusCompleted, dto.StepStatusBlocked, dto.StepStatusCancelled:
			default:
				return common.NewValidationError("invalid step status", nil)
			}
			add("status", *p.Status)
		}
		if p.AssignedTo != nil {
			add("assigned_to", *p.AssignedTo)
		}
		if p.StartDate != nil {
			add("start_date", *p.StartDate)
		}
		if p.CompletionDate != nil {
			add("completion_date", *p.CompletionDate)
		}
		if p.Notes != nil {
			add("notes", *p.Notes)
		}
	}
	if p := e.UpdateSolution; p != nil {
		table = "problem_solutions"
		if p.SolutionType != nil {
			if !validSolutionType(*p.SolutionType) {
				return common.NewValidationError("invalid candidate type", nil)
			}
			add("solution_type", *p.SolutionType)
		}
		if p.SolutionDescription != nil {
			if strings.TrimSpace(*p.SolutionDescription) == "" {
				return common.NewValidationError("candidate description required", nil)
			}
			add("solution_description", *p.SolutionDescription)
		}
		if p.Priority != nil {
			if !isValidProblemPriority(*p.Priority) {
				return common.NewValidationError("invalid candidate priority", nil)
			}
			add("priority", *p.Priority)
		}
		if p.Status != nil {
			switch *p.Status {
			case dto.SolutionStatusProposed, dto.SolutionStatusInProgress, dto.SolutionStatusImplemented, dto.SolutionStatusRejected, dto.SolutionStatusCancelled:
			default:
				return common.NewValidationError("candidate status requires the owning workflow", nil)
			}
			add("status", *p.Status)
		}
		if p.EstimatedEffortHours != nil {
			add("estimated_effort_hours", *p.EstimatedEffortHours)
		}
		if p.EstimatedCost != nil {
			add("estimated_cost", *p.EstimatedCost)
		}
		if p.RiskAssessment != nil {
			add("risk_assessment", *p.RiskAssessment)
		}
	}
	if len(changes) == 1 {
		return common.NewValidationError("evidence changes required", nil)
	}
	query := "UPDATE " + table + " SET " + strings.Join(changes, ",") + fmt.Sprintf(" WHERE id=$%d AND ", len(args)+1) + fmt.Sprintf(relation, len(args)+2)
	return exec(query, append(args, e.ID, problemID)...)
}

func validSolutionType(t dto.SolutionType) bool {
	switch t {
	case dto.SolutionTypeWorkaround, dto.SolutionTypeFix, dto.SolutionTypePrevention, dto.SolutionTypeProcess:
		return true
	}
	return false
}
