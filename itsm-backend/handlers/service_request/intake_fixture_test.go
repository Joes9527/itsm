package service_request_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/processbinding"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"itsm-backend/handlers/service_catalog"
	sr "itsm-backend/handlers/service_request"
	"itsm-backend/middleware"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ServiceRequest = sr.ServiceRequest
type ListFilters = sr.ListFilters
type EntRepository = sr.EntRepository
type Handler = sr.Handler

var NewEntRepository = sr.NewEntRepository

type Service struct {
	*sr.Service
	app    *intake.Service
	client *ent.Client
}

func NewService(repo sr.Repository, client *ent.Client, logger *zap.SugaredLogger, chain *service.ApprovalChainResolver) *Service {
	if chain == nil {
		chain = service.NewApprovalChainResolver(client, logger)
	}
	owner := sr.NewService(repo, client, logger, chain)
	registry := intake.NewCreatorRegistry()
	if err := registry.Register(owner); err != nil {
		panic(err)
	}
	incident := service.NewIncidentService(client, logger)
	incident.SetPriorityMatrixService(service.NewPriorityMatrixService(logger))
	if err := registry.Register(incident); err != nil {
		panic(err)
	}
	resolver := intake.NewResolver(service_catalog.NewService(nil, client, logger), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
	for _, tenant := range client.Tenant.Query().AllX(context.Background()) {
		configureSRIntakeFixture(context.Background(), client, tenant.ID)
	}
	return &Service{owner, intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), sameTransactionDirectory{}), client}
}
func NewHandler(owner *Service) *Handler {
	h := sr.NewHandler(owner.Service)
	h.SetCreationApplication(owner.app)
	return h
}

