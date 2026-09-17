package service

import (
	"itsm-backend/common"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func genericBindingConfig(definition *ent.ProcessDefinition, recordClass string, overrides map[string]interface{}) (*GenericFulfillmentConfig, error) {
	parsed, err := NewBPMNParser().ParseXML(definition.BpmnXML)
	if err != nil {
		return nil, err
	}
	if err = ValidateWorkItemLifecycleRecordClass(parsed, recordClass); err != nil {
		return nil, err
	}
	flags, err := ReadGenericFulfillmentConfig(parsed, definition.ProcessVariables)
	if err != nil || flags == nil {
		return flags, err
	}
	if err = rejectGenericStartInputs(overrides); err != nil {
		return nil, err
	}
	return flags, nil
}

func rejectGenericStartInputs(variables map[string]interface{}) error {
	if err := RejectGenericFulfillmentReservedInputs(variables); err != nil {
		return err
	}
	if _, present := variables["workItemCompletionNote"]; present {
		return common.NewValidationError("workItemCompletionNote is accepted only by fulfillment completion", nil)
	}
	return nil
}

// Domain-prepared defaults are not user inputs. Freeze the definition flags
// after validating the original form/override boundary and before intake writes.
func freezeGenericCreationFlags(plan *creation.CreationPlan, flags *GenericFulfillmentConfig) error {
	if flags == nil {
		return nil
	}
	if err := rejectGenericStartInputs(plan.Resolved.Command.FormValues); err != nil {
		return err
	}
	if _, present := plan.WorkflowVariables["approvalResult"]; present {
		return common.NewValidationError("approvalResult requires an authoritative approval decision", nil)
	}
	if _, present := plan.WorkflowVariables["workItemCompletionNote"]; present {
		return common.NewValidationError("workItemCompletionNote is accepted only by fulfillment completion", nil)
	}
	if plan.WorkflowVariables == nil {
		plan.WorkflowVariables = map[string]interface{}{}
	}
	plan.WorkflowVariables["approval_required"] = flags.ApprovalRequired
	plan.WorkflowVariables["need_escalate"] = flags.NeedEscalate
	return nil
}
