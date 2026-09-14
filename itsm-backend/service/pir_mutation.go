package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/change"
	"itsm-backend/ent/changepir"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
)

// PIRMutationResult is the immutable mutation receipt, never a reread of mutable PIR facts.
type PIRMutationResult struct {
	workitemmutation.Result
	PIRID int `json:"pirId"`
}

func (s *ChangePIRService) SetDirectorySnapshot(directory database.DirectorySnapshot) {
	s.directory = directory
}

func (s *ChangePIRService) CreatePIR(ctx context.Context, req *dto.CreateChangePIRRequest, meta workitemmutation.Meta) (PIRMutationResult, error) {
	if req == nil {
		return PIRMutationResult{}, common.NewValidationError("PIR input required", nil)
	}
	copy := *req
	return s.mutatePIR(ctx, meta, copy.ChangeID, 0, "create", &copy, nil)
}

func (s *ChangePIRService) UpdatePIR(ctx context.Context, id int, req *dto.UpdateChangePIRRequest, meta workitemmutation.Meta) (PIRMutationResult, error) {
	if req == nil {
		return PIRMutationResult{}, common.NewValidationError("PIR input required", nil)
	}
	copy := *req
	return s.mutatePIR(ctx, meta, copy.ChangeID, id, "update", nil, &copy)
}

func (s *ChangePIRService) DeletePIR(ctx context.Context, id int, req *dto.DeleteChangePIRRequest, meta workitemmutation.Meta) (PIRMutationResult, error) {
	if req == nil {
		return PIRMutationResult{}, common.NewValidationError("PIR input required", nil)
	}
	return s.mutatePIR(ctx, meta, req.ChangeID, id, "delete", nil, nil)
}

func (s *ChangePIRService) authorizePIR(ctx context.Context, tx *ent.Tx, meta workitemmutation.Meta, changeID int, action string) (*ent.Change, error) {
	c, err := tx.Change.Query().Where(change.ID(changeID), change.HasWorkItemWith(ticket.TenantID(meta.TenantID), ticket.DeletedAtIsNil(), ticket.RecordClass("change_request"))).WithWorkItem().Only(ctx)
	if err != nil {
		return nil, common.NewNotFoundError("change")
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, meta.ActorID, meta.TenantID)
	if err != nil {
		return nil, err
	}
	role := authorization.EffectiveSessionRole(actor)
	permission, rowAction := "write", "update"
	if action == "delete" {
		permission, rowAction = "delete", "delete"
	}
	if _, _, err = authorization.AuthorizeWorkItem(ctx, tx.Client(), c.WorkItemID, meta.TenantID, role, rowAction); err != nil {
		return nil, err
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: meta.TenantID, ActorID: actor.ID, Role: role}, "change", permission); err != nil {
		return nil, err
	}
	return c, nil
}

func replayPIR(ctx context.Context, tx *ent.Tx, meta workitemmutation.Meta, itemID int, digest string) (PIRMutationResult, bool, error) {
	result, found, err := workitemmutation.Replay(ctx, tx.Client(), meta, itemID, digest)
	if err != nil || !found {
		return PIRMutationResult{}, found, err
	}
	row, err := tx.AuditLog.Query().Where(auditlog.TenantID(meta.TenantID), auditlog.UserID(meta.ActorID), auditlog.OperationID(meta.OperationID)).Only(ctx)
	if err != nil {
		return PIRMutationResult{}, false, err
	}
	var facts struct {
		PIRID int `json:"pirId"`
	}
	if row.RequestBody == nil {
		return PIRMutationResult{}, false, common.NewConflictError("PIR receipt", "missing immutable facts")
	}
	if err = json.Unmarshal([]byte(*row.RequestBody), &facts); err != nil {
		return PIRMutationResult{}, false, err
	}
	if facts.PIRID <= 0 {
		return PIRMutationResult{}, false, common.NewConflictError("PIR receipt", "missing immutable identity")
	}
	return PIRMutationResult{Result: result, PIRID: facts.PIRID}, true, nil
}

