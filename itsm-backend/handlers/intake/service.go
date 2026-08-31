package intake

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	itsmservice "itsm-backend/service"
)

const (
	workflowStartEventType = "workflow.start.requested"
	intakeCreatePath       = "/api/v1/intake/work-items"
)

type requestIDContextKey struct{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, strings.TrimSpace(requestID))
}

type referenceResolver interface {
	Resolve(context.Context, *ent.Tx, Identity, CreateWorkItemCommand) (*ResolvedIntake, error)
}

type receiptRepository interface {
	Claim(context.Context, *ent.Tx, Identity, string, string, string) (*ent.IntakeRequest, ClaimOutcome, error)
	Complete(context.Context, *ent.Tx, int, int, int) error
}

type workItemWriter interface {
	CreateBase(context.Context, *ent.Tx, *CreationPlan) (*ent.Ticket, error)
}

type fieldValueWriter interface {
	CreateValuesTx(context.Context, *ent.Tx, int, string, int, string, int, map[string]any) error
}

type snapshotWriter interface {
	Create(context.Context, *ent.Tx, SnapshotInput) (*ent.IntakeResolutionSnapshot, error)
}

type auditWriter interface {
	RecordCreated(context.Context, *ent.Tx, CreatedAuditInput) error
}

type outboxWriter interface {
	Enqueue(context.Context, *ent.Tx, itsmservice.NewOutboxEvent) (*ent.OutboxEvent, error)
}

// Service owns the only transactional orchestration path for WorkItem intake.
// Its collaborators may read configuration or allocate numbers, but all
// authoritative writes use the transaction opened here.
type Service struct {
	client      *ent.Client
	resolver    referenceResolver
	receipts    receiptRepository
	registry    *CreatorRegistry
	workItems   workItemWriter
	fieldValues fieldValueWriter
	snapshots   snapshotWriter
	audits      auditWriter
	outbox      outboxWriter
}

func NewService(client *ent.Client, resolver referenceResolver, registry *CreatorRegistry, workItems workItemWriter) *Service {
	return &Service{
		client: client, resolver: resolver, receipts: NewIdempotencyRepository(), registry: registry, workItems: workItems,
		fieldValues: itsmservice.NewFieldValueService(client), snapshots: NewSnapshotRepository(),
		audits: NewAuditRepository(), outbox: itsmservice.NewOutboxEventRepository(client),
	}
}

func (s *Service) Create(ctx context.Context, identity Identity, command CreateWorkItemCommand) (*CreateWorkItemResult, error) {
	if s == nil || s.client == nil || s.resolver == nil || s.receipts == nil || s.registry == nil || s.workItems == nil || s.fieldValues == nil || s.snapshots == nil || s.audits == nil || s.outbox == nil {
		return nil, NewInternalFailure("intake service is not fully configured", nil)
	}
	normalized, digest, err := CanonicalizeCommand(command)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		result, retry, attemptErr := s.createAttempt(ctx, identity, normalized, digest)
		if !retry {
			return result, attemptErr
		}
	}
	return nil, NewInfrastructureUnavailable("idempotency owner rolled back before completing", errIdempotencyOwnerInProgress)
}

