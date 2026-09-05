package workitemcreation

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func catalogCommand() CreateWorkItemCommand {
	id := 101
	return CreateWorkItemCommand{IdempotencyKey: " key ", IntakeKind: "catalog_item", RecordClass: "service_request_item", Confirmation: "confirmed", Title: " Request ", CatalogItemID: &id, CatalogVersion: "catalog-v1", FormSchemaVersion: "form-v1"}
}
func TestDecodeRejectsInvalidWire(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{} {}`, `{"tenantId":2}`, `{"actorId":2}`, `{"requesterId":2}`, `{"incident":{"tenantId":2}}`, `{"incident":{"priority":"high"}}`, `{"incident":{"impactAnalysis":{"unknown":1}}}`, `{"incident":{"impactAnalysis":{"businessImpact":{"unknown":1}}}}`, `{"problem":{"impactScope":"global"}}`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := DecodeCreateWorkItemCommand(strings.NewReader(raw)); err == nil {
				t.Fatal("accepted invalid wire")
			}
		})
	}
}
func TestCanonicalSemanticFields(t *testing.T) {
	base := catalogCommand()
	_, digest, err := CanonicalizeCommand(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*CreateWorkItemCommand){
		"confirmation": func(c *CreateWorkItemCommand) { c.Confirmation = "" }, "catalogVersion": func(c *CreateWorkItemCommand) { c.CatalogVersion = "v2" }, "formSchemaVersion": func(c *CreateWorkItemCommand) { c.FormSchemaVersion = "v2" }, "recordClass": func(c *CreateWorkItemCommand) { c.RecordClass = "change_request" }, "priority": func(c *CreateWorkItemCommand) { c.Priority = "high" }, "assignment": func(c *CreateWorkItemCommand) { id := 2; c.AssigneeID = &id }, "group": func(c *CreateWorkItemCommand) { id := 2; c.AssignmentGroupID = &id }, "form": func(c *CreateWorkItemCommand) { c.FormValues = map[string]any{"duration": "30d"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := base
			change(&c)
			_, got, err := CanonicalizeCommand(c)
			if err == nil && got == digest {
				t.Fatal("semantic change omitted from digest")
			}
		})
	}
	normalized, d2, err := CanonicalizeCommand(base)
	if err != nil || normalized.IdempotencyKey != "key" || normalized.Title != "Request" {
		t.Fatalf("normalization: %+v %v", normalized, err)
	}
	base.IdempotencyKey = "different"
	_, d3, err := CanonicalizeCommand(base)
	if err != nil || d2 != d3 {
		t.Fatal("key entered semantic digest")
	}
}
func TestCanonicalRejectsInvalidStructure(t *testing.T) {
	cases := map[string]func(*CreateWorkItemCommand){"key": func(c *CreateWorkItemCommand) { c.IdempotencyKey = " " }, "version": func(c *CreateWorkItemCommand) { c.CatalogVersion = " " }, "formVersion": func(c *CreateWorkItemCommand) { c.FormSchemaVersion = "" }, "class": func(c *CreateWorkItemCommand) { c.RecordClass = "unknown" }, "kind": func(c *CreateWorkItemCommand) { c.IntakeKind = "unknown" }, "id": func(c *CreateWorkItemCommand) { id := -1; c.AssigneeID = &id }, "ci": func(c *CreateWorkItemCommand) { c.CIIDs = []int{0} }, "professional": func(c *CreateWorkItemCommand) { c.Incident = &IncidentInput{} }, "json": func(c *CreateWorkItemCommand) { c.FormValues = map[string]any{"bad": make(chan int)} }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := catalogCommand()
			change(&c)
			if _, _, err := CanonicalizeCommand(c); err == nil {
				t.Fatal("accepted invalid command")
			}
		})
	}
}
func TestCanonicalClonesAndOrdersMaps(t *testing.T) {
	c := catalogCommand()
	c.FormValues = map[string]any{"b": []any{map[string]any{"x": "before"}}, "a": 1}
	n, d, err := CanonicalizeCommand(c)
	if err != nil {
		t.Fatal(err)
	}
	other := catalogCommand()
	other.FormValues = map[string]any{"a": 1, "b": []any{map[string]any{"x": "before"}}}
	_, d2, err := CanonicalizeCommand(other)
	if err != nil || d != d2 {
		t.Fatal("equivalent maps differ")
	}
	c.FormValues["b"].([]any)[0].(map[string]any)["x"] = "after"
	if n.FormValues["b"].([]any)[0].(map[string]any)["x"] != "before" {
		t.Fatal("caller mutation changed canonical payload")
	}
}
func TestIncidentNestedFieldsAndTimestamps(t *testing.T) {
	raw := `{"idempotencyKey":"i","intakeKind":"incident","recordClass":"incident","confirmation":"confirmed","title":"Incident","incident":{"detectedAt":"2026-09-05T08:00:00+08:00","impactAnalysis":{"businessImpact":{"affectedUsers":3,"revenueImpact":4.5,"serviceAvailability":0.9},"technicalImpact":"down","affectedUsers":5,"timeImpact":{"isOverdue":true,"hoursSinceCreation":4,"responseDeadline":"2026-09-06T00:00:00Z","resolutionDeadline":"2026-09-07T00:00:00Z"}},"metadata":{"nested":{"value":1}}}}`
	c, err := DecodeCreateWorkItemCommand(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	n, d, err := CanonicalizeCommand(c)
	if err != nil {
		t.Fatal(err)
	}
	if n.Incident.DetectedAt != "2026-09-05T00:00:00Z" {
		t.Fatal("timestamp not UTC")
	}
	c.Incident.ImpactAnalysis.BusinessImpact.RevenueImpact = 8
	c.Incident.Metadata["nested"].(map[string]any)["value"] = 2
	if n.Incident.ImpactAnalysis.BusinessImpact.RevenueImpact != 4.5 || n.Incident.Metadata["nested"].(map[string]any)["value"] != json.Number("1") {
		t.Fatal("incident payload not cloned")
	}
	_, d2, err := CanonicalizeCommand(c)
	if err != nil || d == d2 {
		t.Fatal("nested impact omitted")
	}
	c.Incident.DetectedAt = "yesterday"
	if _, _, err := CanonicalizeCommand(c); err == nil {
		t.Fatal("invalid timestamp accepted")
	}
}
func TestCreateFixture(t *testing.T) {
	f, err := os.Open("../../../../docs/contracts/fixtures/intake-create.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := DecodeCreateWorkItemCommand(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = CanonicalizeCommand(c); err != nil {
		t.Fatal(err)
	}
}

// Removing any accepted field from canonical serialization must fail this test.
func TestEveryProfessionalFieldContributesToDigest(t *testing.T) {
	bodies := map[string]string{
		"generic":              `{"generic":{"type":"improvement","typeId":"3","source":"manual","category":"ops","templateId":2,"parentTicketId":3,"tagIds":[4],"workflowDefinitionKey":"process"}}`,
		"problem":              `{"problem":{"category":"ops","rootCause":"fault","impact":"high"}}`,
		"change_request":       `{"change":{"justification":"reason","type":"normal","impactScope":"service","riskLevel":"low","plannedStartDate":"2026-09-06T00:00:00Z","plannedEndDate":"2026-09-07T00:00:00Z","implementationPlan":"apply","rollbackPlan":"undo","affectedCis":["2"],"relatedTickets":[3]}}`,
		"incident":             `{"incident":{"type":"incident","severity":"high","impact":"medium","urgency":"low","category":"ops","subcategory":"network","detectedAt":"2026-09-05T00:00:00Z","source":"manual","metadata":{"deep":{"items":["a"]}},"impactAnalysis":{"businessImpact":{"affectedUsers":2,"revenueImpact":3.5,"serviceAvailability":0.9},"technicalImpact":"outage","affectedUsers":4,"timeImpact":{"isOverdue":true,"hoursSinceCreation":5,"responseDeadline":"2026-09-06T00:00:00Z","resolutionDeadline":"2026-09-07T00:00:00Z"}}}}`,
		"service_request_item": `{"serviceRequest":{"costCenter":"it","dataClassification":"internal","needsPublicIp":true,"sourceIpWhitelist":["192.0.2.1"],"expireAt":"2026-10-05T00:00:00Z","complianceAck":true,"contactName":"Fixture","contactEmail":"fixture@example.invalid","quantity":2,"expectedAt":"2026-09-06T00:00:00Z"}}`,
	}
	for class, body := range bodies {
		t.Run(class, func(t *testing.T) {
			var fields map[string]any
			if err := json.Unmarshal([]byte(body), &fields); err != nil {
				t.Fatal(err)
			}
			base := catalogCommand()
			base.RecordClass = class
			raw, _ := json.Marshal(base)
			var command map[string]any
			json.Unmarshal(raw, &command)
			for k, v := range fields {
				command[k] = v
			}
			decode := func(m map[string]any) (CreateWorkItemCommand, string) {
				raw, _ := json.Marshal(m)
				c, e := DecodeCreateWorkItemCommand(strings.NewReader(string(raw)))
				if e != nil {
					t.Fatal(e)
				}
				n, d, e := CanonicalizeCommand(c)
				if e != nil {
					t.Fatal(e)
				}
				return n, d
			}
			_, baseDigest := decode(command)
			var visit func(map[string]any, string)
			visit = func(node map[string]any, path string) {
				for k, v := range node {
					if child, ok := v.(map[string]any); ok {
						visit(child, path+"."+k)
						continue
					}
					delete(node, k)
					_, digest := decode(command)
					if digest == baseDigest {
						t.Errorf("field %s.%s omitted from digest", path, k)
					}
					node[k] = v
				}
			}
			for k, v := range fields {
				visit(v.(map[string]any), k)
			}
		})
	}
}
func TestCanonicalClonesTypedJSONContainers(t *testing.T) {
	c := catalogCommand()
	nested := map[string]string{"x": "before"}
	c.FormValues = map[string]any{"typed": []map[string]string{nested}}
	n, _, e := CanonicalizeCommand(c)
	if e != nil {
		t.Fatal(e)
	}
	nested["x"] = "after"
	if n.FormValues["typed"].([]any)[0].(map[string]any)["x"] != "before" {
		t.Fatal("typed map shared with caller")
	}
}

func TestSharedFieldsContributeAndIDsNormalize(t *testing.T) {
	c := catalogCommand()
	raw, _ := json.Marshal(c)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	optional := map[string]any{"description": "details", "priority": "urgent", "assigneeId": 2, "assignmentGroupId": 3, "cti": map[string]any{"categoryId": 2, "typeId": 3, "itemId": 4}, "ciIds": []int{2, 3}, "sourceReference": map[string]any{"provider": "kaf", "eventId": "event", "conversationId": "conversation"}}
	for k, v := range optional {
		fields[k] = v
	}
	digest := func() string {
		raw, _ := json.Marshal(fields)
		cmd, e := DecodeCreateWorkItemCommand(strings.NewReader(string(raw)))
		if e != nil {
			t.Fatal(e)
		}
		_, d, e := CanonicalizeCommand(cmd)
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	baseline := digest()
	for k, v := range optional {
		delete(fields, k)
		if got := digest(); got == baseline {
			t.Errorf("%s omitted", k)
		}
		fields[k] = v
	}
	fields["ciIds"] = []int{3, 2, 3}
	if digest() != baseline {
		t.Fatal("CI ordering/duplication changed digest")
	}
	for _, key := range []string{"cti", "sourceReference"} {
		node := fields[key].(map[string]any)
		for k, v := range node {
			if k == "provider" || k == "eventId" {
				node[k] = "changed"
			} else {
				delete(node, k)
			}
			if digest() == baseline {
				t.Errorf("%s.%s omitted", key, k)
			}
			node[k] = v
		}
	}
}
