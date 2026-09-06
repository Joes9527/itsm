package integration

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"itsm-backend/controller"
	creation "itsm-backend/handlers/common/workitemcreation"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestIntakeInputMeaningLegacyChangeCategory(t *testing.T) {
	for _, scenario := range []string{"unique", "unknown", "ambiguous", "foreign", "id precedence", "foreign id"} {
		t.Run(scenario, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			category := f.client.TicketCategory.Create().SetTenantID(f.identity.TenantID).SetName("Network").SetCode("network").SaveX(ctx)
			foreign := f.client.Tenant.Create().SetName("Foreign").SetCode("foreign").SaveX(ctx)
			foreignCategory := f.client.TicketCategory.Create().SetTenantID(foreign.ID).SetName("Foreign only").SetCode("foreign").SaveX(ctx)
			body := map[string]any{"title": "Change network", "description": "Change network configuration", "priority": "medium", "type": "change", "category": " Network "}
			want := "change.justification is required"
			switch scenario {
			case "unknown":
				body["category"] = "Missing"
				want = "ReferenceNotFound"
			case "ambiguous":
				f.client.TicketCategory.Create().SetTenantID(f.identity.TenantID).SetName("Network").SetCode("duplicate").SaveX(ctx)
				want = "ReferenceNotFound"
			case "foreign":
				body["category"] = "Foreign only"
				want = "ReferenceNotFound"
			case "id precedence":
				body["category"] = "Missing"
				body["categoryId"] = category.ID
			case "foreign id":
				body["categoryId"] = foreignCategory.ID
				want = "ReferenceNotFound"
			}
			handler := controller.NewTicketController(nil, nil, nil, f.client, zap.NewNop().Sugar())
			handler.SetCreationApplication(f.app)
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			w, _ := intakeHTTP(t, f, handler.CreateTicket, string(raw), "change-category", nil)
			require.Contains(t, w.Body.String(), want)
			assertNoEntryGraph(t, f.client)
		})
	}
}
func TestIntakeInputMeaningLegacyPresetRejectsBeforeGraph(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	handler := controller.NewTicketController(nil, nil, nil, f.client, zap.NewNop().Sugar())
	handler.SetCreationApplication(f.app)
	w, _ := intakeHTTP(t, f, handler.CreateTicket, `{"title":"Manual task","description":"Manual request description","priority":"medium","type":"improvement","formFields":{"presetTypeId":"custom-entry","fieldDefs":[{"name":"amount","label":"Amount"}],"values":[{"name":"amount","value":3}]}}`, "stale-preset", nil)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "formFields.presetTypeId")
	assertNoEntryGraph(t, f.client)
}
func TestIntakeInputMeaningTemplateWorkflowSteps(t *testing.T) {
	for _, tc := range []struct {
		name, steps string
		valid       bool
	}{
		{"absent", "", true}, {"null", "null", true}, {"empty", "[]", true}, {"spaced empty", " [ ] ", true}, {"steps", `[{"type":"approval"}]`, false}, {"null step", "[null]", false}, {"object", "{}", false}, {"string", `""`, false}, {"malformed", "[", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			builder := f.client.TicketTemplate.Create().SetTenantID(f.identity.TenantID).SetName("Template").SetCategory("general")
			if tc.steps != "" {
				builder.SetWorkflowSteps([]byte(tc.steps))
			}
			template := builder.SaveX(ctx)
			handler := controller.NewTicketController(nil, nil, nil, f.client, zap.NewNop().Sugar())
			handler.SetCreationApplication(f.app)
			body := `{"title":"Template task","description":"Template request description","priority":"medium","type":"improvement","templateId":` + strconv.Itoa(template.ID) + `}`
			w, result := intakeHTTP(t, f, handler.CreateTicket, body, "template", nil)
			if !tc.valid {
				require.Contains(t, w.Body.String(), "WorkflowBindingRequired")
				assertNoEntryGraph(t, f.client)
				return
			}
			require.Equal(t, 201, w.Code, w.Body.String())
			require.Equal(t, "not_required", result.WorkflowStartStatus)
			require.Equal(t, template.ID, f.client.Ticket.GetX(ctx, result.WorkItemID).TemplateID)
			template.Update().SetWorkflowSteps([]byte(`[{"type":"later"}]`)).SaveX(ctx)
			f.client.ProcessBinding.Update().SetProcessDefinitionKey("changed-later").SetConditions(map[string]any{}).SaveX(ctx)
			w, replay := intakeHTTP(t, f, handler.CreateTicket, body, "template", nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.True(t, replay.Replayed)
			require.Equal(t, result.WorkItemID, replay.WorkItemID)
			require.Equal(t, "not_required", replay.WorkflowStartStatus)
		})
	}
}
func TestIntakeInputMeaningChangeCategoryPersistence(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	category := f.client.TicketCategory.Create().SetTenantID(f.identity.TenantID).SetName("Network").SetCode("network").SaveX(ctx)
	raw := `{"idempotencyKey":"change","intakeKind":"change_request","recordClass":"change_request","confirmation":"confirmed","title":"Change","change":{"category":" Network ","justification":"Security","impactScope":"low","riskLevel":"low","implementationPlan":"Deploy","rollbackPlan":"Restore"}}`
	var command creation.CreateWorkItemCommand
	require.NoError(t, json.Unmarshal([]byte(raw), &command))
	result, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)
	require.Equal(t, category.ID, f.client.Ticket.GetX(ctx, result.WorkItemID).CategoryID)
	require.Positive(t, result.ProfessionalReference.ID)
}

// An accepted pre-slice receipt must replay without a global digest-version cutover.
func TestIntakeInputMeaningPreviousV4ReceiptReplay(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	first, err := f.app.Create(ctx, f.identity, f.command)
	require.NoError(t, err)
	receipt := f.client.IntakeRequest.Query().OnlyX(ctx)
	require.Equal(t, "2ec76db736b1fd9435526ee7bc7da176e37d1b83d7ac522cd997273b9dd9e08d", receipt.RequestDigest)
	require.Equal(t, "intake-v4", receipt.DigestVersion)
	replay, err := f.app.Create(ctx, f.identity, f.command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
}
