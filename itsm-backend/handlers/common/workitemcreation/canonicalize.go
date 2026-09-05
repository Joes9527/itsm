package workitemcreation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const CanonicalDigestVersion = "intake-v4"

func invalid(field, message string) error {
	return NewInvalidCommand("invalid intake command", FieldError{Field: field, Message: message}, nil)
}
func CanonicalizeCommand(command CreateWorkItemCommand) (CreateWorkItemCommand, string, error) {
	// JSON round trip detaches all nested maps, slices and pointers, including typed map values.
	payload, err := json.Marshal(command)
	if err != nil {
		return CreateWorkItemCommand{}, "", invalid("body", "must contain JSON-compatible values")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var n CreateWorkItemCommand
	if err = decoder.Decode(&n); err != nil {
		return n, "", invalid("body", "must contain JSON-compatible values")
	}
	n.IdempotencyKey = strings.TrimSpace(n.IdempotencyKey)
	n.IntakeKind = strings.TrimSpace(n.IntakeKind)
	n.RecordClass = strings.TrimSpace(n.RecordClass)
	n.Confirmation = strings.TrimSpace(n.Confirmation)
	n.Title = strings.TrimSpace(n.Title)
	n.Description = strings.TrimSpace(n.Description)
	n.Priority = strings.TrimSpace(n.Priority)
	n.CatalogVersion = strings.TrimSpace(n.CatalogVersion)
	n.FormSchemaVersion = strings.TrimSpace(n.FormSchemaVersion)
	if n.IdempotencyKey == "" || utf8.RuneCountInString(n.IdempotencyKey) > 200 {
		return n, "", invalid("idempotencyKey", "must contain 1 to 200 characters")
	}
	if (n.Title == "" && (n.Problem == nil || n.Problem.SourceIncidentID == nil) && (n.Change == nil || n.Change.StandardTemplateID == nil)) || utf8.RuneCountInString(n.Title) > 500 {
		return n, "", invalid("title", "must contain 1 to 500 characters")
	}
	if utf8.RuneCountInString(n.Description) > 20000 {
		return n, "", invalid("description", "must not exceed 20000 characters")
	}
	if n.Confirmation != "confirmed" {
		return n, "", invalid("confirmation", "must be confirmed")
	}
	if !IsSupportedRecordClass(n.RecordClass) {
		return n, "", invalid("recordClass", "unsupported record class")
	}
	if n.IntakeKind == IntakeKindCatalogItem {
		if n.CatalogItemID == nil || n.CatalogVersion == "" || n.FormSchemaVersion == "" {
			return n, "", invalid("catalogItemId", "catalog ID and both versions are required")
		}
	} else {
		if n.IntakeKind != n.RecordClass || n.RecordClass == RecordClassServiceRequestItem {
			return n, "", invalid("intakeKind", "must match a supported direct creation class")
		}
		if n.CatalogItemID != nil || n.CatalogVersion != "" || n.FormSchemaVersion != "" {
			return n, "", invalid("catalogItemId", "catalog fields require catalog_item")
		}
	}
	for _, v := range []struct {
		present bool
		class   string
	}{{n.Incident != nil, RecordClassIncident}, {n.Change != nil, RecordClassChangeRequest}, {n.Generic != nil, RecordClassGeneric}, {n.Problem != nil, RecordClassProblem}, {n.ServiceRequest != nil, RecordClassServiceRequestItem}} {
		if v.present && v.class != n.RecordClass {
			return n, "", invalid("recordClass", "professional input does not match record class")
		}
	}
	ids := map[string]*int{"catalogItemId": n.CatalogItemID, "assigneeId": n.AssigneeID, "assignmentGroupId": n.AssignmentGroupID}
	if n.CTI != nil {
		ids["cti.categoryId"] = n.CTI.CategoryID
		ids["cti.typeId"] = n.CTI.TypeID
		ids["cti.itemId"] = n.CTI.ItemID
	}
	ids["templateId"] = n.TemplateID
	ids["parentTicketId"] = n.ParentTicketID
	for field, id := range ids {
		if id != nil && *id <= 0 {
			return n, "", invalid(field, "must be positive")
		}
	}
	if n.CIIDs, err = normalizeCIIDs(n.CIIDs); err != nil {
		return n, "", err
	}
	if n.SourceReference != nil {
		r := n.SourceReference
		r.Provider = strings.TrimSpace(r.Provider)
		r.EventID = strings.TrimSpace(r.EventID)
		r.ConversationID = strings.TrimSpace(r.ConversationID)
		if r.Provider == "" || r.EventID == "" {
			return n, "", invalid("sourceReference", "provider and eventId are required")
		}
	}
	if n.FeishuTask != nil {
		f := n.FeishuTask
		f.TaskGUID = strings.TrimSpace(f.TaskGUID)
		f.CreatorOpenID = strings.TrimSpace(f.CreatorOpenID)
		f.Status = strings.TrimSpace(f.Status)
		if n.RecordClass != RecordClassGeneric || n.Email != nil || n.SourceReference == nil || n.SourceReference.Provider != "feishu" || n.SourceReference.EventID != f.TaskGUID || f.TaskGUID == "" || f.CreatorOpenID == "" {
			return n, "", invalid("feishuTask", "complete verified Feishu task identity is required")
		}
	}
	if n.Email != nil {
		e := n.Email
		e.Mailbox = strings.ToLower(strings.TrimSpace(e.Mailbox))
		e.SenderEmail = strings.ToLower(strings.TrimSpace(e.SenderEmail))
		e.GraphMessageID = strings.TrimSpace(e.GraphMessageID)
		if n.RecordClass != RecordClassGeneric || n.SourceReference == nil || n.SourceReference.Provider != "msgraph_email" || e.Mailbox == "" || e.SenderEmail == "" || e.GraphMessageID == "" {
			return n, "", invalid("email", "complete verified email source is required")
		}
		if len(e.TriageComment) > 20000 {
			return n, "", invalid("email.triageComment", "must not exceed 20000 bytes")
		}
	}
	if n.Generic != nil {
		g := n.Generic
		g.Type = strings.TrimSpace(g.Type)
		g.TypeID = strings.TrimSpace(g.TypeID)
		g.Source = strings.TrimSpace(g.Source)
		g.Category = strings.TrimSpace(g.Category)
		if g.TypeID != "" {
			id, e := strconv.Atoi(g.TypeID)
			if e != nil || id <= 0 {
				return n, "", invalid("generic.typeId", "must be a positive integer ID")
			}
			g.TypeID = strconv.Itoa(id)
		}
	}
	n.WorkflowDefinitionKey = strings.TrimSpace(n.WorkflowDefinitionKey)
	if n.TagIDs, err = normalizeCIIDs(n.TagIDs); err != nil {
		return n, "", err
	}
	if len(n.AdHocFields) > 0 {
		if n.TemplateID != nil || n.CatalogItemID != nil {
			return n, "", invalid("adHocFields", "cannot replace catalog or template definitions")
		}
		if len(n.AdHocFields) > 100 {
			return n, "", invalid("adHocFields", "must not exceed 100 fields")
		}
		names := map[string]bool{}
		for index := range n.AdHocFields {
			field := &n.AdHocFields[index]
			field.Name = strings.TrimSpace(field.Name)
			field.Label = strings.TrimSpace(field.Label)
			if field.Name == "" || len(field.Name) > 100 || len(field.Label) > 255 || names[field.Name] {
				return n, "", invalid("adHocFields", "field names must be unique and bounded")
			}
			if field.Label == "" {
				field.Label = field.Name
			}
			names[field.Name] = true
		}
		for name := range n.FormValues {
			if !names[name] {
				return n, "", invalid("formValues."+name, "ad-hoc field definition is required")
			}
		}
	}
	if n.Problem != nil {
		if n.Problem.SourceIncidentID != nil && *n.Problem.SourceIncidentID <= 0 {
			return n, "", invalid("problem.sourceIncidentId", "must be positive")
		}
		p := n.Problem
		p.Category = strings.TrimSpace(p.Category)
		p.RootCause = strings.TrimSpace(p.RootCause)
		p.Impact = strings.TrimSpace(p.Impact)
	}
	dates := map[string]*string{}
	if n.Incident != nil {
		i := n.Incident
		i.Type = strings.TrimSpace(i.Type)
		i.Severity = strings.TrimSpace(i.Severity)
		i.Impact = strings.TrimSpace(i.Impact)
		i.Urgency = strings.TrimSpace(i.Urgency)
		i.Category = strings.TrimSpace(i.Category)
		i.Subcategory = strings.TrimSpace(i.Subcategory)
		i.Source = strings.TrimSpace(i.Source)
		dates["incident.detectedAt"] = &i.DetectedAt
		for field, value := range map[string]string{"incident.severity": i.Severity, "incident.impact": i.Impact, "incident.urgency": i.Urgency} {
			if value != "" && value != "low" && value != "medium" && value != "high" && value != "critical" {
				return n, "", invalid(field, "unsupported level")
			}
		}
		if i.ImpactAnalysis != nil && i.ImpactAnalysis.TimeImpact != nil {
			ti := i.ImpactAnalysis.TimeImpact
			dates["incident.impactAnalysis.timeImpact.responseDeadline"] = &ti.ResponseDeadline
			dates["incident.impactAnalysis.timeImpact.resolutionDeadline"] = &ti.ResolutionDeadline
		}
	}
	if n.Change != nil {
		c := n.Change
		if c.StandardTemplateID != nil && *c.StandardTemplateID <= 0 {
			return n, "", invalid("change.standardTemplateId", "must be positive")
		}
		refs := map[string]bool{}
		for _, number := range c.RelatedTicketNumbers {
			number = strings.TrimSpace(number)
			if number == "" {
				return n, "", invalid("change.relatedTicketNumbers", "number is required")
			}
			refs[number] = true
		}
		c.RelatedTicketNumbers = nil
		for number := range refs {
			c.RelatedTicketNumbers = append(c.RelatedTicketNumbers, number)
		}
		sort.Strings(c.RelatedTicketNumbers)
		c.Justification = strings.TrimSpace(c.Justification)
		c.Type = strings.TrimSpace(c.Type)
		c.ImpactScope = strings.TrimSpace(c.ImpactScope)
		c.RiskLevel = strings.TrimSpace(c.RiskLevel)
		c.ImplementationPlan = strings.TrimSpace(c.ImplementationPlan)
		c.RollbackPlan = strings.TrimSpace(c.RollbackPlan)
		dates["change.plannedStartDate"] = &c.PlannedStartDate
		dates["change.plannedEndDate"] = &c.PlannedEndDate
		ci := []int{}
		for _, value := range c.AffectedCIs {
			id, e := strconv.Atoi(strings.TrimSpace(value))
			if e != nil || id <= 0 {
				return n, "", invalid("change.affectedCis", "must contain positive integer IDs")
			}
			ci = append(ci, id)
		}
		ci, _ = normalizeCIIDs(ci)
		c.AffectedCIs = nil
		for _, id := range ci {
			c.AffectedCIs = append(c.AffectedCIs, strconv.Itoa(id))
		}
		if c.RelatedTickets, err = normalizeCIIDs(c.RelatedTickets); err != nil {
			return n, "", err
		}
	}
	if n.ServiceRequest != nil {
		s := n.ServiceRequest
		if s.CloudResourceRefID != nil && *s.CloudResourceRefID <= 0 {
			return n, "", invalid("serviceRequest.cloudResourceRefId", "must be positive")
		}
		dates["serviceRequest.expireAt"] = &s.ExpireAt
		dates["serviceRequest.expectedAt"] = &s.ExpectedAt
		if s.Quantity != nil && (*s.Quantity < 1 || *s.Quantity > 1000) {
			return n, "", invalid("serviceRequest.quantity", "must be between 1 and 1000")
		}
	}
	for field, value := range dates {
		*value = strings.TrimSpace(*value)
		if *value != "" {
			v, e := time.Parse(time.RFC3339Nano, *value)
			if e != nil {
				return n, "", invalid(field, "must be an RFC3339 timestamp")
			}
			*value = v.UTC().Format(time.RFC3339Nano)
		}
	}
	canonical := n
	canonical.IdempotencyKey = ""
	payload, err = json.Marshal(struct {
		Version string                `json:"version"`
		Command CreateWorkItemCommand `json:"command"`
	}{CanonicalDigestVersion, canonical})
	if err != nil {
		return n, "", invalid("body", "could not be canonicalized")
	}
	digest := sha256.Sum256(payload)
	return n, hex.EncodeToString(digest[:]), nil
}
func normalizeCIIDs(ids []int) ([]int, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	unique := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, NewInvalidCommand("invalid intake command", FieldError{Field: "ciIds", Message: "must contain only positive IDs"}, nil)
		}
		unique[id] = struct{}{}
	}
	normalized := make([]int, 0, len(unique))
	for id := range unique {
		normalized = append(normalized, id)
	}
	sort.Ints(normalized)
	return normalized, nil
}
