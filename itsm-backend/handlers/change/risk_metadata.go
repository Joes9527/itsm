package change

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
)

func hasRiskDetails(p dto.ChangeRiskPatch) bool {
	return p.RiskDescription != nil || p.ImpactAnalysis != nil || p.MitigationMeasures != nil || p.ContingencyPlan != nil || p.RiskOwner != nil || p.RiskReviewDate != nil
}

func riskDetailsChanged(current *RiskAssessment, p dto.ChangeRiskPatch) bool {
	if current == nil {
		current = &RiskAssessment{}
	}
	for _, field := range []struct {
		patch  *string
		stored string
	}{{p.RiskDescription, current.RiskDescription}, {p.ImpactAnalysis, current.ImpactAnalysis}, {p.MitigationMeasures, current.MitigationMeasures}, {p.ContingencyPlan, current.ContingencyPlan}, {p.RiskOwner, current.RiskOwner}} {
		if field.patch != nil && *field.patch != field.stored {
			return true
		}
	}
	return p.RiskReviewDate != nil && (current.RiskReviewDate == nil || !p.RiskReviewDate.Equal(*current.RiskReviewDate))
}

// Risk level is owned by Change; the obsolete raw-table column is never read or written.
func readRiskDetails(ctx context.Context, tx *ent.Tx, changeID, tenantID int, riskLevel string) (*RiskAssessment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(risk_description,''),COALESCE(impact_analysis,''),COALESCE(mitigation_measures,''),COALESCE(contingency_plan,''),COALESCE(risk_owner,''),risk_review_date,created_at,updated_at FROM change_risk_assessments WHERE change_id=$1 AND tenant_id=$2`, changeID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	value := &RiskAssessment{ChangeID: changeID, TenantID: tenantID, RiskLevel: riskLevel}
	var review sql.NullTime
	if err = rows.Scan(&value.ID, &value.RiskDescription, &value.ImpactAnalysis, &value.MitigationMeasures, &value.ContingencyPlan, &value.RiskOwner, &review, &value.CreatedAt, &value.UpdatedAt); err != nil {
		return nil, err
	}
	if rows.Next() {
		return nil, fmt.Errorf("ambiguous Change risk assessment")
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if review.Valid {
		value.RiskReviewDate = &review.Time
	}
	return value, nil
}

func writeRiskDetails(ctx context.Context, tx *ent.Tx, changeID, tenantID int, current *RiskAssessment, p dto.ChangeRiskPatch) error {
	value := RiskAssessment{}
	if current != nil {
		value = *current
	}
	for _, field := range []struct {
		patch *string
		value *string
	}{{p.RiskDescription, &value.RiskDescription}, {p.ImpactAnalysis, &value.ImpactAnalysis}, {p.MitigationMeasures, &value.MitigationMeasures}, {p.ContingencyPlan, &value.ContingencyPlan}, {p.RiskOwner, &value.RiskOwner}} {
		if field.patch != nil {
			*field.value = *field.patch
		}
	}
	if p.RiskReviewDate != nil {
		value.RiskReviewDate = p.RiskReviewDate
	}
	if current == nil {
		_, err := tx.ExecContext(ctx, `INSERT INTO change_risk_assessments(change_id,tenant_id,risk_description,impact_analysis,mitigation_measures,contingency_plan,risk_owner,risk_review_date,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, changeID, tenantID, value.RiskDescription, value.ImpactAnalysis, value.MitigationMeasures, value.ContingencyPlan, value.RiskOwner, value.RiskReviewDate, time.Now())
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE change_risk_assessments SET risk_description=$1,impact_analysis=$2,mitigation_measures=$3,contingency_plan=$4,risk_owner=$5,risk_review_date=$6,updated_at=$7 WHERE id=$8 AND change_id=$9 AND tenant_id=$10`, value.RiskDescription, value.ImpactAnalysis, value.MitigationMeasures, value.ContingencyPlan, value.RiskOwner, value.RiskReviewDate, time.Now(), value.ID, changeID, tenantID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("risk assessment changed concurrently")
	}
	return nil
}
