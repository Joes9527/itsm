package problem

import (
	"context"
	"fmt"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/user"
	"strings"
	"time"
)

// RootCauseMetadata describes investigation evidence attached to the same Problem mutation.
// The owning metadata transaction controls identity, version, audit and receipt.
type RootCauseMetadata struct {
	ID     int
	Create *dto.CreateRootCauseAnalysisRequest
	Update *dto.UpdateRootCauseAnalysisRequest
	Delete bool
}

func (r *RootCauseMetadata) validate(problemID int) error {
	count := 0
	if r.Create != nil {
		count++
	}
	if r.Update != nil {
		count++
	}
	if r.Delete {
		count++
	}
	if count != 1 {
		return common.NewValidationError("one root cause mutation required", nil)
	}
	if r.Create != nil {
		if r.ID != 0 || r.Create.ProblemID != problemID || strings.TrimSpace(r.Create.RootCauseDescription) == "" || strings.TrimSpace(r.Create.AnalysisMethod) == "" {
			return common.NewValidationError("invalid root cause creation", nil)
		}
	} else if r.ID <= 0 {
		return common.NewValidationError("root cause analysis ID required", nil)
	}
	if r.Update != nil {
		p := r.Update
		if p.AnalysisMethod == nil && p.RootCauseDescription == nil && p.ContributingFactors == nil && p.Evidence == nil && p.ConfidenceLevel == nil && p.ReviewedBy == nil && p.ReviewDate == nil {
			return common.NewValidationError("root cause changes required", nil)
		}
	}
	return nil
}

func (r *RootCauseMetadata) applyTx(ctx context.Context, tx *ent.Tx, problemID, tenantID int) error {
	now := time.Now().UTC()
	if r.Create != nil {
		p := r.Create
		eligible, err := tx.User.Query().Where(user.ID(p.AnalystID), user.TenantID(tenantID), user.Active(true)).Exist(ctx)
		if err != nil {
			return err
		}
		if !eligible {
			return common.NewValidationError("active target-tenant analyst required", nil)
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO problem_root_cause_analyses(problem_id,analyst_id,analysis_method,contributing_factors,evidence,confidence_level,analysis_date,created_at,updated_at) SELECT $1,$2,$3,$4,$5,$6,$7,$7,$7 WHERE NOT EXISTS (SELECT 1 FROM problem_root_cause_analyses WHERE problem_id=$1)`, problemID, p.AnalystID, p.AnalysisMethod, p.ContributingFactors, p.Evidence, p.ConfidenceLevel, now)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return common.NewValidationError("Problem already has a root cause analysis", nil)
		}
		return nil
	}
	if r.Delete {
		result, err := tx.ExecContext(ctx, `DELETE FROM problem_root_cause_analyses WHERE id=$1 AND problem_id=$2`, r.ID, problemID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return common.NewNotFoundError("root cause analysis")
		}
		return nil
	}
	p := r.Update
	if p.ReviewedBy != nil {
		eligible, err := tx.User.Query().Where(user.ID(*p.ReviewedBy), user.TenantID(tenantID), user.Active(true)).Exist(ctx)
		if err != nil {
			return err
		}
		if !eligible {
			return common.NewValidationError("active target-tenant reviewer required", nil)
		}
	}
	query := "UPDATE problem_root_cause_analyses SET updated_at=$1"
	args := []any{now}
	add := func(column string, value any) {
		args = append(args, value)
		query += fmt.Sprintf(", %s=$%d", column, len(args))
	}
	if p.AnalysisMethod != nil {
		add("analysis_method", *p.AnalysisMethod)
	}
	if p.ContributingFactors != nil {
		add("contributing_factors", *p.ContributingFactors)
	}
	if p.Evidence != nil {
		add("evidence", *p.Evidence)
	}
	if p.ConfidenceLevel != nil {
		add("confidence_level", *p.ConfidenceLevel)
	}
	if p.ReviewedBy != nil {
		add("reviewed_by", *p.ReviewedBy)
	}
	if p.ReviewDate != nil {
		add("review_date", *p.ReviewDate)
	}
	query += fmt.Sprintf(" WHERE id=$%d AND problem_id=$%d", len(args)+1, len(args)+2)
	args = append(args, r.ID, problemID)
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return common.NewNotFoundError("root cause analysis")
	}
	return nil
}
