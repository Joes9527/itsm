package change

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	ec "itsm-backend/ent/change"
	"itsm-backend/ent/changepir"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
)

// Command carries professional facts only. The transport supplies trusted Meta;
// CAB authorization references an immutable decision produced by the BPMN engine.
type Command struct {
	Meta                      workitemmutation.Meta
	ChangeID                  int
	Action, Outcome, Evidence string
	ApprovalDecisionID        int
	PlannedStart, PlannedEnd  *time.Time
	ActualEnd                 *time.Time
	PIRID                     int
}

func (s *Service) ApplyCommand(ctx context.Context, cmd Command) (workitemmutation.Result, error) {
	var empty workitemmutation.Result
	cmd.Action, cmd.Outcome, cmd.Evidence = strings.TrimSpace(cmd.Action), strings.TrimSpace(cmd.Outcome), strings.TrimSpace(cmd.Evidence)
	m := cmd.Meta
	if m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.Source) == "" || strings.TrimSpace(m.OperationID) == "" {
		return empty, common.NewValidationError("trusted actor, tenant, version, source and operationId required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	switch cmd.Action {
	case "submit", "assess", "authorize", "schedule", "implement", "record_outcome", "review", "close", "cancel":
	default:
		return empty, common.NewValidationError("unsupported change action", nil)
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	digest, err := workitemmutation.Digest(struct {
		ChangeID, Version, DecisionID, PIRID int
		Action, Outcome, Evidence            string
		PlannedStart, PlannedEnd, ActualEnd  *time.Time
	}{cmd.ChangeID, m.ExpectedVersion, cmd.ApprovalDecisionID, cmd.PIRID, cmd.Action, cmd.Outcome, cmd.Evidence, cmd.PlannedStart, cmd.PlannedEnd, cmd.ActualEnd})
	if err != nil {
		return empty, err
	}
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	current, err := s.authorizeCommand(ctx, tx, cmd)
	if err != nil {
		return empty, err
	}
	if result, ok, err := workitemmutation.Replay(ctx, tx.Client(), m, current.WorkItemID, digest); ok || err != nil {
		return result, err
	}
	result, err := s.applyCommandTx(ctx, tx, cmd, current, digest)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		// Resolve a concurrent same-key winner only in a new, currently authorized snapshot.
		fresh, openErr := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if openErr != nil {
			return empty, err
		}
		defer fresh.Rollback()
		current, authErr := s.authorizeCommand(ctx, fresh, cmd)
		if authErr != nil {
			return empty, authErr
		}
		if replay, ok, replayErr := workitemmutation.Replay(ctx, fresh.Client(), m, current.WorkItemID, digest); ok || replayErr != nil {
			return replay, replayErr
		}
		return empty, err
	}
	return result, nil
}

