package authorization

import (
	"context"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strings"
)

// RequireCurrentPermission reads current RBAC inside the caller transaction.
// Durable idempotency replay must never rely on the process-wide permission cache.
func RequireCurrentPermission(ctx context.Context, tx *ent.Tx, identity creation.Identity, resource, action string) error {
	if identity.Role == "super_admin" {
		return nil
	}
	record, err := tx.Role.Query().Where(role.CodeEQ(identity.Role), role.TenantIDEQ(identity.TenantID), role.IsActiveEQ(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return creation.NewPermissionDenied("active role is required", nil)
	}
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not load current role", err)
	}
	links, err := tx.RolePermission.Query().Where(rolepermission.RoleIDEQ(record.ID), rolepermission.TenantIDEQ(identity.TenantID)).All(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not load current role permissions", err)
	}
	ids := make([]int, 0, len(links))
	for _, link := range links {
		ids = append(ids, link.PermissionID)
	}
	permissions, err := tx.Permission.Query().Where(permission.IDIn(ids...), permission.TenantIDEQ(identity.TenantID)).All(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not load current permissions", err)
	}
	rules := make([]Permission, 0, len(permissions))
	for _, p := range permissions {
		rules = append(rules, Permission{Resource: p.Resource, Action: p.Action})
	}
	if !CheckPermissionMatch(rules, resource, action) {
		return creation.NewPermissionDenied("permission denied for "+resource+":"+action, nil)
	}
	return nil
}

// AuthorizeWorkItemCreation runs before receipt claim, including matching replays.
// Command adapters cannot choose the role or bypass requester delegation policy.
func AuthorizeWorkItemCreation(ctx context.Context, tx *ent.Tx, identity creation.Identity, command creation.CreateWorkItemCommand) error {
	actor, err := tx.User.Query().Where(user.IDEQ(identity.ActorID), user.TenantIDEQ(identity.TenantID), user.ActiveEQ(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return creation.NewAuthenticationRequired("current actor is unavailable", nil)
	}
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not load current actor", err)
	}
	if command.FeishuTask != nil && (identity.ActorID != identity.RequesterID || actor.FeishuOpenID != command.FeishuTask.CreatorOpenID) {
		return creation.NewPermissionDenied("verified Feishu creator no longer matches the requester", nil)
	}
	if command.Email != nil && (identity.ActorID != identity.RequesterID || !strings.EqualFold(actor.Email, command.Email.SenderEmail)) {
		return creation.NewPermissionDenied("verified email sender no longer matches the requester", nil)
	}
	if actor.Role != identity.Role {
		return creation.NewAuthenticationRequired("current actor role changed", nil)
	}
	requester, err := tx.User.Query().Where(user.IDEQ(identity.RequesterID), user.TenantIDEQ(identity.TenantID), user.ActiveEQ(true)).Exist(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not load current requester", err)
	}
	if !requester {
		return creation.NewPermissionDenied("current requester is unavailable", nil)
	}
	resource := map[string]string{creation.RecordClassGeneric: "ticket", creation.RecordClassIncident: "incident", creation.RecordClassProblem: "problem", creation.RecordClassChangeRequest: "change", creation.RecordClassServiceRequestItem: "service_request"}[command.RecordClass]
	if resource == "" {
		return creation.NewUnsupportedRecordClass("unsupported creation class", nil)
	}
	for _, action := range []string{"write", "read"} {
		if err := RequireCurrentPermission(ctx, tx, identity, resource, action); err != nil {
			return err
		}
	}
	if identity.RequesterID != identity.ActorID {
		if err := RequireCurrentPermission(ctx, tx, identity, resource, "create_on_behalf"); err != nil {
			return err
		}
	}
	if command.Problem != nil && command.Problem.SourceIncidentID != nil {
		for _, action := range []string{"read", "write"} {
			if err := RequireCurrentPermission(ctx, tx, identity, "incident", action); err != nil {
				return err
			}
		}
		exists, err := tx.Incident.Query().Where(incident.IDEQ(*command.Problem.SourceIncidentID), incident.HasWorkItemWith(ticket.TenantIDEQ(identity.TenantID), ticket.RecordClassEQ("incident"), ticket.DeletedAtIsNil())).Exist(ctx)
		if err != nil {
			return creation.NewInfrastructureUnavailable("could not authorize conversion source", err)
		}
		if !exists {
			return creation.NewReferenceNotFound("source incident is unavailable", nil)
		}
	}
	return nil
}
