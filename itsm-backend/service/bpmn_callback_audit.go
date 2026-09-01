package service

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/service/bpmn"
)

// RecordCallbackBlocked records a terminal required callback block without
// persisting callback payloads, configuration references, or handler errors.
func (s *BPMNAuditService) RecordCallbackBlocked(ctx context.Context, row *ent.ProcessCallbackOutbox, code bpmn.CallbackBlockCode) error {
	return s.recordCallbackOutcome(ctx, row, bpmn.CallbackAuditActionBlocked, code)
}

// RecordCallbackSkippedOptional records the engine-derived optional skip. The
// declared optional flag is read only from the durable outbox snapshot.
func (s *BPMNAuditService) RecordCallbackSkippedOptional(ctx context.Context, row *ent.ProcessCallbackOutbox, code bpmn.CallbackBlockCode) error {
	if row == nil || !row.OptionalDeclared {
		return fmt.Errorf("bpmn callback optional skip was not declared")
	}
	return s.recordCallbackOutcome(ctx, row, bpmn.CallbackAuditActionSkippedOptional, code)
}

func (s *BPMNAuditService) recordCallbackOutcome(ctx context.Context, row *ent.ProcessCallbackOutbox, action string, code bpmn.CallbackBlockCode) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("bpmn callback audit client is required")
	}
	if row == nil || row.TenantID <= 0 || row.ProcessInstanceID <= 0 || !bpmn.IsAllowedCallbackBlockCode(code) {
		return fmt.Errorf("bpmn callback audit input is invalid")
	}
	instance, err := s.client.ProcessInstance.Query().Where(
		processinstance.ID(row.ProcessInstanceID),
		processinstance.TenantID(row.TenantID),
	).Only(ctx)
	if err != nil {
		return fmt.Errorf("bpmn callback audit process instance lookup failed")
	}

	return s.RecordAudit(ctx, &AuditContext{
		ProcessInstanceID:    instance.ID,
		ProcessInstanceKey:   instance.ProcessInstanceID,
		ProcessDefinitionKey: instance.ProcessDefinitionKey,
		ProcessDefinitionID:  instance.ProcessDefinitionID,
		ActivityID:           row.ElementID,
		ActivityName:         "BPMN callback",
		ActivityType:         ActivityTypeServiceTask,
		Action:               action,
		TenantID:             row.TenantID,
		Metadata: map[string]interface{}{
			"process_task_id":   row.ProcessTaskID,
			"task_id":           row.TaskID,
			"handler_id":        row.HandlerID,
			"action":            row.Action,
			"block_code":        string(code),
			"optional_declared": row.OptionalDeclared,
		},
	})
}
