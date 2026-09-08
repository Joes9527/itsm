package workitemcreation

import "time"

// NewPlan copies resolved shared fields. The owning domain supplies lifecycle,
// priority and source policy; this helper never dispatches professional behavior.
func NewPlan(in ResolvedIntake, status, priority, source string) *CreationPlan {
	categoryID := in.CTI.CategoryID
	if in.CTI.TypeID != nil {
		categoryID = in.CTI.TypeID
	}
	if in.CTI.ItemID != nil {
		categoryID = in.CTI.ItemID
	}
	return &CreationPlan{Resolved: in, WorkflowVariables: map[string]any{
		"title": in.Command.Title, "description": in.Command.Description, "priority": priority, "status": status,
		"form_values": in.Command.FormValues, "approval_required": false,
	}, WorkItem: WorkItemDraft{
		TenantID: in.Identity.TenantID, ActorID: in.Identity.ActorID, RequesterID: in.Identity.RequesterID,
		TemplateID: in.Command.TemplateID, ParentTicketID: in.Command.ParentTicketID, TagIDs: append([]int(nil), in.Command.TagIDs...),
		RecordClass: in.RecordClass, Title: in.Command.Title, Description: in.Command.Description,
		Status: status, Priority: priority, Source: source, AssigneeID: in.Command.AssigneeID,
		AssignmentGroupID: in.Command.AssignmentGroupID, CategoryID: categoryID, SLADefinitionID: in.SLADefinitionID,
	}}
}

func ParseOptionalTime(value, field string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, NewDomainValidationFailed("invalid date", err, FieldError{Field: field, Message: "must be an RFC3339 timestamp"})
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
