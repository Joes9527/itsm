package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"itsm-backend/controller"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/intakerequest"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	problemdomain "itsm-backend/handlers/problem"
	standarddomain "itsm-backend/handlers/standard_change"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type requesterAdapter struct {
	name, resource, class, body string
	handle                      gin.HandlerFunc
	params                      gin.Params
}

func requesterAdapters(t *testing.T, client *ent.Client, app creation.Application, tenantID, actorID int) []requesterAdapter {
	t.Helper()
	incident := controller.NewIncidentController(nil, nil, nil, nil, nil, zap.NewNop().Sugar())
	incident.SetCreationApplication(app)
	problem := problemdomain.NewHandler(nil, client)
	problem.SetCreationApplication(app)
	change := changedomain.NewHandler(nil)
	change.SetCreationApplication(app)
	standard := standarddomain.NewHandler(client, zap.NewNop().Sugar())
	standard.SetCreationApplication(app)
	template := client.StandardChange.Create().SetTenantID(tenantID).SetCreatedBy(actorID).SetTitle("Standard backup").SetDescription("Backup database").SetJustification("Recovery point").SetImplementationPlan("Run backup").SetRollbackPlan("Restore backup").SetRiskLevel("low").SetImpactScope("low").SaveX(context.Background())
	return []requesterAdapter{
		{"incident", "incident", "incident", `{"title":"Customer outage","description":"Investigate customer outage","priority":"high","severity":"high","impact":"high","urgency":"high"}`, incident.CreateIncident, nil},
		{"problem", "problem", "problem", `{"title":"Customer investigation","description":"Investigate customer root cause","priority":"high"}`, problem.Create, nil},
		{"change", "change", "change_request", `{"title":"Customer configuration","description":"Change customer configuration","priority":"high","justification":"Reliability","type":"normal","impactScope":"low","riskLevel":"low","implementationPlan":"Apply configuration","rollbackPlan":"Restore configuration"}`, change.CreateChange, nil},
		{"standard_change", "change", "change_request", `{"title":"Customer backup"}`, standard.InstantiateStandardChange, gin.Params{{Key: "id", Value: strconv.Itoa(template.ID)}}},
	}
}

func withRequester(body, value string) string {
	return strings.TrimSuffix(body, "}") + `,"requesterId":` + value + `}`
}

// Includes allocator values: a rejected command must not consume a number.
func requesterGraph(t *testing.T, client *ent.Client) string {
	t.Helper()
	ctx := context.Background()
	seqs := client.WorkItemNumberSequence.Query().AllX(ctx)
	values := []string{}
	for _, s := range seqs {
		values = append(values, fmt.Sprintf("%d:%s:%d", s.TenantID, s.Period, s.LastValue))
	}
	sort.Strings(values)
	counts := []int{client.Ticket.Query().CountX(ctx), client.Incident.Query().CountX(ctx), client.Problem.Query().CountX(ctx), client.Change.Query().CountX(ctx), client.IntakeRequest.Query().CountX(ctx), client.IntakeResolutionSnapshot.Query().CountX(ctx), client.OutboxEvent.Query().CountX(ctx), client.AuditLog.Query().CountX(ctx), client.WorkItemRelation.Query().CountX(ctx), client.IncidentEvent.Query().CountX(ctx)}
	return fmt.Sprint(counts, values)
}

