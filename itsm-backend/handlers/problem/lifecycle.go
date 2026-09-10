package problem

import (
	"context"
	"database/sql"
	"errors"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	ep "itsm-backend/ent/problem"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

// ResolutionEvidence is a projection of authoritative persisted evidence.
type ResolutionEvidence struct {
	RootCause, PermanentSolution string
	Verified                     bool
	VerificationNote             string
}

func ValidateResolution(e ResolutionEvidence) error {
	if strings.TrimSpace(e.RootCause) == "" || strings.TrimSpace(e.PermanentSolution) == "" || !e.Verified || strings.TrimSpace(e.VerificationNote) == "" {
		return errors.New("verified permanent resolution required")
	}
	return nil
}

type Command struct {
	Meta                             workitemmutation.Meta
	ProblemID                        int
	SolutionID                       int
	Action, Reason, VerificationNote string
	// Investigation is supplied only by the investigation creation entry point.
	Investigation *dto.CreateProblemInvestigationRequest
}

func resolutionDigest(p *ent.Problem) (string, error) {
	return workitemmutation.Digest(struct{ RootCause, Resolution string }{p.RootCause, p.Resolution})
}

func (s *Service) authorizeCommand(ctx context.Context, tx *ent.Tx, cmd Command) (*ent.Problem, error) {
	m := cmd.Meta
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, m.ActorID, m.TenantID)
	if err != nil {
		return nil, err
	}
	p, err := tx.Problem.Query().Where(ep.ID(cmd.ProblemID), problemTenantScope(m.TenantID, ticket.RecordClass("problem"))).WithWorkItem().Only(ctx)
	if err != nil {
		return nil, common.NewNotFoundError("problem")
	}
	if _, _, err = authorization.AuthorizeWorkItem(ctx, tx.Client(), p.WorkItemID, m.TenantID, authorization.EffectiveSessionRole(actor), "update"); err != nil {
		return nil, err
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: m.TenantID, ActorID: actor.ID, Role: authorization.EffectiveSessionRole(actor)}, "problem", "write"); err != nil {
		return nil, common.NewForbiddenError("insufficient current Problem permission")
	}
	return p, nil
}

func (s *Service) ApplyCommand(ctx context.Context, cmd Command) (workitemmutation.Result, error) {
	var empty workitemmutation.Result
	if s.client == nil || s.investigations == nil {
		return empty, errors.New("problem transaction repository unavailable")
	}
	cmd.Action = strings.TrimSpace(cmd.Action)
	cmd.Reason = strings.TrimSpace(cmd.Reason)
	cmd.VerificationNote = strings.TrimSpace(cmd.VerificationNote)
	m := cmd.Meta
	if m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.OperationID) == "" || strings.TrimSpace(m.Source) == "" {
		return empty, common.NewValidationError("trusted actor, tenant, version, source and operationId required", nil)
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	p, err := s.authorizeCommand(ctx, tx, cmd)
	if err != nil {
		return empty, err
	}
	digest, err := workitemmutation.Digest(struct {
		ProblemID, Version, SolutionID   int
		Action, Reason, VerificationNote string
		Investigation                    *dto.CreateProblemInvestigationRequest
	}{cmd.ProblemID, m.ExpectedVersion, cmd.SolutionID, cmd.Action, cmd.Reason, cmd.VerificationNote, cmd.Investigation})
	if err != nil {
		return empty, err
	}
	if result, ok, err := workitemmutation.Replay(ctx, tx.Client(), m, p.WorkItemID, digest); ok || err != nil {
		return result, err
	}
	result, err := s.applyCommandTx(ctx, tx, cmd, digest)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		replayTx, openErr := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if openErr != nil {
			return result, err
		}
		defer replayTx.Rollback()
		if _, authErr := s.authorizeCommand(ctx, replayTx, cmd); authErr != nil {
			return empty, authErr
		}
		if replay, ok, replayErr := workitemmutation.Replay(ctx, replayTx.Client(), m, p.WorkItemID, digest); ok || replayErr != nil {
			return replay, replayErr
		}
	}
	return result, err
}

