package change

import (
	"context"
	"database/sql"
	"slices"
	"sort"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/handlers/shared/workitemmutation"
)

// MetadataCommand accepts editable professional facts and an explicitly observed version.
type MetadataCommand struct {
	Meta               workitemmutation.Meta
	ChangeID           int
	Patch              dto.UpdateChangeRequest
	processingCallback bool
}

func (s *Service) ApplyMetadata(ctx context.Context, cmd MetadataCommand) (out workitemmutation.Result, resultErr error) {
	var empty workitemmutation.Result
	cmd.Patch = normalizeMetadataPatch(cmd.Patch)
	m := cmd.Meta
	if m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.Source) == "" || strings.TrimSpace(m.OperationID) == "" {
		return empty, common.NewValidationError("trusted actor, tenant, version, source and operationId required", nil)
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	if err := validateMetadataPatch(cmd.Patch); err != nil {
		return empty, err
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	digest, err := workitemmutation.Digest(struct {
		Action            string
		ChangeID, Version int
		Patch             dto.UpdateChangeRequest
	}{"metadata", cmd.ChangeID, m.ExpectedVersion, cmd.Patch})
	if err != nil {
		return empty, err
	}
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	attempted := false
	defer func() {
		if resultErr == nil || !attempted {
			return
		}
		// Only inspect a competing receipt after a confirmed rollback. Never
		// repeat side effects, uncertain commits, or a failed rollback.
		if tx.Rollback() != nil {
			return
		}
		fresh, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return
		}
		defer fresh.Rollback()
		current, err := s.authorizeCommand(ctx, fresh, Command{Meta: m, ChangeID: cmd.ChangeID, Action: "metadata"})
		if err != nil {
			resultErr = err
			return
		}
		replay, ok, err := workitemmutation.Replay(ctx, fresh.Client(), m, current.WorkItemID, digest)
		if err != nil {
			resultErr = err
			return
		}
		if ok {
			out = replay
			resultErr = nil
		}
	}()
	current, err := s.authorizeCommand(ctx, tx, Command{Meta: m, ChangeID: cmd.ChangeID, Action: "metadata"})
	if err != nil {
		return empty, err
	}
	if result, ok, err := workitemmutation.Replay(ctx, tx.Client(), m, current.WorkItemID, digest); ok || err != nil {
		return result, err
	}
	item := current.Edges.WorkItem
	if item.Version != m.ExpectedVersion {
		return empty, common.NewVersionConflictError("change", cmd.ChangeID, m.ExpectedVersion, item.Version)
	}
	if item.Status == "completed" || item.Status == "cancelled" || item.Status == "rejected" {
		return empty, common.NewValidationError("terminal change metadata is locked", nil)
	}
	var ownCallback []workitemmutation.Meta
	if cmd.processingCallback {
		ownCallback = append(ownCallback, m)
	}
	if err = workitemmutation.RequireSettledChangeCallbacks(ctx, tx, m.TenantID, current.WorkItemID, ownCallback...); err != nil {
		if _, unresolved := err.(*workitemmutation.UnresolvedChangeCallbackError); unresolved {
			err = common.NewConflictError("Change workflow", err.Error())
		}
		return empty, err
	}
	p := cmd.Patch
	if p.AssigneeID != nil && item.AssigneeID > 0 && *p.AssigneeID != item.AssigneeID && p.AssignmentReason == "" {
		return empty, common.NewValidationError("assignment reason required", nil)
	}
	governedFacts := hasRiskDetails(p.ChangeRiskPatch) || p.Type != nil || p.Justification != nil || p.ImpactScope != nil || p.RiskLevel != nil || p.ImplementationPlan != nil || p.RollbackPlan != nil || p.AffectedCIs != nil
	if governedFacts && item.Status != "draft" && !isChangeSubmitted(item.Status) {
		return empty, common.NewValidationError("authorized change scope and assessment facts are locked", nil)
	}
	if p.Type != nil && item.Status != "draft" {
		return empty, common.NewValidationError("change type is locked after submission", nil)
	}
	if (p.PlannedStartDate != nil || p.PlannedEndDate != nil) && item.Status != "draft" {
		return empty, common.NewValidationError("use governed schedule action for implementation window", nil)
	}
	domain := toDomain(current)
	if hasRiskDetails(p.ChangeRiskPatch) {
		domain.RiskAssessment, err = readRiskDetails(ctx, tx, current.ID, m.TenantID, current.RiskLevel)
		if err != nil {
			return empty, err
		}
	}
	if p.AssigneeID != nil {
		// The existing Change assign route requires change:write, already
		// checked by authorizeCommand. Recipients remain active target-tenant users.
		eligible, err := tx.User.Query().Where(user.ID(*p.AssigneeID), user.TenantID(m.TenantID), user.Active(true)).Exist(ctx)
		if err != nil {
			return empty, err
		}
		if *p.AssigneeID <= 0 || !eligible {
			return empty, common.NewValidationError("assignee must be an active target-tenant user", nil)
		}
	}
	// Assessment advances to CAB without changing submitted status. Its
	// persisted evidence freezes the covered facts; there is no reassessment task.
	assessed := current.AssessmentDigest != "" || current.AssessmentEvidence != "" || current.AssessedBy > 0 || !current.AssessedAt.IsZero()
	if assessed && (riskDetailsChanged(domain.RiskAssessment, p.ChangeRiskPatch) || metadataChanges(domain, dto.UpdateChangeRequest{
		Type: p.Type, Justification: p.Justification, RiskLevel: p.RiskLevel,
		ImpactScope: p.ImpactScope, ImplementationPlan: p.ImplementationPlan,
		RollbackPlan: p.RollbackPlan, AffectedCIs: p.AffectedCIs,
	})) {
		return empty, common.NewValidationError("assessed change facts are locked", nil)
	}
	if assessed && p.AffectedCIs != nil {
		// Equivalent normalized input must preserve the exact assessed ordering
		// because the persisted assessment digest binds that array too.
		p.AffectedCIs = current.AffectedCis
	}
	if !metadataChanges(domain, p) {
		return empty, common.NewValidationError("new metadata facts required", nil)
	}
	if err := s.requireExecutionTx(ctx, tx, m.TenantID, item.ID); err != nil {
		return empty, err
	}
	update := tx.Ticket.UpdateOneID(item.ID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil(), ticket.Version(m.ExpectedVersion)).SetVersion(m.ExpectedVersion + 1).SetUpdatedAt(time.Now())
	professional := tx.Change.UpdateOneID(current.ID)
	if p.AssigneeID != nil {
		update.SetAssigneeID(*p.AssigneeID)
	}
	if p.Title != nil {
		if strings.TrimSpace(*p.Title) == "" {
			return empty, common.NewValidationError("title required", nil)
		}
		update.SetTitle(*p.Title)
	}
	if p.Description != nil {
		update.SetDescription(*p.Description)
	}
	if p.Priority != nil {
		update.SetPriority(string(*p.Priority))
	}
	if p.Justification != nil {
		professional.SetJustification(*p.Justification)
	}
	if p.Type != nil {
		professional.SetType(string(*p.Type))
	}
	if p.ImpactScope != nil {
		professional.SetImpactScope(string(*p.ImpactScope))
	}
	if p.RiskLevel != nil {
		professional.SetRiskLevel(string(*p.RiskLevel))
	}
	if p.ImplementationPlan != nil {
		professional.SetImplementationPlan(*p.ImplementationPlan)
	}
	if p.RollbackPlan != nil {
		professional.SetRollbackPlan(*p.RollbackPlan)
	}
	if p.AffectedCIs != nil {
		professional.SetAffectedCis(p.AffectedCIs)
	}
	if p.PlannedStartDate != nil {
		professional.SetPlannedStartDate(*p.PlannedStartDate)
	}
	if p.PlannedEndDate != nil {
		professional.SetPlannedEndDate(*p.PlannedEndDate)
	}
	attempted = true
	saved, err := update.Save(ctx)
	if ent.IsNotFound(err) {
		return empty, common.NewVersionConflictError("change", cmd.ChangeID, m.ExpectedVersion, item.Version)
	}
	if err != nil {
		return empty, err
	}
	if _, err = professional.Save(ctx); err != nil {
		return empty, err
	}
	// Preserve absent details and their assessed digest when the patch changes no risk facts.
	if riskDetailsChanged(domain.RiskAssessment, p.ChangeRiskPatch) {
		if err = writeRiskDetails(ctx, tx, current.ID, m.TenantID, domain.RiskAssessment, p.ChangeRiskPatch); err != nil {
			return empty, err
		}
	}
	result := workitemmutation.Result{WorkItemID: item.ID, Version: saved.Version, Status: saved.Status}
	if err = workitemmutation.RecordTx(ctx, tx, m, result, "change.metadata", digest, map[string]any{"changeId": current.ID, "patch": p, "previousAssigneeId": item.AssigneeID}); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func normalizeMetadataPatch(p dto.UpdateChangeRequest) dto.UpdateChangeRequest {
	p.AssignmentReason = strings.TrimSpace(p.AssignmentReason)
	for _, field := range []**string{&p.Title, &p.Description, &p.Justification, &p.ImplementationPlan, &p.RollbackPlan, &p.RiskDescription, &p.ImpactAnalysis, &p.MitigationMeasures, &p.ContingencyPlan, &p.RiskOwner} {
		if *field != nil {
			value := strings.TrimSpace(**field)
			*field = &value
		}
	}
	normalize := func(values []string) []string {
		if values == nil {
			return nil
		}
		set := map[string]bool{}
		result := []string{}
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v != "" && !set[v] {
				set[v] = true
				result = append(result, v)
			}
		}
		sort.Strings(result)
		return result
	}
	p.AffectedCIs = normalize(p.AffectedCIs)
	return p
}

