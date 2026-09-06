package service

import (
	"context"
	"encoding/json"
	"fmt"
	"itsm-backend/config"
	"itsm-backend/ent"
	"itsm-backend/service/bpmn"
	"strconv"
	"strings"
)

// SetPublicationKAFConfig receives the existing deployment configuration owner.
// Static completeness does not assert live API/worker/provider health.
func (e *CustomProcessEngine) SetPublicationKAFConfig(cfg *config.Config) {
	e.publicationKAFConfig = cfg
}

func (e *CustomProcessEngine) publicationCapabilityConfigurations(ctx context.Context, client *ent.Client, tenantID int, definition *ent.ProcessDefinition) ([]json.RawMessage, error) {
	result := []json.RawMessage{}
	parsed, err := NewBPMNParser().ParseXML(definition.BpmnXML)
	if err != nil {
		return result, nil
	} // malformed drafts still have their XML digest
	collect := func(taskType, action, ref string) error {
		handler := e.findHandlerByTaskType(taskType)
		if owner, ok := handler.(bpmn.PublicationConfigurationProvider); ok {
			raw, err := owner.PublicationConfiguration(ctx, client, tenantID, action, ref)
			if err != nil {
				return err
			}
			if !json.Valid(raw) {
				return fmt.Errorf("capability returned invalid public configuration")
			}
			result = append(result, raw)
		}
		return nil
	}
	for _, p := range parsed.Processes {
		for _, t := range p.UserTasks {
			if err := collect(t.ServiceTaskType(), t.ServiceTaskAction(), t.CallbackConfigRef()); err != nil {
				return nil, err
			}
		}
		for _, t := range p.ServiceTasks {
			if err := collect(t.ServiceTaskType(), t.ServiceTaskAction(), t.CallbackConfigRef()); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func (e *CustomProcessEngine) ValidateDefinitionForPublication(ctx context.Context, client *ent.Client, tenantID int, definition *ent.ProcessDefinition, requiresApproval bool) error {
	parsed, err := NewBPMNParser().ParseXML(definition.BpmnXML)
	if err != nil {
		return err
	}
	approvals := 0
	validateCapability := func(taskType, action, ref string, optional bool) error {
		handler := e.findHandlerByTaskType(taskType)
		if handler == nil {
			return fmt.Errorf("required capability %q is not registered", taskType)
		}
		if owner, ok := handler.(bpmn.PublicationConfigurationProvider); ok {
			return owner.ValidatePublicationConfiguration(ctx, client, tenantID, action, ref)
		}
		if isAsyncHandler(handler) {
			return fmt.Errorf("async capability %q has no publication validator", taskType)
		}
		provider, ok := handler.(bpmn.CallbackContractProvider)
		if !ok {
			return fmt.Errorf("capability %q has no action contract", taskType)
		}
		contract, ok := provider.CallbackContract(action)
		if !ok {
			return fmt.Errorf("capability %q does not support action %q", taskType, action)
		}
		_, err := normalizeBPMNCallbackContractConfigRef(contract, ref)
		if err != nil {
			return err
		}
		// A required trusted configuration must be validated by its registered owner.
		if contract.ConfigRefRequired {
			return fmt.Errorf("capability %q requires a configuration validator", taskType)
		}
		return nil
	}
	for _, p := range parsed.Processes {
		if !p.IsExecutable {
			return fmt.Errorf("process is not executable")
		}
		for _, t := range p.UserTasks {
			if t.TaskPurpose == "approval" {
				approvals++
			}
			if strings.TrimSpace(t.Assignee) == "" && strings.TrimSpace(t.CandidateUsers) == "" && strings.TrimSpace(t.CandidateGroups) == "" && strings.TrimSpace(t.AssigneeRole) == "" && !t.AssigneeGmChain && t.AssigneeDeptId <= 0 && t.AssigneeTeamId <= 0 && t.AssigneeProjectId <= 0 && t.AssigneeTempTeamId <= 0 {
				return fmt.Errorf("task %q requires candidate resolution configuration", t.ID)
			}
			for _, identifier := range splitNonEmptyCSV(t.Assignee + "," + t.CandidateUsers) {
				if strings.Contains(identifier, "${") {
					continue
				}
				if _, err := resolveTaskAssignee(ctx, client, tenantID, identifier); err != nil {
					return err
				}
			}
			// The normal runtime group resolver owns expansion. Membership is still
			// checked live on claim/approval; it is never frozen into the catalog hash.
			if t.CandidateGroups != "" {
				ids, _, err := bpmn.NewGroupResolver(client).ExpandGroupsToUsers(ctx, tenantID, t.CandidateGroups)
				if err != nil {
					return err
				}
				for _, id := range ids {
					if _, err := resolveTaskAssignee(ctx, client, tenantID, strconv.Itoa(id)); err != nil {
						return err
					}
				}
				if len(ids) == 0 {
					return fmt.Errorf("task %q candidate groups resolve to no users", t.ID)
				}
			}
			if t.AssigneeRole != "" {
				candidates, err := e.forClient(client, nil).resolveRoleCandidates(ctx, tenantID, t.AssigneeRole)
				if err != nil {
					return err
				}
				if len(candidates) == 0 {
					return fmt.Errorf("task %q role resolves to no candidates", t.ID)
				}
			}
			optional, err := t.CallbackOptionalDeclared()
			if err != nil {
				return err
			}
			if t.ServiceTaskType() != "" {
				if err := validateCapability(t.ServiceTaskType(), t.ServiceTaskAction(), t.CallbackConfigRef(), optional); err != nil {
					return err
				}
			}
		}
		for _, t := range p.ServiceTasks {
			optional, err := t.CallbackOptionalDeclared()
			if err != nil {
				return err
			}
			handler := e.findHandlerByTaskType(t.ServiceTaskType())
			if handler != nil && handler.GetTaskType() == bpmn.KafDelegateTaskType {
				if err := validateKafDeclaredActions(t.AllowedActions()); err != nil {
					return err
				}
				if err := config.ValidateKAFWorkerStartupConfig(e.publicationKAFConfig); err != nil {
					return err
				}
				continue
			}
			if err := validateCapability(t.ServiceTaskType(), t.ServiceTaskAction(), t.CallbackConfigRef(), optional); err != nil {
				return err
			}
		}
	}
	if requiresApproval && approvals == 0 {
		return fmt.Errorf("catalog requires an approval user task")
	}
	return nil
}
