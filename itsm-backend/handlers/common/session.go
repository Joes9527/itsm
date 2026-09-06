package common

import (
	"context"

	"itsm-backend/authorization"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// SessionUser is the authenticated /auth/me (and /users/me, /users/profile)
// projection. User CRUD retains native TenantID; here TenantID is selected,
// and ActorTenantID is the verified native identity, never a request input.
type SessionUser struct {
	*User
	ActorTenantID int `json:"actorTenantId"`
}

type SessionTenant struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Code   string `json:"code"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

func (s *Service) GetSession(ctx context.Context, identity creation.Identity) (*SessionUser, error) {
	var result *SessionUser
	err := s.sessions.Read(ctx, identity, func(session *authorization.SessionSnapshot) error {
		profile := toUserDomain(session.Actor)
		profile.Role = identity.Role
		profile.TenantID = identity.TenantID
		profile.Permissions = make([]string, 0, len(session.Permissions))
		for _, permission := range session.Permissions {
			key := permission.Resource + ":" + permission.Action
			if permission.Resource == "*" && permission.Action == "*" {
				key = "*"
			}
			profile.Permissions = append(profile.Permissions, key)
		}
		result = &SessionUser{User: profile, ActorTenantID: session.Actor.TenantID}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) GetUserTenants(ctx context.Context, identity creation.Identity) ([]SessionTenant, error) {
	result := make([]SessionTenant, 0)
	err := s.sessions.Read(ctx, identity, func(session *authorization.SessionSnapshot) error {
		tenants, err := session.SelectableTenants(ctx)
		if err != nil {
			return err
		}
		for _, tenant := range tenants {
			result = append(result, SessionTenant{ID: tenant.ID, Name: tenant.Name, Code: tenant.Code, Type: string(tenant.Type), Status: string(tenant.Status)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