func metadataChanges(c *Change, p dto.UpdateChangeRequest) bool {
	if p.AssigneeID != nil && (c.AssigneeID == nil || *p.AssigneeID != *c.AssigneeID) {
		return true
	}
	if riskDetailsChanged(c.RiskAssessment, p.ChangeRiskPatch) {
		return true
	}
	for _, field := range []struct {
		patch   *string
		current string
	}{{p.Title, c.Title}, {p.Description, c.Description}, {p.Justification, c.Justification}, {p.ImplementationPlan, c.ImplementationPlan}, {p.RollbackPlan, c.RollbackPlan}} {
		if field.patch != nil && *field.patch != field.current {
			return true
		}
	}
	if p.Type != nil && string(*p.Type) != c.Type || p.Priority != nil && string(*p.Priority) != c.Priority || p.ImpactScope != nil && string(*p.ImpactScope) != c.ImpactScope || p.RiskLevel != nil && string(*p.RiskLevel) != c.RiskLevel {
		return true
	}
	for _, field := range []struct{ patch, current *time.Time }{{p.PlannedStartDate, c.PlannedStartDate}, {p.PlannedEndDate, c.PlannedEndDate}} {
		if field.patch != nil && (field.current == nil || !field.patch.Equal(*field.current)) {
			return true
		}
	}
	normalized := normalizeMetadataPatch(dto.UpdateChangeRequest{AffectedCIs: c.AffectedCIs})
	if p.AffectedCIs != nil && !slices.Equal(p.AffectedCIs, normalized.AffectedCIs) {
		return true
	}
	return false
}

