package bpmn

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/handlers/shared/workflowcallback"

	"go.uber.org/zap"
)

type ServiceRequestServiceTaskHandler struct {
	HandlerBase
	service workflowcallback.ServiceRequestService
}

func NewServiceRequestServiceTaskHandler(_ *ent.Client, _ *zap.SugaredLogger) *ServiceRequestServiceTaskHandler {
	return &ServiceRequestServiceTaskHandler{}
}

func (h *ServiceRequestServiceTaskHandler) SetServiceRequestService(service workflowcallback.ServiceRequestService) {
	h.service = service
}

func (h *ServiceRequestServiceTaskHandler) GetTaskType() string  { return "service_request_task" }
func (h *ServiceRequestServiceTaskHandler) GetHandlerID() string { return "service_request_handler" }

func (h *ServiceRequestServiceTaskHandler) Execute(ctx context.Context, _ *ent.ProcessTask, variables map[string]interface{}) (*CallbackEffect, error) {
	action, _ := variables["action"].(string)
	if action == "create_request" {
		return BlockedEffect(CallbackBlockHandlerContract, "service requests are created before BPMN starts"), nil
	}
	switch action {
	case "update_request", "approve_request", "reject_request", "assign_request", "provision_resource", "complete_request", "cancel_request":
	default:
		return BlockedEffect(CallbackBlockHandlerContract, "unsupported service request callback action"), nil
	}
	if h.service == nil {
		return nil, fmt.Errorf("service request service is not injected")
	}
	command, err := bindServiceRequestWorkflowCommand(ctx, action, variables)
	if err != nil {
		return nil, err
	}
	result, err := h.service.ApplyServiceRequestWorkflowCallback(ctx, command)
	if err != nil {
		return nil, err
	}
	return callbackEffectFromWorkflowResult(result)
}

func bindServiceRequestWorkflowCommand(ctx context.Context, action string, variables map[string]interface{}) (workflowcallback.ServiceRequestCommand, error) {
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return workflowcallback.ServiceRequestCommand{}, err
	}
	command := workflowcallback.ServiceRequestCommand{Action: action, RequestID: GetIntFromVars(variables, "request_id"), TenantID: tenantID}
	if command.RequestID <= 0 {
		return command, fmt.Errorf("invalid service request id")
	}
	if action == "update_request" {
		command.FormData, _ = variables["form_data"].(map[string]interface{})
		if value, ok := variables["cost_center"].(string); ok && value != "" {
			command.CostCenter = &value
		}
		if value, ok := variables["data_classification"].(string); ok && value != "" {
			command.DataClassification = &value
		}
		if value, ok := variables["needs_public_ip"].(bool); ok {
			command.NeedsPublicIP = &value
		}
		if value, ok := variables["compliance_ack"].(bool); ok {
			command.ComplianceAck = &value
		}
		if raw, ok := variables["expire_at"].(string); ok && raw != "" {
			value, parseErr := time.Parse(time.RFC3339, raw)
			if parseErr != nil {
				return command, fmt.Errorf("expire_at must be RFC3339")
			}
			command.ExpireAt = &value
		}
		if raw, ok := variables["source_ip_whitelist"].([]string); ok {
			command.SourceIPWhitelist = &raw
		} else if raw, ok := variables["source_ip_whitelist"].([]interface{}); ok {
			values := make([]string, 0, len(raw))
			for _, item := range raw {
				value, ok := item.(string)
				if !ok {
					return command, fmt.Errorf("source_ip_whitelist must contain strings")
				}
				values = append(values, value)
			}
			command.SourceIPWhitelist = &values
		}
	}
	if action == "assign_request" {
		command.AssigneeID = GetIntFromVars(variables, "assignee_id")
	}
	if action == "provision_resource" {
		command.ResourceType, _ = variables["resource_type"].(string)
	}
	if action == "complete_request" {
		command.CompletionNote, _ = variables["completion_note"].(string)
	}
	if action == "reject_request" {
		command.CompletionNote, _ = variables["reject_reason"].(string)
		if command.CompletionNote == "" {
			command.CompletionNote = "rejected"
		}
	}
	if action == "cancel_request" {
		command.CompletionNote, _ = variables["cancel_reason"].(string)
		if command.CompletionNote == "" {
			command.CompletionNote = "cancelled"
		}
	}
	return command, nil
}

var _ ServiceTaskHandlerInterface = (*ServiceRequestServiceTaskHandler)(nil)
