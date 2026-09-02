//go:build integration_postgres

package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	"itsm-backend/middleware"
	itsmservice "itsm-backend/service"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type e2eNumbers struct{ value atomic.Int64 }

func (n *e2eNumbers) GenerateWorkItemNumber(context.Context, int) (string, error) {
	return fmt.Sprintf("TKT-E2E-%06d", n.value.Add(1)), nil
}

func (n *e2eNumbers) GenerateIncidentNumberForIntake(context.Context, int) (string, error) {
	return fmt.Sprintf("INC-E2E-%06d", n.value.Add(1)), nil
}

type e2eProcessEngine struct {
	client *ent.Client
	err    error
}

func (e *e2eProcessEngine) StartProcessByDefinitionID(ctx context.Context, definitionID int, businessKey, businessType string, businessID int, variables map[string]any) (*ent.ProcessInstance, error) {
	if e.err != nil {
		return nil, e.err
	}
	existing, err := e.client.ProcessInstance.Query().Where(
		processinstance.ProcessDefinitionIDEQ(definitionID),
		processinstance.BusinessKeyEQ(businessKey),
	).Only(ctx)
	if err == nil {
		return existing, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}
	definition, err := e.client.ProcessDefinition.Get(ctx, definitionID)
	if err != nil {
		return nil, err
	}
	return e.client.ProcessInstance.Create().
		SetProcessInstanceID("PI-" + businessKey).
		SetBusinessKey(businessKey).
		SetBusinessType(businessType).
		SetBusinessID(businessID).
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetStatus("running").
		SetVariables(variables).
		SetTenantID(definition.TenantID).
		Save(ctx)
}

type e2eEnvelope struct {
	Code any             `json:"code"`
	Data json.RawMessage `json:"data"`
}

