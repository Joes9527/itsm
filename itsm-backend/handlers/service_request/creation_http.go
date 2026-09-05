package service_request

import (
	"encoding/json"
	"itsm-backend/dto"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"
	"time"
)

// catalogCreationCommand normalizes the supported form envelope once. Definition
// revisions are supplied by the confirmed read and are never refreshed here.
func catalogCreationCommand(req dto.CreateServiceRequestRequest, present func(string) bool) (creation.CreateWorkItemCommand, error) {
	command := creation.CreateWorkItemCommand{RecordClass: req.RecordClass, IntakeKind: creation.IntakeKindCatalogItem, CatalogItemID: &req.CatalogID, CatalogVersion: req.CatalogVersion, FormSchemaVersion: req.FormSchemaVersion, Title: req.Title, Description: req.Reason, Priority: req.Priority, AssigneeID: req.AssigneeID, CTI: req.CTI, CIIDs: req.CIIDs, Generic: req.Generic, Incident: req.Incident, Problem: req.Problem, Change: req.Change}
	if req.CatalogID <= 0 {
		return command, intakehttp.Invalid("catalogId", "positive catalogId is required")
	}
	form := map[string]any{}
	for key, value := range req.FormData {
		form[key] = value
	}
	bad := func(key string) error { return intakehttp.Invalid("formData."+key, "invalid system form value") }
	input := creation.ServiceRequestInput{CostCenter: req.CostCenter, DataClassification: req.DataClassification, NeedsPublicIP: req.NeedsPublicIP, SourceIPWhitelist: req.SourceIPWhitelist, ComplianceAck: req.ComplianceAck, ContactName: req.ContactName, ContactEmail: req.ContactEmail}
	if req.ExpireAt != nil {
		input.ExpireAt = req.ExpireAt.UTC().Format(time.RFC3339Nano)
	}
	if req.ExpectedAt != nil {
		input.ExpectedAt = req.ExpectedAt.UTC().Format(time.RFC3339Nano)
	}
	if present("quantity") {
		input.Quantity = &req.Quantity
	}
	for _, field := range []struct {
		key, wire string
		target    *string
	}{
		{"title", "title", &command.Title}, {"reason", "reason", &command.Description}, {"cost_center", "costCenter", &input.CostCenter}, {"data_classification", "dataClassification", &input.DataClassification}, {"contact_name", "contactName", &input.ContactName}, {"contact_email", "contactEmail", &input.ContactEmail}, {"expire_at", "expireAt", &input.ExpireAt}, {"expected_at", "expectedAt", &input.ExpectedAt},
	} {
		if raw, ok := form[field.key]; ok {
			value, valid := raw.(string)
			if !valid {
				return command, bad(field.key)
			}
			if !present(field.wire) {
				*field.target = value
			}
			delete(form, field.key)
		}
	}
	for _, field := range []struct {
		key, wire string
		target    *bool
	}{{"compliance_ack", "complianceAck", &input.ComplianceAck}, {"needs_public_ip", "needsPublicIp", &input.NeedsPublicIP}} {
		if raw, ok := form[field.key]; ok {
			value, valid := raw.(bool)
			if !valid {
				return command, bad(field.key)
			}
			if !present(field.wire) {
				*field.target = value
			}
			delete(form, field.key)
		}
	}
	if raw, ok := form["source_ip_whitelist"]; ok {
		entries, valid := raw.([]any)
		if !valid {
			return command, bad("source_ip_whitelist")
		}
		values := []string{}
		for _, entry := range entries {
			value, valid := entry.(string)
			if !valid {
				return command, bad("source_ip_whitelist")
			}
			values = append(values, value)
		}
		if !present("sourceIpWhitelist") {
			input.SourceIPWhitelist = values
		}
		delete(form, "source_ip_whitelist")
	}
	for _, key := range []string{"quantity", "cloud_resource_ref_id"} {
		if raw, ok := form[key]; ok {
			number, valid := raw.(json.Number)
			if !valid {
				return command, bad(key)
			}
			value, err := strconv.Atoi(string(number))
			if err != nil {
				return command, bad(key)
			}
			if key == "quantity" {
				if !present("quantity") {
					input.Quantity = &value
				}
			} else {
				input.CloudResourceRefID = &value
			}
			delete(form, key)
		}
	}
	if raw, ok := form["amount"]; ok {
		value, valid := raw.(json.Number)
		if !valid {
			return command, bad("amount")
		}
		input.Amount = value
		delete(form, "amount")
	}
	if raw, ok := form["customFieldValues"]; ok {
		entries, valid := raw.([]any)
		if !valid {
			return command, bad("customFieldValues")
		}
		delete(form, "customFieldValues")
		for _, entry := range entries {
			value, valid := entry.(map[string]any)
			if !valid || len(value) != 2 {
				return command, bad("customFieldValues")
			}
			name, valid := value["name"].(string)
			if !valid || name == "" {
				return command, bad("customFieldValues.name")
			}
			fieldValue, exists := value["value"]
			if !exists {
				return command, bad("customFieldValues.value")
			}
			if _, exists := form[name]; exists {
				return command, bad("customFieldValues.name")
			}
			form[name] = fieldValue
		}
	}
	if req.RecordClass == creation.RecordClassServiceRequestItem {
		command.ServiceRequest = &input
	} else {
		if input.CostCenter != "" || input.DataClassification != "" || input.NeedsPublicIP || len(input.SourceIPWhitelist) > 0 || input.ExpireAt != "" || input.ComplianceAck || input.ContactName != "" || input.ContactEmail != "" || input.Quantity != nil || input.ExpectedAt != "" || input.Amount != "" || input.CloudResourceRefID != nil {
			return command, intakehttp.Invalid("recordClass", "service request fields require service_request_item")
		}
	}
	command.FormValues = form
	return command, nil
}
