package authorization

import (
	"context"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	"itsm-backend/ent/standardchange"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strings"
	"sync/atomic"
	"time"
)

// RequireCurrentPermission reads current RBAC inside the caller transaction.
// Durable idempotency replay must never rely on the process-wide permission cache.
func RequireCurrentPermission(ctx context.Context, tx *ent.Tx, identity creation.Identity, resource, action string) error {
	rules, err := CurrentSessionPermissions(ctx, tx, identity)
	if err != nil {
		return err
	}
	if !CheckPermissionMatch(rules, resource, action) {
		return creation.NewPermissionDenied("permission denied for "+resource+":"+action, nil)
	}
	return nil
}

// CurrentSessionPermissions projects live target-tenant RBAC without consulting
// the process cache or native actor role assignments.
func CurrentSessionPermissions(ctx context.Context, tx *ent.Tx, identity creation.Identity) ([]Permission, error) {
	if identity.Role == "super_admin" {
		return []Permission{{Resource: "*", Action: "*"}}, nil
	}
	record, err := tx.Role.Query().Where(role.CodeEQ(identity.Role), role.TenantIDEQ(identity.TenantID), role.IsActiveEQ(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, creation.NewPermissionDenied("active role is required", nil)
	}
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not load current role", err)
	}
	links, err := tx.RolePermission.Query().Where(rolepermission.RoleIDEQ(record.ID), rolepermission.TenantIDEQ(identity.TenantID)).All(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not load current role permissions", err)
	}
	ids := make([]int, 0, len(links))
	for _, link := range links {
		ids = append(ids, link.PermissionID)
	}
	permissions, err := tx.Permission.Query().Where(permission.IDIn(ids...), permission.TenantIDEQ(identity.TenantID)).All(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not load current permissions", err)
	}
	rules := make([]Permission, 0, len(permissions))
	for _, p := range permissions {
		rules = append(rules, Permission{Resource: p.Resource, Action: p.Action})
	}
	return rules, nil
}

// AuthorizeWorkItemCreation runs before receipt claim, including matching replays.
// Command adapters cannot choose the role or bypass requester delegation policy.
func AuthorizeWorkItemCreation(ctx context.Context, tx *ent.Tx, directory *ent.Client, identity creation.Identity, command creation.CreateWorkItemCommand) (*CreationAuthorization, error) {
	if tx == nil {
		return nil, creation.NewInternalFailure("creation transaction is required", nil)
	}
	actor, err := ResolveCurrentSessionActor(ctx, directory, identity.ActorID, identity.TenantID, identity.Role, time.Now())
	if err != nil {
		return nil, err
	}
	return authorizeWorkItemCreationForActor(ctx, tx, actor, identity, command)
}

