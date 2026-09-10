package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	relationmeta "itsm-backend/common/workitemrelation"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/workitemrelation"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
)

// RelationCommand identifies actual WorkItems, never professional extension IDs.
type RelationCommand struct {
	Meta     workitemmutation.Meta
	SourceID int
	TargetID int
	Type     string
	Required bool
}

// RelationFacts is the immutable audit source for reliable delivery and outcome
// dependency processing. Required is a relation property, not a second ID store.
type RelationFacts struct {
	RelationID int `json:"relationId"`
	// MutationWorkItemID owns Version and the immutable receipt path. For an
	// undirected relation it can be either canonical stored endpoint.
	MutationWorkItemID int    `json:"mutationWorkItemId"`
	SourceID           int    `json:"sourceWorkItemId"`
	TargetID           int    `json:"targetWorkItemId"`
	Type               string `json:"relationType"`
	Required           bool   `json:"required"`
	TenantID           int    `json:"tenantId"`
	ActorID            int    `json:"actorId"`
	ActorTenantID      int    `json:"actorTenantId"`
	Source             string `json:"source"`
	OperationID        string `json:"operationId"`
	CorrelationID      string `json:"correlationId"`
	Version            int    `json:"version"`
	Removed            bool   `json:"removed"`
}

type WorkItemRelationService struct {
	client    *ent.Client
	directory database.DirectorySnapshot
}

