package intake

import (
	"context"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/externalidentity"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"
)

type IdentityRepository interface {
	Resolve(context.Context, string, string, string) (*ent.ExternalIdentity, creation.Identity, error)
	Validate(context.Context, *authentication.IntakeClaims) (creation.Identity, error)
	Audit(context.Context, creation.Identity, string, int) error
}
type EntIdentityRepository struct {
	runtime, system *ent.Client
	sessions        *authorization.SessionReader
}

func NewIdentityRepository(runtime, system *ent.Client, sessions *authorization.SessionReader) *EntIdentityRepository {
	return &EntIdentityRepository{runtime: runtime, system: system, sessions: sessions}
}
func (r *EntIdentityRepository) Resolve(ctx context.Context, provider, workspace, subject string) (*ent.ExternalIdentity, creation.Identity, error) {
	var identity creation.Identity
	if r == nil || r.system == nil || r.runtime == nil || r.sessions == nil {
		return nil, identity, creation.NewInfrastructureUnavailable("identity repository unavailable", nil)
	}
	lookup := tenantctx.SystemContext(ctx, "intake:identity-exchange", "verified provider identity lookup")
	mapping, err := r.system.ExternalIdentity.Query().Where(externalidentity.ProviderEQ(provider), externalidentity.WorkspaceEQ(workspace), externalidentity.SubjectEQ(subject)).Only(lookup)
	if ent.IsNotFound(err) {
		return nil, identity, creation.NewAuthenticationRequired("identity mapping unavailable", nil)
	}
	if err != nil {
		return nil, identity, creation.NewInfrastructureUnavailable("identity mapping lookup failed", err)
	}
	actor, err := r.system.User.Query().Where(user.IDEQ(mapping.UserID), user.ActiveEQ(true)).Only(lookup)
	if ent.IsNotFound(err) {
		return nil, identity, creation.NewAuthenticationRequired("identity actor unavailable", nil)
	}
	if err != nil {
		return nil, identity, creation.NewInfrastructureUnavailable("identity actor lookup failed", err)
	}
	claims := &authentication.IntakeClaims{UserID: actor.ID, TenantID: mapping.TenantID, Role: authorization.EffectiveSessionRole(actor), Provider: provider, MappingID: mapping.ID, MappingVersion: mapping.Version}
	identity, err = r.Validate(tenantctx.WithTenantID(ctx, mapping.TenantID), claims)
	return mapping, identity, err
}
func (r *EntIdentityRepository) Validate(ctx context.Context, c *authentication.IntakeClaims) (creation.Identity, error) {
	identity := creation.Identity{TenantID: c.TenantID, ActorID: c.UserID, RequesterID: c.UserID, Role: c.Role, Provider: c.Provider, Channel: c.Channel, TokenID: c.ID}
	if r == nil || r.sessions == nil {
		return identity, creation.NewInfrastructureUnavailable("identity session unavailable", nil)
	}
	err := r.sessions.Read(ctx, identity, func(s *authorization.SessionSnapshot) error {
		m, err := s.Tx.ExternalIdentity.Query().Where(externalidentity.IDEQ(c.MappingID), externalidentity.TenantIDEQ(c.TenantID)).Only(ctx)
		if ent.IsNotFound(err) {
			return creation.NewAuthenticationRequired("identity mapping unavailable", nil)
		}
		if err != nil {
			return creation.NewInfrastructureUnavailable("identity mapping lookup failed", err)
		}
		if !m.Active || m.Version != c.MappingVersion || m.UserID != c.UserID || m.Provider != c.Provider {
			return creation.NewAuthenticationRequired("identity mapping revoked", nil)
		}
		identity.ActorTenantID = s.Actor.TenantID
		return nil
	})
	return identity, err
}
func (r *EntIdentityRepository) Audit(ctx context.Context, identity creation.Identity, action string, mappingID int) error {
	ctx = tenantctx.WithTenantID(ctx, identity.TenantID)
	_, err := r.runtime.AuditLog.Create().SetTenantID(identity.TenantID).SetUserID(identity.ActorID).SetResource("intake_identity").SetRequestBody(`{"mappingId":` + strconv.Itoa(mappingID) + `}`).SetAction(action).SetRequestID("intake-identity").SetIP("").SetPath("/api/v1/intake/identity-exchange").SetMethod("POST").SetStatusCode(200).Save(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("identity audit unavailable", err)
	}
	return nil
}

func createIdentityMappingTx(ctx context.Context, tx *ent.Tx, tenantID int, input CreateIdentityMapping) (*ent.ExternalIdentity, error) {
	row, err := tx.ExternalIdentity.Create().SetTenantID(tenantID).SetProvider(input.Provider).SetWorkspace(input.Workspace).SetSubject(input.Subject).SetUserID(input.UserID).Save(ctx)
	if ent.IsConstraintError(err) {
		return nil, creation.NewIdempotencyConflict("identity mapping already exists", nil)
	}
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("mapping creation unavailable", err)
	}
	return row, nil
}
func updateIdentityMappingTx(ctx context.Context, tx *ent.Tx, tenantID, id, version int, active bool) (*ent.ExternalIdentity, error) {
	count, err := tx.ExternalIdentity.Update().Where(externalidentity.IDEQ(id), externalidentity.TenantIDEQ(tenantID), externalidentity.VersionEQ(version)).SetActive(active).AddVersion(1).Save(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("mapping update unavailable", err)
	}
	if count != 1 {
		return nil, creation.NewIdempotencyConflict("mapping version changed or mapping unavailable", nil)
	}
	row, err := tx.ExternalIdentity.Query().Where(externalidentity.IDEQ(id), externalidentity.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("mapping projection unavailable", err)
	}
	return row, nil
}
func recordIdentityMappingAuditTx(ctx context.Context, tx *ent.Tx, i creation.Identity, action string, id, version int) error {
	_, err := tx.AuditLog.Create().SetTenantID(i.TenantID).SetUserID(i.ActorID).SetResource("intake_identity_mapping").SetAction(action).SetPath("/api/v1/intake/identity-mappings").SetMethod("POST").SetStatusCode(200).SetRequestBody(`{"mappingId":` + strconv.Itoa(id) + `,"version":` + strconv.Itoa(version) + `}`).Save(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("mapping audit unavailable", err)
	}
	return nil
}