func (s *Service) authorizeCommand(ctx context.Context, tx *ent.Tx, cmd Command) (*ent.Change, error) {
	m := cmd.Meta
	current, err := tx.Change.Query().Where(ec.ID(cmd.ChangeID), ec.HasWorkItemWith(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil(), ticket.RecordClass("change_request"))).WithWorkItem().Only(ctx)
	if err != nil {
		return nil, common.NewNotFoundError("change")
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, m.ActorID, m.TenantID)
	if err != nil {
		return nil, err
	}
	role := authorization.EffectiveSessionRole(actor)
	if _, _, err = authorization.AuthorizeWorkItem(ctx, tx.Client(), current.WorkItemID, m.TenantID, role, "update"); err != nil {
		return nil, err
	}
	permission := "write"
	if cmd.Action == "authorize" && !qualifyingStandardPolicy(current, m.TenantID) {
		permission = "approve"
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: m.TenantID, ActorID: actor.ID, Role: role}, "change", permission); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Service) applyCommandTx(ctx context.Context, tx *ent.Tx, cmd Command, c *ent.Change, digest string) (workitemmutation.Result, error) {
	var empty workitemmutation.Result
	m, item := cmd.Meta, c.Edges.WorkItem
	invalid := func(message string) (workitemmutation.Result, error) {
		return empty, common.NewValidationError(message, nil)
	}
	if item.Version != m.ExpectedVersion {
		return empty, common.NewVersionConflictError("change", c.ID, m.ExpectedVersion, item.Version)
	}
	now := time.Now().UTC()
	assessment, err := assessmentDigest(c)
	if err != nil {
		return empty, err
	}
	target := item.Status
	professional := tx.Change.UpdateOneID(c.ID)
	evidenceOnly := false
	switch cmd.Action {
	case "submit":
		target = common.ChangeStatusSubmitted
		if item.Status != common.ChangeStatusDraft {
			return invalid("only draft changes can be submitted")
		}
		if strings.TrimSpace(c.ImplementationPlan) == "" || strings.TrimSpace(c.RollbackPlan) == "" {
			return invalid("implementation and rollback plans required")
		}
		if s.processEngine == nil {
			return invalid("change process engine required")
		}
	case "assess":
		evidenceOnly = true
		if !isChangeSubmitted(item.Status) || cmd.Evidence == "" || (cmd.Evidence == c.AssessmentEvidence && assessment == c.AssessmentDigest) {
			return invalid("submitted change and new assessment evidence required")
		}
		professional.SetAssessmentEvidence(cmd.Evidence).SetAssessmentDigest(assessment).SetAssessedBy(m.ActorID).SetAssessedAt(now)
	case "authorize":
		target = common.ChangeStatusApproved
		if !isChangeSubmitted(item.Status) {
			return invalid("submitted change required for authorization")
		}
		if c.AssessmentEvidence == "" || c.AssessedBy <= 0 || c.AssessedAt.IsZero() || c.AssessmentDigest != assessment {
			return invalid("current assessment required")
		}
		if !qualifyingStandardPolicy(c, m.TenantID) {
			if m.ActorID == item.OpenedByID {
				return invalid("cannot approve own change")
			}
			decision, err := tx.ProcessApprovalDecision.Query().Where(processapprovaldecision.ID(cmd.ApprovalDecisionID), processapprovaldecision.TenantID(m.TenantID), processapprovaldecision.ActorID(m.ActorID), processapprovaldecision.BusinessType("change"), processapprovaldecision.BusinessID(strconv.Itoa(item.ID)), processapprovaldecision.DecisionIn("approved", "rejected")).Only(ctx)
			if err != nil {
				return invalid("current CAB approval decision required")
			}
			instance, err := tx.ProcessInstance.Query().Where(processinstance.ID(decision.ProcessInstanceID), processinstance.TenantID(m.TenantID), processinstance.BusinessKey(fmt.Sprintf("change:%d", item.ID)), processinstance.BusinessID(item.ID)).Only(ctx)
			if err != nil || decision.CreatedAt.Before(c.AssessedAt) || instance.StartTime.Before(item.CreatedAt) {
				return invalid("CAB decision does not match current assessment and change")
			}
			if decision.Decision == "rejected" {
				target = common.ChangeStatusRejected
			}
		}
	case "schedule":
		target = common.ChangeStatusScheduled
		if (item.Status != common.ChangeStatusApproved && item.Status != common.ChangeStatusFailed) || c.AssessmentEvidence == "" || c.AssessedBy <= 0 || c.AssessedAt.IsZero() || c.AssessmentDigest != assessment {
			return invalid("current assessed and authorized change required for scheduling")
		}
		// The permissive legacy type helper is not authorization evidence. A
		// retry retains only an actual governed approval after this assessment.
		authorized, err := tx.AuditLog.Query().Where(auditlog.TenantID(m.TenantID), auditlog.Resource("work_item"), auditlog.Path(strconv.Itoa(item.ID)), auditlog.Action("change.authorize"), auditlog.OperationIDNotNil(), auditlog.ResultStatus(common.ChangeStatusApproved), auditlog.ResultVersionGT(0), auditlog.ResultVersionLTE(item.Version), auditlog.CreatedAtGTE(c.AssessedAt)).Exist(ctx)
		if err != nil {
			return empty, err
		}
		if !authorized || !common.IsValidChangeStatusTransition(common.ChangeStatusScheduled, common.ChangeStatusInProgress, c.Type) {
			return invalid("governed authorization and a legal scheduled implementation path required")
		}
		if cmd.PlannedStart == nil || cmd.PlannedEnd == nil || !cmd.PlannedEnd.After(*cmd.PlannedStart) {
			return invalid("valid implementation window required")
		}
		professional.SetPlannedStartDate(*cmd.PlannedStart).SetPlannedEndDate(*cmd.PlannedEnd)
	case "implement":
		target = common.ChangeStatusInProgress
		if c.AssessmentEvidence == "" || c.AssessmentDigest != assessment {
			return invalid("current assessment required for implementation")
		}
		if item.Status != "approved" && item.Status != "scheduled" {
			return invalid("authorized change required for implementation")
		}
		if c.Type != "emergency" && (c.PlannedStartDate.IsZero() || c.PlannedEndDate.IsZero() || now.Before(c.PlannedStartDate) || now.After(c.PlannedEndDate)) {
			return invalid("implementation must be within planned window")
		}
		professional.SetActualStartDate(now).ClearActualEndDate().ClearOutcome().ClearOutcomeEvidence().ClearReviewedBy().ClearReviewedAt().ClearReviewEvidence().ClearReviewDigest()
	case "record_outcome":
		evidenceOnly = true
		if item.Status != "in_progress" && item.Status != "failed" && item.Status != "rolled_back" {
			return invalid("implementation required before recording outcome")
		}
		if !validOutcome(cmd.Outcome) || cmd.Evidence == "" || cmd.ActualEnd == nil || c.ActualStartDate.IsZero() || cmd.ActualEnd.Before(c.ActualStartDate) || cmd.ActualEnd.After(now) {
			return invalid("explicit outcome, evidence and valid implementation times required")
		}
		if cmd.Outcome == c.Outcome && cmd.Evidence == c.OutcomeEvidence && cmd.ActualEnd.Equal(c.ActualEndDate) {
			return invalid("new outcome facts required")
		}
		professional.SetOutcome(cmd.Outcome).SetOutcomeEvidence(cmd.Evidence).SetActualEndDate(*cmd.ActualEnd).ClearReviewedBy().ClearReviewedAt().ClearReviewEvidence().ClearReviewDigest()
	case "review":
		evidenceOnly = true
		if item.Status != "in_progress" && item.Status != "failed" && item.Status != "rolled_back" {
			return invalid("implementation required before review")
		}
		binding, err := reviewDigest(ctx, tx, c, cmd.PIRID, m.TenantID, m.ActorID)
		if err != nil {
			return empty, err
		}
		if cmd.Evidence == "" || (binding == c.ReviewDigest && cmd.Evidence == c.ReviewEvidence) {
			return invalid("explicit new review evidence required")
		}
		professional.SetReviewDigest(binding).SetReviewEvidence(cmd.Evidence).SetReviewedBy(m.ActorID).SetReviewedAt(now)
	case "close":
		target = common.ChangeStatusCompleted
		binding, err := reviewDigest(ctx, tx, c, cmd.PIRID, m.TenantID, c.ReviewedBy)
		if err != nil {
			return empty, err
		}
		if cmd.Evidence == "" || c.ReviewedBy <= 0 || c.ReviewedAt.IsZero() || c.ReviewEvidence == "" || binding != c.ReviewDigest {
			return invalid("current explicit PIR review required for close")
		}
	case "cancel":
		target = common.ChangeStatusCancelled
		if cmd.Evidence == "" {
			return invalid("cancellation reason required")
		}
	}
	if !evidenceOnly && (target == item.Status || !common.IsValidChangeStatusTransition(item.Status, target, c.Type)) {
		return invalid(fmt.Sprintf("invalid change action %s from %s", cmd.Action, item.Status))
	}
	update := tx.Ticket.UpdateOneID(item.ID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil(), ticket.Version(m.ExpectedVersion)).SetVersion(m.ExpectedVersion + 1).SetUpdatedAt(now)
	if !evidenceOnly {
		update.SetStatus(target)
	}
	if cmd.Action == "close" {
		update.SetResolvedAt(now).SetClosedAt(now)
	}
	if cmd.Action == "cancel" {
		update.SetClosedAt(now)
	}
	if cmd.Action == "assess" && item.FirstResponseAt.IsZero() {
		update.SetFirstResponseAt(now)
	}
	updated, err := update.Save(ctx)
	if ent.IsNotFound(err) {
		return empty, common.NewVersionConflictError("change", c.ID, m.ExpectedVersion, item.Version)
	}
	if err != nil {
		return empty, err
	}
	saved, err := professional.Save(ctx)
	if err != nil {
		return empty, err
	}
	result := workitemmutation.Result{WorkItemID: item.ID, Version: updated.Version, Status: updated.Status}
	facts := map[string]any{"changeId": c.ID, "oldStatus": item.Status, "status": updated.Status, "outcome": saved.Outcome, "outcomeEvidence": saved.OutcomeEvidence, "actualStart": saved.ActualStartDate, "actualEnd": saved.ActualEndDate, "evidence": cmd.Evidence, "approvalDecisionId": cmd.ApprovalDecisionID, "pirId": cmd.PIRID, "statusChanged": target != item.Status}
	if cmd.Action == "authorize" {
		facts["authorizationKind"] = "cab_decision"
		if qualifyingStandardPolicy(c, m.TenantID) {
			facts["authorizationKind"] = "standard_policy"
			facts["standardPolicy"] = c.StandardPolicy
		}
	}
	if err = workitemmutation.RecordTx(ctx, tx, m, result, "change."+cmd.Action, digest, facts); err != nil {
		return empty, err
	}
	if cmd.Action == "submit" {
		key := "change_normal_flow"
		if c.Type == "emergency" {
			key = "change_emergency_flow"
		}
		startCtx := service.WithTrustedBPMNTenantContext(ctx, m.TenantID)
		startCtx = context.WithValue(startCtx, bpmn.BPMNUserIDContextKey, m.ActorID)
		_, err = s.processEngine.StartProcessTx(startCtx, tx, key, fmt.Sprintf("change:%d", item.ID), "change", item.ID, map[string]interface{}{"approval_required": !qualifyingStandardPolicy(c, m.TenantID), "requester_id": float64(m.ActorID), "work_item_id": item.ID, "record_class": "change_request", "version": result.Version, "status": result.Status, "change_id": c.ID})
		if err != nil {
			return empty, err
		}
	}
	return result, nil
}