func assertRequesterReceipt(t *testing.T, w *httptest.ResponseRecorder, replayed bool) creation.CreateWorkItemResult {
	t.Helper()
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.ElementsMatch(t, []string{"workItemId", "number", "recordClass", "professionalReference", "workflowStartStatus", "replayed"}, requesterReceiptKeys(envelope.Data))
	var result struct {
		Data creation.CreateWorkItemResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Equal(t, replayed, result.Data.Replayed)
	require.Positive(t, result.Data.WorkItemID)
	require.Positive(t, result.Data.ProfessionalReference.ID)
	require.NotEmpty(t, result.Data.Number)
	return result.Data
}

func requesterReceiptKeys(m map[string]json.RawMessage) []string {
	keys := []string{}
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func assertRequesterProvenance(t *testing.T, client *ent.Client, result creation.CreateWorkItemResult, tenantID, actorTenantID, actorID, requesterID int) {
	t.Helper()
	ctx := context.Background()
	item := client.Ticket.GetX(ctx, result.WorkItemID)
	require.Equal(t, tenantID, item.TenantID)
	require.Equal(t, actorID, item.OpenedByID)
	require.Equal(t, requesterID, item.RequesterID)
	receipt := client.IntakeRequest.Query().Where(intakerequest.WorkItemID(item.ID)).OnlyX(ctx)
	require.Equal(t, actorTenantID, receipt.ActorTenantID)
	require.Equal(t, actorID, receipt.ActorID)
	require.Equal(t, requesterID, receipt.RequesterID)
	audit := client.AuditLog.Query().Where(auditlog.Action("intake.created"), auditlog.Resource(fmt.Sprintf("work_item:%d", item.ID))).OnlyX(ctx)
	var provenance creation.ActorProvenance
	require.NotNil(t, audit.RequestBody)
	require.NoError(t, json.Unmarshal([]byte(*audit.RequestBody), &provenance))
	require.Equal(t, actorID, provenance.ActorUserID)
	require.Equal(t, actorTenantID, provenance.ActorTenantID)
	require.Equal(t, tenantID, provenance.TargetTenantID)
	require.Equal(t, item.ID, provenance.WorkItemID)
	require.Equal(t, receipt.ID, provenance.IntakeRequestID)
}

// Dropping a requester mapping or its positive validator breaks these real
// Intake tests at the HTTP boundary or in the persisted requester/opener graph.
func TestIntakeHTTPRequesterAdapters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, name := range []string{"incident", "problem", "change", "standard_change"} {
		t.Run(name, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			var adapter requesterAdapter
			for _, candidate := range requesterAdapters(t, f.client, f.app, f.identity.TenantID, f.identity.ActorID) {
				if candidate.name == name {
					adapter = candidate
				}
			}
			// Replace the convenience fixture wildcard with explicit owning-resource permissions.
			f.client.RolePermission.Delete().ExecX(ctx)
			f.client.Permission.Delete().ExecX(ctx)
			role := f.client.Role.Query().OnlyX(ctx)
			var behalf *ent.RolePermission
			var behalfPermissionID int
			for _, action := range []string{"read", "write", "create_on_behalf"} {
				permission := f.client.Permission.Create().SetTenantID(f.identity.TenantID).SetCode(adapter.resource + ":" + action).SetName(action).SetResource(adapter.resource).SetAction(action).SaveX(ctx)
				grant := f.client.RolePermission.Create().SetTenantID(f.identity.TenantID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
				if action == "create_on_behalf" {
					behalf = grant
					behalfPermissionID = permission.ID
				}
			}
			if name == "standard_change" {
				permission := f.client.Permission.Create().SetTenantID(f.identity.TenantID).SetCode("standard_change:read").SetName("Read standard template").SetResource("standard_change").SetAction("read").SaveX(ctx)
				f.client.RolePermission.Create().SetTenantID(f.identity.TenantID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
			}
			requester := f.client.User.Create().SetTenantID(f.identity.TenantID).SetUsername("customer").SetName("Customer").SetEmail("customer@example.test").SetPasswordHash("unused").SetRole("requester").SaveX(ctx)
			foreignTenant := f.client.Tenant.Create().SetCode("foreign").SetName("Foreign").SaveX(ctx)
			foreign := f.client.User.Create().SetTenantID(foreignTenant.ID).SetUsername("foreign").SetName("Foreign").SetEmail("foreign@example.test").SetPasswordHash("unused").SetRole("requester").SaveX(ctx)
			body := withRequester(adapter.body, strconv.Itoa(requester.ID))
			w, _ := intakeHTTP(t, f, adapter.handle, body, "explicit", adapter.params)
			require.Equal(t, 201, w.Code, w.Body.String())
			first := assertRequesterReceipt(t, w, false)
			require.Equal(t, adapter.class, first.RecordClass)
			assertRequesterProvenance(t, f.client, first, f.identity.TenantID, f.identity.TenantID, f.identity.ActorID, requester.ID)
			stable := requesterGraph(t, f.client)
			w, _ = intakeHTTP(t, f, adapter.handle, body, "explicit", adapter.params)
			require.Equal(t, 200, w.Code, w.Body.String())
			replay := assertRequesterReceipt(t, w, true)
			require.Equal(t, first.WorkItemID, replay.WorkItemID)
			require.Equal(t, first.Number, replay.Number)
			require.Equal(t, stable, requesterGraph(t, f.client))
			for _, tc := range []struct {
				name, value string
				status      int
			}{{"zero", "0", 400}, {"negative", "-1", 400}, {"missing", "999999", 403}, {"foreign", strconv.Itoa(foreign.ID), 403}, {"changed_requester", strconv.Itoa(f.identity.ActorID), 409}} {
				t.Run(tc.name, func(t *testing.T) {
					key := tc.name
					if tc.name == "changed_requester" {
						key = "explicit"
					}
					w, _ := intakeHTTP(t, f, adapter.handle, withRequester(adapter.body, tc.value), key, adapter.params)
					require.Equal(t, tc.status, w.Code, w.Body.String())
					require.Equal(t, stable, requesterGraph(t, f.client))
				})
			}
			requester.Update().SetActive(false).ExecX(ctx)
			w, _ = intakeHTTP(t, f, adapter.handle, body, "inactive", adapter.params)
			require.Equal(t, 403, w.Code, w.Body.String())
			require.Equal(t, stable, requesterGraph(t, f.client))
			requester.Update().SetActive(true).ExecX(ctx)
			f.client.RolePermission.DeleteOne(behalf).ExecX(ctx)
			for _, key := range []string{"no-behalf", "explicit"} {
				w, _ = intakeHTTP(t, f, adapter.handle, body, key, adapter.params)
				require.Equal(t, 403, w.Code, w.Body.String())
				require.Equal(t, stable, requesterGraph(t, f.client))
			}
			f.client.RolePermission.Create().SetTenantID(f.identity.TenantID).SetRoleID(role.ID).SetPermissionID(behalfPermissionID).SaveX(ctx)
			for _, tc := range []struct{ name, body string }{{"omitted", adapter.body}, {"null", withRequester(adapter.body, "null")}} {
				w, _ = intakeHTTP(t, f, adapter.handle, tc.body, tc.name, adapter.params)
				require.Equal(t, 201, w.Code, w.Body.String())
				result := assertRequesterReceipt(t, w, false)
				assertRequesterProvenance(t, f.client, result, f.identity.TenantID, f.identity.TenantID, f.identity.ActorID, f.identity.ActorID)
			}
		})
	}
}
