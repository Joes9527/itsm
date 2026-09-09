package service

import (
	"context"
	"fmt"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/ticket"
	"itsm-backend/service/bpmn"
	"strconv"
)

// Only a declared typed result can update the source lifecycle projection.
// The source WorkItem identity never comes from handler OutputVars.
func callbackContinuationOutputs(handler bpmn.ServiceTaskHandlerInterface, row *ent.ProcessCallbackOutbox, instance *ent.ProcessInstance, effect *bpmn.CallbackEffect) (map[string]any, error) {
	outputs, err := creationCallbackOutputs(handler, row.Action, effect)
	if err != nil {
		return nil, err
	}
	provider, ok := handler.(bpmn.CallbackContractProvider)
	var contract bpmn.CallbackActionContract
	var declared bool
	if ok {
		contract, declared = provider.CallbackContract(row.Action)
	}
	if !declared || contract.LifecycleRecordClass == "" {
		if effect.LifecycleResult != nil {
			return nil, fmt.Errorf("undeclared lifecycle result")
		}
		return outputs, nil
	}
	if effect.Status == bpmn.CallbackEffectBlocked {
		return nil, nil
	}
	class, subtype := common.WorkItemIdentityFilter(instance.BusinessType)
	result := effect.LifecycleResult
	if result == nil || effect.CreationResult != nil || len(effect.OutputVars) > 0 || len(effect.UpdatedData) > 0 || subtype != "" || class != contract.LifecycleRecordClass || result.WorkItemID != instance.BusinessID || result.WorkItemID <= 0 || result.Version != bpmn.GetIntFromVars(row.Variables, "version")+1 || result.Status == "" {
		return nil, fmt.Errorf("invalid typed lifecycle result")
	}
	return map[string]any{"version": result.Version, "status": result.Status}, nil
}

func persistCallbackOutputs(ctx context.Context, tx *ent.Tx, handler bpmn.ServiceTaskHandlerInterface, row *ent.ProcessCallbackOutbox, instance *ent.ProcessInstance, effect *bpmn.CallbackEffect) error {
	outputs, err := callbackContinuationOutputs(handler, row, instance, effect)
	if err != nil {
		return newBPMNCallbackHandlerError(err)
	}
	if len(outputs) == 0 {
		return nil
	}
	if result := effect.LifecycleResult; result != nil {
		contract, _ := handler.(bpmn.CallbackContractProvider).CallbackContract(row.Action)
		valid, err := tx.Ticket.Query().Where(ticket.ID(result.WorkItemID), ticket.TenantID(row.TenantID), ticket.RecordClass(contract.LifecycleRecordClass), ticket.DeletedAtIsNil()).Exist(ctx)
		if err != nil {
			return newBPMNCallbackAdvanceError(err)
		}
		if !valid {
			return newBPMNCallbackHandlerError(fmt.Errorf("lifecycle source record class mismatch"))
		}

		actorID := row.ActorID
		if row.CallbackKind == "service_task" {
			actorID, _ = strconv.Atoi(instance.Initiator)
		}
		proven, err := tx.AuditLog.Query().Where(auditlog.TenantID(row.TenantID), auditlog.UserID(actorID), auditlog.OperationID(row.ExecutionKey), auditlog.Resource("work_item"), auditlog.Path(strconv.Itoa(result.WorkItemID)), auditlog.ResultVersion(result.Version), auditlog.ResultStatus(result.Status)).Exist(ctx)
		if err != nil {
			return newBPMNCallbackAdvanceError(err)
		}
		if !proven {
			return newBPMNCallbackHandlerError(fmt.Errorf("lifecycle result lacks immutable command receipt"))
		}
	}
	merged := copyBPMNCallbackVariables(instance.Variables)
	for key, value := range outputs {
		merged[key] = value
	}
	affected, err := tx.ProcessInstance.Update().Where(processinstance.ID(instance.ID), processinstance.TenantID(instance.TenantID), processinstance.Version(instance.Version)).SetVariables(merged).SetVersion(instance.Version + 1).Save(ctx)
	if err != nil {
		return newBPMNCallbackAdvanceError(err)
	}
	if affected != 1 {
		return newBPMNCallbackAdvanceError(fmt.Errorf("source process changed during callback"))
	}
	instance.Variables = merged
	instance.Version++
	return nil
}