func (s *ChangePIRService) mutatePIR(ctx context.Context, m workitemmutation.Meta, changeID, id int, action string, create *dto.CreateChangePIRRequest, patch *dto.UpdateChangePIRRequest) (out PIRMutationResult, resultErr error) {
	empty := PIRMutationResult{}
	if m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.Source) == "" || strings.TrimSpace(m.OperationID) == "" || changeID <= 0 || (action != "create" && id <= 0) {
		return empty, common.NewValidationError("trusted actor, tenant, version, source, operationId and PIR identity required", nil)
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	normalize := func(fields ...**string) {
		for _, p := range fields {
			if *p != nil {
				v := strings.TrimSpace(**p)
				*p = &v
			}
		}
	}
	valid := func(value string) bool {
		return value == "successful" || value == "partially_successful" || value == "failed" || value == "rolled_back"
	}
	if create != nil {
		create.PIRMutationRequest = dto.PIRMutationRequest{}
		create.OverallResult = strings.TrimSpace(create.OverallResult)
		normalize(&create.SuccessSummary, &create.IssuesEncountered, &create.LessonsLearned, &create.ImprovementRecommendations, &create.RollbackReason)
		if !valid(create.OverallResult) {
			return empty, common.NewValidationError("explicit valid PIR result required", nil)
		}
		if (create.ActualStartTime == nil) != (create.ActualEndTime == nil) || (create.ActualStartTime != nil && (create.ActualEndTime.Before(*create.ActualStartTime) || create.ActualEndTime.After(time.Now()))) {
			return empty, common.NewValidationError("valid PIR actual time range required", nil)
		}
	}
	if patch != nil {
		patch.PIRMutationRequest = dto.PIRMutationRequest{}
		normalize(&patch.OverallResult, &patch.SuccessSummary, &patch.IssuesEncountered, &patch.LessonsLearned, &patch.ImprovementRecommendations)
		if patch.OverallResult != nil && !valid(*patch.OverallResult) {
			return empty, common.NewValidationError("valid PIR result required", nil)
		}
	}
	digest, err := workitemmutation.Digest(struct {
		Action                   string
		ChangeID, PIRID, Version int
		Create                   *dto.CreateChangePIRRequest
		Patch                    *dto.UpdateChangePIRRequest
	}{action, changeID, id, m.ExpectedVersion, create, patch})
	if err != nil {
		return empty, err
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	attempted := false
	defer func() {
		if resultErr == nil || !attempted || tx.Rollback() != nil {
			return
		}
		fresh, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return
		}
		defer fresh.Rollback()
		c, err := s.authorizePIR(ctx, fresh, m, changeID, action)
		if err != nil {
			resultErr = err
			return
		}
		replay, found, err := replayPIR(ctx, fresh, m, c.WorkItemID, digest)
		if err != nil {
			resultErr = err
			return
		}
		if found {
			out = replay
			resultErr = nil
		}
	}()
	c, err := s.authorizePIR(ctx, tx, m, changeID, action)
	if err != nil {
		return empty, err
	}
	if replay, found, err := replayPIR(ctx, tx, m, c.WorkItemID, digest); found || err != nil {
		return replay, err
	}
	item := c.Edges.WorkItem
	if item.Version != m.ExpectedVersion {
		return empty, common.NewVersionConflictError("change", changeID, m.ExpectedVersion, item.Version)
	}
	if item.Status == "completed" || item.Status == "cancelled" || item.Status == "rejected" {
		return empty, common.NewValidationError("terminal change PIR is locked", nil)
	}
	if err = workitemmutation.RequireSettledChangeCallbacks(ctx, tx, m.TenantID, item.ID); err != nil {
		if _, unresolved := err.(*workitemmutation.UnresolvedChangeCallbackError); unresolved {
			err = common.NewConflictError("Change workflow", err.Error())
		}
		return empty, err
	}
	if err := s.execution.BindEnt(ctx, tx, m.TenantID); err != nil {
		return empty, pirExecutionFailure(err)
	}
	if err := s.execution.RequireEntMembers(ctx, tx, m.TenantID, item.ID); err != nil {
		return empty, pirExecutionFailure(err)
	}
	// The existing-row lock can itself fail under RR contention. Include it
	// in confirmed-rollback receipt recovery without retrying business effects.
	attempted = true
	var pir *ent.ChangePIR
	if action != "create" {
		// Match review/close: PIR row precedes the WorkItem CAS. RR detects a
		// concurrent close or task acceptance through the same WorkItem tuple.
		rows, err := tx.QueryContext(ctx, "SELECT id FROM change_pi_rs WHERE id=$1 AND tenant_id=$2 AND change_pir=$3 FOR UPDATE", id, m.TenantID, changeID)
		if err != nil {
			return empty, err
		}
		found := rows.Next()
		rowErr := rows.Err()
		closeErr := rows.Close()
		if rowErr != nil {
			return empty, rowErr
		}
		if closeErr != nil {
			return empty, closeErr
		}
		if !found {
			return empty, common.NewNotFoundError("PIR")
		}
		pir, err = tx.ChangePIR.Query().Where(changepir.ID(id), changepir.TenantID(m.TenantID), changepir.HasChangeWith(change.ID(changeID))).Only(ctx)
		if err != nil {
			return empty, err
		}
	} else {
		exists, err := tx.ChangePIR.Query().Where(changepir.TenantID(m.TenantID), changepir.HasChangeWith(change.ID(changeID))).Exist(ctx)
		if err != nil {
			return empty, err
		}
		if exists {
			return empty, common.NewConflictError("PIR", "already exists")
		}
	}
	if patch != nil && !pirPatchChanges(pir, patch) {
		return empty, common.NewValidationError("new PIR facts required", nil)
	}
	now := time.Now().UTC()
	saved, err := tx.Ticket.UpdateOneID(item.ID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil(), ticket.Version(m.ExpectedVersion)).SetVersion(m.ExpectedVersion + 1).SetUpdatedAt(now).Save(ctx)
	if err != nil {
		return empty, err
	}
	if create != nil {
		b := tx.ChangePIR.Create().SetChangeID(changeID).SetTenantID(m.TenantID).SetReviewerID(m.ActorID).SetReviewDate(now).SetOverallResult(create.OverallResult).SetObjectivesAchieved(create.ObjectivesAchieved).SetRollbackPerformed(create.RollbackPerformed)
		if create.SuccessSummary != nil {
			b.SetSuccessSummary(*create.SuccessSummary)
		}
		if create.IssuesEncountered != nil {
			b.SetIssuesEncountered(*create.IssuesEncountered)
		}
		if create.LessonsLearned != nil {
			b.SetLessonsLearned(*create.LessonsLearned)
		}
		if create.ImprovementRecommendations != nil {
			b.SetImprovementRecommendations(*create.ImprovementRecommendations)
		}
		if create.RollbackReason != nil {
			b.SetRollbackReason(*create.RollbackReason)
		}
		if create.ActualStartTime != nil {
			b.SetActualStartTime(*create.ActualStartTime).SetActualEndTime(*create.ActualEndTime).SetActualDurationMinutes(int(create.ActualEndTime.Sub(*create.ActualStartTime).Minutes()))
		}
		pir, err = b.Save(ctx)
		if err != nil {
			return empty, err
		}
		id = pir.ID
	} else if patch != nil {
		b := tx.ChangePIR.UpdateOneID(id).SetReviewerID(m.ActorID).SetReviewDate(now).SetUpdatedAt(now)
		if patch.OverallResult != nil {
			b.SetOverallResult(*patch.OverallResult)
		}
		if patch.ObjectivesAchieved != nil {
			b.SetObjectivesAchieved(*patch.ObjectivesAchieved)
		}
		if patch.SuccessSummary != nil {
			b.SetSuccessSummary(*patch.SuccessSummary)
		}
		if patch.IssuesEncountered != nil {
			b.SetIssuesEncountered(*patch.IssuesEncountered)
		}
		if patch.LessonsLearned != nil {
			b.SetLessonsLearned(*patch.LessonsLearned)
		}
		if patch.ImprovementRecommendations != nil {
			b.SetImprovementRecommendations(*patch.ImprovementRecommendations)
		}
		if _, err = b.Save(ctx); err != nil {
			return empty, err
		}
	} else {
		if err = tx.ChangePIR.DeleteOneID(id).Exec(ctx); err != nil {
			return empty, err
		}
	}
	result := PIRMutationResult{Result: workitemmutation.Result{WorkItemID: item.ID, Version: saved.Version, Status: saved.Status}, PIRID: id}
	facts := struct {
		PIRID    int                         `json:"pirId"`
		ChangeID int                         `json:"changeId"`
		Create   *dto.CreateChangePIRRequest `json:"create,omitempty"`
		Patch    *dto.UpdateChangePIRRequest `json:"patch,omitempty"`
	}{id, changeID, create, patch}
	if err = workitemmutation.RecordTx(ctx, tx, m, result.Result, "change.pir_"+action, digest, facts); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func pirPatchChanges(p *ent.ChangePIR, u *dto.UpdateChangePIRRequest) bool {
	for _, field := range []struct {
		patch   *string
		current string
	}{{u.OverallResult, p.OverallResult}, {u.SuccessSummary, p.SuccessSummary}, {u.IssuesEncountered, p.IssuesEncountered}, {u.LessonsLearned, p.LessonsLearned}, {u.ImprovementRecommendations, p.ImprovementRecommendations}} {
		if field.patch != nil && *field.patch != field.current {
			return true
		}
	}
	return u.ObjectivesAchieved != nil && *u.ObjectivesAchieved != p.ObjectivesAchieved
}

func pirExecutionFailure(err error) error {
	if errors.Is(err, executionscope.ErrDenied) {
		return common.NewForbiddenError("PIR execution scope denied")
	}
	return fmt.Errorf("PIR execution scope: %w", err)
}
