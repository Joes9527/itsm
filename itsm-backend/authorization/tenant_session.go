package authorization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/mspallocation"
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
