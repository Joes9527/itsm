package change

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"itsm-backend/ent"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/processbinding"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
)

// NewChangeIntakeApp wires the real shared Intake application with the given
// Change service registered as its only professional creator. This mirrors
// the pattern already used by handlers/problem and handlers/service_request:
// creation assertions exercise the real HTTP->Intake boundary, direct ent
// stays for read/update fixture setup only.
func NewChangeIntakeApp(client *ent.Client, svc *Service, logger *zap.SugaredLogger) *intake.Service {
	registry := intake.NewCreatorRegistry()
	if err := registry.Register(svc); err != nil {
		panic(err)
	}
	resolver := intake.NewResolver(
		service_catalog.NewService(nil, client, logger),
		service.NewProcessBindingService(client),
		service.NewConfigurationItemService(client, logger, nil, nil),
		service.NewTicketCategoryService(client),
	)
	return intake.NewService(client, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()))
}

// ConfigureChangeIntakeFixture grants the given actor role current change/ticket
// read+write permission and provisions an unconditional no-process binding for
// the "change" business type, idempotently so it can be called once per tenant.
func ConfigureChangeIntakeFixture(ctx context.Context, client *ent.Client, tenantID int, actorRole string) {
	if !client.ProcessBinding.Query().Where(processbinding.TenantIDEQ(tenantID), processbinding.BusinessTypeEQ("change")).ExistX(ctx) {
		client.ProcessBinding.Create().SetTenantID(tenantID).SetBusinessType("change").SetIsDefault(true).
			SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(ctx)
	}
	r, err := client.Role.Query().Where(role.TenantIDEQ(tenantID), role.CodeEQ(actorRole)).Only(ctx)
	if ent.IsNotFound(err) {
		r = client.Role.Create().SetTenantID(tenantID).SetCode(actorRole).SetName(actorRole).SetIsActive(true).SaveX(ctx)
	}
	for _, resource := range []string{"change", "ticket"} {
		for _, action := range []string{"read", "write"} {
			p, err := client.Permission.Query().Where(permission.TenantIDEQ(tenantID), permission.CodeEQ(resource+":"+action)).Only(ctx)
			if ent.IsNotFound(err) {
				p = client.Permission.Create().SetTenantID(tenantID).SetCode(resource+":"+action).SetName(resource+action).SetResource(resource).SetAction(action).SaveX(ctx)
			}
			if !client.RolePermission.Query().Where(rolepermission.TenantIDEQ(tenantID), rolepermission.RoleIDEQ(r.ID), rolepermission.PermissionIDEQ(p.ID)).ExistX(ctx) {
				client.RolePermission.Create().SetTenantID(tenantID).SetRoleID(r.ID).SetPermissionID(p.ID).SaveX(ctx)
			}
		}
	}
}

// CreateChangeViaIntake submits a Change through the real shared Intake
// application (Resolve -> Prepare -> CreateExtension), the same path the
// production HTTP handler uses, then reads back the persisted professional
// record through the existing authoritative GetChange for assertions.
func CreateChangeViaIntake(ctx context.Context, client *ent.Client, svc *Service, app *intake.Service, tenantID, actorID int, in *Change) (*Change, error) {
	actor, err := client.User.Get(ctx, actorID)
	if err != nil {
		return nil, err
	}
	command := creation.CreateWorkItemCommand{
		RecordClass:    creation.RecordClassChangeRequest,
		IntakeKind:     creation.IntakeKindChangeRequest,
		Confirmation:   "confirmed",
		IdempotencyKey: uuid.NewString(),
		Title:          in.Title,
		Description:    in.Description,
		Priority:       in.Priority,
		Change: &creation.ChangeInput{
			Type:                 in.Type,
			ImpactScope:          in.ImpactScope,
			RiskLevel:            in.RiskLevel,
			Justification:        in.Justification,
			ImplementationPlan:   in.ImplementationPlan,
			RollbackPlan:         in.RollbackPlan,
			AffectedCIs:          in.AffectedCIs,
			RelatedTicketNumbers: in.RelatedTickets,
		},
	}
	result, err := app.Create(ctx, creation.Identity{TenantID: tenantID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "http"}, command)
	if err != nil {
		return nil, err
	}
	return svc.GetChange(ctx, result.ProfessionalReference.ID, tenantID)
}
