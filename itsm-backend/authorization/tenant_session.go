package authorization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/mspallocation"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/pkg/tenantmode"
)

var (
	ErrTenantAccessDenied             = errors.New("tenant access denied")
	ErrTenantInactive                 = errors.New("tenant is inactive")
	ErrTenantExpired                  = errors.New("tenant is expired")
	ErrTenantAuthorizationUnavailable = errors.New("tenant authorization unavailable")
)

// AuthorizeTenantSession is the shared policy for selecting the tenant bound
// into an authentication session. Only the user's native tenant, a target
// selected by a super administrator, or an actively allocated MSP customer is
// eligible. The target tenant must also be active and unexpired.
func AuthorizeTenantSession(ctx context.Context, client *ent.Client, actor *ent.User, targetTenantID int, now time.Time) (*ent.Tenant, error) {
	if client == nil || actor == nil || targetTenantID <= 0 {
		return nil, ErrTenantAccessDenied
	}
	target, err := client.Tenant.Get(ctx, targetTenantID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrTenantAccessDenied
		}
		return nil, fmt.Errorf("%w: load target tenant: %v", ErrTenantAuthorizationUnavailable, err)
	}

	allowed := actor.TenantID == targetTenantID || actor.Role == "super_admin"
	if !allowed && actor.MspRole != "" {
		origin, originErr := client.Tenant.Get(ctx, actor.TenantID)
		if originErr != nil {
			if !ent.IsNotFound(originErr) {
				return nil, fmt.Errorf("%w: load actor tenant: %v", ErrTenantAuthorizationUnavailable, originErr)
			}
		} else if tenantmode.IsMSPProviderTenantType(string(origin.Type)) && tenantmode.IsCustomerTenantType(string(target.Type)) {
			allocated, allocationErr := client.MSPAllocation.Query().
				Where(
					mspallocation.MspUserIDEQ(actor.ID),
					mspallocation.CustomerTenantIDEQ(targetTenantID),
					mspallocation.DeassignedAtIsNil(),
				).
				Exist(ctx)
			if allocationErr != nil {
				return nil, fmt.Errorf("%w: query MSP allocation: %v", ErrTenantAuthorizationUnavailable, allocationErr)
			}
			allowed = allocated
		}
	}
	if !allowed {
		return nil, ErrTenantAccessDenied
	}
	if target.Status != "active" {
		return nil, ErrTenantInactive
	}
	if !target.ExpiresAt.IsZero() && !now.Before(target.ExpiresAt) {
		return nil, ErrTenantExpired
	}
	return target, nil
}

// EffectiveSessionRole is the canonical role issued by login, refresh and switch.
func EffectiveSessionRole(actor *ent.User) string {
	if actor == nil {
		return ""
	}
	if actor.MspRole != "" {
		return GetMSPRBACRole(string(actor.MspRole))
	}
	return actor.Role
}

// ResolveCurrentSessionActor reads only the restricted directory. Callers that
// need transactional authorization must supply a directory at their snapshot.
func ResolveCurrentSessionActor(ctx context.Context, directory *ent.Client, actorID, targetTenantID int, authenticatedRole string, now time.Time) (*ent.User, error) {
	lookup := tenantctx.SystemContext(ctx, "session:current", "resolve current native actor and selected tenant")
	return resolveCurrentSessionActor(lookup, directory, actorID, targetTenantID, authenticatedRole, now)
}

// Native provider deliveries use this same policy in their existing target
// transaction context; SystemContext must never rebind a scoped transaction.
func resolveCurrentSessionActor(ctx context.Context, directory *ent.Client, actorID, targetTenantID int, authenticatedRole string, now time.Time) (*ent.User, error) {
	if directory == nil {
		return nil, creation.NewInfrastructureUnavailable("session directory is required", nil)
	}
	actor, err := directory.User.Get(ctx, actorID)
	if ent.IsNotFound(err) || (err == nil && !actor.Active) {
		return nil, creation.NewAuthenticationRequired("current actor is unavailable", nil)
	}
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not load current actor", err)
	}
	if EffectiveSessionRole(actor) != authenticatedRole {
		return nil, creation.NewAuthenticationRequired("current actor role changed", nil)
	}
	if _, err = AuthorizeTenantSession(ctx, directory, actor, targetTenantID, now); err != nil {
		if errors.Is(err, ErrTenantAuthorizationUnavailable) {
			return nil, creation.NewInfrastructureUnavailable("could not authorize current tenant", err)
		}
		return nil, creation.NewPermissionDenied("current tenant session is unavailable", err)
	}
	return actor, nil
}
