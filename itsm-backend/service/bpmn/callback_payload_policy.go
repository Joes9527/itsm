package bpmn

// Built-in handlers explicitly own their durable payload schemas. Identity,
// routing, endpoints, headers, and credentials are intentionally absent.

func (h *ChangeServiceTaskHandler) CallbackPayloadFields(action string) []string {
	switch action {
	case "create_change":
		return []string{"title", "description", "type", "priority", "created_by"}
	case "update_change":
		return []string{"title", "description", "status"}
	case "schedule_change":
		return []string{"planned_start_date", "planned_end_date"}
	case "verify_change":
		return []string{"verification_result"}
	case "close_change":
		return []string{"feedback"}
	case "notify_stakeholders":
		return []string{"notification_type"}
	default:
		return nil
	}
}

func (h *IncidentServiceTaskHandler) CallbackPayloadFields(action string) []string {
	switch action {
	case "create_incident":
		return []string{"title", "description", "type", "priority", "severity", "reporter_id"}
	case "assign_incident":
		return []string{"assignee_id"}
	case "escalate_incident":
		return []string{"escalation_level", "escalation_reason"}
	case "resolve_incident":
		return []string{"resolution"}
	case "close_incident":
		return []string{"feedback"}
	case "update_incident":
		return []string{"title", "description", "priority", "severity", "status"}
	case "categorize_incident":
		return []string{"category", "subcategory"}
	default:
		return nil
	}
}

func (h *TicketServiceTaskHandler) CallbackPayloadFields(action string) []string {
	switch action {
	case "update_status":
		return []string{"new_status"}
	case "notify_requester", "notify_handler":
		return []string{"notification_type", "content"}
	case "escalate":
		return []string{"escalate_to", "escalation_reason", "notify_admin_ids"}
	case "assign":
		return []string{"assignee_id", "notify_content"}
	default:
		return nil
	}
}

func (h *GenericServiceTaskHandler) CallbackPayloadFields(action string) []string {
	switch action {
	case "complete_service":
		return []string{"operation"}
	case "notify_rejection":
		return []string{"reject_reason"}
	default:
		return nil
	}
}

func (h *ServiceRequestServiceTaskHandler) CallbackPayloadFields(action string) []string {
	switch action {
	case "update_request":
		return []string{"cost_center", "data_classification", "needs_public_ip", "source_ip_whitelist", "expire_at", "compliance_ack"}
	case "reject_request":
		return []string{"reject_reason"}
	case "assign_request":
		return []string{"assignee_id"}
	case "provision_resource":
		return []string{"resource_type"}
	case "complete_request":
		return []string{"completion_note"}
	case "cancel_request":
		return []string{"cancel_reason"}
	default:
		return nil
	}
}

func (h *NotificationHandler) CallbackPayloadFields(action string) []string {
	switch action {
	case "send_in_app":
		return []string{"user_ids", "title", "content", "notification_type"}
	default:
		return nil
	}
}

var ccCallbackPayloadFields = []string{
	"ccType", "ccUserIds", "ccGroupIds", "ccRoleIds", "ccVariable", "ccNotify", "notifyChannels", "ccResolvedUserIds",
}

func (h *CCTaskHandler) CallbackPayloadFields(action string) []string {
	return append([]string(nil), ccCallbackPayloadFields...)
}

func (h *WebhookHandler) CallbackPayloadFields(action string) []string {
	return []string{"event_type", "title", "content"}
}

func (h *ReleaseServiceTaskHandler) CallbackPayloadFields(action string) []string {
	if action == "tech_review" {
		return []string{"comment"}
	}
	return nil
}

func (h *KafDelegateServiceTaskHandler) CallbackPayloadFields(action string) []string {
	return nil
}

var (
	_ CallbackPayloadPolicy     = (*ChangeServiceTaskHandler)(nil)
	_ CallbackPayloadPolicy     = (*IncidentServiceTaskHandler)(nil)
	_ CallbackPayloadPolicy     = (*TicketServiceTaskHandler)(nil)
	_ CallbackPayloadPolicy     = (*GenericServiceTaskHandler)(nil)
	_ CallbackPayloadPolicy     = (*ServiceRequestServiceTaskHandler)(nil)
	_ CallbackPayloadPolicy     = (*NotificationHandler)(nil)
	_ CallbackPayloadPolicy     = (*CCTaskHandler)(nil)
	_ CallbackPayloadPolicy     = (*WebhookHandler)(nil)
	_ CallbackPayloadPolicy     = (*ReleaseServiceTaskHandler)(nil)
	_ CallbackPayloadPolicy     = (*KafDelegateServiceTaskHandler)(nil)
	_ CallbackPayloadNormalizer = (*CCTaskHandler)(nil)
)