// This fixture submits the existing public Catalog form through its real HTTP
// mapper, with the revisions confirmed before submission, then reads the
// professional projection separately for lifecycle assertions.
func (s *Service) SubmitCreation(ctx context.Context, tenantID, actorID, catalogID int, input *ServiceRequest) (*ServiceRequest, error) {
	result, err := s.SubmitCatalog(ctx, tenantID, actorID, catalogID, input)
	if err != nil {
		return nil, err
	}
	if result.ProfessionalReference.Type != "service_request" {
		return nil, fmt.Errorf("expected service request, got %s", result.RecordClass)
	}
	return s.Get(ctx, result.ProfessionalReference.ID, tenantID)
}
func (s *Service) SubmitCatalog(ctx context.Context, tenantID, actorID, catalogID int, input *ServiceRequest) (*creation.CreateWorkItemResult, error) {
	actor, err := s.client.User.Get(ctx, actorID)
	if err != nil {
		return nil, err
	}
	catalog, err := service_catalog.NewService(service_catalog.NewEntRepository(s.client), s.client, zap.NewNop().Sugar()).Read(ctx, creation.Identity{TenantID: tenantID, ActorID: actorID, RequesterID: actorID, Role: actor.Role, Channel: "http"}, catalogID)
	if err != nil {
		return nil, err
	}
	request := dto.CreateServiceRequestRequest{CatalogID: catalogID, RecordClass: catalog.TargetClass, CatalogVersion: catalog.CatalogVersion, FormSchemaVersion: catalog.FormSchemaVersion, FormData: input.FormData, CostCenter: input.CostCenter, DataClassification: input.DataClassification, NeedsPublicIP: input.NeedsPublicIP, SourceIPWhitelist: input.SourceIPWhitelist, ExpireAt: input.ExpireAt, ComplianceAck: input.ComplianceAck, ContactName: input.ContactName, ContactEmail: input.ContactEmail, Quantity: input.Quantity, ExpectedAt: input.ExpectedAt}
	if catalog.TargetClass == "incident" {
		request.Incident = &creation.IncidentInput{Severity: "medium"}
		request.Priority = "medium"
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	// A zero-value fixture field means omitted; explicit zero is tested by HTTP cases.
	if input.Quantity == 0 {
		delete(fields, "quantity")
	}
	for _, key := range []string{"title", "reason", "costCenter", "dataClassification", "contactName", "contactEmail"} {
		if fields[key] == "" {
			delete(fields, key)
		}
	}
	data, err = json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/service-requests", bytes.NewReader(data)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Idempotency-Key", uuid.NewString())
	c.Set("tenant_id", tenantID)
	c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: tenantID})
	c.Set("user_id", actorID)
	c.Set("role", actor.Role)
	NewHandler(s).Create(c)
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
func configureSRIntakeFixture(ctx context.Context, client *ent.Client, tenantID int) {
	for _, business := range []string{"service_request", "incident"} {
		if !client.ProcessBinding.Query().Where(processbinding.TenantIDEQ(tenantID), processbinding.BusinessTypeEQ(business)).ExistX(ctx) {
			if business == "service_request" {
				deployment := client.ProcessDeployment.Create().SetTenantID(tenantID).SetDeploymentID(fmt.Sprintf("sr-fixture-%d", tenantID)).SetDeploymentName("Request approval fixture").SaveX(ctx)
				client.ProcessDefinition.Create().SetTenantID(tenantID).SetDeploymentID(deployment.ID).SetKey("sr_fixture_approval").SetName("Request approval").SetVersion("1").SetIsActive(true).SetIsLatest(true).SetBpmnXML([]byte(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn"><process id="sr_fixture_approval" isExecutable="true"><startEvent id="start"/><userTask id="approval" camunda:assignee="${requester_id}"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="approval"/><sequenceFlow id="b" sourceRef="approval" targetRef="end"/></process></definitions>`)).SaveX(ctx)
				client.ProcessBinding.Create().SetTenantID(tenantID).SetBusinessType(business).SetIsDefault(true).SetProcessDefinitionKey("sr_fixture_approval").SaveX(ctx)
			} else {
				client.ProcessBinding.Create().SetTenantID(tenantID).SetBusinessType(business).SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
			}
		}
	}
	for _, code := range []string{"end_user", "manager", "agent"} {
		r, err := client.Role.Query().Where(role.TenantIDEQ(tenantID), role.CodeEQ(code)).Only(ctx)
		if ent.IsNotFound(err) {
			r = client.Role.Create().SetTenantID(tenantID).SetCode(code).SetName(code).SaveX(ctx)
		} else if err != nil {
			panic(err)
		}
		for _, resource := range []string{"service_request", "service_catalog", "incident", "ticket", "cmdb"} {
			for _, action := range []string{"read", "write"} {
				p, err := client.Permission.Query().Where(permission.TenantIDEQ(tenantID), permission.CodeEQ(resource+":"+action)).Only(ctx)
				if ent.IsNotFound(err) {
					p = client.Permission.Create().SetTenantID(tenantID).SetCode(resource + ":" + action).SetName(resource + action).SetResource(resource).SetAction(action).SaveX(ctx)
				} else if err != nil {
					panic(err)
				}
				if !client.RolePermission.Query().Where(rolepermission.TenantIDEQ(tenantID), rolepermission.RoleIDEQ(r.ID), rolepermission.PermissionIDEQ(p.ID)).ExistX(ctx) {
					client.RolePermission.Create().SetTenantID(tenantID).SetRoleID(r.ID).SetPermissionID(p.ID).SaveX(ctx)
				}
			}
		}
	}
}

// Creation responses intentionally have no detail aliases. Tests that need
// details perform the same separate GET as application consumers.
func receiptProfessionalID(data map[string]interface{}) int {
	return int(data["professionalReference"].(map[string]interface{})["id"].(float64))
}
func catalogFixturePath(id int) string { return "/api/v1/service-catalogs/" + strconv.Itoa(id) }

// Explicit persistence fixture for repository read/isolation tests. Creation
// field/default assertions live in the real Intake service and HTTP tests.
func createSRRepositoryFixture(ctx context.Context, client *ent.Client, input *ServiceRequest) (*ServiceRequest, error) {
	create := client.ServiceRequest.Create().SetTenantID(input.TenantID).SetRequesterID(input.RequesterID).SetTicketID(input.TicketID).SetCatalogID(input.CatalogID).SetDataClassification(input.DataClassification).SetComplianceAck(input.ComplianceAck).SetContactName(input.ContactName).SetContactEmail(input.ContactEmail).SetNillableExpectedAt(input.ExpectedAt).SetNillableExpireAt(input.ExpireAt)
	if input.Quantity > 0 {
		create.SetQuantity(input.Quantity)
	}
	record, err := create.Save(ctx)
	if err != nil {
		return nil, err
	}
	return NewEntRepository(client).Get(ctx, record.ID, input.TenantID)
}
