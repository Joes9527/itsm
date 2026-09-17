package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/processdefinition"
)

// Called under the owning WorkItem/start transaction, before instance writes.
// Only the private intake delivery context can consume creation-frozen evidence.
func admitGenericWorkflowStart(ctx context.Context, client *ent.Client, definition *ent.ProcessDefinition, parsed *BPMNDefinitions, businessType string, workItemID int, variables map[string]interface{}, instanceIdentity, startDigest string) (map[string]interface{}, error) {
	flags, err := ReadGenericFulfillmentConfig(parsed, definition.ProcessVariables)
	if err != nil || flags == nil {
		return variables, err
	}
	if err = ValidateWorkItemLifecycleRecordClass(parsed, businessType); err != nil {
		return nil, err
	}
	provenance, trusted := ctx.Value(intakeStartActorKey{}).(intakeStartActor)
	if !trusted {
		if err = rejectGenericStartInputs(variables); err != nil {
			return nil, err
		}
		return nil, common.NewValidationError("generic lifecycle workflow requires frozen creation delivery", nil)
	}
	expectedKey := fmt.Sprintf("workflow-start:%d:%d", workItemID, definition.ID)
	expectedIdentity := fmt.Sprintf("PI-start-%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s", definition.TenantID, expectedKey))))
	if provenance.targetTenantID != definition.TenantID || provenance.workItemID != workItemID || provenance.receiptID <= 0 || startDigest == "" || instanceIdentity != expectedIdentity {
		return nil, common.NewValidationError("generic workflow creation identity mismatch", nil)
	}
	snapshot, err := client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.TenantID(definition.TenantID), intakeresolutionsnapshot.WorkItemID(workItemID), intakeresolutionsnapshot.IntakeRequestID(provenance.receiptID)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("generic workflow creation snapshot unavailable: %w", err)
	}
	if snapshot.NoProcess || snapshot.RecordClass != "generic" || snapshot.WorkflowDefinitionID == nil || *snapshot.WorkflowDefinitionID != definition.ID || snapshot.WorkflowDefinitionKey != definition.Key || snapshot.WorkflowDefinitionVersion != definition.Version || snapshot.WorkflowDefinitionDigest != FreezeProcessDefinition(definition).Digest {
		return nil, common.NewValidationError("generic workflow does not match frozen creation definition", nil)
	}
	receipt, err := client.IntakeRequest.Query().Where(intakerequest.ID(provenance.receiptID), intakerequest.TenantID(definition.TenantID), intakerequest.WorkItemID(workItemID), intakerequest.ActorID(provenance.actor.ID), intakerequest.Status("completed"), intakerequest.Operation("create_work_item")).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("generic workflow creation receipt unavailable: %w", err)
	}
	if receipt.RequestDigest != snapshot.RequestDigest || receipt.Channel != snapshot.Channel || (receipt.ActorTenantID > 0 && receipt.ActorTenantID != provenance.actor.TenantID) {
		return nil, common.NewValidationError("generic workflow creation receipt mismatch", nil)
	}
	var frozen map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(snapshot.WorkflowVariables))
	decoder.UseNumber()
	if err = decoder.Decode(&frozen); err != nil || frozen == nil {
		return nil, common.NewValidationError("generic workflow frozen variables invalid", err)
	}
	expected, err := json.Marshal(frozen)
	if err != nil {
		return nil, err
	}
	supplied, err := json.Marshal(variables)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(expected, supplied) {
		return nil, common.NewValidationError("generic workflow variables conflict with creation snapshot", nil)
	}
	for key, want := range map[string]string{"work_item_id": fmt.Sprint(workItemID), "tenant_id": fmt.Sprint(definition.TenantID), "record_class": "generic", "requester_id": fmt.Sprint(receipt.RequesterID), "triggered_by": fmt.Sprint(receipt.ActorID), "channel": receipt.Channel} {
		if fmt.Sprint(frozen[key]) != want {
			return nil, common.NewValidationError("generic workflow frozen identity mismatch", nil)
		}
	}
	if _, present := frozen["approvalResult"]; present {
		return nil, common.NewValidationError("approvalResult is not creation evidence", nil)
	}
	if _, present := frozen["workItemCompletionNote"]; present {
		return nil, common.NewValidationError("workItemCompletionNote is not creation evidence", nil)
	}
	for key, want := range map[string]bool{"approval_required": flags.ApprovalRequired, "need_escalate": flags.NeedEscalate} {
		got, ok := frozen[key].(bool)
		if !ok || got != want {
			return nil, common.NewValidationError(key+" conflicts with immutable definition configuration", nil)
		}
		// New instances use the fixed definition's value, never ordinary start input.
		frozen[key] = want
	}
	return frozen, nil
}

// Replay keeps the original start digest but must not turn a public request into
// an intake-authorized path. Legacy definitions retain their existing replay rule.
func validateGenericWorkflowReplay(ctx context.Context, client *ent.Client, identity ProcessDefinitionIdentity, instance *ent.ProcessInstance, variables map[string]interface{}) error {
	definition, err := client.ProcessDefinition.Query().Where(processdefinition.ID(instance.ProcessDefinitionID), processdefinition.TenantID(instance.TenantID)).Only(ctx)
	if err != nil {
		return err
	}
	parsed, err := NewBPMNParser().ParseXML(definition.BpmnXML)
	if err != nil {
		return err
	}
	flags, err := ReadGenericFulfillmentConfig(parsed, definition.ProcessVariables)
	if err != nil || flags == nil {
		return err
	}
	if FreezeProcessDefinition(definition) != identity {
		return &processStartDefinitionError{}
	}
	_, err = admitGenericWorkflowStart(ctx, client, definition, parsed, instance.BusinessType, instance.BusinessID, variables, instance.ProcessInstanceID, instance.StartRequestDigest)
	return err
}