func (s *Service) createAttempt(ctx context.Context, identity Identity, command CreateWorkItemCommand, digest string) (*CreateWorkItemResult, bool, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, false, NewInfrastructureUnavailable("could not begin intake transaction", err)
	}
	defer func() { _ = tx.Rollback() }()

	receipt, outcome, err := s.receipts.Claim(ctx, tx, identity, command.IdempotencyKey, digest, CanonicalDigestVersion)
	if err != nil {
		return nil, errors.Is(err, errIdempotencyOwnerInProgress), err
	}
	if outcome == ClaimReplay {
		result, loadErr := s.loadResult(ctx, tx, identity.TenantID, *receipt.WorkItemID, true)
		return result, false, loadErr
	}

	resolved, err := s.resolver.Resolve(ctx, tx, identity, command)
	if err != nil {
		return nil, false, err
	}
	creator, err := s.registry.Get(resolved.RecordClass)
	if err != nil {
		return nil, false, err
	}
	plan, err := creator.Prepare(ctx, tx, *resolved)
	if err != nil {
		return nil, false, err
	}
	workItem, err := s.workItems.CreateBase(ctx, tx, plan)
	if err != nil {
		return nil, false, err
	}
	professional, err := creator.CreateExtension(ctx, tx, workItem, plan)
	if err != nil {
		return nil, false, err
	}
	if err := s.writeFieldValues(ctx, tx, resolved, workItem.ID, professional); err != nil {
		return nil, false, NewInfrastructureUnavailable("could not persist intake field values", err)
	}
	if _, err := s.snapshots.Create(ctx, tx, buildSnapshot(receipt.ID, workItem.ID, digest, resolved)); err != nil {
		return nil, false, err
	}
	if err := s.audits.RecordCreated(ctx, tx, CreatedAuditInput{
		TenantID: identity.TenantID, UserID: identity.ActorID, WorkItemID: workItem.ID,
		RequestID: auditRequestID(ctx, identity, digest), Path: intakeCreatePath, Method: "POST", StatusCode: 201,
	}); err != nil {
		return nil, false, err
	}
	workflowStatus := "not_required"
	if !resolved.Workflow.NoProcess {
		if err := s.enqueueWorkflowStart(ctx, tx, receipt.ID, workItem.ID, identity, resolved); err != nil {
			return nil, false, err
		}
		workflowStatus = "pending"
	}
	if err := s.receipts.Complete(ctx, tx, identity.TenantID, receipt.ID, workItem.ID); err != nil {
		return nil, false, err
	}
	result := &CreateWorkItemResult{
		WorkItemID: workItem.ID, Number: workItem.TicketNumber, RecordClass: resolved.RecordClass,
		ProfessionalReference: *professional, WorkflowStartStatus: workflowStatus,
	}
	if err := tx.Commit(); err != nil {
		return nil, false, NewInfrastructureUnavailable("could not commit intake transaction", err)
	}
	return result, false, nil
}

func (s *Service) writeFieldValues(ctx context.Context, tx *ent.Tx, resolved *ResolvedIntake, workItemID int, professional *ProfessionalReference) error {
	if resolved.Catalog == nil || len(resolved.Command.FormValues) == 0 {
		return nil
	}
	valueType, valueID := "ticket", workItemID
	if resolved.RecordClass == RecordClassServiceRequestItem {
		valueType, valueID = "service_request", professional.ID
	}
	return s.fieldValues.CreateValuesTx(
		ctx, tx, resolved.Identity.TenantID, "service_catalog", resolved.Catalog.ID,
		valueType, valueID, resolved.Command.FormValues,
	)
}

func buildSnapshot(receiptID, workItemID int, digest string, resolved *ResolvedIntake) SnapshotInput {
	input := SnapshotInput{
		TenantID: resolved.Identity.TenantID, IntakeRequestID: receiptID, WorkItemID: workItemID,
		Channel: resolved.Identity.Channel, SourceProvider: resolved.Identity.Provider,
		RecordClass: resolved.RecordClass, CIIDs: append([]int(nil), resolved.CIIDs...),
		WorkflowDefinitionID: copyInt(resolved.Workflow.DefinitionID), WorkflowDefinitionKey: resolved.Workflow.DefinitionKey,
		WorkflowDefinitionVersion: resolved.Workflow.DefinitionVersion, NoProcess: resolved.Workflow.NoProcess,
		SLADefinitionID: copyInt(resolved.SLADefinitionID), ResolverVersion: resolved.ResolverVersion, RequestDigest: digest,
	}
	if resolved.Command.SourceReference != nil {
		input.SourceProvider = resolved.Command.SourceReference.Provider
		input.SourceEventID = resolved.Command.SourceReference.EventID
		input.SourceConversationID = resolved.Command.SourceReference.ConversationID
	}
	if resolved.Catalog != nil {
		catalogID := resolved.Catalog.ID
		input.CatalogItemID = &catalogID
		input.CatalogVersion = resolved.Catalog.Version
		input.FormSchemaVersion = resolved.Catalog.FormSchemaVersion
	}
	if resolved.CTI.CategoryID != nil {
		input.CTISnapshot = map[string]any{"categoryId": *resolved.CTI.CategoryID, "typeId": *resolved.CTI.TypeID, "itemId": *resolved.CTI.ItemID}
	}
	return input
}

func auditRequestID(ctx context.Context, identity Identity, digest string) string {
	if requestID, _ := ctx.Value(requestIDContextKey{}).(string); requestID != "" {
		return requestID
	}
	if identity.TokenID != "" {
		return identity.TokenID
	}
	if len(digest) > 16 {
		digest = digest[:16]
	}
	return "intake-" + digest
}