func validOutcome(outcome string) bool {
	return outcome == "successful" || outcome == "failed" || outcome == "rolled_back"
}

func assessmentDigest(c *ent.Change) (string, error) {
	return workitemmutation.Digest(struct {
		Type, Justification, Risk, Impact, Implementation, Rollback string
		CIs                                                         []string
	}{c.Type, c.Justification, c.RiskLevel, c.ImpactScope, c.ImplementationPlan, c.RollbackPlan, c.AffectedCis})
}

func qualifyingStandardPolicy(c *ent.Change, tenantID int) bool {
	p := c.StandardPolicy
	stamp, ok := p["updatedAt"].(string)
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if !ok || err != nil || parsed.IsZero() {
		return false
	}
	scope, exists := p["affectedCis"]
	expected, err := workitemmutation.Digest(scope)
	actual, actualErr := workitemmutation.Digest(c.AffectedCis)
	if !exists || err != nil || actualErr != nil || actual != expected {
		return false
	}
	return c.Type == "standard" && c.StandardTemplateID > 0 && fmt.Sprint(p["templateId"]) == strconv.Itoa(c.StandardTemplateID) && fmt.Sprint(p["tenantId"]) == strconv.Itoa(tenantID) && p["active"] == true && p["approvalRequired"] == false && p["implementationPlan"] == c.ImplementationPlan && p["rollbackPlan"] == c.RollbackPlan && p["riskLevel"] == c.RiskLevel && p["impactScope"] == c.ImpactScope
}

