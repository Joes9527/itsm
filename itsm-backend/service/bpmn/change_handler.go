package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workflowcallback"

	"go.uber.org/zap"
)

type ChangeDomainServiceInterface interface {
	workflowcallback.ChangeService
	CreateChangeForWorkflow(ctx context.Context, tenantID, createdBy int, title, description, changeType, priority string) (int, error)
}

type ChangeServiceTaskHandler struct {
	creationApplication creation.Application
	HandlerBase
	client        *ent.Client
	changeService ChangeDomainServiceInterface
}

func NewChangeServiceTaskHandler(client *ent.Client, _ *zap.SugaredLogger) *ChangeServiceTaskHandler {
	return &ChangeServiceTaskHandler{client: client}
}
func (h *ChangeServiceTaskHandler) SetChangeService(service ChangeDomainServiceInterface) {
	h.changeService = service
}
func (h *ChangeServiceTaskHandler) GetTaskType() string  { return "change_task" }
func (h *ChangeServiceTaskHandler) GetHandlerID() string { return "change_service_handler" }

func (h *ChangeServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	action, _ := variables["action"].(string)
	switch action {
	case "create_change":
		return h.createChange(ctx, variables)
	case "approve_change":
		return h.approveChange(ctx, task, variables)
	case "notify_stakeholders":
		return h.notifyStakeholders(ctx, variables)
	case "update_change", "reject_change", "schedule_change", "implement_change", "verify_change", "close_change", "assess_risk":
		if h.changeService == nil {
			return nil, fmt.Errorf("change service is not injected")
		}
		if _, err := RequireTenantID(ctx, variables); err != nil {
			return nil, err
		}
		command, effect := bindChangeWorkflowCommand(ctx, action, variables)
		if effect != nil {
			return effect, nil
		}
		result, err := h.changeService.ApplyChangeWorkflowCallback(ctx, command)
		if err != nil {
			return nil, err
		}
		return callbackEffectFromWorkflowResult(result)
	default:
		return BlockedEffect(CallbackBlockHandlerContract, "unsupported change callback action"), nil
	}
}

func bindChangeWorkflowCommand(ctx context.Context, action string, variables map[string]interface{}) (workflowcallback.ChangeCommand, *CallbackEffect) {
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return workflowcallback.ChangeCommand{}, BlockedEffect(CallbackBlockHandlerContract, err.Error())
	}
	command := workflowcallback.ChangeCommand{Action: action, ChangeID: GetIntFromVars(variables, "change_id"), TenantID: tenantID}
	if command.ChangeID <= 0 {
		return command, BlockedEffect(CallbackBlockTargetMissing, "change_id is required")
	}
	if action == "update_change" {
		if status, ok := variables["status"].(string); ok && status != "" {
			return command, BlockedEffect(CallbackBlockHandlerContract, "update_change cannot mutate lifecycle status")
		}
		if value, ok := variables["title"].(string); ok && value != "" {
			command.Title = &value
		}
		if value, ok := variables["description"].(string); ok && value != "" {
			command.Description = &value
		}
	}
	if action == "schedule_change" {
		if raw, ok := variables["planned_start_date"].(string); ok && raw != "" {
			value, parseErr := time.Parse(time.RFC3339, raw)
			if parseErr != nil {
				return command, BlockedEffect(CallbackBlockHandlerContract, "planned_start_date must be RFC3339")
			}
			command.PlannedStart = &value
		}
		if raw, ok := variables["planned_end_date"].(string); ok && raw != "" {
			value, parseErr := time.Parse(time.RFC3339, raw)
			if parseErr != nil {
				return command, BlockedEffect(CallbackBlockHandlerContract, "planned_end_date must be RFC3339")
			}
			command.PlannedEnd = &value
		}
	}
	if action == "verify_change" {
		command.VerificationResult, _ = variables["verification_result"].(string)
		if command.VerificationResult != "" && command.VerificationResult != "passed" && command.VerificationResult != "failed" {
			return command, BlockedEffect(CallbackBlockHandlerContract, "unsupported verification_result")
		}
	}
	return command, nil
}

func callbackEffectFromWorkflowResult(result workflowcallback.Result) (*CallbackEffect, error) {
	switch result.Status {
	case workflowcallback.StatusApplied:
		return AppliedEffect(result.Message, result.Output), nil
	case workflowcallback.StatusIdempotent:
		return IdempotentEffect(result.Message, result.Output), nil
	case workflowcallback.StatusBlocked:
		code := CallbackBlockHandlerContract
		if result.BlockCode == string(CallbackBlockTargetMissing) {
			code = CallbackBlockTargetMissing
		}
		return BlockedEffect(code, result.Message), nil
	default:
		return nil, fmt.Errorf("invalid owning service callback outcome %q", result.Status)
	}
}

func (h *ChangeServiceTaskHandler) SetCreationApplication(app creation.Application) {
	h.creationApplication = app
}
func (h *ChangeServiceTaskHandler) createChange(ctx context.Context, _ map[string]interface{}) (*CallbackEffect, error) {
	return executeWorkItemCreation(ctx, h.client, h.creationApplication, h.GetHandlerID(), "create_change", creation.RecordClassChangeRequest)
}

func (h *ChangeServiceTaskHandler) approveChange(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	changeID := GetIntFromVars(variables, "change_id")
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	if _, err := h.client.Change.Query().Where(change.ID(changeID), change.HasWorkItemWith(ticket.TenantID(tenantID), ticket.DeletedAtIsNil())).Only(ctx); err != nil {
		return nil, fmt.Errorf("load change approval target: %w", err)
	}
	return persistedApprovalDecisionEffect(ctx, h.client, task, variables, fmt.Sprintf("change %d approval decision persisted", changeID))
}

func (h *ChangeServiceTaskHandler) notifyStakeholders(ctx context.Context, variables map[string]interface{}) (*CallbackEffect, error) {
	changeID := GetIntFromVars(variables, "change_id")
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	if _, err := h.client.Change.Query().Where(change.ID(changeID), change.HasWorkItemWith(ticket.TenantID(tenantID), ticket.DeletedAtIsNil())).Only(ctx); err != nil {
		return nil, fmt.Errorf("load change notification target: %w", err)
	}
	return BlockedEffect(CallbackBlockHandlerContract, fmt.Sprintf("change %d stakeholder delivery is not configured", changeID)), nil
}

var _ ServiceTaskHandlerInterface = (*ChangeServiceTaskHandler)(nil)