type RelationEndpoint struct {
	WorkItemID  int    `json:"workItemId"`
	Number      string `json:"number"`
	RecordClass string `json:"recordClass"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Version     int    `json:"version"`
}

type RelationView struct {
	ID       int              `json:"id"`
	Type     string           `json:"relationType"`
	Required bool             `json:"required"`
	Source   RelationEndpoint `json:"source"`
	Target   RelationEndpoint `json:"target"`
}

// ListTx reads only live structured relations, traversing either endpoint. It
// authorizes every returned endpoint at the caller snapshot; unavailable or
// unreadable endpoints fail closed rather than leaking IDs or claiming no link.
// Callers supply an RR transaction, including B2 dependency verification owners.
func (s *WorkItemRelationService) ListTx(ctx context.Context, tx *ent.Tx, meta workitemmutation.Meta, workItemID int) ([]RelationView, error) {
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, meta.ActorID, meta.TenantID)
	if err != nil {
		return nil, err
	}
	identity := creation.Identity{TenantID: meta.TenantID, ActorID: actor.ID, ActorTenantID: actor.TenantID, Role: authorization.EffectiveSessionRole(actor)}
	load := func(id int) (*ent.Ticket, error) {
		item, policy, err := authorization.ResolveWorkItemIdentity(ctx, tx.Client(), id, meta.TenantID, authorization.WorkItemReadScope(actor.ID, identity.Role))
		if err != nil {
			return nil, err
		}
		if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, "read"); err != nil {
			return nil, err
		}
		return item, nil
	}
	if _, err = load(workItemID); err != nil {
		return nil, err
	}
	rows, err := tx.WorkItemRelation.Query().Where(workitemrelation.TenantID(meta.TenantID), workitemrelation.DeletedAtIsNil(), workitemrelation.Or(workitemrelation.SourceWorkItemID(workItemID), workitemrelation.TargetWorkItemID(workItemID))).Order(ent.Asc(workitemrelation.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]RelationView, 0, len(rows))
	endpoint := func(item *ent.Ticket) RelationEndpoint {
		return RelationEndpoint{WorkItemID: item.ID, Number: item.TicketNumber, RecordClass: item.RecordClass, Title: item.Title, Status: item.Status, Version: item.Version}
	}
	for _, row := range rows {
		source, err := load(row.SourceWorkItemID)
		if err != nil {
			return nil, err
		}
		target, err := load(row.TargetWorkItemID)
		if err != nil {
			return nil, err
		}
		if err = ValidateRelationClasses(row.RelationType, source.RecordClass, target.RecordClass); err != nil {
			return nil, err
		}
		if row.Metadata.Required && row.RelationType != "resolved_by_change" {
			return nil, common.NewValidationError("invalid required relation metadata", nil)
		}
		result = append(result, RelationView{ID: row.ID, Type: row.RelationType, Required: row.Metadata.Required, Source: endpoint(source), Target: endpoint(target)})
	}
	return result, nil
}

func NewWorkItemRelationService(client *ent.Client, directory database.DirectorySnapshot) *WorkItemRelationService {
	return &WorkItemRelationService{client: client, directory: directory}
}

// ValidateRelationClasses implements the accepted domain contract's explicit
// eight-type matrix over the authoritative registered WorkItem classes.
func ValidateRelationClasses(kind, source, target string) error {
	if _, err := authorization.ResolveWorkItemPolicy(source); err != nil {
		return err
	}
	if _, err := authorization.ResolveWorkItemPolicy(target); err != nil {
		return err
	}
	allowed := false
	switch kind {
	case "investigated_by", "caused_by":
		allowed = source == "incident" && target == "problem"
	case "resolved_by_change":
		allowed = (source == "incident" || source == "problem") && target == "change_request"
	case "requested_change":
		allowed = source == "service_request_item" && target == "change_request"
	case "fulfilled_by":
		allowed = source == "service_request_item" && target == "catalog_task"
	case "duplicate_of":
		allowed = source == target
	case "parent_child", "related_to":
		allowed = true
	}
	if !allowed {
		return fmt.Errorf("unsupported relation classes")
	}
	return nil
}

// AddTx requires the owner's RR transaction. The owner must roll back its entire
// transaction on error; this method never commits, rolls back or starts a Tx.
func (s *WorkItemRelationService) AddTx(ctx context.Context, tx *ent.Tx, cmd RelationCommand) error {
	_, err := s.mutateTx(ctx, tx, cmd, false)
	return err
}

// RemoveTx has the same caller-owned RR/rollback contract as AddTx.
func (s *WorkItemRelationService) RemoveTx(ctx context.Context, tx *ent.Tx, cmd RelationCommand) error {
	_, err := s.mutateTx(ctx, tx, cmd, true)
	return err
}

// Apply owns the RR transaction for public relation mutations. Creation owners
// call AddTx inside their existing RR transaction so failures roll back creation.
func (s *WorkItemRelationService) Apply(ctx context.Context, cmd RelationCommand, remove bool) (workitemmutation.Result, error) {
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != cmd.Meta.TenantID {
		return workitemmutation.Result{}, common.NewForbiddenError("tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, cmd.Meta.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return workitemmutation.Result{}, err
	}
	defer tx.Rollback()
	result, err := s.mutateTx(ctx, tx, cmd, remove)
	if err != nil {
		// Read a competing same-operation receipt only after confirmed rollback
		// and current authorization in a fresh snapshot. Never repeat writes.
		if rollbackErr := tx.Rollback(); rollbackErr == nil {
			fresh, openErr := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if openErr == nil {
				defer fresh.Rollback()
				if _, _, _, authErr := s.authorize(ctx, fresh, cmd); authErr != nil {
					return workitemmutation.Result{}, authErr
				}
				digest, digestErr := relationDigest(cmd, remove)
				if digestErr != nil {
					return workitemmutation.Result{}, digestErr
				}
				if replay, ok, replayErr := workitemmutation.Replay(ctx, fresh.Client(), cmd.Meta, cmd.SourceID, digest); ok || replayErr != nil {
					return replay, replayErr
				}
			}
		}
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return workitemmutation.Result{}, err
	}
	return result, nil
}

func (s *WorkItemRelationService) authorize(ctx context.Context, tx *ent.Tx, cmd RelationCommand) (*ent.Ticket, *ent.Ticket, *ent.User, error) {
	m := cmd.Meta
	if tx == nil || m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.Source) == "" || strings.TrimSpace(m.OperationID) == "" || cmd.SourceID <= 0 || cmd.TargetID <= 0 {
		return nil, nil, nil, common.NewValidationError("trusted relation identity, version and operationId required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return nil, nil, nil, common.NewForbiddenError("tenant context mismatch")
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, m.ActorID, m.TenantID)
	if err != nil {
		return nil, nil, nil, err
	}
	identity := creation.Identity{TenantID: m.TenantID, ActorID: actor.ID, ActorTenantID: actor.TenantID, Role: authorization.EffectiveSessionRole(actor)}
	items := make([]*ent.Ticket, 0, 2)
	for index, id := range []int{cmd.SourceID, cmd.TargetID} {
		item, policy, err := authorization.ResolveWorkItemIdentity(ctx, tx.Client(), id, m.TenantID, authorization.WorkItemReadScope(actor.ID, identity.Role))
		if err != nil {
			return nil, nil, nil, err
		}
		if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, "read"); err != nil {
			return nil, nil, nil, err
		}
		if index == 0 {
			if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, policy.ResolveAction("update")); err != nil {
				return nil, nil, nil, err
			}
		}
		items = append(items, item)
	}
	if err = ValidateRelationClasses(cmd.Type, items[0].RecordClass, items[1].RecordClass); err != nil {
		return nil, nil, nil, common.NewValidationError(err.Error(), err)
	}
	if cmd.SourceID == cmd.TargetID {
		return nil, nil, nil, common.NewValidationError("self relation is forbidden", nil)
	}
	if cmd.Required && cmd.Type != "resolved_by_change" {
		return nil, nil, nil, common.NewValidationError("required is only supported for resolved_by_change", nil)
	}
	return items[0], items[1], actor, nil
}

func relationTuple(cmd RelationCommand) []predicate.WorkItemRelation {
	source, target := cmd.SourceID, cmd.TargetID
	ends := workitemrelation.And(workitemrelation.SourceWorkItemID(source), workitemrelation.TargetWorkItemID(target))
	if cmd.Type == "related_to" {
		ends = workitemrelation.Or(ends, workitemrelation.And(workitemrelation.SourceWorkItemID(target), workitemrelation.TargetWorkItemID(source)))
	}
	return []predicate.WorkItemRelation{workitemrelation.TenantID(cmd.Meta.TenantID), ends, workitemrelation.RelationType(cmd.Type), workitemrelation.DeletedAtIsNil()}
}

func relationDigest(cmd RelationCommand, remove bool) (string, error) {
	return workitemmutation.Digest(struct {
		SourceID, TargetID, Version int
		Type, Source                string
		Required, Remove            bool
	}{cmd.SourceID, cmd.TargetID, cmd.Meta.ExpectedVersion, cmd.Type, cmd.Meta.Source, cmd.Required, remove})
}

func (s *WorkItemRelationService) mutateTx(ctx context.Context, tx *ent.Tx, cmd RelationCommand, remove bool) (workitemmutation.Result, error) {
	var empty workitemmutation.Result
	source, _, actor, err := s.authorize(ctx, tx, cmd)
	if err != nil {
		return empty, err
	}
	m := cmd.Meta
	digest, err := relationDigest(cmd, remove)
	if err != nil {
		return empty, err
	}
	if replay, ok, err := workitemmutation.Replay(ctx, tx.Client(), m, source.ID, digest); ok || err != nil {
		return replay, err
	}
	if source.Version != m.ExpectedVersion {
		return empty, common.NewVersionConflictError("work item", source.ID, m.ExpectedVersion, source.Version)
	}
	if source.RecordClass == "change_request" {
		if err = workitemmutation.RequireSettledChangeCallbacks(ctx, tx, m.TenantID, source.ID); err != nil {
			return empty, err
		}
	}
	if !remove {
		exists, err := tx.WorkItemRelation.Query().Where(relationTuple(cmd)...).Exist(ctx)
		if err != nil {
			return empty, err
		}
		if exists {
			return empty, common.NewValidationError("active relation already exists", nil)
		}
	}
	now := time.Now().UTC()
	saved, err := tx.Ticket.UpdateOneID(source.ID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil(), ticket.Version(m.ExpectedVersion)).AddVersion(1).SetUpdatedAt(now).Save(ctx)
	if err != nil {
		return empty, err
	}
	var relation *ent.WorkItemRelation
	if remove {
		relation, err = tx.WorkItemRelation.Query().Where(relationTuple(cmd)...).Only(ctx)
		if err == nil {
			relation, err = tx.WorkItemRelation.UpdateOneID(relation.ID).SetDeletedAt(now).Save(ctx)
		}
	} else {
		sourceID, targetID := cmd.SourceID, cmd.TargetID
		if cmd.Type == "related_to" && sourceID > targetID {
			sourceID, targetID = targetID, sourceID
		}
		relation, err = tx.WorkItemRelation.Create().SetTenantID(m.TenantID).SetSourceWorkItemID(sourceID).SetTargetWorkItemID(targetID).SetRelationType(cmd.Type).SetCreatedByID(actor.ID).SetMetadata(relationmeta.Metadata{Required: cmd.Required}).Save(ctx)
	}
	if err != nil {
		return empty, err
	}
	result := workitemmutation.Result{WorkItemID: source.ID, Version: saved.Version, Status: saved.Status}
	required := relation.Metadata.Required
	facts := RelationFacts{RelationID: relation.ID, MutationWorkItemID: source.ID, SourceID: relation.SourceWorkItemID, TargetID: relation.TargetWorkItemID, Type: cmd.Type, Required: required, TenantID: m.TenantID, ActorID: actor.ID, ActorTenantID: actor.TenantID, Source: m.Source, OperationID: m.OperationID, CorrelationID: m.CorrelationID, Version: result.Version, Removed: remove}
	action := "work_item.relation_added"
	if remove {
		action = "work_item.relation_removed"
	}
	if err = workitemmutation.RecordTx(ctx, tx, m, result, action, digest, facts); err != nil {
		return empty, err
	}
	return result, nil
}
