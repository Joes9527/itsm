package authorization

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"itsm-backend/ent/ticket"

	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/mspallocation"
	"itsm-backend/ent/tenant"
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
	mutation    *sessionMutationIdentity
}

// Captured only by SessionReader.Write. Public snapshot projections are not an
// authorization capability and cannot extend its lifetime or change its scope.
type sessionMutationIdentity struct {
	active                           atomic.Bool
	client                           *ent.Client
	actorID, actorTenantID, tenantID int
	role                             string
}

func (s *SessionSnapshot) ValidateMutationActor(client *ent.Client, actorID, actorTenantID, tenantID int) error {
	if s == nil || s.mutation == nil || !s.mutation.active.Load() ||
		client == nil || client != s.mutation.client || actorID != s.mutation.actorID ||
		actorTenantID != s.mutation.actorTenantID || tenantID != s.mutation.tenantID {
		return creation.NewPermissionDenied("active verified session mutation identity is required", nil)
	}
	return nil
}

// ValidateAssignmentIdentities is valid only within the verified write callback.
// The selected target is captured privately; public projection fields cannot
// redirect assignee authorization to another tenant.
func (s *SessionSnapshot) ValidateAssignmentIdentities(ctx context.Context, client *ent.Client, actorID, actorTenantID, targetTenantID, assigneeID int) error {
	if err := s.ValidateMutationActor(client, actorID, actorTenantID, targetTenantID); err != nil {
		return err
	}
	if assigneeID < 0 {
		return creation.NewPermissionDenied("invalid assignment identity", nil)
	}
	if assigneeID == 0 {
		return nil
	}
	return s.authorizeMappedActorInTenant(ctx, assigneeID, s.mutation.tenantID)
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
		snapshot := &SessionSnapshot{Actor: actor, Identity: identity, Permissions: permissions, Tx: tx, directory: directory, now: now}
		if !readOnly {
			snapshot.mutation = &sessionMutationIdentity{client: tx.Client(), actorID: actor.ID, actorTenantID: actor.TenantID, tenantID: identity.TenantID, role: identity.Role}
			snapshot.mutation.active.Store(true)
		}
		readErr = func() error {
			if snapshot.mutation != nil {
				defer snapshot.mutation.active.Store(false)
			}
			return project(snapshot)
		}()
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
	return s.authorizeMappedActorInTenant(ctx, id, s.Identity.TenantID)
}

func (s *SessionSnapshot) authorizeMappedActorInTenant(ctx context.Context, id, targetTenantID int) error {
	_, err := ResolveCurrentTenantUser(ctx, s.directory, id, targetTenantID, s.now)
	return err
}

// AuthorizeWorkItemAssignment intersects the verified actor with existing row and professional ACLs.
func (session *SessionSnapshot) AuthorizeWorkItemAssignment(ctx context.Context, workItemID int) (*ent.Ticket, error) {
	if session == nil || session.Tx == nil || session.mutation == nil {
		return nil, creation.NewPermissionDenied("active verified assignment session required", nil)
	}
	if err := session.ValidateMutationActor(session.Tx.Client(), session.mutation.actorID, session.mutation.actorTenantID, session.mutation.tenantID); err != nil {
		return nil, err
	}
	identity := creation.Identity{ActorID: session.mutation.actorID, TenantID: session.mutation.tenantID, Role: session.mutation.role}
	return AuthorizeWorkItemAssignment(ctx, session.Tx, identity, workItemID)
}

// AuthorizeWorkItemAssignment shares the current transaction's active-role grants
// and WorkItem row scope. The caller supplies an already authenticated identity;
// this operation does not validate a proposed assignee or perform side effects.
func AuthorizeWorkItemAssignment(ctx context.Context, tx *ent.Tx, identity creation.Identity, workItemID int) (*ent.Ticket, error) {
	if tx == nil || identity.ActorID <= 0 || identity.TenantID <= 0 {
		return nil, creation.NewPermissionDenied("authenticated assignment transaction required", nil)
	}
	item, policy, err := ResolveWorkItemIdentity(ctx, tx.Client(), workItemID, identity.TenantID)
	if err != nil {
		return nil, err
	}
	action := policy.FulfillmentAction()
	if item.RecordClass == "generic" {
		action = "assign"
	}
	if err := RequireCurrentPermission(ctx, tx, identity, policy.Resource, action); err != nil {
		return nil, err
	}
	visible, err := tx.Ticket.Query().Where(ticket.ID(item.ID), ticket.TenantID(identity.TenantID), WorkItemRowScope(identity.ActorID, identity.Role)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, creation.NewPermissionDenied("insufficient WorkItem row visibility", nil)
	}
	return item, nil
}

// AuthorizeWorkItemForward preserves the workflow route permission and row
// visibility. Transferring ownership additionally requires assignment ACLs.
func (session *SessionSnapshot) AuthorizeWorkItemForward(ctx context.Context, id int) (*ent.Ticket, error) {
	client := session.Tx.Client()
	item, _, err := ResolveWorkItemIdentity(ctx, client, id, session.Identity.TenantID)
	if err != nil {
		return nil, err
	}
	if !HasResourcePermission(client, session.Identity.Role, "workflow", "update", session.Identity.TenantID) {
		return nil, fmt.Errorf("insufficient workflow permission")
	}
	visible, err := client.Ticket.Query().Where(ticket.ID(id), ticket.TenantID(session.Identity.TenantID), WorkItemRowScope(session.Actor.ID, session.Identity.Role)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, fmt.Errorf("insufficient WorkItem row visibility")
	}
	return item, nil
}
