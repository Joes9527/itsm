package bpmn

// CallbackActionContract is the handler-owned allowlist for one declared
// callback action. Only these fields may cross the durable callback boundary.
type CallbackActionContract struct {
	LifecycleRecordClass  string
	CreatedRecordClass    string
	PayloadFields         []string
	PositiveIntegerFields []string
	// NonEmptyStringFields lists payload fields that, when present, must be a
	// non-blank JSON string. RequiredFields only proves key presence, so an
	// action whose value reaches a user-facing command needs this stronger,
	// handler-owned rule as well.
	NonEmptyStringFields []string
	RequiredFields       []string
	ConfigRefRequired    bool
	// RejectInvalidUserInput marks an action whose payload violations are
	// user-fixable input errors. The task-completion command must reject those
	// before writing the task or a callback, instead of persisting a callback
	// that can never succeed. Actions that leave this false keep the existing
	// visible blocked-plan behaviour reserved for definition defects.
	RejectInvalidUserInput bool
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
		"create_change":       {"title", "description", "type", "priority", "created_by", "justification", "impact_scope", "risk_level", "planned_start_date", "planned_end_date", "implementation_plan", "rollback_plan", "affected_cis", "source_relations", "assignee_id", "ci_ids", "template_id", "parent_ticket_id", "tag_ids", "workflow_definition_key", "form_values"},
		"update_change":       {"title", "description", "status", "version"},
		"approve_change":      {"version", "evidence"},
		"authorize_change":    {"version", "evidence"},
		"review_change":       {"version", "evidence", "pir_id"},
		"cancel_change":       {"version", "evidence"},
		"reject_change":       {"version", "evidence"},
		"schedule_change":     {"version", "planned_start_date", "planned_end_date"},
		"implement_change":    {"version"},
		"verify_change":       {"version", "outcome", "evidence", "actual_end_date"},
		"close_change":        {"version", "evidence", "pir_id"},
		"assess_risk":         {"version", "evidence"},
		"notify_stakeholders": {"notification_type"},
	}
	fields, ok := payload[action]
	contract := callbackActionContract(fields, nil)
	if action == "create_change" {
		contract.CreatedRecordClass = "change_request"
	}
	switch action {
	case "update_change", "assess_risk", "approve_change", "authorize_change", "reject_change", "schedule_change", "implement_change", "verify_change", "review_change", "close_change", "cancel_change":
		contract.LifecycleRecordClass = "change_request"
	}
	return contract, ok
}

func (h *IncidentServiceTaskHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	payload := map[string][]string{
		"create_incident":      {"title", "description", "type", "priority", "severity", "reporter_id", "impact", "urgency", "category", "subcategory", "detected_at", "impact_analysis", "metadata", "source", "assignee_id", "ci_ids", "template_id", "parent_ticket_id", "tag_ids", "workflow_definition_key", "form_values"},
		"assign_incident":      {"assignee_id", "version", "reason"},
		"escalate_incident":    {"escalation_level", "escalation_reason", "version"},
		"resolve_incident":     {"resolution", "version"},
		"start_incident":       {"version"},
		"close_incident":       {"feedback", "reason", "version"},
		"reopen_incident":      {"reason", "version"},
		"update_incident":      {"title", "description", "priority", "severity", "status", "version"},
		"acknowledge_incident": {"version"},
		"categorize_incident":  {"category", "subcategory", "version"},
	}
	fields, ok := payload[action]
	contract := callbackActionContract(fields, nil)
	if action == "create_incident" {
		contract.CreatedRecordClass = "incident"
	}
	switch action {
	case "acknowledge_incident", "start_incident", "resolve_incident", "close_incident", "reopen_incident", "assign_incident", "escalate_incident":
		contract.LifecycleRecordClass = "incident"
	}
	if action == "assign_incident" {
		contract.PositiveIntegerFields = []string{"assignee_id"}
	}
	return contract, ok
}

func (h *TicketServiceTaskHandler) CallbackContract(action string) (CallbackActionContract, bool) {
	payload := map[string][]string{
		"update_status":    {"new_status"},
		"notify_requester": {"notification_type", "content"},
		"notify_handler":   {"notification_type", "content"},
		"escalate":         {"escalate_to", "escalation_reason", "notify_admin_ids", "version"},
		"assign":           {"assignee_id", "notify_content"},
	}
	fields, ok := payload[action]
	contract := callbackActionContract(fields, nil)
	if action == "escalate" {
		contract.LifecycleRecordClass = "generic"
		contract.PositiveIntegerFields = []string{"version"}
	}
	// Activity_Assign takes its target from the actor and has no fixed fallback,
	// so a completion that cannot supply it must be rejected instead of
	// persisting a callback that can never succeed.
	if action == "assign" {
		contract.RequiredFields = []string{"assignee_id"}
		contract.PositiveIntegerFields = []string{"assignee_id"}
		contract.RejectInvalidUserInput = true
	}
	// update_status deliberately keeps a fixed fallback: updateTicketStatus maps a
	// missing new_status to "in_progress". Per the execution contract §2.3 a
	// requirement is only rejected when the fixed configuration cannot satisfy the
	// input, so absence stays valid here and the stricter "must be supplied" rule
	// belongs to the gated generic lifecycle contract (workItemLifecycleContract
	// = generic_fulfillment_v1). Only an explicitly supplied value is bound.
	if action == "update_status" {
		contract.NonEmptyStringFields = []string{"new_status"}
	}
	return contract, ok
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
	contract := callbackActionContract(fields, nil)
	if action == "assign_request" {
		contract.PositiveIntegerFields = []string{"assignee_id"}
	}
	return contract, ok
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