func postE2E(t *testing.T, router http.Handler, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeE2EResult(t *testing.T, response *httptest.ResponseRecorder) CreateWorkItemResult {
	t.Helper()
	var envelope e2eEnvelope
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	var result CreateWorkItemResult
	require.NoError(t, json.Unmarshal(envelope.Data, &result), response.Body.String())
	return result
}

func TestUnifiedIntakeE2EPostgresHTTPIdentityReplayAndWorkflowRecovery(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("INTAKE_POSTGRES_TEST_DSN not set")
	}
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := ent.Open("postgres", dsn)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.Schema.Create(ctx), "test DSN must point to a disposable database")

	suffix := fmt.Sprint(time.Now().UnixNano())
	tenant, err := client.Tenant.Create().SetName("Intake E2E " + suffix).SetCode("intake-e2e-" + suffix).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	roleCode := "intake_e2e_" + suffix
	role, err := client.Role.Create().SetName("Intake E2E Creator").SetCode(roleCode).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	permission, err := client.Permission.Create().SetCode("intake:create").SetName("Intake Create").SetResource("intake").SetAction("create").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.RolePermission.Create().SetRoleID(role.ID).SetPermissionID(permission.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	middleware.InvalidateRolePermissionCache(roleCode, tenant.ID)
	userEntity, err := client.User.Create().SetUsername("intake-e2e-" + suffix).SetEmail("intake-e2e-" + suffix + "@example.com").
		SetName("Intake E2E Requester").SetPasswordHash("hash").SetRole(roleCode).SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.ExternalIdentity.Create().SetTenantID(tenant.ID).SetProvider("microsoft").SetWorkspace("it-support").
		SetSubject("e2e-subject-" + suffix).SetUserID(userEntity.ID).Save(ctx)
	require.NoError(t, err)

	seedE2EDefinition := func(key, version, subtype string) {
		deployment, createErr := client.ProcessDeployment.Create().SetDeploymentID(key + "-deploy-" + suffix).
			SetDeploymentName(key).SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, createErr)
		_, createErr = client.ProcessDefinition.Create().SetKey(key).SetName(key).SetVersion(version).
			SetBpmnXML([]byte("<definitions/>")).SetIsActive(true).SetIsLatest(true).
			SetDeploymentID(deployment.ID).SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, createErr)
		_, createErr = client.ProcessBinding.Create().SetBusinessType("ticket").SetBusinessSubType(subtype).
			SetProcessDefinitionKey(key).SetProcessVersion(1).SetPriority(100).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, createErr)
	}
	seedE2EDefinition("incident-e2e-"+suffix, "1", "incident")
	seedE2EDefinition("request-e2e-"+suffix, "1", "service_request")
	catalog, err := client.ServiceCatalog.Create().SetName("E2E access request").SetStatus("enabled").SetIsActive(true).
		SetTenantID(tenant.ID).SetTargetClass(RecordClassServiceRequestItem).Save(ctx)
	require.NoError(t, err)

	numbers := &e2eNumbers{}
	registry := NewCreatorRegistry()
	require.NoError(t, registry.Register(NewIncidentCreator(numbers)))
	require.NoError(t, registry.Register(NewServiceRequestItemCreator()))
	resolver := NewResolver(itsmservice.NewProcessBindingService(client), PermissionCheckFunc(func(*ent.Client, Identity, string, string) bool { return true }))
	createService := NewService(client, resolver, registry, NewWorkItemCreator(numbers))
	createHandler := NewHandler(createService)
	const jwtSecret = "e2e-jwt-secret"
	const exchangeSecret = "e2e-exchange-secret"
	exchangeHandler := NewIdentityExchangeHandler(client, &memoryNonceStore{}, exchangeSecret, jwtSecret, time.Minute, 5*time.Minute)

	router := gin.New()
	router.POST("/api/v1/intake/identity-exchange", exchangeHandler.Exchange)
	protected := router.Group("/api/v1")
	protected.Use(middleware.IntakeAuthMiddleware(jwtSecret))
	createHandler.RegisterRoutes(protected)

	accessToken, err := middleware.GenerateAccessToken(userEntity.ID, userEntity.Username, userEntity.Role, tenant.ID, jwtSecret, time.Hour)
	require.NoError(t, err)
	serviceRequestBody := map[string]any{
		"idempotencyKey": "e2e-access-sr-" + suffix, "intakeKind": IntakeKindCatalogItem,
		"title": "E2E service request", "catalogItemId": catalog.ID,
	}
	srFirst := postE2E(t, router, "/api/v1/intake/work-items", accessToken, serviceRequestBody)
	require.Equal(t, http.StatusCreated, srFirst.Code, srFirst.Body.String())
	srCreated := decodeE2EResult(t, srFirst)
	require.Equal(t, RecordClassServiceRequestItem, srCreated.RecordClass)
	srReplayResponse := postE2E(t, router, "/api/v1/intake/work-items", accessToken, serviceRequestBody)
	require.Equal(t, http.StatusOK, srReplayResponse.Code, srReplayResponse.Body.String())
	srReplay := decodeE2EResult(t, srReplayResponse)
	require.True(t, srReplay.Replayed)
	require.Equal(t, srCreated.WorkItemID, srReplay.WorkItemID)

	incidentBody := map[string]any{
		"idempotencyKey": "e2e-access-incident-" + suffix, "intakeKind": IntakeKindIncident,
		"title": "E2E access-token incident", "incident": map[string]any{"impact": "high", "urgency": "high"},
	}
	incidentFirst := postE2E(t, router, "/api/v1/intake/work-items", accessToken, incidentBody)
	require.Equal(t, http.StatusCreated, incidentFirst.Code, incidentFirst.Body.String())
	incidentCreated := decodeE2EResult(t, incidentFirst)
	require.Equal(t, RecordClassIncident, incidentCreated.RecordClass)
	incidentReplay := decodeE2EResult(t, postE2E(t, router, "/api/v1/intake/work-items", accessToken, incidentBody))
	require.True(t, incidentReplay.Replayed)
	require.Equal(t, incidentCreated.WorkItemID, incidentReplay.WorkItemID)

	assertion := IdentityAssertion{
		Provider: "microsoft", Workspace: "it-support", Subject: "e2e-subject-" + suffix, Channel: "teams",
		EventID: "teams-event-" + suffix, IssuedAt: time.Now().UTC().Unix(), Nonce: "e2e-nonce-" + suffix,
	}
	assertion.Signature = strings.Repeat("0", 64)
	forged := postE2E(t, router, "/api/v1/intake/identity-exchange", "", assertion)
	require.Equal(t, http.StatusUnauthorized, forged.Code, forged.Body.String())
	assertion.Nonce = "e2e-valid-nonce-" + suffix
	assertion.Signature = signIdentityAssertion(assertion, exchangeSecret)
	exchange := postE2E(t, router, "/api/v1/intake/identity-exchange", "", assertion)
	require.Equal(t, http.StatusOK, exchange.Code, exchange.Body.String())
	var exchangeEnvelope struct {
		Data IdentityExchangeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(exchange.Body.Bytes(), &exchangeEnvelope))
	require.NotEmpty(t, exchangeEnvelope.Data.Token)
	kafIncidentBody := map[string]any{
		"idempotencyKey": "teams:it-support:" + assertion.EventID, "intakeKind": IntakeKindIncident,
		"title":           "E2E KAF channel incident",
		"sourceReference": map[string]any{"provider": "microsoft", "eventId": assertion.EventID},
	}
	kafCreate := postE2E(t, router, "/api/v1/intake/work-items", exchangeEnvelope.Data.Token, kafIncidentBody)
	require.Equal(t, http.StatusCreated, kafCreate.Code, kafCreate.Body.String())
	kafCreated := decodeE2EResult(t, kafCreate)
	require.Equal(t, RecordClassIncident, kafCreated.RecordClass)

	workItemIDs := []int{srCreated.WorkItemID, incidentCreated.WorkItemID, kafCreated.WorkItemID}
	for _, workItemID := range workItemIDs {
		require.Equal(t, 1, e2eCount(t, client.Ticket.Query().Where(ticket.IDEQ(workItemID))))
		require.Equal(t, 1, e2eCount(t, client.IntakeRequest.Query().Where(intakerequest.WorkItemIDEQ(workItemID))))
		require.Equal(t, 1, e2eCount(t, client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemIDEQ(workItemID))))
		require.Equal(t, 1, e2eCount(t, client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(fmt.Sprint(workItemID)), outboxevent.EventTypeEQ(workflowStartEventType))))
	}
	require.Equal(t, 1, e2eCount(t, client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(srCreated.WorkItemID))))
	require.Equal(t, 1, e2eCount(t, client.Incident.Query().Where(incident.WorkItemIDEQ(incidentCreated.WorkItemID))))
	require.Equal(t, 1, e2eCount(t, client.Incident.Query().Where(incident.WorkItemIDEQ(kafCreated.WorkItemID))))

	outboxRepository := itsmservice.NewOutboxEventRepository(client)
	outage := itsmservice.NewWorkflowStartOutboxDispatcher(outboxRepository, &e2eProcessEngine{client: client, err: errors.New("simulated BPMN outage")}, itsmservice.WorkflowStartOutboxConfig{BatchSize: 20, MaxAttempts: 1})
	require.NoError(t, outage.DispatchOnce(ctx))
	for _, workItemID := range workItemIDs {
		event, loadErr := client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(fmt.Sprint(workItemID)), outboxevent.EventTypeEQ(workflowStartEventType)).Only(ctx)
		require.NoError(t, loadErr)
		require.Equal(t, "dead", event.Status)
		retryCtx := context.WithValue(ctx, "user_id", userEntity.ID)
		retryCtx = context.WithValue(retryCtx, "request_id", "e2e-retry-"+fmt.Sprint(workItemID))
		require.NoError(t, outboxRepository.RetryDeadWorkflowStart(retryCtx, tenant.ID, workItemID))
	}
	recovered := itsmservice.NewWorkflowStartOutboxDispatcher(outboxRepository, &e2eProcessEngine{client: client}, itsmservice.WorkflowStartOutboxConfig{BatchSize: 20, MaxAttempts: 3})
	require.NoError(t, recovered.DispatchOnce(ctx))
	for _, workItemID := range workItemIDs {
		event, loadErr := client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(fmt.Sprint(workItemID)), outboxevent.EventTypeEQ(workflowStartEventType)).Only(ctx)
		require.NoError(t, loadErr)
		require.Equal(t, "published", event.Status)
		require.Equal(t, 1, e2eCount(t, client.ProcessInstance.Query().Where(processinstance.BusinessIDEQ(workItemID), processinstance.BusinessTypeEQ("work_item"))))
	}
}

func e2eCount(t *testing.T, query countQuery) int {
	t.Helper()
	count, err := query.Count(context.Background())
	require.NoError(t, err)
	return count
}