func authorizeWorkItemCreationForActor(ctx context.Context, tx *ent.Tx, actor *ent.User, identity creation.Identity, command creation.CreateWorkItemCommand) (*CreationAuthorization, error) {
	identity.ActorTenantID = actor.TenantID
	if identity.ActorTenantID != identity.TenantID && identity.RequesterID == identity.ActorID {
		return nil, creation.NewInvalidCommand("an active target-tenant requester must be explicitly selected", creation.FieldError{Field: "requesterId", Message: "select an active target-tenant requester"}, nil)
	}

	if command.FeishuTask != nil && (identity.ActorID != identity.RequesterID || actor.FeishuOpenID != command.FeishuTask.CreatorOpenID) {
		return nil, creation.NewPermissionDenied("verified Feishu creator no longer matches the requester", nil)
	}
	if command.Email != nil && (identity.ActorID != identity.RequesterID || !strings.EqualFold(actor.Email, command.Email.SenderEmail)) {
		return nil, creation.NewPermissionDenied("verified email sender no longer matches the requester", nil)
	}
	requester, err := tx.User.Query().Where(user.IDEQ(identity.RequesterID), user.TenantIDEQ(identity.TenantID), user.ActiveEQ(true)).Exist(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not load current requester", err)
	}
	if !requester {
		return nil, creation.NewPermissionDenied("current requester is unavailable", nil)
	}
	resource := map[string]string{creation.RecordClassGeneric: "ticket", creation.RecordClassIncident: "incident", creation.RecordClassProblem: "problem", creation.RecordClassChangeRequest: "change", creation.RecordClassServiceRequestItem: "service_request"}[command.RecordClass]
	if resource == "" {
		return nil, creation.NewUnsupportedRecordClass("unsupported creation class", nil)
	}
	for _, action := range []string{"write", "read"} {
		if err := RequireCurrentPermission(ctx, tx, identity, resource, action); err != nil {
			return nil, err
		}
	}
	if identity.RequesterID != identity.ActorID {
		if err := RequireCurrentPermission(ctx, tx, identity, resource, "create_on_behalf"); err != nil {
			return nil, err
		}
	}
	// Runtime input, including BPMN callback values, cannot delegate workflow
	// management. Server-owned catalog and resolver bindings are not overrides.
	if command.WorkflowDefinitionKey != "" {
		if err := RequireCurrentPermission(ctx, tx, identity, "workflow", "write"); err != nil {
			return nil, err
		}
	}
	if command.TemplateID != nil || command.ParentTicketID != nil || len(command.TagIDs) > 0 {
		if err := RequireCurrentPermission(ctx, tx, identity, "ticket", "read"); err != nil {
			return nil, err
		}
	}
	if command.Change != nil && command.Change.StandardTemplateID != nil {
		if err := RequireCurrentPermission(ctx, tx, identity, "standard_change", "read"); err != nil {
			return nil, err
		}
		exists, err := tx.StandardChange.Query().Where(standardchange.IDEQ(*command.Change.StandardTemplateID), standardchange.TenantIDEQ(identity.TenantID), standardchange.IsActiveEQ(true)).Exist(ctx)
		if err != nil {
			return nil, creation.NewInfrastructureUnavailable("could not authorize standard change template", err)
		}
		if !exists {
			return nil, creation.NewReferenceNotFound("standard change template is unavailable", nil)
		}
	}
	if command.Problem != nil && command.Problem.SourceIncidentID != nil {
		for _, action := range []string{"read", "write"} {
			if err := RequireCurrentPermission(ctx, tx, identity, "incident", action); err != nil {
				return nil, err
			}
		}
		exists, err := tx.Incident.Query().Where(incident.IDEQ(*command.Problem.SourceIncidentID), incident.HasWorkItemWith(ticket.TenantIDEQ(identity.TenantID), ticket.RecordClassEQ("incident"), ticket.DeletedAtIsNil())).Exist(ctx)
		if err != nil {
			return nil, creation.NewInfrastructureUnavailable("could not authorize conversion source", err)
		}
		if !exists {
			return nil, creation.NewReferenceNotFound("source incident is unavailable", nil)
		}
	}
	authorized := &CreationAuthorization{tx: tx, identity: identity}
	authorized.active.Store(true)
	tx.OnCommit(func(next ent.Committer) ent.Committer {
		return ent.CommitFunc(func(ctx context.Context, tx *ent.Tx) error {
			authorized.active.Store(false)
			return next.Commit(ctx, tx)
		})
	})
	tx.OnRollback(func(next ent.Rollbacker) ent.Rollbacker {
		return ent.RollbackFunc(func(ctx context.Context, tx *ent.Tx) error {
			authorized.active.Store(false)
			return next.Rollback(ctx, tx)
		})
	})
	return authorized, nil
}

// CreationAuthorization is materialized after all current checks and contains
// no directory connection. Only the authorizer can construct a valid value.
type CreationAuthorization struct {
	active   atomic.Bool
	tx       *ent.Tx
	identity creation.Identity
}

func (a *CreationAuthorization) Identity() creation.Identity {
	if a == nil {
		return creation.Identity{}
	}
	return a.identity
}
func (a *CreationAuthorization) Validate(tx *ent.Tx, identity creation.Identity) error {
	if a == nil || !a.active.Load() || tx == nil || a.tx != tx || a.identity != identity || a.identity.ActorTenantID <= 0 {
		return creation.NewPermissionDenied("creation authorization does not match transaction identity", nil)
	}
	return nil
}

// AuthorizeNativeWorkItemCreation is reserved for verified provider deliveries
// whose actor/requester are explicitly native to the target tenant.
func AuthorizeNativeWorkItemCreation(ctx context.Context, tx *ent.Tx, identity creation.Identity, command creation.CreateWorkItemCommand) error {
	if tx == nil {
		return creation.NewInternalFailure("creation transaction is required", nil)
	}
	actor, err := resolveCurrentSessionActor(ctx, tx.Client(), identity.ActorID, identity.TenantID, identity.Role, time.Now())
	if err != nil {
		return err
	}
	if actor.TenantID != identity.TenantID {
		return creation.NewPermissionDenied("provider actor must be native to target", nil)
	}
	_, err = authorizeWorkItemCreationForActor(ctx, tx, actor, identity, command)
	return err
}
