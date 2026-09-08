package service

import (
	"fmt"
	"itsm-backend/service/bpmn"
)

// creationCallbackOutputs accepts only the typed result of a declared creation
// action. Source business identity and arbitrary OutputVars cannot be replaced.
func creationCallbackOutputs(handler bpmn.ServiceTaskHandlerInterface, action string, effect *bpmn.CallbackEffect) (map[string]any, error) {
	provider, ok := handler.(bpmn.CallbackContractProvider)
	if !ok {
		if effect.CreationResult != nil {
			return nil, fmt.Errorf("undeclared creation result")
		}
		return nil, nil
	}
	contract, declared := provider.CallbackContract(action)
	if !declared || contract.CreatedRecordClass == "" {
		if effect.CreationResult != nil {
			return nil, fmt.Errorf("undeclared creation result")
		}
		return nil, nil
	}
	if effect.Status == bpmn.CallbackEffectBlocked {
		return nil, nil
	}
	result := effect.CreationResult
	if result == nil || result.WorkItemID <= 0 || result.Number == "" || result.RecordClass != contract.CreatedRecordClass || result.ProfessionalReference.ID <= 0 || len(effect.OutputVars) > 0 {
		return nil, fmt.Errorf("invalid typed creation result")
	}
	professional := ""
	switch contract.CreatedRecordClass {
	case "incident":
		professional = "incident"
	case "change_request":
		professional = "change"
	default:
		return nil, fmt.Errorf("unsupported creation result class")
	}
	if result.ProfessionalReference.Type != professional {
		return nil, fmt.Errorf("creation reference type mismatch")
	}
	return map[string]any{"created_work_item_id": result.WorkItemID, "created_work_item_number": result.Number, "created_record_class": result.RecordClass, "created_" + professional + "_id": result.ProfessionalReference.ID}, nil
}
