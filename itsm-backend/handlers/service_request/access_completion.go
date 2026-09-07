package service_request

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/servicerequestaccessresult"
	"itsm-backend/ent/ticket"
	"itsm-backend/service/bpmn"
)

// ContributeAccessCompletion owns verified professional fulfillment inside
// BPMN's caller-owned transaction. It never commits, starts another transaction,
// or invokes an external system.
func (s *Service) ContributeAccessCompletion(ctx context.Context, client *ent.Client, task *ent.ProcessTask, ledger *ent.KafTaskActionLedger, raw json.RawMessage) error {
	if task == nil || ledger == nil || ledger.TaskID != task.TaskID || ledger.TenantID != task.TenantID || ledger.Action != "complete_bpmn_task" || ledger.ResultStatus != "executing" {
		return fmt.Errorf("verified access requires the executing task action")
	}
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	if actorID <= 0 || tenantID != task.TenantID {
		return fmt.Errorf("verified access actor scope missing")
	}
	instance, err := client.ProcessInstance.Query().Where(processinstance.IDEQ(task.ProcessInstanceID), processinstance.TenantIDEQ(tenantID), processinstance.BusinessTypeEQ("service_request")).Only(ctx)
	if err != nil {
		return fmt.Errorf("load verified access process: %w", err)
	}
	approved, err := s.ReadApprovedAccess(ctx, client, tenantID, instance.BusinessID, task)
	if err != nil {
		return err
	}
	result, expiry, err := ValidateAccessResult(raw, approved.ApprovalSnapshot)
	if err != nil {
		return fmt.Errorf("invalid verified access result: %w", err)
	}
	if result.EvidenceRef != ledger.IdempotencyKey || result.VerifiedAt.Before(task.CreatedTime) || result.VerifiedAt.After(time.Now()) {
		return fmt.Errorf("verified access evidence or verification time does not match task")
	}
	item, err := client.Ticket.Query().Where(ticket.IDEQ(instance.BusinessID), ticket.TenantIDEQ(tenantID), ticket.RecordClassEQ("service_request_item"), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return err
	}
	request, err := client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(item.ID)).Only(ctx)
	if err != nil {
		return err
	}
	exists, err := client.ServiceRequestAccessResult.Query().Where(servicerequestaccessresult.WorkItemIDEQ(item.ID)).Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("verified access result already exists; replay its owning action")
	}
	if err := client.ServiceRequestAccessResult.Create().SetWorkItemID(item.ID).SetProcessTaskID(task.ID).
		SetOutcome(servicerequestaccessresult.Outcome(result.Outcome)).SetProvider(servicerequestaccessresult.Provider(result.Provider)).
		SetSubjectID(result.SubjectID).SetGroupID(result.GroupID).SetBaseline(servicerequestaccessresult.Baseline(result.Baseline)).
		SetVerifiedAt(result.VerifiedAt).SetNillableExpiresAt(expiry).SetEvidenceRef(result.EvidenceRef).Exec(ctx); err != nil {
		return err
	}
	now := time.Now()
	changed, err := client.Ticket.Update().Where(ticket.IDEQ(item.ID), ticket.TenantIDEQ(tenantID), ticket.VersionEQ(item.Version), ticket.DeletedAtIsNil()).
		SetStatus("resolved").SetResolvedAt(now).AddVersion(1).Save(ctx)
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("verified access WorkItem changed concurrently")
	}
	if err := client.ServiceRequest.UpdateOneID(request.ID).SetCompletedAt(now).SetCompletionNote(result.Outcome).Exec(ctx); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]interface{}{
		"ledgerId": ledger.ID, "taskId": task.TaskID, "workItemId": item.ID,
		"correlationId": ledger.CorrelationID, "actorSource": "itsm_kaf_delegation",
		"outcome": result.Outcome, "verifiedAt": result.VerifiedAt, "expiresAt": expiry,
		"evidenceRef": result.EvidenceRef, "procedureRef": ledger.ProcedureRef, "procedureVersion": ledger.ProcedureVersion,
	})
	if err != nil {
		return err
	}
	return client.AuditLog.Create().SetTenantID(tenantID).SetUserID(actorID).SetResource("work_item").
		SetAction("service_request.access_verified").SetPath("bpmn/process-tasks/actions").SetMethod("POST").
		SetStatusCode(200).SetRequestBody(string(body)).Exec(ctx)
}

// ValidateAccessCompletionReplay accepts only the result already committed with
// this task. It does not refresh approval, identity, baseline or verification time.
func (s *Service) ValidateAccessCompletionReplay(ctx context.Context, client *ent.Client, task *ent.ProcessTask, ledger *ent.KafTaskActionLedger) error {
	unavailable := fmt.Errorf("verified access replay evidence unavailable")
	if ledger.RequestDigest == "" || ledger.TaskID != task.TaskID || ledger.TenantID != task.TenantID {
		return unavailable
	}
	saved, err := client.ServiceRequestAccessResult.Query().Where(servicerequestaccessresult.ProcessTaskIDEQ(task.ID), servicerequestaccessresult.HasWorkItemWith(ticket.TenantIDEQ(task.TenantID), ticket.RecordClassEQ("service_request_item"))).Only(ctx)
	if err != nil {
		return unavailable
	}
	snapshot, err := s.ReadAccessSnapshot(ctx, client, task.TenantID, saved.WorkItemID)
	if err != nil || snapshot == nil {
		return unavailable
	}
	raw, err := json.Marshal(task.TaskVariables["kaf_access_result"])
	if err != nil {
		return unavailable
	}
	result, expiry, err := ValidateAccessResult(raw, *snapshot)
	if err != nil || result.EvidenceRef != ledger.IdempotencyKey || saved.EvidenceRef != result.EvidenceRef ||
		string(saved.Outcome) != result.Outcome || string(saved.Baseline) != result.Baseline ||
		string(saved.Provider) != string(result.Provider) || saved.SubjectID != result.SubjectID || saved.GroupID != result.GroupID ||
		!saved.VerifiedAt.Equal(result.VerifiedAt) {
		return unavailable
	}
	if (expiry == nil) != (saved.ExpiresAt == nil) || (expiry != nil && !expiry.Equal(*saved.ExpiresAt)) {
		return unavailable
	}
	return nil
}