func validateMetadataPatch(p dto.UpdateChangeRequest) error {
	valid := func(value string, allowed ...string) bool {
		for _, candidate := range allowed {
			if value == candidate {
				return true
			}
		}
		return false
	}
	if p.Type != nil && !valid(string(*p.Type), string(dto.ChangeTypeNormal), string(dto.ChangeTypeStandard), string(dto.ChangeTypeEmergency)) {
		return common.NewValidationError("invalid change type", nil)
	}
	if p.Priority != nil && !valid(string(*p.Priority), string(dto.ChangePriorityLow), string(dto.ChangePriorityMedium), string(dto.ChangePriorityHigh), string(dto.ChangePriorityCritical)) {
		return common.NewValidationError("invalid change priority", nil)
	}
	if p.ImpactScope != nil && !valid(string(*p.ImpactScope), string(dto.ChangeImpactLow), string(dto.ChangeImpactMedium), string(dto.ChangeImpactHigh)) {
		return common.NewValidationError("invalid change impact", nil)
	}
	if p.RiskLevel != nil && !valid(string(*p.RiskLevel), string(dto.ChangeRiskLow), string(dto.ChangeRiskMedium), string(dto.ChangeRiskHigh)) {
		return common.NewValidationError("invalid change risk", nil)
	}
	return nil
}
