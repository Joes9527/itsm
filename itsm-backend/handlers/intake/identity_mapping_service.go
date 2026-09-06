package intake

import (
	"context"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/externalidentity"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strings"
)

type IdentityMappingService struct {
	sessions  *authorization.SessionReader
	providers map[string]IdentityProvider
}
type CreateIdentityMapping struct {
	Provider  string `json:"provider"`
	Workspace string `json:"workspace"`
	Subject   string `json:"subject"`
	UserID    int    `json:"userId"`
}
type IdentityMappingView struct {
	ID       int    `json:"id"`
	Provider string `json:"provider"`
	UserID   int    `json:"userId"`
	Active   bool   `json:"active"`
	Version  int    `json:"version"`
}

func NewIdentityMappingService(sessions *authorization.SessionReader, providers map[string]IdentityProvider) *IdentityMappingService {
	return &IdentityMappingService{sessions: sessions, providers: providers}
}
func (s *IdentityMappingService) Create(ctx context.Context, i creation.Identity, input CreateIdentityMapping) (*IdentityMappingView, error) {
	if s == nil || s.sessions == nil {
		return nil, creation.NewInfrastructureUnavailable("mapping management unavailable", nil)
	}
	if _, ok := s.providers[input.Provider]; !ok {
		return nil, creation.NewInvalidCommand("unregistered identity provider", creation.FieldError{}, nil)
	}
	for _, field := range []string{input.Provider, input.Workspace, input.Subject} {
		if field == "" || len(field) > 512 || strings.TrimSpace(field) != field || strings.ContainsAny(field, "\r\n") {
			return nil, creation.NewInvalidCommand("invalid identity mapping", creation.FieldError{}, nil)
		}
	}
	if input.UserID <= 0 {
		return nil, creation.NewInvalidCommand("invalid mapping user", creation.FieldError{}, nil)
	}
	var result *IdentityMappingView
	err := s.sessions.Write(ctx, i, func(snapshot *authorization.SessionSnapshot) error {
		if err := authorization.RequireCurrentPermission(ctx, snapshot.Tx, i, "intake_identity_mapping", "write"); err != nil {
			return err
		}
		if err := snapshot.AuthorizeMappedActor(ctx, input.UserID); err != nil {
			return err
		}
		row, err := createIdentityMappingTx(ctx, snapshot.Tx, i.TenantID, input)
		if err != nil {
			return err
		}
		if err = recordIdentityMappingAuditTx(ctx, snapshot.Tx, i, "mapping.create", row.ID, row.Version); err != nil {
			return err
		}
		result = mappingView(row)
		return nil
	})
	return result, err
}
func (s *IdentityMappingService) Update(ctx context.Context, i creation.Identity, id, version int, active bool) (*IdentityMappingView, error) {
	if s == nil || s.sessions == nil {
		return nil, creation.NewInfrastructureUnavailable("mapping management unavailable", nil)
	}
	if id < 1 || version < 1 {
		return nil, creation.NewInvalidCommand("mapping id and version required", creation.FieldError{}, nil)
	}
	var result *IdentityMappingView
	err := s.sessions.Write(ctx, i, func(snapshot *authorization.SessionSnapshot) error {
		if err := authorization.RequireCurrentPermission(ctx, snapshot.Tx, i, "intake_identity_mapping", "write"); err != nil {
			return err
		}
		row, err := updateIdentityMappingTx(ctx, snapshot.Tx, i.TenantID, id, version, active)
		if err != nil {
			return err
		}
		if active {
			if _, ok := s.providers[row.Provider]; !ok {
				return creation.NewInvalidCommand("unregistered identity provider", creation.FieldError{}, nil)
			}
			if err = snapshot.AuthorizeMappedActor(ctx, row.UserID); err != nil {
				return err
			}
		}
		if err = recordIdentityMappingAuditTx(ctx, snapshot.Tx, i, "mapping.update", row.ID, row.Version); err != nil {
			return err
		}
		result = mappingView(row)
		return nil
	})
	return result, err
}
func (s *IdentityMappingService) List(ctx context.Context, i creation.Identity) ([]IdentityMappingView, error) {
	if s == nil || s.sessions == nil {
		return nil, creation.NewInfrastructureUnavailable("mapping management unavailable", nil)
	}
	result := []IdentityMappingView{}
	err := s.sessions.Read(ctx, i, func(snapshot *authorization.SessionSnapshot) error {
		if err := authorization.RequireCurrentPermission(ctx, snapshot.Tx, i, "intake_identity_mapping", "read"); err != nil {
			return err
		}
		rows, err := snapshot.Tx.ExternalIdentity.Query().Where(externalidentity.TenantIDEQ(i.TenantID)).Order(ent.Asc(externalidentity.FieldID)).Limit(100).All(ctx)
		if err != nil {
			return creation.NewInfrastructureUnavailable("mapping list unavailable", err)
		}
		for _, row := range rows {
			result = append(result, *mappingView(row))
		}
		return nil
	})
	return result, err
}
func mappingView(row *ent.ExternalIdentity) *IdentityMappingView {
	return &IdentityMappingView{ID: row.ID, Provider: row.Provider, UserID: row.UserID, Active: row.Active, Version: row.Version}
}
