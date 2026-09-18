package service

import (
	"fmt"
	"strings"
)

// WorkItemLifecycleContract is an opt-in contract on an immutable definition.
type WorkItemLifecycleContract string

const GenericFulfillmentV1 WorkItemLifecycleContract = "generic_fulfillment_v1"

type WorkItemPrerequisite string

const (
	WorkItemPrerequisiteAssigned   WorkItemPrerequisite = "assigned"
	WorkItemPrerequisiteInProgress WorkItemPrerequisite = "in_progress"
	WorkItemPrerequisiteEscalated  WorkItemPrerequisite = "escalated"
	WorkItemPrerequisiteResolved   WorkItemPrerequisite = "resolved"
	WorkItemPrerequisiteClosed     WorkItemPrerequisite = "closed"
)

const (
	lifecycleContractMetadata     = "workItemLifecycleContract"
	lifecyclePrerequisiteMetadata = "workItemPrerequisite"
)

// strictLifecycleMetadata preserves absence and rejects ambiguous declarations.
func strictLifecycleMetadata(ext *BPMNExtensionElements, key string) (string, bool, error) {
	if ext == nil {
		return "", false, nil
	}
	value, seen := "", false
	for _, m := range ext.MetaData {
		if m.Name != key {
			continue
		}
		if seen {
			return "", true, fmt.Errorf("%s must be declared at most once", key)
		}
		seen = true
		value = strings.TrimSpace(m.Value)
		if value == "" {
			return "", true, fmt.Errorf("%s must not be empty", key)
		}
	}
	return value, seen, nil
}

func (p *BPMNProcess) WorkItemLifecycleContract() (WorkItemLifecycleContract, error) {
	if p == nil {
		return "", fmt.Errorf("process is required")
	}
	value, present, err := strictLifecycleMetadata(p.ExtensionElements, lifecycleContractMetadata)
	if err != nil {
		return "", err
	}
	if !present {
		return "", nil
	}
	if WorkItemLifecycleContract(value) != GenericFulfillmentV1 {
		return "", fmt.Errorf("unsupported %s %q", lifecycleContractMetadata, value)
	}
	return GenericFulfillmentV1, nil
}

func (t *BPMNUserTask) WorkItemPrerequisite() (WorkItemPrerequisite, error) {
	if t == nil {
		return "", fmt.Errorf("task is required")
	}
	value, present, err := strictLifecycleMetadata(t.ExtensionElements, lifecyclePrerequisiteMetadata)
	if err != nil {
		return "", err
	}
	if !present {
		return "", nil
	}
	switch WorkItemPrerequisite(value) {
	case WorkItemPrerequisiteAssigned, WorkItemPrerequisiteInProgress, WorkItemPrerequisiteEscalated, WorkItemPrerequisiteResolved, WorkItemPrerequisiteClosed:
		return WorkItemPrerequisite(value), nil
	default:
		return "", fmt.Errorf("unsupported %s %q", lifecyclePrerequisiteMetadata, value)
	}
}

func hasLifecycleMetadata(ext *BPMNExtensionElements, key string) bool {
	if ext != nil {
		for _, m := range ext.MetaData {
			if m.Name == key {
				return true
			}
		}
	}
	return false
}

func validateWorkItemLifecycleProcess(p *BPMNProcess) error {
	contract, err := p.WorkItemLifecycleContract()
	if err != nil {
		return err
	}
	if contract != "" && (len(p.SubProcesses) > 0 || len(p.CallActivities) > 0 || len(p.ServiceTasks) > 0 || len(p.ScriptTasks) > 0 || len(p.BusinessRuleTasks) > 0 || len(p.ManualTasks) > 0) {
		return fmt.Errorf("generic_fulfillment_v1 supports only top-level user tasks")
	}
	for _, t := range p.UserTasks {
		if contract != "" && (hasLifecycleMetadata(t.ExtensionElements, bpmnMetaDataServiceTaskType) || hasLifecycleMetadata(t.ExtensionElements, bpmnMetaDataAction)) {
			return fmt.Errorf("task %q lifecycle contract cannot use a handler or action", t.ID)
		}
		prerequisite, err := t.WorkItemPrerequisite()
		if err != nil {
			return fmt.Errorf("task %q: %w", t.ID, err)
		}
		if prerequisite == "" {
			continue
		}
		if contract == "" {
			return fmt.Errorf("task %q prerequisite requires a lifecycle contract", t.ID)
		}
		if t.TaskPurpose != "fulfillment" || t.AssigneeSource != BPMNAssigneeSourceWorkItem {
			return fmt.Errorf("task %q prerequisite requires owner-bound fulfillment", t.ID)
		}
		if err := validateBPMNAssigneeSource(t); err != nil {
			return err
		}
	}
	return nil
}

// GenericFulfillmentConfig contains trusted, definition-owned branch flags.
// A nil result from ReadGenericFulfillmentConfig means the contract is absent.
type GenericFulfillmentConfig struct {
	ApprovalRequired bool
	NeedEscalate     bool
}

func ReadGenericFulfillmentConfig(definitions *BPMNDefinitions, variables map[string]interface{}) (*GenericFulfillmentConfig, error) {
	enabled, err := hasGenericFulfillmentContract(definitions)
	if err != nil || !enabled {
		return nil, err
	}
	result := &GenericFulfillmentConfig{}
	for key, target := range map[string]*bool{"approval_required": &result.ApprovalRequired, "need_escalate": &result.NeedEscalate} {
		value, present := variables[key]
		if !present {
			continue
		}
		flag, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%s must be a JSON boolean", key)
		}
		*target = flag
	}
	return result, nil
}

func hasGenericFulfillmentContract(definitions *BPMNDefinitions) (bool, error) {
	if definitions == nil || len(definitions.Processes) == 0 {
		return false, fmt.Errorf("process definitions are required")
	}
	enabled := false
	for _, p := range definitions.Processes {
		if p == nil {
			return false, fmt.Errorf("process is required")
		}
		if err := validateWorkItemLifecycleProcess(p); err != nil {
			return false, err
		}
		contract, err := p.WorkItemLifecycleContract()
		if err != nil {
			return false, err
		}
		enabled = enabled || contract == GenericFulfillmentV1
	}
	if enabled && len(definitions.Processes) != 1 {
		return false, fmt.Errorf("generic_fulfillment_v1 requires a single process")
	}
	return enabled, nil
}

// ValidateWorkItemLifecycleRecordClass must receive the authoritative business
// class at binding/start, never the mutable definition category.
func ValidateWorkItemLifecycleRecordClass(definitions *BPMNDefinitions, recordClass string) error {
	enabled, err := hasGenericFulfillmentContract(definitions)
	if err != nil {
		return err
	}
	if enabled && recordClass != "generic" {
		return fmt.Errorf("generic_fulfillment_v1 requires generic recordClass, got %q", recordClass)
	}
	return nil
}
