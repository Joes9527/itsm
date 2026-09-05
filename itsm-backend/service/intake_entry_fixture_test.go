package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"itsm-backend/controller"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/processbinding"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	"itsm-backend/ent/ticketcategory"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/middleware"
	"itsm-backend/repository/ticket"
	"itsm-backend/repository/workitemnumber"
	domain "itsm-backend/service"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type FieldDefinitionInput = domain.FieldDefinitionInput

var NewFieldDefinitionService = domain.NewFieldDefinitionService
var NewFieldValueService = domain.NewFieldValueService
var ToTicketResponse = domain.ToTicketResponse
var ToTicketResponseWithCustomFields = domain.ToTicketResponseWithCustomFields

// External fixtures exercise real HTTP adapters and the shared application;
// returned detail projections are separate reads solely for lifecycle assertions.
type TicketService struct {
	*domain.TicketService
	client *ent.Client
	app    *intake.Service
}

func NewTicketServiceForTest(client *ent.Client, logger *zap.SugaredLogger) *TicketService {
	owner := domain.NewTicketServiceForTest(client, logger)
	return &TicketService{owner, client, newEntryApplication(client, owner, domain.NewIncidentService(client, logger))}
}
func (s *TicketService) SubmitCreation(ctx context.Context, req *dto.CreateTicketRequest, tenantID int) (*ticket.Ticket, error) {
	return s.SubmitCreationAsActor(ctx, req, tenantID, req.RequesterID)
}

// SubmitCreationAsActor exercises the real HTTP->Intake path as a distinct
// currently-authenticated actor, independent of req.RequesterID. This lets
// negative fixtures submit a legitimate actor's session while req.RequesterID
// references a foreign tenant, so the actual application (not fixture setup)
// rejects the cross-tenant reference.
func (s *TicketService) SubmitCreationAsActor(ctx context.Context, req *dto.CreateTicketRequest, tenantID, actorID int) (*ticket.Ticket, error) {
	if err := configureEntryFixture(ctx, s.client, tenantID, actorID); err != nil {
		return nil, err
	}
	actor, err := s.client.User.Get(ctx, actorID)
	if err != nil {
		return nil, err
	}
	h := controller.NewTicketController(s.TicketService, nil, nil, s.client, zap.NewNop().Sugar())
	h.SetCreationApplication(s.app)
	result, err := submitEntryFixture(ctx, h.CreateTicket, tenantID, actor, req)
	if err != nil {
		return nil, err
	}
	return s.GetTicket(ctx, result.WorkItemID, tenantID)
}

type IncidentService struct {
	*domain.IncidentService
	client *ent.Client
	app    *intake.Service
}

