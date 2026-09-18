package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	relationmeta "itsm-backend/common/workitemrelation"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
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

// ListTx reads only live structured relations, traversing either endpoint. It
// authorizes every returned endpoint at the caller snapshot; unavailable or
// unreadable endpoints fail closed rather than leaking IDs or claiming no link.
// Callers supply an RR transaction, including B2 dependency verification owners.
func (s *WorkItemRelationService) ListTx(ctx context.Context, tx *ent.Tx, meta workitemmutation.Meta, workItemID int) ([]relationmeta.View, error) {
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
	result := make([]relationmeta.View, 0, len(rows))
	endpoint := func(item *ent.Ticket) relationmeta.Endpoint {
		return relationmeta.Endpoint{WorkItemID: item.ID, Number: item.TicketNumber, RecordClass: item.RecordClass, Title: item.Title, Status: item.Status, Version: item.Version}
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
		result = append(result, relationmeta.View{ID: row.ID, Type: row.RelationType, Required: row.Metadata.Required, Source: endpoint(source), Target: endpoint(target)})
	}
	return result, nil
}

func NewWorkItemRelationService(client *ent.Client, directory database.DirectorySnapshot) *WorkItemRelationService {
	return &WorkItemRelationService{client: client, directory: directory}
}

// List owns the application RR snapshot for shared HTTP reads.
func (s *WorkItemRelationService) List(ctx context.Context, meta workitemmutation.Meta, workItemID int) ([]relationmeta.View, error) {
	if s.client == nil {
		return nil, common.NewInternalError("relation application unavailable", nil)
	}
	if meta.ActorID <= 0 || meta.TenantID <= 0 {
		return nil, common.NewUnauthorizedError("authenticated actor and tenant required")
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != meta.TenantID {
		return nil, common.NewForbiddenError("tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, meta.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return s.ListTx(ctx, tx, meta, workItemID)
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
	source, actor, err := s.authorizeSource(ctx, tx, m, cmd.SourceID)
	if err != nil {
		return nil, nil, nil, err
	}
	target, policy, err := authorization.ResolveWorkItemIdentity(ctx, tx.Client(), cmd.TargetID, m.TenantID, authorization.WorkItemReadScope(actor.ID, authorization.EffectiveSessionRole(actor)))
	if err != nil {
		return nil, nil, nil, err
	}
	identity := creation.Identity{TenantID: m.TenantID, ActorID: actor.ID, ActorTenantID: actor.TenantID, Role: authorization.EffectiveSessionRole(actor)}
	if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, "read"); err != nil {
		return nil, nil, nil, err
	}
	items := []*ent.Ticket{source, target}
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
	if err = lockRelationEndpointsTx(ctx, tx, m.TenantID, cmd.SourceID, cmd.TargetID); err != nil {
		return empty, err
	}
	// A row lock alone cannot invalidate a deletion owner's older RR snapshot:
	// incoming references otherwise change only their source. This physical
	// target tuple fence preserves every public field and bypasses Ent defaults.
	fence, err := tx.ExecContext(ctx, "UPDATE tickets SET id=id WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL", cmd.TargetID, m.TenantID)
	if err != nil {
		return empty, err
	}
	count, err := fence.RowsAffected()
	if err != nil {
		return empty, err
	}
	if count != 1 {
		return empty, common.NewNotFoundError("work item")
	}
	if source.RecordClass == "change_request" {
		if err = workitemmutation.RequireSettledChangeCallbacks(ctx, tx, m.TenantID, source.ID); err != nil {
			return empty, err
		}
	}
	if !remove {
		scope := relationTuple(cmd)
		if cmd.Type == "investigated_by" {
			scope = []predicate.WorkItemRelation{workitemrelation.TenantID(m.TenantID), workitemrelation.SourceWorkItemID(cmd.SourceID), workitemrelation.RelationType(cmd.Type), workitemrelation.DeletedAtIsNil()}
		}
		exists, err := tx.WorkItemRelation.Query().Where(scope...).Exist(ctx)
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
	// The delivery event shares this transaction with the relation row, the source
	// version bump and the immutable receipt, so a delivery fault rolls back the
	// whole mutation instead of publishing an event with no committed effect.
	if err = emitRelationEventTx(ctx, tx, facts); err != nil {
		return empty, err
	}
	return result, nil
}

// PrepareSourceTx authorizes the existing source before target creation. The
// relation owner retains the same source/read policy as AddTx, with no writes.
func (s *WorkItemRelationService) PrepareSourceTx(ctx context.Context, tx *ent.Tx, cmd RelationCommand, targetClass string) error {
	source, _, err := s.authorizeSource(ctx, tx, cmd.Meta, cmd.SourceID)
	if err != nil {
		return err
	}
	if err = ValidateRelationClasses(cmd.Type, source.RecordClass, targetClass); err != nil {
		return common.NewValidationError(err.Error(), err)
	}
	if cmd.Required && cmd.Type != "resolved_by_change" {
		return common.NewValidationError("required is only supported for resolved_by_change", nil)
	}
	if source.Version != cmd.Meta.ExpectedVersion {
		return common.NewVersionConflictError("work item", source.ID, cmd.Meta.ExpectedVersion, source.Version)
	}
	return nil
}

func (s *WorkItemRelationService) authorizeSource(ctx context.Context, tx *ent.Tx, m workitemmutation.Meta, sourceID int) (*ent.Ticket, *ent.User, error) {
	if tx == nil || m.TenantID <= 0 || m.ActorID <= 0 || m.ExpectedVersion <= 0 || strings.TrimSpace(m.Source) == "" || strings.TrimSpace(m.OperationID) == "" || sourceID <= 0 {
		return nil, nil, common.NewValidationError("trusted relation identity, version and operationId required", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return nil, nil, common.NewForbiddenError("tenant context mismatch")
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, m.ActorID, m.TenantID)
	if err != nil {
		return nil, nil, err
	}
	identity := creation.Identity{TenantID: m.TenantID, ActorID: actor.ID, ActorTenantID: actor.TenantID, Role: authorization.EffectiveSessionRole(actor)}
	item, policy, err := authorization.ResolveWorkItemIdentity(ctx, tx.Client(), sourceID, m.TenantID, authorization.WorkItemReadScope(actor.ID, identity.Role))
	if err != nil {
		return nil, nil, err
	}
	for _, action := range []string{"read", policy.ResolveAction("update")} {
		if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, action); err != nil {
			return nil, nil, err
		}
	}
	return item, actor, nil
}

// ReplayTx validates current access to both endpoints and reads an existing
// immutable receipt only. Missing receipts never recreate a relation.
func (s *WorkItemRelationService) ReplayTx(ctx context.Context, tx *ent.Tx, cmd RelationCommand) (workitemmutation.Result, error) {
	_, _, actor, err := s.authorize(ctx, tx, cmd)
	if err != nil {
		return workitemmutation.Result{}, err
	}
	digest, err := relationDigest(cmd, false)
	if err != nil {
		return workitemmutation.Result{}, err
	}
	result, found, err := workitemmutation.Replay(ctx, tx.Client(), cmd.Meta, cmd.SourceID, digest)
	if err != nil {
		return result, err
	}
	if !found {
		return result, common.NewInternalError("completed intake relation receipt is missing", nil)
	}
	row, err := tx.AuditLog.Query().Where(auditlog.TenantID(cmd.Meta.TenantID), auditlog.UserID(cmd.Meta.ActorID), auditlog.OperationID(cmd.Meta.OperationID)).Only(ctx)
	if err != nil {
		return result, err
	}
	var facts RelationFacts
	if row.RequestBody == nil || json.Unmarshal([]byte(*row.RequestBody), &facts) != nil {
		return result, common.NewInternalError("completed intake relation facts are missing", nil)
	}
	source, target := cmd.SourceID, cmd.TargetID
	if cmd.Type == "related_to" && source > target {
		source, target = target, source
	}
	if row.Action != "work_item.relation_added" || facts.RelationID <= 0 || facts.MutationWorkItemID != cmd.SourceID || facts.SourceID != source || facts.TargetID != target || facts.Type != cmd.Type || facts.Required != cmd.Required || facts.Removed || facts.TenantID != cmd.Meta.TenantID || facts.ActorID != cmd.Meta.ActorID || facts.ActorTenantID != actor.TenantID || result.Version != cmd.Meta.ExpectedVersion+1 || facts.OperationID != cmd.Meta.OperationID || facts.Source != cmd.Meta.Source || facts.Version != result.Version {
		return result, common.NewInternalError("completed intake relation facts are inconsistent", nil)
	}
	return result, nil
}
