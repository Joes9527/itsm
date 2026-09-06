package service

import (
	"context"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/handlers/common/accessgrant"
	"itsm-backend/service/bpmn"
	"strconv"
)

// ValidateAccessPolicyBinding ties the declared capability to this Catalog's
// policy on every possible executable route. Ordinary Catalogs have no policy.
func (*ProcessBindingService) ValidateAccessPolicyBinding(ctx context.Context, tx *ent.Tx, tenantID int, class, key string, policy *accessgrant.Policy) error {
	records, err := loadCreationConfiguration(ctx, tx, tenantID, class, key)
	if err != nil {
		return err
	}
	if policy != nil {
		for _, b := range records.bindings {
			if b.Conditions["no_process"] == true {
				return fmt.Errorf("access policy requires a grant workflow")
			}
		}
		if len(records.definitions) == 0 {
			return fmt.Errorf("access policy requires a grant workflow")
		}
	}
	for _, d := range records.definitions {
		parsed, err := NewBPMNParser().ParseXML(d.BpmnXML)
		if err != nil {
			if policy == nil {
				continue
			} // Non-access definition validity stays with the workflow owner.
			return err
		}
		found := false
		for _, p := range parsed.Processes {
			for _, t := range p.ServiceTasks {
				if t.ServiceTaskType() != bpmn.KafDelegateTaskType || t.ServiceTaskAction() != accessgrant.Capability {
					continue
				}
				found = true
				if policy == nil || t.CallbackConfigRef() != strconv.Itoa(policy.ID) {
					return fmt.Errorf("grant capability must reference this catalog access policy")
				}
			}
		}
		if policy != nil && !found {
			return fmt.Errorf("access policy route does not declare external grant capability")
		}
	}
	return nil
}