func NewIncidentService(client *ent.Client, logger *zap.SugaredLogger) *IncidentService {
	owner := domain.NewIncidentService(client, logger)
	return &IncidentService{owner, client, newEntryApplication(client, domain.NewTicketServiceForTest(client, logger), owner)}
}
func (s *IncidentService) SubmitCreation(ctx context.Context, req *dto.CreateIncidentRequest, tenantID, actorID int) (*dto.IncidentResponse, error) {
	result, err := s.SubmitReceipt(ctx, req, tenantID, actorID)
	if err != nil {
		return nil, err
	}
	return s.GetIncident(ctx, result.ProfessionalReference.ID, tenantID)
}
func (s *IncidentService) SubmitReceipt(ctx context.Context, req *dto.CreateIncidentRequest, tenantID, actorID int) (*creation.CreateWorkItemResult, error) {
	if err := configureEntryFixture(ctx, s.client, tenantID, actorID); err != nil {
		return nil, err
	}
	actor, err := s.client.User.Get(ctx, actorID)
	if err != nil {
		return nil, err
	}
	h := controller.NewIncidentController(s.IncidentService, s.RuleEngine(), nil, nil, nil, zap.NewNop().Sugar())
	h.SetCreationApplication(s.app)
	return submitEntryFixture(ctx, h.CreateIncident, tenantID, actor, req)
}
func newEntryApplication(client *ent.Client, tickets *domain.TicketService, incidents *domain.IncidentService) *intake.Service {
	registry := intake.NewCreatorRegistry()
	for _, owner := range []creation.ProfessionalCreator{tickets, incidents} {
		if err := registry.Register(owner); err != nil {
			panic(err)
		}
	}
	logger := zap.NewNop().Sugar()
	resolver := intake.NewResolver(service_catalog.NewService(nil, client, logger), domain.NewProcessBindingService(client), domain.NewConfigurationItemService(client, logger, nil, nil), domain.NewTicketCategoryService(client))
	return intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), sameTransactionDirectory{})
}
func submitEntryFixture(ctx context.Context, h gin.HandlerFunc, tenantID int, actor *ent.User, body any) (*creation.CreateWorkItemResult, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/creation", bytes.NewReader(raw)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Idempotency-Key", uuid.NewString())
	c.Set("tenant_id", tenantID)
	c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tenantID})
	c.Set("user_id", actor.ID)
	c.Set("role", actor.Role)
	h(c)
	var response struct {
		Code    int                           `json:"code"`
		Message string                        `json:"message"`
		Data    creation.CreateWorkItemResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		return nil, err
	}
	if w.Code != 201 {
		return nil, fmt.Errorf("create HTTP %d code %d: %s", w.Code, response.Code, response.Message)
	}
	return &response.Data, nil
}
func configureEntryFixture(ctx context.Context, client *ent.Client, tenantID, actorID int) error {
	actor, err := client.User.Get(ctx, actorID)
	if err != nil {
		return err
	}
	if tenantID <= 0 || actor.TenantID != tenantID {
		return fmt.Errorf("invalid fixture tenant")
	}
	if !client.TicketCategory.Query().Where(ticketcategory.TenantIDEQ(tenantID), ticketcategory.CodeEQ(fmt.Sprintf("fixture-incident-%d", tenantID))).ExistX(ctx) {
		client.TicketCategory.Create().SetTenantID(tenantID).SetCode(fmt.Sprintf("fixture-incident-%d", tenantID)).SetName("incident").SaveX(ctx)
	}
	r, err := client.Role.Query().Where(role.TenantIDEQ(tenantID), role.CodeEQ(actor.Role)).Only(ctx)
	if ent.IsNotFound(err) {
		r = client.Role.Create().SetTenantID(tenantID).SetCode(actor.Role).SetName(actor.Role).SaveX(ctx)
	} else if err != nil {
		return err
	}
	for _, resource := range []string{"ticket", "incident", "service_catalog", "workflow"} {
		for _, action := range []string{"read", "write"} {
			p, err := client.Permission.Query().Where(permission.TenantIDEQ(tenantID), permission.CodeEQ(resource+":"+action)).Only(ctx)
			if ent.IsNotFound(err) {
				p = client.Permission.Create().SetTenantID(tenantID).SetCode(resource + ":" + action).SetName(resource + action).SetResource(resource).SetAction(action).SaveX(ctx)
			} else if err != nil {
				return err
			}
			if !client.RolePermission.Query().Where(rolepermission.TenantIDEQ(tenantID), rolepermission.RoleIDEQ(r.ID), rolepermission.PermissionIDEQ(p.ID)).ExistX(ctx) {
				client.RolePermission.Create().SetTenantID(tenantID).SetRoleID(r.ID).SetPermissionID(p.ID).SaveX(ctx)
			}
		}
	}
	for _, business := range []string{"ticket", "incident"} {
		if !client.ProcessBinding.Query().Where(processbinding.TenantIDEQ(tenantID), processbinding.BusinessTypeEQ(business)).ExistX(ctx) {
			client.ProcessBinding.Create().SetTenantID(tenantID).SetBusinessType(business).SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
		}
	}
	return nil
}
func testDSN() string { return "file:entry_" + uuid.NewString() + "?mode=memory&cache=shared&_fk=1" }
func setupIncidentTest(t *testing.T) (*ent.Client, *IncidentService, context.Context) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", testDSN())
	svc := NewIncidentService(client, zap.NewNop().Sugar())
	svc.RuleEngine().SetActorDirectory(client)
	return client, svc, context.Background()
}
func createIncidentTestTenant(ctx context.Context, client *ent.Client, suffix string) (*ent.Tenant, error) {
	return client.Tenant.Create().SetName("Tenant " + suffix).SetCode(suffix).SetStatus("active").Save(ctx)
}
func createIncidentTestUser(ctx context.Context, client *ent.Client, tenantID int, suffix string) (*ent.User, error) {
	return client.User.Create().SetTenantID(tenantID).SetUsername(suffix).SetEmail(suffix + "@example.test").SetName(suffix).SetPasswordHash("unused").SetRole("agent").SetActive(true).Save(ctx)
}
func deployEntryApproval(t *testing.T, client *ent.Client, tenantID int, key, business string) {
	t.Helper()
	ctx := context.Background()
	deployment := client.ProcessDeployment.Create().SetTenantID(tenantID).SetDeploymentID(key).SetDeploymentName(key).SaveX(ctx)
	client.ProcessDefinition.Create().SetTenantID(tenantID).SetDeploymentID(deployment.ID).SetKey(key).SetName(key).SetVersion("1").SetIsActive(true).SetIsLatest(true).SetBpmnXML([]byte(fmt.Sprintf(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn"><process id="%s" isExecutable="true"><startEvent id="start"/><userTask id="approval" camunda:assignee="${requester_id}"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="approval"/><sequenceFlow id="b" sourceRef="approval" targetRef="end"/></process></definitions>`, key))).SaveX(ctx)
	client.ProcessBinding.Delete().Where(processbinding.TenantIDEQ(tenantID), processbinding.BusinessTypeEQ(business)).ExecX(ctx)
	client.ProcessBinding.Create().SetTenantID(tenantID).SetBusinessType(business).SetIsDefault(true).SetProcessDefinitionKey(key).SaveX(ctx)
	require.Positive(t, deployment.ID)
}