func (s *Service) enqueueWorkflowStart(ctx context.Context, tx *ent.Tx, receiptID, workItemID int, identity Identity, resolved *ResolvedIntake) error {
	if resolved.Workflow.DefinitionID == nil {
		return NewWorkflowBindingRequired("workflow definition is required for process start", nil)
	}
	eventID := itsmservice.NewWorkflowStartEventID(workItemID, *resolved.Workflow.DefinitionID)
	payload, err := json.Marshal(map[string]any{
		"tenantId": identity.TenantID, "workItemId": workItemID, "recordClass": resolved.RecordClass,
		"workflowDefinitionId": *resolved.Workflow.DefinitionID, "workflowDefinitionKey": resolved.Workflow.DefinitionKey,
		"workflowDefinitionVersion": resolved.Workflow.DefinitionVersion, "actorId": identity.ActorID,
		"channel": identity.Channel, "intakeRequestId": receiptID, "dedupeKey": eventID,
	})
	if err != nil {
		return NewInternalFailure("could not encode workflow start event", err)
	}
	_, err = s.outbox.Enqueue(ctx, tx, itsmservice.NewOutboxEvent{
		EventID: eventID, EventType: workflowStartEventType, TenantID: identity.TenantID,
		AggregateType: "work_item", AggregateID: strconv.Itoa(workItemID), Payload: payload,
	})
	if err != nil {
		return NewInfrastructureUnavailable("could not enqueue workflow start", err)
	}
	return nil
}

func (s *Service) loadResult(ctx context.Context, tx *ent.Tx, tenantID, workItemID int, replayed bool) (*CreateWorkItemResult, error) {
	workItem, err := tx.Ticket.Query().Where(ticket.IDEQ(workItemID), ticket.TenantIDEQ(tenantID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, NewReferenceNotFound("completed intake work item was not found", err)
	}
	if err != nil {
		return nil, NewInfrastructureUnavailable("could not load completed intake work item", err)
	}
	result := &CreateWorkItemResult{WorkItemID: workItem.ID, Number: workItem.TicketNumber, RecordClass: workItem.RecordClass, Replayed: replayed}
	switch workItem.RecordClass {
	case RecordClassIncident:
		extension, queryErr := tx.Incident.Query().Where(incident.WorkItemIDEQ(workItem.ID), incident.OwnedByTenant(tenantID)).Only(ctx)
		if queryErr != nil {
			return nil, NewInternalFailure("completed work item is missing its incident extension", queryErr)
		}
		result.ProfessionalReference = ProfessionalReference{Type: "incident", ID: extension.ID}
	case RecordClassServiceRequestItem:
		extension, queryErr := tx.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(workItem.ID), servicerequest.HasWorkItemWith(ticket.TenantIDEQ(tenantID))).Only(ctx)
		if queryErr != nil {
			return nil, NewInternalFailure("completed work item is missing its service request extension", queryErr)
		}
		result.ProfessionalReference = ProfessionalReference{Type: "service_request", ID: extension.ID}
	default:
		return nil, NewUnsupportedRecordClass("completed work item has an unsupported record class", nil)
	}
	snapshot, err := tx.IntakeResolutionSnapshot.Query().Where(
		intakeresolutionsnapshot.WorkItemIDEQ(workItem.ID), intakeresolutionsnapshot.TenantIDEQ(tenantID),
	).Only(ctx)
	if err != nil {
		return nil, NewInternalFailure("completed work item is missing its intake snapshot", err)
	}
	if snapshot.NoProcess {
		result.WorkflowStartStatus = "not_required"
		return result, nil
	}
	if snapshot.WorkflowDefinitionID == nil {
		return nil, NewInternalFailure("intake snapshot is missing its workflow definition", nil)
	}
	eventID := itsmservice.NewWorkflowStartEventID(workItem.ID, *snapshot.WorkflowDefinitionID)
	event, err := tx.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID), outboxevent.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil {
		return nil, NewInternalFailure("completed work item is missing its workflow start event", err)
	}
	result.WorkflowStartStatus = projectWorkflowStartStatus(event.Status)
	return result, nil
}

func projectWorkflowStartStatus(outboxStatus string) string {
	switch outboxStatus {
	case "pending", "publishing":
		return "pending"
	case "published":
		return "active"
	default:
		return "manual_intervention_required"
	}
}
