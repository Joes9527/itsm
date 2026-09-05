package controller

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"itsm-backend/dto"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/middleware"
	"strings"
)

func (tc *TicketController) SetCreationApplication(app creation.Application) {
	tc.creationApplication = app
}
func (tc *TicketController) createFromRequest(c *gin.Context, req dto.CreateTicketRequest) {
	tenantID, err := middleware.ResolveRequestTenantID(c)
	if middleware.AbortIfTenantError(c, err) {
		return
	}
	command, err := ticketCreationCommand(req)
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	intakehttp.Execute(c, tc.creationApplication, tenantID, req.RequesterID, command)
}
func ticketCreationCommand(req dto.CreateTicketRequest) (creation.CreateWorkItemCommand, error) {
	command := creation.CreateWorkItemCommand{Title: req.Title, Description: req.Description, Priority: req.Priority, TemplateID: req.TemplateID, ParentTicketID: req.ParentTicketID, TagIDs: req.TagIDs, WorkflowDefinitionKey: req.WorkflowDefinitionKey}
	for field, present := range map[string]bool{"creatorEmail": req.CreatorEmail != "", "externalMessageId": req.ExternalMessageID != "", "conversationId": req.ConversationID != "", "attachments": len(req.Attachments) > 0, "tags": len(req.Tags) > 0, "approvalChain": req.ApprovalChain != nil, "source": req.Source != "" && req.Source != "manual"} {
		if present {
			return command, intakehttp.Invalid(field, "unsupported public creation field: "+field)
		}
	}
	if req.AssigneeID != 0 {
		command.AssigneeID = &req.AssigneeID
	}
	if req.CategoryID != nil {
		command.CTI = &creation.CTIInput{CategoryID: req.CategoryID}
	}
	kind := strings.TrimSpace(req.Type)
	switch kind {
	case "", "ticket", "improvement":
		command.RecordClass = creation.RecordClassGeneric
		command.Generic = &creation.GenericInput{Type: kind, TypeID: req.TypeID, Category: req.Category, Source: "manual"}
	case "incident":
		command.RecordClass = creation.RecordClassIncident
		command.Incident = &creation.IncidentInput{Category: req.Category, Source: "manual"}
	case "problem":
		command.RecordClass = creation.RecordClassProblem
		command.Problem = &creation.ProblemInput{Category: req.Category}
	case "change":
		command.RecordClass = creation.RecordClassChangeRequest
		command.Change = &creation.ChangeInput{}
	case "service_request":
		return command, intakehttp.Invalid("type", "service_request creation requires the catalog creation contract and confirmed revisions")
	default:
		return command, creation.NewUnsupportedRecordClass("unsupported ticket creation type", nil)
	}
	if command.RecordClass != creation.RecordClassGeneric && req.TypeID != "" {
		return command, intakehttp.Invalid("typeId", "professional subtype metadata must use its owning creation contract")
	}
	if command.RecordClass == creation.RecordClassChangeRequest && req.Category != "" && req.CategoryID == nil {
		return command, intakehttp.Invalid("category", "change creation requires categoryId for shared classification")
	}
	command.IntakeKind = command.RecordClass
	values, defs, preset, err := ticketCreationFields(req.FormFields)
	if err != nil {
		return command, err
	}
	command.FormValues = values
	command.AdHocFields = defs
	command.FormPresetID = preset
	return command, nil
}
func ticketCreationFields(fields map[string]any) (map[string]any, []creation.AdHocFieldDefinition, string, error) {
	fail := func(field string) (map[string]any, []creation.AdHocFieldDefinition, string, error) {
		return nil, nil, "", intakehttp.Invalid("formFields."+field, "invalid or unsupported form field envelope")
	}
	for name := range fields {
		if name != "values" && name != "fieldDefs" && name != "presetTypeId" {
			return fail(name)
		}
	}
	preset := ""
	if raw, ok := fields["presetTypeId"]; ok {
		var valid bool
		preset, valid = raw.(string)
		if !valid {
			return fail("presetTypeId")
		}
	}
	values := map[string]any{}
	if raw, present := fields["values"]; present {
		switch typed := raw.(type) {
		case map[string]any:
			for key, value := range typed {
				values[key] = value
			}
		case []any:
			for _, rawEntry := range typed {
				entry, ok := rawEntry.(map[string]any)
				if !ok || len(entry) != 2 {
					return fail("values")
				}
				name, ok := entry["name"].(string)
				if !ok || strings.TrimSpace(name) == "" {
					return fail("values.name")
				}
				value, present := entry["value"]
				if !present {
					return fail("values.value")
				}
				if _, exists := values[name]; exists {
					return fail("values.name")
				}
				values[name] = value
			}
		default:
			return fail("values")
		}
	}
	defs := []creation.AdHocFieldDefinition{}
	if raw, present := fields["fieldDefs"]; present {
		entries, ok := raw.([]any)
		if !ok {
			return fail("fieldDefs")
		}
		for index, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok || len(entry) != 2 {
				return fail(fmt.Sprintf("fieldDefs.%d", index))
			}
			name, validName := entry["name"].(string)
			label, validLabel := entry["label"].(string)
			if !validName || !validLabel {
				return fail("fieldDefs")
			}
			defs = append(defs, creation.AdHocFieldDefinition{Name: name, Label: label})
		}
	}
	return values, defs, preset, nil
}
