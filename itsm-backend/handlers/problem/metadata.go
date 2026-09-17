package problem

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	assignment "itsm-backend/handlers/common/workitemassignment"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

type MetadataCommand struct {
	Evidence          *EvidenceMetadata
	RootCauseAnalysis *RootCauseMetadata
	Meta              workitemmutation.Meta
	ProblemID         int
	Patch             dto.UpdateProblemRequest
}

func normalizeProblemPatch(p dto.UpdateProblemRequest) dto.UpdateProblemRequest {
	for _, v := range []**string{&p.Title, &p.Description, &p.Priority, &p.RootCause, &p.Workaround, &p.Resolution, &p.Impact} {
		if *v != nil {
			s := strings.TrimSpace(**v)
			*v = &s
		}
	}
	p.AssignmentReason = strings.TrimSpace(p.AssignmentReason)
	return p
}

func canAssignProblemStatus(status string) bool {
	switch status {
	case "open", "investigating", "identified", "in_progress":
		return true
	}
	return false
}

func (s *Service) ApplyMetadata(ctx context.Context, cmd MetadataCommand) (out workitemmutation.Result, resultErr error) {
	var empty workitemmutation.Result
	m := cmd.Meta
	if cmd.Evidence != nil {
		if cmd.RootCauseAnalysis != nil {
			return empty, common.NewValidationError("one evidence family required", nil)
		}
		if err := cmd.Evidence.validate(cmd.ProblemID); err != nil {
			return empty, err
		}
	}
	if cmd.RootCauseAnalysis != nil {
		if err := cmd.RootCauseAnalysis.validate(cmd.ProblemID); err != nil {
			return empty, err
		}
		if req := cmd.RootCauseAnalysis.Create; req != nil {
			cmd.Patch.RootCause = &req.RootCauseDescription
		}
		if req := cmd.RootCauseAnalysis.Update; req != nil {
			cmd.Patch.RootCause = req.RootCauseDescription
		}
	}
	cmd.Patch = normalizeProblemPatch(cmd.Patch)
	p := cmd.Patch
	if s.client == nil {
		return empty, errors.New("problem transaction repository unavailable")
	}
	if m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.Source) == "" || strings.TrimSpace(m.OperationID) == "" {
		return empty, common.NewValidationError("trusted actor and tenant, version required, source and operationId required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return empty, common.NewForbiddenError("tenant context mismatch")
	}
	if p.Status != nil {
		return empty, common.NewValidationError("status changes require a problem lifecycle command", nil)
	}
	if p.Version != 0 && p.Version != m.ExpectedVersion {
		return empty, common.NewValidationError("patch version must match observed version", nil)
	}
	if p.OperationID != "" && p.OperationID != m.OperationID {
		return empty, common.NewValidationError("patch operationId must match command", nil)
	}
	p.Version = 0
	p.OperationID = ""
	cmd.Patch = p
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	digest, err := workitemmutation.Digest(struct {
		Action             string
		ProblemID, Version int
		Patch              dto.UpdateProblemRequest
		RCA                *RootCauseMetadata
		Evidence           *EvidenceMetadata
	}{"metadata", cmd.ProblemID, m.ExpectedVersion, p, cmd.RootCauseAnalysis, cmd.Evidence})
	if err != nil {
		return empty, err
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	attempted := false
	defer func() {
		if resultErr == nil || !attempted {
			return
		}
		if tx.Rollback() != nil {
			return
		}
		fresh, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return
		}
		defer fresh.Rollback()
		current, err := s.authorizeCommand(ctx, fresh, Command{Meta: m, ProblemID: cmd.ProblemID, Action: "metadata"})
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
	current, err := s.authorizeCommand(ctx, tx, Command{Meta: m, ProblemID: cmd.ProblemID, Action: "metadata"})
	if err != nil {
		return empty, err
	}
	if result, ok, err := workitemmutation.Replay(ctx, tx.Client(), m, current.WorkItemID, digest); ok || err != nil {
		return result, err
	}
	item := current.Edges.WorkItem
	if item.Version != m.ExpectedVersion {
		return empty, common.NewVersionConflictError("problem", cmd.ProblemID, m.ExpectedVersion, item.Version)
	}
	if p.AssigneeID != nil && *p.AssigneeID != item.AssigneeID && !canAssignProblemStatus(item.Status) {
		return empty, common.NewValidationError("terminal or unsupported Problem assignment is locked", nil)
	}
	changed := cmd.RootCauseAnalysis != nil || cmd.Evidence != nil
	var classificationBefore, classificationAfter []service.CTINode
	for _, pair := range []struct {
		next *string
		old  string
	}{{p.Title, item.Title}, {p.Description, item.Description}, {p.Priority, item.Priority}, {p.RootCause, current.RootCause}, {p.Workaround, current.Workaround}, {p.Resolution, current.Resolution}, {p.Impact, current.Impact}} {
		if pair.next != nil && *pair.next != pair.old {
			changed = true
		}
	}
	if p.Title != nil && *p.Title == "" {
		return empty, common.NewValidationError("title required", nil)
	}
	if p.Priority != nil && !isValidProblemPriority(*p.Priority) {
		return empty, common.NewValidationError("invalid problem priority", nil)
	}
	if p.AssigneeID != nil {
		if *p.AssigneeID <= 0 {
			return empty, common.NewValidationError("assignee must be a positive user ID", nil)
		}
		if *p.AssigneeID != item.AssigneeID {
			changed = true
			if item.AssigneeID > 0 && p.AssignmentReason == "" {
				return empty, common.NewValidationError("assignment reason required", nil)
			}
		}
	}
	if p.CategoryID != nil {
		if *p.CategoryID < 0 {
			return empty, common.NewValidationError("invalid category", nil)
		}
		classificationChanged := *p.CategoryID != item.CategoryID
		// 分类纠正契约：目标必须是当前有效的完整三级路径（或显式清除），且必须给出原因。
		if err := service.RequireCTICorrectionReason(p.ClassificationReason, classificationChanged); err != nil {
			return empty, err
		}
		beforePath, err := service.CTICorrectionBeforePathTx(ctx, tx, m.TenantID, item.CategoryID)
		if err != nil {
			return empty, err
		}
		targetPath, err := service.ValidateCTICorrectionTargetTx(ctx, tx, m.TenantID, *p.CategoryID, service.CTICorrectionTargetPolicy{AllowClear: true})
		if err != nil {
			return empty, err
		}
		classificationBefore, classificationAfter = beforePath, targetPath
		changed = changed || classificationChanged
	}
	if !changed {
		return empty, common.NewValidationError("new metadata facts required", nil)
	}
	if err := s.requireExecutionTx(ctx, tx, m.TenantID, item.ID); err != nil {
		return empty, err
	}
	// Assignment owns the aggregate increment when the owner changes. The
	// remaining metadata and operation receipt use that exact resulting version.
	expected, version := m.ExpectedVersion, m.ExpectedVersion+1
	if p.AssigneeID != nil {
		attempted = true
		err = assignment.WithLifecycleWriter(ctx, tx, s.directory, m.ActorID, m.TenantID, service.EnqueueWorkItemAssignment, func(writer *assignment.Writer, actor *ent.User) error {
			assigned, writeErr := writer.Apply(ctx, tx.Client(), assignment.Command{
				WorkItemID: item.ID, TenantID: m.TenantID, ActorID: actor.ID,
				ActorTenantID: actor.TenantID, AssigneeID: *p.AssigneeID,
				ExpectedVersion: m.ExpectedVersion, Source: m.Source, Reason: p.AssignmentReason,
			})
			if writeErr == nil {
				expected = assigned.Version
				if assigned.Version != m.ExpectedVersion {
					version = assigned.Version
				}
			}
			return writeErr
		})
		if err != nil {
			return empty, err
		}
	}
	update := tx.Ticket.UpdateOneID(item.ID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil(), ticket.Version(expected)).SetVersion(version).SetUpdatedAt(time.Now().UTC())
	if p.Title != nil {
		update.SetTitle(*p.Title)
	}
	if p.Description != nil {
		update.SetDescription(*p.Description)
	}
	if p.Priority != nil {
		update.SetPriority(*p.Priority)
	}
	if p.CategoryID != nil {
		if *p.CategoryID == 0 {
			update.ClearCategoryID()
		} else {
			update.SetCategoryID(*p.CategoryID)
		}
	}
	professional := tx.Problem.UpdateOneID(current.ID)
	if p.RootCause != nil {
		professional.SetRootCause(*p.RootCause)
	}
	if p.Resolution != nil {
		professional.SetResolution(*p.Resolution)
	}
	if p.Workaround != nil {
		professional.SetWorkaround(*p.Workaround)
	}
	if p.Impact != nil {
		professional.SetImpact(*p.Impact)
	}
	if cmd.RootCauseAnalysis != nil || (p.RootCause != nil && *p.RootCause != current.RootCause) || (p.Resolution != nil && *p.Resolution != current.Resolution) {
		professional.ClearVerifiedVersion().ClearVerificationDigest().ClearVerifiedBy().ClearVerifiedAt().ClearVerificationNote()
	}
	attempted = true
	saved, err := update.Save(ctx)
	if ent.IsNotFound(err) {
		return empty, common.NewVersionConflictError("problem", current.ID, m.ExpectedVersion, item.Version)
	}
	if err != nil {
		return empty, err
	}
	if cmd.RootCauseAnalysis != nil {
		if err = cmd.RootCauseAnalysis.applyTx(ctx, tx, current.ID, m.TenantID); err != nil {
			return empty, err
		}
	}
	if cmd.Evidence != nil {
		if err = cmd.Evidence.applyTx(ctx, tx, current.ID, m.TenantID); err != nil {
			return empty, err
		}
	}
	if cmd.RootCauseAnalysis != nil || cmd.Evidence != nil {
		if _, err = s.authorizeCommand(ctx, tx, Command{Meta: m, ProblemID: cmd.ProblemID, Action: "metadata"}); err != nil {
			return empty, err
		}
	}
	if _, err = professional.Save(ctx); err != nil {
		return empty, err
	}
	result := workitemmutation.Result{WorkItemID: item.ID, Version: saved.Version, Status: saved.Status}
	receipt := map[string]any{"problemId": current.ID, "previousAssigneeId": item.AssigneeID, "assigneeId": saved.AssigneeID, "assignmentReason": p.AssignmentReason, "patch": p, "rootCauseAnalysis": cmd.RootCauseAnalysis, "evidence": cmd.Evidence}
	// 分类纠正证据写进同一操作回执（原因 + 前后完整路径），与专业写入同一事务。
	if classificationBefore != nil || classificationAfter != nil {
		for key, value := range (service.CTICorrectionEvidence{Before: classificationBefore, After: classificationAfter}).Metadata(p.ClassificationReason) {
			receipt[key] = value
		}
	}
	if err = workitemmutation.RecordTx(ctx, tx, m, result, "problem.metadata", digest, receipt); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}
