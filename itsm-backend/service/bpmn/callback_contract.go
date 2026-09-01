package bpmn

// CallbackActionContract is the handler-owned allowlist for one declared
// callback action. Only these fields may cross the durable callback boundary.
type CallbackActionContract struct {
	PayloadFields     []string
	RequiredFields    []string
	ConfigRefRequired bool
}

// CallbackContractProvider is implemented only by synchronous handlers.
// Asynchronous handlers (such as KAF delegation) are intentionally excluded.
type CallbackContractProvider interface {
	CallbackContract(action string) (CallbackActionContract, bool)
}

func callbackActionContract(payload, required []string) CallbackActionContract {
	return CallbackActionContract{
		PayloadFields:  append([]string(nil), payload...),
		RequiredFields: append([]string(nil), required...),
	}
}

func (h *ChangeServiceTaskHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	payload := map[string][]string{
		"create_change":       {"title", "description", "type", "priority", "created_by"},
		"update_change":       {"title", "description", "status"},
		"approve_change":      nil,
		"reject_change":       nil,
		"schedule_change":     {"planned_start_date", "planned_end_date"},
		"implement_change":    nil,
		"verify_change":       {"verification_result"},
		"close_change":        {"feedback"},
		"assess_risk":         nil,
		"notify_stakeholders": {"notification_type"},
	}
	fields, ok := payload[action]
	return callbackActionContract(fields, nil), ok
}

func (h *IncidentServiceTaskHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	payload := map[string][]string{
		"create_incident":      {"title", "description", "type", "priority", "severity", "reporter_id"},
		"assign_incident":      {"assignee_id"},
		"escalate_incident":    {"escalation_level", "escalation_reason"},
		"resolve_incident":     {"resolution"},
		"close_incident":       {"feedback"},
		"update_incident":      {"title", "description", "priority", "severity", "status"},
		"acknowledge_incident": nil,
		"categorize_incident":  {"category", "subcategory"},
	}
	fields, ok := payload[action]
	return callbackActionContract(fields, nil), ok
}

func (h *TicketServiceTaskHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	payload := map[string][]string{
		"update_status":    {"new_status"},
		"notify_requester": {"notification_type", "content"},
		"notify_handler":   {"notification_type", "content"},
		"escalate":         {"escalate_to", "escalation_reason", "notify_admin_ids"},
		"assign":           {"assignee_id", "notify_content"},
	}
	fields, ok := payload[action]
	return callbackActionContract(fields, nil), ok
}

func (h *ServiceRequestServiceTaskHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	payload := map[string][]string{
		"create_request":     nil,
		"update_request":     {"cost_center", "data_classification", "needs_public_ip", "source_ip_whitelist", "expire_at", "compliance_ack"},
		"approve_request":    nil,
		"reject_request":     {"reject_reason"},
		"assign_request":     {"assignee_id"},
		"provision_resource": {"resource_type"},
		"complete_request":   {"completion_note"},
		"cancel_request":     {"cancel_reason"},
	}
	fields, ok := payload[action]
	return callbackActionContract(fields, nil), ok
}

func (h *NotificationHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	switch action {
	case "send_in_app":
		return callbackActionContract([]string{"user_ids", "title", "content", "notification_type"}, nil), true
	case "send_email", "send_sms", "send_webhook":
		return callbackActionContract(nil, nil), true
	default:
		return CallbackActionContract{}, false
	}
}

func (h *CCTaskHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	if action != "" {
		return CallbackActionContract{}, false
	}
	return callbackActionContract([]string{"ccType", "ccTargets", "ccUserIds", "ccGroupIds", "ccRoleIds", "ccVariable", "ccNotify", "notifyChannels", "ccResolvedUserIds"}, nil), true
}

func (h *WebhookHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	switch action {
	case "call_webhook", "send_notification":
		return callbackActionContract([]string{"event_type", "title", "content"}, nil), true
	default:
		return CallbackActionContract{}, false
	}
}

func (h *ReleaseServiceTaskHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	switch action {
	case "tech_review":
		return callbackActionContract([]string{"comment"}, nil), true
	case "approval", "schedule", "execute", "verify":
		return callbackActionContract(nil, nil), true
	default:
		return CallbackActionContract{}, false
	}
}