func (s *Service) applyCommandTx(ctx context.Context, tx *ent.Tx, cmd Command, digest string) (workitemmutation.Result, error) {
	var empty workitemmutation.Result
	m := cmd.Meta
	p, err := s.authorizeCommand(ctx, tx, cmd)
	if err != nil {
		return empty, err
	}
	item := p.Edges.WorkItem
	if item.Version != m.ExpectedVersion {
		return empty, common.NewVersionConflictError("problem", p.ID, m.ExpectedVersion, item.Version)
	}
	target := item.Status
	valid := false
	switch cmd.Action {
	case "investigate":
		target = "investigating"
		valid = item.Status == "open" || item.Status == "identified" || item.Status == "in_progress" || (item.Status == "investigating" && cmd.Investigation != nil)
	case "verify_resolution":
		valid = item.Status == "investigating" || item.Status == "identified" || item.Status == "in_progress"
	case "select_resolution":
		valid = item.Status == "investigating" || item.Status == "identified" || item.Status == "in_progress"
	case "resolve":
		target = "resolved"
		valid = item.Status == "open" || item.Status == "investigating" || item.Status == "identified" || item.Status == "in_progress"
	case "close":
		target = "closed"
		valid = item.Status == "resolved"
	case "reopen":
		target = "investigating"
		valid = item.Status == "resolved" || item.Status == "closed"
	default:
		return empty, common.NewValidationError("unsupported problem action", nil)
	}
	if !valid {
		return empty, common.NewValidationError("invalid problem lifecycle transition", nil)
	}
	evidenceDigest, err := resolutionDigest(p)
	if err != nil {
		return empty, err
	}
	if cmd.Action == "verify_resolution" {
		if err := ValidateResolution(ResolutionEvidence{p.RootCause, p.Resolution, true, cmd.VerificationNote}); err != nil {
			return empty, common.NewValidationError(err.Error(), err)
		}
	}
	if cmd.Action == "resolve" || cmd.Action == "close" {
		expected := item.Version
		if cmd.Action == "close" {
			expected--
		} // resolve is the sole permitted intervening mutation.
		verified := p.VerifiedVersion == expected && p.VerificationDigest == evidenceDigest && p.VerifiedBy > 0 && !p.VerifiedAt.IsZero()
		if err := ValidateResolution(ResolutionEvidence{p.RootCause, p.Resolution, verified, p.VerificationNote}); err != nil {
			return empty, common.NewValidationError(err.Error(), err)
		}
	}
	// A problem may only be resolved once every dependency explicitly marked as a
	// required fix has an authoritative successful Change outcome. Optional
	// associations never block, and a dependency whose outcome is unreadable fails
	// closed rather than counting as satisfied.
	if cmd.Action == "resolve" {
		dependencies, err := service.NewWorkItemRelationService(s.client, s.directory).RequiredFixDependenciesTx(ctx, tx, m, item.ID)
		if err != nil {
			return empty, err
		}
		for _, dependency := range dependencies {
			if !service.RequiresProblemVerification(dependency.Outcome) {
				return empty, common.NewValidationError("required fix dependency has no successful change outcome", nil)
			}
		}
	}
	now := time.Now().UTC()
	update := tx.Ticket.UpdateOneID(item.ID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil(), ticket.Version(m.ExpectedVersion)).SetVersion(m.ExpectedVersion + 1).SetUpdatedAt(now).SetStatus(target)
	if cmd.Action == "resolve" {
		update.SetResolvedAt(now).ClearClosedAt()
	}
	if cmd.Action == "close" {
		update.SetClosedAt(now)
	}
	updated, err := update.Save(ctx)
	if ent.IsNotFound(err) {
		return empty, common.NewVersionConflictError("problem", p.ID, m.ExpectedVersion, item.Version)
	}
	if err != nil {
		return empty, err
	}
	if cmd.Action == "select_resolution" {
		rows, err := tx.QueryContext(ctx, `SELECT solution_description,solution_type FROM problem_solutions WHERE id=$1 AND problem_id=$2`, cmd.SolutionID, p.ID)
		if err != nil {
			return empty, err
		}
		var body, kind string
		if !rows.Next() {
			rows.Close()
			return empty, common.NewNotFoundError("problem solution")
		}
		err = rows.Scan(&body, &kind)
		rows.Close()
		if err != nil {
			return empty, err
		}
		if (kind != "fix" && kind != "prevention" && kind != "process") || strings.TrimSpace(body) == "" {
			return empty, common.NewValidationError("permanent solution required", nil)
		}
		if _, err = tx.Problem.UpdateOneID(p.ID).SetResolution(body).Save(ctx); err != nil {
			return empty, err
		}
		p.Resolution = body
		evidenceDigest, err = resolutionDigest(p)
		if err != nil {
			return empty, err
		}
	}
	if cmd.Action == "verify_resolution" {
		_, err = tx.Problem.UpdateOneID(p.ID).SetVerifiedVersion(updated.Version).SetVerificationDigest(evidenceDigest).SetVerifiedBy(m.ActorID).SetVerifiedAt(now).SetVerificationNote(cmd.VerificationNote).Save(ctx)
		if err != nil {
			return empty, err
		}
	}
	if cmd.Action == "reopen" {
		sla := service.NewTicketSLAService(s.client, s.logger)
		sla.SetDirectorySnapshot(s.directory)
		if err = sla.ResetCycleTx(ctx, tx, item, now, m); err != nil {
			return empty, err
		}
		updated, err = tx.Ticket.UpdateOneID(item.ID).ClearFirstResponseAt().ClearResolvedAt().ClearClosedAt().Save(ctx)
		if err != nil {
			return empty, err
		}
		if _, err = tx.Problem.UpdateOneID(p.ID).ClearVerifiedVersion().ClearVerificationDigest().ClearVerifiedBy().ClearVerifiedAt().ClearVerificationNote().Save(ctx); err != nil {
			return empty, err
		}
	}
	investigationID := 0
	if cmd.Investigation != nil {
		if cmd.Action != "investigate" || cmd.Investigation.ProblemID != p.ID {
			return empty, common.NewValidationError("invalid investigation command", nil)
		}
		investigationID, err = s.investigations.createInvestigationTx(ctx, tx, cmd.Investigation, m.TenantID, now)
		if err != nil {
			return empty, err
		}
		if _, err = s.authorizeCommand(ctx, tx, cmd); err != nil {
			return empty, err
		}
	}
	result := workitemmutation.Result{WorkItemID: item.ID, Version: updated.Version, Status: updated.Status}
	facts := map[string]any{"problemId": p.ID, "oldStatus": item.Status, "status": updated.Status, "version": updated.Version, "cycleNumber": updated.SLACycleNumber, "previousCycleNumber": item.SLACycleNumber, "reason": cmd.Reason, "verificationDigest": evidenceDigest, "verificationNote": cmd.VerificationNote, "solutionId": cmd.SolutionID, "investigationId": investigationID}
	if err = workitemmutation.RecordTx(ctx, tx, m, result, "problem."+cmd.Action, digest, facts); err != nil {
		return empty, err
	}
	return result, nil
}

func (r *EntRepository) createInvestigationTx(ctx context.Context, tx *ent.Tx, req *dto.CreateProblemInvestigationRequest, tenantID int, now time.Time) (int, error) {
	_, err := tx.User.Query().Where(user.ID(req.InvestigatorID), user.TenantID(tenantID), user.Active(true)).Only(ctx)
	if err != nil {
		return 0, common.NewForbiddenError("select an active investigator in the customer tenant")
	}
	rows, err := tx.QueryContext(ctx, `INSERT INTO problem_investigations (problem_id,investigator_id,estimated_completion_date,investigation_summary,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$5) RETURNING id`, req.ProblemID, req.InvestigatorID, req.EstimatedCompletionDate, req.InvestigationSummary, now)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, errors.New("investigation creation returned no record")
	}
	var id int
	err = rows.Scan(&id)
	return id, err
}
