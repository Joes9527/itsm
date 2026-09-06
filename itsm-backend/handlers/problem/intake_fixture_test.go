package problem_test

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
)

type Problem = problemDomain.Problem
type Handler = problemDomain.Handler

// The external test harness keeps lifecycle assertions in their domain package
// while every creation assertion now executes the real shared application.
type EntRepository struct {
	*problemDomain.EntRepository
	client *ent.Client
}

func NewEntRepository(client *ent.Client) *EntRepository {
	return &EntRepository{problemDomain.NewEntRepository(client), client}
}

type Service struct {
	*problemDomain.Service
	app    *intake.Service
	client *ent.Client
}

func NewService(repo *EntRepository, logger *zap.SugaredLogger) *Service {
	owner := problemDomain.NewService(repo.EntRepository, logger)
	registry := intake.NewCreatorRegistry()
	if err := registry.Register(owner); err != nil {
		panic(err)
	}
	resolver := intake.NewResolver(service_catalog.NewService(nil, repo.client, logger, nil), service.NewProcessBindingService(repo.client), service.NewConfigurationItemService(repo.client, logger, nil, nil), service.NewTicketCategoryService(repo.client))
	app := intake.NewService(repo.client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), sameTransactionDirectory{})
	return &Service{owner, app, repo.client}
}
func NewHandler(owner *Service, client *ent.Client) *Handler {
	h := problemDomain.NewHandler(owner.Service, client)
	h.SetCreationApplication(owner.app)
	return h
}
func (s *Service) SubmitCreation(ctx context.Context, tenantID int, p *Problem) (*Problem, error) {
	return s.submit(ctx, tenantID, p.CreatedBy, creation.CreateWorkItemCommand{RecordClass: "problem", IntakeKind: "problem", Confirmation: "confirmed", IdempotencyKey: uuid.NewString(), Title: p.Title, Description: p.Description, Priority: p.Priority, AssigneeID: p.AssigneeID, Problem: &creation.ProblemInput{Category: p.Category, RootCause: p.RootCause, Impact: p.Impact}})
}
func (s *Service) SubmitIncidentConversion(ctx context.Context, tenantID, incidentID, actorID int, req dto.ConvertIncidentToProblemRequest) (*Problem, error) {
	return s.submit(ctx, tenantID, actorID, creation.CreateWorkItemCommand{RecordClass: "problem", IntakeKind: "problem", Confirmation: "confirmed", IdempotencyKey: uuid.NewString(), Title: req.Title, Description: req.Description, Problem: &creation.ProblemInput{SourceIncidentID: &incidentID, RootCause: req.RootCause}})
}
func (s *Service) submit(ctx context.Context, tenantID, actorID int, command creation.CreateWorkItemCommand) (*Problem, error) {
	actor, err := s.client.User.Query().Where(user.IDEQ(actorID), user.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("creator is unavailable: %w", err)
	}
	result, err := s.app.Create(ctx, creation.Identity{TenantID: tenantID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "http"}, command)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, result.ProfessionalReference.ID, tenantID)
}

func configureProblemIntakeFixture(ctx context.Context, client *ent.Client, tenantID int) {
	// Explicit fixture routing and current permissions, consumed by real Intake.
	client.ProcessBinding.Create().SetTenantID(tenantID).SetBusinessType("problem").SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	role := client.Role.Create().SetTenantID(tenantID).SetCode("agent").SetName("Agent").SaveX(ctx)
	for _, resource := range []string{"problem", "incident", "ticket"} {
		for _, action := range []string{"read", "write"} {
			permission := client.Permission.Create().SetTenantID(tenantID).SetCode(resource + ":" + action).SetName(resource + action).SetResource(resource).SetAction(action).SaveX(ctx)
			client.RolePermission.Create().SetTenantID(tenantID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
		}
	}

}