// Bind every persisted PIR fact, including its update time, to the independent
// implementation result. A default result or an old review cannot authorize close.
func reviewDigest(ctx context.Context, tx *ent.Tx, c *ent.Change, pirID, tenantID, reviewerID int) (string, error) {
	invalid := func() (string, error) {
		return "", common.NewValidationError("explicit current PIR and implementation outcome required", nil)
	}
	if !validOutcome(c.Outcome) || c.OutcomeEvidence == "" || c.ActualStartDate.IsZero() || c.ActualEndDate.Before(c.ActualStartDate) || reviewerID <= 0 {
		return invalid()
	}
	// PIR edits use another owning service. Lock the existing tenant row before
	// reading its digest so it cannot change between review validation and commit.
	// PostgreSQL RR raises a serialization conflict if it changed since snapshot.
	rows, err := tx.QueryContext(ctx, "SELECT id FROM change_pi_rs WHERE id=$1 AND tenant_id=$2 AND change_pir=$3 FOR UPDATE", pirID, tenantID, c.ID)
	if err != nil {
		return "", err
	}
	found := rows.Next()
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return "", err
	}
	if err = rows.Close(); err != nil {
		return "", err
	}
	if !found {
		return invalid()
	}
	pir, err := tx.ChangePIR.Query().Where(changepir.ID(pirID), changepir.TenantID(tenantID), changepir.HasChangeWith(ec.ID(c.ID))).Only(ctx)
	if err != nil {
		return invalid()
	}
	if pir.ReviewerID != reviewerID || pir.ReviewDate.IsZero() || pir.ReviewDate.Before(c.ActualEndDate) || pir.ReviewDate.After(time.Now()) || pir.OverallResult != c.Outcome || (strings.TrimSpace(pir.SuccessSummary) == "" && strings.TrimSpace(pir.IssuesEncountered) == "") {
		return invalid()
	}
	return workitemmutation.Digest(struct {
		PIR               *ent.ChangePIR
		Outcome, Evidence string
		Start, End        time.Time
	}{pir, c.Outcome, c.OutcomeEvidence, c.ActualStartDate, c.ActualEndDate})
}

// IsSuccessfulOutcome evaluates the professional implementation result.
// A terminal lifecycle status does not establish a successful implementation.
func IsSuccessfulOutcome(outcome string) bool { return outcome == "successful" }
