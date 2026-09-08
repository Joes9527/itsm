package authorization

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/mspallocation"
	"itsm-backend/ent/tenant"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// SessionReader shares the current actor/tenant policy and a single read-only
// snapshot across browser identity and navigation projections.
type SessionReader struct {
	runtime   *ent.Client
	directory database.DirectorySnapshot
}

func NewSessionReader(runtime *ent.Client, directory database.DirectorySnapshot) *SessionReader {
	return &SessionReader{runtime: runtime, directory: directory}
}

type SessionSnapshot struct {
	Actor       *ent.User
	Identity    creation.Identity
	Permissions []Permission
	Tx          *ent.Tx
	directory   *ent.Client
	now         time.Time
}

// Read owns both transactions. A projection is usable only after Read succeeds,
// including directory cleanup and completion of the target transaction.
func (s *SessionReader) Read(ctx context.Context, identity creation.Identity, project func(*SessionSnapshot) error) error {
	return s.withSnapshot(ctx, identity, true, project)
}

// Write shares current session authorization with an atomic tenant mutation.
func (s *SessionReader) Write(ctx context.Context, identity creation.Identity, apply func(*SessionSnapshot) error) error {
	return s.withSnapshot(ctx, identity, false, apply)
}
func (s *SessionReader) withSnapshot(ctx context.Context, identity creation.Identity, readOnly bool, project func(*SessionSnapshot) error) error {
	scope, ok := tenantctx.TenantID(ctx)
	if !ok || scope != identity.TenantID || tenantctx.IsSystemBypass(ctx) || identity.ActorID <= 0 || strings.TrimSpace(identity.Role) == "" {
		return creation.NewAuthenticationRequired("authenticated session is required", nil)
	}
	if s == nil || s.runtime == nil || s.directory == nil {
		return creation.NewInfrastructureUnavailable("session directory snapshot is required", nil)
	}
	tx, err := s.runtime.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: readOnly})
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not begin session read", err)
	}
	defer tx.Rollback()
	directory, closeDirectory, err := s.directory.Open(ctx, tx, identity.TenantID)
	if err != nil || directory == nil || closeDirectory == nil {
		if closeDirectory != nil {
			err = errors.Join(err, closeDirectory())
		}
		return creation.NewInfrastructureUnavailable("could not open session directory snapshot", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = closeDirectory()
		}
	}()
	now := time.Now()
	actor, readErr := ResolveCurrentSessionActor(ctx, directory, identity.ActorID, identity.TenantID, identity.Role, now)
	var permissions []Permission
	if readErr == nil {
		permissions, readErr = CurrentSessionPermissions(ctx, tx, identity)
	}
	if readErr == nil {
		readErr = project(&SessionSnapshot{Actor: actor, Identity: identity, Permissions: permissions, Tx: tx, directory: directory, now: now})
	}
	closeErr := closeDirectory()
	closed = true
	if closeErr != nil {
		return creation.NewInfrastructureUnavailable("could not close session directory snapshot", errors.Join(readErr, closeErr))
	}
	if readErr != nil {
		return readErr
	}
	if err = tx.Commit(); err != nil {
		return creation.NewInfrastructureUnavailable("could not complete session read", err)
	}
	return nil
}

// SelectableTenants limits candidates to native/current identity and active
// allocations; only the existing tenant-session policy decides eligibility.
func (s *SessionSnapshot) SelectableTenants(ctx context.Context) ([]*ent.Tenant, error) {
	lookup := tenantctx.SystemContext(ctx, "session:tenants", "list current actor selectable tenants")
	ids := []int{s.Actor.TenantID, s.Identity.TenantID}
	if s.Actor.MspRole != "" {
		allocations, err := s.directory.MSPAllocation.Query().Where(mspallocation.MspUserIDEQ(s.Actor.ID), mspallocation.DeassignedAtIsNil()).All(lookup)
		if err != nil {
			return nil, creation.NewInfrastructureUnavailable("could not list current allocations", err)
		}
		for _, allocation := range allocations {
			ids = append(ids, allocation.CustomerTenantID)
		}
	}
	query := s.directory.Tenant.Query().Order(ent.Asc(tenant.FieldID))
	if s.Actor.Role != "super_admin" {
		query.Where(tenant.IDIn(ids...))
	}
	candidates, err := query.All(lookup)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not list session tenants", err)
	}
	result := make([]*ent.Tenant, 0, len(candidates))
	for _, candidate := range candidates {
		allowed, err := AuthorizeTenantSession(lookup, s.directory, s.Actor, candidate.ID, s.now)
		if errors.Is(err, ErrTenantAuthorizationUnavailable) {
			return nil, creation.NewInfrastructureUnavailable("could not authorize selectable tenant", err)
		}
		if err == nil {
			result = append(result, allowed)
		}
	}
	return result, nil
}

// AuthorizeMappedActor uses this same directory snapshot to validate a mapping
// target's native identity and eligibility for the selected tenant.
func (s *SessionSnapshot) AuthorizeMappedActor(ctx context.Context, id int) error {
	lookup := tenantctx.SystemContext(ctx, "intake:mapping-target", "validate mapped actor for target tenant")
	actor, err := s.directory.User.Query().Where(user.IDEQ(id), user.ActiveEQ(true)).Only(lookup)
	if ent.IsNotFound(err) {
		return creation.NewPermissionDenied("mapped user unavailable", nil)
	}
	if err != nil {
		return creation.NewInfrastructureUnavailable("mapped user lookup unavailable", err)
	}
	_, err = ResolveCurrentSessionActor(ctx, s.directory, id, s.Identity.TenantID, EffectiveSessionRole(actor), s.now)
	return err
}
