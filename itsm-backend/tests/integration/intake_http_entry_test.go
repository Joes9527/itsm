package integration

import (
	"context"
	"encoding/json"
	"errors"
	"itsm-backend/controller"
	"itsm-backend/ent"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	problemdomain "itsm-backend/handlers/problem"
	catalogdomain "itsm-backend/handlers/service_catalog"
	requestdomain "itsm-backend/handlers/service_request"
	standarddomain "itsm-backend/handlers/standard_change"
	"itsm-backend/middleware"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func intakeHTTP(t *testing.T, f *unifiedIntakeFixture, handle gin.HandlerFunc, body, key string, params gin.Params) (*httptest.ResponseRecorder, creation.CreateWorkItemResult) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Idempotency-Key", key)
	c.Set("tenant_id", f.identity.TenantID)
	c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: f.identity.TenantID})
	c.Set("user_id", f.identity.ActorID)
	c.Set("role", f.identity.Role)
	c.Params = params
	handle(c)
	var envelope struct {
		Data creation.CreateWorkItemResult `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	return w, envelope.Data
}
func TestIntakeHTTPProblemAndIncidentEntry(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	f.client.TicketCategory.Create().SetTenantID(f.identity.TenantID).SetName("network").SetCode("network").SaveX(ctx)
	problem := problemdomain.NewHandler(nil, f.client)
	problem.SetCreationApplication(f.app)
	body := `{"title":"Root cause investigation","description":"Investigate service degradation","priority":"high","category":"network","rootCause":"packet loss","impact":"regional"}`
	w, result := intakeHTTP(t, f, problem.Create, body, "problem-http", nil)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "problem", result.RecordClass)
	require.Positive(t, result.ProfessionalReference.ID)
	p := f.client.Problem.GetX(ctx, result.ProfessionalReference.ID)
	require.Equal(t, "packet loss", p.RootCause)
	w, replay := intakeHTTP(t, f, problem.Create, body, "problem-http", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.True(t, replay.Replayed)
	require.Equal(t, result.WorkItemID, replay.WorkItemID)
	w, _ = intakeHTTP(t, f, problem.Create, strings.TrimSuffix(body, "}")+`,"impactScope":"ignored"}`, "bad", nil)
	require.Equal(t, 400, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "impactScope")
	incident := controller.NewIncidentController(nil, nil, nil, nil, nil, zap.NewNop().Sugar())
	incident.SetCreationApplication(f.app)
	incidentBody := `{"title":"Service unavailable","description":"Detailed service failure","priority":"critical","severity":"high","impact":"high","urgency":"high","type":"alert","source":"manual","impactAnalysis":{"businessImpact":{"revenueImpact":9007199254740993.125}},"detectedAt":"2026-09-05T08:00:00+08:00"}`
	w, result = intakeHTTP(t, f, incident.CreateIncident, incidentBody, "incident-http", nil)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "incident", result.RecordClass)
	require.Positive(t, result.ProfessionalReference.ID)
	w, replay = intakeHTTP(t, f, incident.CreateIncident, incidentBody, "incident-http", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.True(t, replay.Replayed)
	w, _ = intakeHTTP(t, f, incident.CreateIncident, incidentBody, "", nil)
	require.Equal(t, 400, w.Code)
}

func TestIntakeHTTPChangeReferencesAndStandardTemplate(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	source, err := f.app.Create(ctx, f.identity, f.command)
	require.NoError(t, err)
	handler := changedomain.NewHandler(nil)
	handler.SetCreationApplication(f.app)
	body := `{"title":"Change service configuration","description":"Change description","justification":"service reliability","type":"normal","priority":"high","riskLevel":"low","impactScope":"medium","implementationPlan":"apply configuration","rollbackPlan":"restore configuration","plannedStartDate":"2026-09-06T08:00:00+08:00","plannedEndDate":"2026-09-06T09:00:00+08:00","relatedTickets":["` + source.Number + `"]}`
	w, result := intakeHTTP(t, f, handler.CreateChange, body, "change-http", nil)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "change_request", result.RecordClass)
	require.Positive(t, result.ProfessionalReference.ID)
	require.Equal(t, 1, f.client.WorkItemRelation.Query().CountX(ctx))
	ch := f.client.Change.GetX(ctx, result.ProfessionalReference.ID)
	require.Equal(t, "restore configuration", ch.RollbackPlan)
	template := f.client.StandardChange.Create().SetTenantID(f.identity.TenantID).SetCreatedBy(f.identity.ActorID).SetTitle("Standard backup").SetDescription("Backup database").SetImplementationPlan("Run backup").SetRollbackPlan("Restore previous").SetJustification("Recovery point").SetRiskLevel("low").SetImpactScope("low").SaveX(ctx)
	standard := standarddomain.NewHandler(f.client, zap.NewNop().Sugar())
	standard.SetCreationApplication(f.app)
	params := gin.Params{{Key: "id", Value: strconv.Itoa(template.ID)}}
	w, result = intakeHTTP(t, f, standard.InstantiateStandardChange, `{"title":"Backup production"}`, "standard-http", params)
	require.Equal(t, 201, w.Code, w.Body.String())
	ch = f.client.Change.GetX(ctx, result.ProfessionalReference.ID)
	require.Equal(t, "standard", ch.Type)
	require.Equal(t, "Run backup", ch.ImplementationPlan)
	template.Update().SetImplementationPlan("Changed later").SaveX(ctx)
	w, replay := intakeHTTP(t, f, standard.InstantiateStandardChange, `{"title":"Backup production"}`, "standard-http", params)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, result.WorkItemID, replay.WorkItemID)
	require.Equal(t, "Run backup", f.client.Change.GetX(ctx, result.ProfessionalReference.ID).ImplementationPlan)
}

func TestIntakeSharedProfessionalEntryFieldsAndAdHocRollback(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(strconv.FormatBool(failure), func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			parent, err := f.app.Create(ctx, f.identity, f.command)
			require.NoError(t, err)
			command := creation.CreateWorkItemCommand{IdempotencyKey: "subtask", Confirmation: "confirmed", IntakeKind: "incident", RecordClass: "incident", Title: "Investigate subsystem", Priority: "high", ParentTicketID: &parent.WorkItemID, AdHocFields: []creation.AdHocFieldDefinition{{Name: "quantity", Label: "Device count"}}, FormValues: map[string]any{"quantity": json.Number("9007199254740993")}}
			if failure {
				f.client.FieldValue.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						return nil, errors.New("injected field persistence failure")
					})
				})
			}
			result, err := f.app.Create(ctx, f.identity, command)
			if failure {
				require.Error(t, err)
				require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
				require.Zero(t, f.client.Incident.Query().CountX(ctx))
				require.Zero(t, f.client.FieldValue.Query().CountX(ctx))
				return
			}
			require.NoError(t, err)
			item := f.client.Ticket.GetX(ctx, result.WorkItemID)
			require.Equal(t, parent.WorkItemID, item.ParentTicketID)
			row := f.client.FieldValue.Query().OnlyX(ctx)
			require.Equal(t, "Device count", row.FieldLabel)
			require.Equal(t, "9007199254740993", string(row.Value))
		})
	}
}

func TestIntakeHTTPManualTicketSubtaskAndProfessionalClass(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	handler := controller.NewTicketController(nil, nil, nil, f.client, zap.NewNop().Sugar())
	handler.SetCreationApplication(f.app)
	body := `{"title":"Manual task","description":"Manual request description","priority":"medium","type":"improvement","formFields":{"fieldDefs":[{"name":"amount","label":"Amount"}],"values":[{"name":"amount","value":9007199254740993.125}]}}`
	w, result := intakeHTTP(t, f, handler.CreateTicket, body, "manual-http", nil)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "generic", result.RecordClass)
	require.Zero(t, result.ProfessionalReference.ID)
	require.Equal(t, "improvement", f.client.Ticket.GetX(ctx, result.WorkItemID).GenericSubtype)
	require.Equal(t, "9007199254740993.125", string(f.client.FieldValue.Query().OnlyX(ctx).Value))
	subtask := `{"title":"Restore subsystem","description":"Professional child","priority":"high","type":"incident"}`
	w, child := intakeHTTP(t, f, handler.CreateSubtask, subtask, "subtask-http", gin.Params{{Key: "id", Value: strconv.Itoa(result.WorkItemID)}})
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "incident", child.RecordClass)
	require.Equal(t, result.WorkItemID, f.client.Ticket.GetX(ctx, child.WorkItemID).ParentTicketID)
	for _, extra := range []string{`,"creatorEmail":"forged@example.test"`, `,"attachments":["raw"]`, `,"approvalChain":[]`, `,"tags":["ignored"]`, `,"source":"service_catalog"`} {
		raw := strings.TrimSuffix(body, "}") + extra + "}"
		w, _ = intakeHTTP(t, f, handler.CreateTicket, raw, "invalid", nil)
		require.Equal(t, 400, w.Code, w.Body.String())
	}
}

func TestIntakeHTTPCatalogTargetsUseConfirmedRevisions(t *testing.T) {
	for _, class := range []string{"generic", "incident", "problem", "change_request", "service_request_item"} {
		t.Run(class, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			catalog := f.client.ServiceCatalog.Create().SetTenantID(f.identity.TenantID).SetName("Catalog service").SetCategory("general").SetDescription("Configured service").SetTargetClass(class).SetRequiresApproval(false).SetStatus("enabled").SaveX(ctx)
			owner := catalogdomain.NewService(nil, f.client, zap.NewNop().Sugar())
			tx, err := f.client.Tx(ctx)
			require.NoError(t, err)
			resolved, _, err := owner.ResolveCreationCatalog(ctx, tx, f.identity, catalog.ID)
			require.NoError(t, err)
			require.NoError(t, tx.Rollback())
			handler := requestdomain.NewHandler(nil)
			handler.SetCreationApplication(f.app)
			body := map[string]any{"catalogId": catalog.ID, "catalogVersion": resolved.Version, "formSchemaVersion": resolved.FormSchemaVersion, "recordClass": class, "title": "Configured catalog request", "reason": "Business reason", "priority": "high"}
			if class == "change_request" {
				body["change"] = map[string]any{"justification": "Apply catalog service configuration", "impactScope": "low", "riskLevel": "medium", "implementationPlan": "Back up configuration, apply catalog settings and verify", "rollbackPlan": "Restore saved configuration"}
			}
			if class == "service_request_item" {
				body["contactName"] = "Requester"
				body["contactEmail"] = "contact@example.test"
				body["quantity"] = 3
				body["costCenter"] = "CC100"
			}
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			w, result := intakeHTTP(t, f, handler.Create, string(raw), "catalog-http", nil)
			require.Equal(t, 201, w.Code, w.Body.String())
			require.Equal(t, class, result.RecordClass)
			w, replay := intakeHTTP(t, f, handler.Create, string(raw), "catalog-http", nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.True(t, replay.Replayed)
			if class == "service_request_item" {
				row := f.client.ServiceRequest.GetX(ctx, result.ProfessionalReference.ID)
				require.Equal(t, 3, row.Quantity)
				require.Equal(t, "CC100", row.CostCenter)
			}
			catalog.Update().SetDescription("Changed definition").SaveX(ctx)
			w, _ = intakeHTTP(t, f, handler.Create, string(raw), "new-stale-key", nil)
			require.Equal(t, 409, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "CatalogVersionConflict")
		})
	}
}

// Template expansion supplies the professional policy values before validation.
// A configured low-risk default is authoritative; an incomplete template must
// still fail at the same Change owner and leave no creation graph.
func TestIntakeStandardChangeRequiredFieldsAfterTemplateExpansion(t *testing.T) {
	for _, field := range []string{"justification", "impactScope", "riskLevel", "implementationPlan", "rollbackPlan"} {
		t.Run(field, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			template := f.client.StandardChange.Create().SetTenantID(f.identity.TenantID).SetCreatedBy(f.identity.ActorID).
				SetTitle("Standard database backup").SetDescription("Create a recoverable daily backup").
				SetJustification("Meet the recovery point objective").SetImplementationPlan("Run backup and verify checksum").SetRollbackPlan("Retain previous verified backup").SaveX(ctx)
			update := template.Update()
			switch field {
			case "justification":
				update.SetJustification("   ")
			case "impactScope":
				update.SetImpactScope("   ")
			case "riskLevel":
				update.SetRiskLevel("   ")
			case "implementationPlan":
				update.SetImplementationPlan("   ")
			case "rollbackPlan":
				update.SetRollbackPlan("   ")
			}
			update.SaveX(ctx)
			handler := standarddomain.NewHandler(f.client, zap.NewNop().Sugar())
			handler.SetCreationApplication(f.app)
			params := gin.Params{{Key: "id", Value: strconv.Itoa(template.ID)}}
			w, _ := intakeHTTP(t, f, handler.InstantiateStandardChange, `{}`, "incomplete-template", params)
			require.Equal(t, 400, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "change."+field+" is required")
			assertNoEntryGraph(t, f.client)
			require.Zero(t, f.client.Change.Query().CountX(ctx))
			restore := template.Update()
			switch field {
			case "justification":
				restore.SetJustification("Meet the recovery point objective")
			case "impactScope":
				restore.SetImpactScope("low")
			case "riskLevel":
				restore.SetRiskLevel("low")
			case "implementationPlan":
				restore.SetImplementationPlan("Run backup and verify checksum")
			case "rollbackPlan":
				restore.SetRollbackPlan("Retain previous verified backup")
			}
			restore.SaveX(ctx)
			w, result := intakeHTTP(t, f, handler.InstantiateStandardChange, `{}`, "complete-template", params)
			require.Equal(t, 201, w.Code, w.Body.String())
			change := f.client.Change.GetX(ctx, result.ProfessionalReference.ID)
			require.Equal(t, "standard", change.Type)
			require.Equal(t, "Meet the recovery point objective", change.Justification)
			require.Equal(t, "Run backup and verify checksum", change.ImplementationPlan)
			require.Equal(t, "Retain previous verified backup", change.RollbackPlan)
			require.Equal(t, "low", change.RiskLevel)
			require.Equal(t, "low", change.ImpactScope)
			require.Equal(t, "Standard database backup", f.client.Ticket.GetX(ctx, result.WorkItemID).Title)
		})
	}
}
