package intake

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"itsm-backend/handlers/common/workitemcreation"
	"reflect"
	"strconv"
	"strings"
	"time"

	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/problem"
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
	ResolveWorkflow(context.Context, *ent.Tx, *workitemcreation.CreationPlan) error
	Resolve(context.Context, *ent.Tx, workitemcreation.Identity, workitemcreation.CreateWorkItemCommand) (*workitemcreation.ResolvedIntake, error)
}

type receiptRepository interface {
	Claim(context.Context, *ent.Tx, workitemcreation.Identity, string, string, string) (*ent.IntakeRequest, ClaimOutcome, error)
	Complete(context.Context, *ent.Tx, int, int, int) error
}

type workItemWriter interface {
	CreateBase(context.Context, *ent.Tx, *workitemcreation.CreationPlan, *authorization.CreationAuthorization) (*ent.Ticket, error)
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
	directory   database.DirectorySnapshot
	resolver    referenceResolver
	receipts    receiptRepository
	registry    *CreatorRegistry
	workItems   workItemWriter
	fieldValues fieldValueWriter
	snapshots   snapshotWriter
	audits      auditWriter
	outbox      outboxWriter
	metrics     *Metrics
}

func NewService(client *ent.Client, resolver referenceResolver, registry *CreatorRegistry, workItems workItemWriter, directory database.DirectorySnapshot) *Service {
	return &Service{
		client: client, directory: directory, resolver: resolver, receipts: NewIdempotencyRepository(), registry: registry, workItems: workItems,
		fieldValues: itsmservice.NewFieldValueService(client), snapshots: NewSnapshotRepository(),
		audits: NewAuditRepository(), outbox: itsmservice.NewOutboxEventRepository(client),
		metrics: defaultMetrics,
	}
}

func (s *Service) Create(ctx context.Context, identity workitemcreation.Identity, command workitemcreation.CreateWorkItemCommand) (result *workitemcreation.CreateWorkItemResult, resultErr error) {
	started := time.Now()
	defer func() {
		if s == nil || s.metrics == nil {
			return
		}
		recordClass, outcome := createMetricResult(result, resultErr)
		s.metrics.ObserveCreate(identity.Channel, recordClass, outcome, time.Since(started))
		if resultErr == nil && result != nil && !result.Replayed && result.WorkflowStartStatus == "pending" {
			s.metrics.ObserveWorkflowStart(identity.Channel, result.RecordClass, "pending")
		}
	}()
	if s == nil || s.client == nil || missingDependency(s.resolver) || missingDependency(s.receipts) || s.registry == nil || missingDependency(s.workItems) || missingDependency(s.fieldValues) || missingDependency(s.snapshots) || missingDependency(s.audits) || missingDependency(s.outbox) {
		return nil, workitemcreation.NewInternalFailure("intake service is not fully configured", nil)
	}
	if scope, ok := tenantctx.TenantID(ctx); tenantctx.IsSystemBypass(ctx) || (ok && scope != identity.TenantID) {
		return nil, workitemcreation.NewPermissionDenied("intake target differs from authenticated tenant", nil)
	}
	if missingDependency(s.directory) {
		return nil, workitemcreation.NewInfrastructureUnavailable("intake directory snapshot is required", nil)
	}
	if err := identity.ValidateCommand(command); err != nil {
		return nil, err
	}
	normalized, digest, err := workitemcreation.CanonicalizeCommand(command)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		result, retry, attemptErr := s.createAttempt(ctx, identity, normalized, digest)
		if !retry && !retryableTransactionConflict(attemptErr) {
			return result, attemptErr
		}
		lastErr = attemptErr
	}
	return nil, workitemcreation.NewInfrastructureUnavailable("could not obtain a stable intake transaction after contention", lastErr)
}

func (s *Service) createAttempt(ctx context.Context, identity workitemcreation.Identity, command workitemcreation.CreateWorkItemCommand, digest string) (*workitemcreation.CreateWorkItemResult, bool, error) {
	// All authorization/configuration reads, revision checks and graph writes
	// share one snapshot. Concurrent receipt/allocator writes may serialize;
	// Create retries the entire rolled-back attempt with a fresh snapshot.
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, false, workitemcreation.NewInfrastructureUnavailable("could not begin intake transaction", err)
	}
	defer func() { _ = tx.Rollback() }()

	directory, closeDirectory, err := s.directory.Open(ctx, tx, identity.TenantID)
	if err != nil {
		return nil, false, workitemcreation.NewInfrastructureUnavailable("could not open intake directory snapshot", err)
	}
	if directory == nil || closeDirectory == nil {
		if closeDirectory != nil {
			_ = closeDirectory()
		}
		return nil, false, workitemcreation.NewInfrastructureUnavailable("invalid intake directory snapshot", nil)
	}
	directoryClosed := false
	defer func() {
		if !directoryClosed {
			_ = closeDirectory()
		}
	}()
	authorized, authErr := authorization.AuthorizeWorkItemCreation(ctx, tx, directory, identity, command)
	closeErr := closeDirectory()
	directoryClosed = true
	if closeErr != nil {
		return nil, false, workitemcreation.NewInfrastructureUnavailable("could not close intake directory snapshot", errors.Join(authErr, closeErr))
	}
	if authErr != nil {
		return nil, false, authErr
	}
	identity = authorized.Identity()

	receipt, outcome, err := s.receipts.Claim(ctx, tx, identity, command.IdempotencyKey, digest, workitemcreation.CanonicalDigestVersion)
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
	if resolved == nil || resolved.Identity != identity || resolved.RecordClass != command.RecordClass {
		return nil, false, workitemcreation.NewDomainValidationFailed("resolver changed immutable creation scope", nil)
	}
	creator, err := s.registry.Get(resolved.RecordClass)
	if err != nil {
		return nil, false, err
	}
	plan, err := creator.Prepare(ctx, tx, *resolved)
	if err != nil {
		return nil, false, err
	}
	if plan == nil || plan.WorkItem.TenantID != identity.TenantID || plan.WorkItem.ActorID != identity.ActorID || plan.WorkItem.RequesterID != identity.RequesterID || plan.WorkItem.RecordClass != resolved.RecordClass {
		return nil, false, workitemcreation.NewDomainValidationFailed("preparation changed immutable creation scope", nil)
	}
	if err := s.resolver.ResolveWorkflow(ctx, tx, plan); err != nil {
		return nil, false, err
	}
	resolved = &plan.Resolved
	workItem, err := s.workItems.CreateBase(ctx, tx, plan, authorized)
	if err != nil {
		return nil, false, err
	}
	professional, err := creator.CreateExtension(ctx, tx, workItem, plan)
	if err != nil {
		return nil, false, err
	}
	if err := validateProfessional(ctx, tx, workItem, professional); err != nil {
		return nil, false, err
	}
	if err := itsmservice.NewTicketSLAService(tx.Client(), nil).ApplyCreationSLA(ctx, tx, workItem, plan.WorkItem.SLADefinitionID); err != nil {
		return nil, false, err
	}
	if err := s.writeFieldValues(ctx, tx, resolved, workItem.ID, professional); err != nil {
		return nil, false, err
	}
	if _, err := s.snapshots.Create(ctx, tx, buildSnapshot(receipt.ID, workItem.ID, digest, resolved)); err != nil {
		return nil, false, err
	}
	if err := s.audits.RecordCreated(ctx, tx, CreatedAuditInput{
		TenantID: identity.TenantID, UserID: identity.ActorID, WorkItemID: workItem.ID,
		Authorization: authorized, IntakeRequestID: receipt.ID,
		RequestID: auditRequestID(ctx, identity, digest), Path: intakeCreatePath, Method: "POST", StatusCode: 201,
	}); err != nil {
		return nil, false, err
	}
	workflowStatus := "not_required"
	if !resolved.Workflow.NoProcess {
		if err := s.enqueueWorkflowStart(ctx, tx, receipt.ID, workItem, identity, plan); err != nil {
			return nil, false, err
		}
		workflowStatus = "pending"
	}
	if err := s.receipts.Complete(ctx, tx, identity.TenantID, receipt.ID, workItem.ID); err != nil {
		return nil, false, err
	}
	result := &workitemcreation.CreateWorkItemResult{
		WorkItemID: workItem.ID, Number: workItem.TicketNumber, RecordClass: resolved.RecordClass,
		ProfessionalReference: *professional, WorkflowStartStatus: workflowStatus,
	}
	if err := tx.Commit(); err != nil {
		return nil, false, workitemcreation.NewInfrastructureUnavailable("could not commit intake transaction", err)
	}
	return result, false, nil
}

func (s *Service) writeFieldValues(ctx context.Context, tx *ent.Tx, resolved *workitemcreation.ResolvedIntake, workItemID int, professional *workitemcreation.ProfessionalReference) error {
	if len(resolved.Command.FormValues) == 0 {
		return nil
	}
	if len(resolved.Command.AdHocFields) > 0 {
		values := []itsmservice.AdHocFieldValue{}
		for index, definition := range resolved.Command.AdHocFields {
			value, present := resolved.Command.FormValues[definition.Name]
			if present {
				values = append(values, itsmservice.AdHocFieldValue{Name: definition.Name, Label: definition.Label, SortOrder: index, Value: value})
			}
		}
		if err := itsmservice.NewFieldValueService(tx.Client()).CreateAdHocValuesTx(ctx, tx, resolved.Identity.TenantID, "ticket", workItemID, values); err != nil {
			return workitemcreation.NewInfrastructureUnavailable("could not persist ad-hoc fields", err)
		}
		return nil
	}
	scopeType, scopeID := "", 0
	if resolved.Catalog != nil {
		scopeType, scopeID = "service_catalog", resolved.Catalog.ID
	} else if resolved.FieldScope != nil {
		scopeType, scopeID = resolved.FieldScope.EntityType, resolved.FieldScope.EntityID
	}
	if scopeType == "" || scopeID <= 0 {
		return workitemcreation.NewDomainValidationFailed("dynamic fields require a resolved definition scope", nil)
	}
	err := s.fieldValues.CreateValuesTx(
		ctx, tx, resolved.Identity.TenantID, scopeType, scopeID,
		"ticket", workItemID, resolved.Command.FormValues,
	)
	if err == nil {
		return nil
	}
	var validation *itsmservice.FieldValidationError
	if errors.As(err, &validation) {
		return workitemcreation.NewDomainValidationFailed("invalid dynamic field value", err, workitemcreation.FieldError{Field: "formValues." + validation.Field, Message: validation.Message})
	}
	return workitemcreation.NewInfrastructureUnavailable("could not persist intake field values", err)
}

func buildSnapshot(receiptID, workItemID int, digest string, resolved *workitemcreation.ResolvedIntake) SnapshotInput {
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
	if resolved.FieldScope != nil {
		input.FormSchemaVersion = resolved.FieldScope.Version
	}
	if resolved.Catalog != nil {
		catalogID := resolved.Catalog.ID
		input.CatalogItemID = &catalogID
		input.CatalogVersion = resolved.Catalog.Version
		input.FormSchemaVersion = resolved.Catalog.FormSchemaVersion
	}
	input.CTISnapshot = map[string]any{}
	for key, id := range map[string]*int{"categoryId": resolved.CTI.CategoryID, "typeId": resolved.CTI.TypeID, "itemId": resolved.CTI.ItemID} {
		if id != nil {
			input.CTISnapshot[key] = *id
		}
	}
	return input
}

func auditRequestID(ctx context.Context, identity workitemcreation.Identity, digest string) string {
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

func (s *Service) enqueueWorkflowStart(ctx context.Context, tx *ent.Tx, receiptID int, item *ent.Ticket, identity workitemcreation.Identity, plan *workitemcreation.CreationPlan) error {
	resolved := &plan.Resolved
	workItemID := item.ID
	if resolved.Workflow.DefinitionID == nil {
		return workitemcreation.NewWorkflowBindingRequired("workflow definition is required for process start", nil)
	}
	variables := make(map[string]any, len(plan.WorkflowVariables)+12)
	for key, value := range plan.WorkflowVariables {
		variables[key] = value
	}
	// Shared identity is set once from the committed creation graph, never from caller form fields.
	for key, value := range map[string]any{"work_item_id": item.ID, "ticket_id": item.ID, "ticket_number": item.TicketNumber, "record_class": item.RecordClass, "tenant_id": item.TenantID, "requester_id": identity.RequesterID, "triggered_by": strconv.Itoa(identity.ActorID), "channel": identity.Channel} {
		variables[key] = value
	}
	if item.AssigneeID > 0 {
		variables["assignee_id"] = item.AssigneeID
	}
	eventID := workflowStartEventID(workItemID, *resolved.Workflow.DefinitionID)
	payload, err := json.Marshal(map[string]any{
		"tenantId": identity.TenantID, "workItemId": workItemID, "recordClass": resolved.RecordClass,
		"workflowDefinitionId": *resolved.Workflow.DefinitionID, "workflowDefinitionKey": resolved.Workflow.DefinitionKey,
		"workflowDefinitionVersion": resolved.Workflow.DefinitionVersion, "workflowDefinitionDigest": resolved.Workflow.DefinitionDigest, "actorId": identity.ActorID,
		"channel": identity.Channel, "intakeRequestId": receiptID, "dedupeKey": eventID, "variables": variables,
	})
	if err != nil {
		return workitemcreation.NewInternalFailure("could not encode workflow start event", err)
	}
	_, err = s.outbox.Enqueue(ctx, tx, itsmservice.NewOutboxEvent{
		EventID: eventID, EventType: workflowStartEventType, TenantID: identity.TenantID,
		AggregateType: "work_item", AggregateID: strconv.Itoa(workItemID), Payload: payload,
	})
	if err != nil {
		return workitemcreation.NewInfrastructureUnavailable("could not enqueue workflow start", err)
	}
	return nil
}

func (s *Service) loadResult(ctx context.Context, tx *ent.Tx, tenantID, workItemID int, replayed bool) (*workitemcreation.CreateWorkItemResult, error) {
	workItem, err := tx.Ticket.Query().Where(ticket.IDEQ(workItemID), ticket.TenantIDEQ(tenantID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, workitemcreation.NewReferenceNotFound("completed intake work item was not found", err)
	}
	if ent.IsNotSingular(err) {
		return nil, workitemcreation.NewInternalFailure("completed intake work item is ambiguous", err)
	}
	if err != nil {
		return nil, workitemcreation.NewInfrastructureUnavailable("could not load completed intake work item", err)
	}
	result := &workitemcreation.CreateWorkItemResult{WorkItemID: workItem.ID, Number: workItem.TicketNumber, RecordClass: workItem.RecordClass, Replayed: replayed}
	switch workItem.RecordClass {
	case workitemcreation.RecordClassGeneric:
	case workitemcreation.RecordClassProblem:
		extension, queryErr := tx.Problem.Query().Where(problem.WorkItemIDEQ(workItem.ID)).Only(ctx)
		if queryErr != nil && !ent.IsNotFound(queryErr) && !ent.IsNotSingular(queryErr) {
			return nil, workitemcreation.NewInfrastructureUnavailable("could not load professional extension", queryErr)
		}
		if queryErr != nil {
			return nil, workitemcreation.NewInternalFailure("missing problem extension", queryErr)
		}
		result.ProfessionalReference = workitemcreation.ProfessionalReference{Type: "problem", ID: extension.ID}
	case workitemcreation.RecordClassIncident:
		extension, queryErr := tx.Incident.Query().Where(incident.WorkItemIDEQ(workItem.ID)).Only(ctx)
		if queryErr != nil && !ent.IsNotFound(queryErr) && !ent.IsNotSingular(queryErr) {
			return nil, workitemcreation.NewInfrastructureUnavailable("could not load professional extension", queryErr)
		}
		if queryErr != nil {
			return nil, workitemcreation.NewInternalFailure("completed work item is missing its incident extension", queryErr)
		}
		result.ProfessionalReference = workitemcreation.ProfessionalReference{Type: "incident", ID: extension.ID}
	case workitemcreation.RecordClassServiceRequestItem:
		extension, queryErr := tx.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(workItem.ID), servicerequest.TenantIDEQ(tenantID)).Only(ctx)
		if queryErr != nil && !ent.IsNotFound(queryErr) && !ent.IsNotSingular(queryErr) {
			return nil, workitemcreation.NewInfrastructureUnavailable("could not load professional extension", queryErr)
		}
		if queryErr != nil {
			return nil, workitemcreation.NewInternalFailure("completed work item is missing its service request extension", queryErr)
		}
		result.ProfessionalReference = workitemcreation.ProfessionalReference{Type: "service_request", ID: extension.ID}
	case workitemcreation.RecordClassChangeRequest:
		extension, queryErr := tx.Change.Query().Where(change.WorkItemIDEQ(workItem.ID)).Only(ctx)
		if queryErr != nil && !ent.IsNotFound(queryErr) && !ent.IsNotSingular(queryErr) {
			return nil, workitemcreation.NewInfrastructureUnavailable("could not load professional extension", queryErr)
		}
		if queryErr != nil {
			return nil, workitemcreation.NewInternalFailure("completed work item is missing its change extension", queryErr)
		}
		result.ProfessionalReference = workitemcreation.ProfessionalReference{Type: "change", ID: extension.ID}
	default:
		return nil, workitemcreation.NewUnsupportedRecordClass("completed work item has an unsupported record class", nil)
	}
	snapshot, err := tx.IntakeResolutionSnapshot.Query().Where(
		intakeresolutionsnapshot.WorkItemIDEQ(workItem.ID), intakeresolutionsnapshot.TenantIDEQ(tenantID),
	).Only(ctx)
	if err != nil && !ent.IsNotFound(err) && !ent.IsNotSingular(err) {
		return nil, workitemcreation.NewInfrastructureUnavailable("could not load completed intake graph", err)
	}
	if err != nil {
		return nil, workitemcreation.NewInternalFailure("completed work item is missing its intake snapshot", err)
	}
	if snapshot.NoProcess {
		result.WorkflowStartStatus = "not_required"
		return result, nil
	}
	if snapshot.WorkflowDefinitionID == nil {
		return nil, workitemcreation.NewInternalFailure("intake snapshot is missing its workflow definition", nil)
	}
	eventID := workflowStartEventID(workItem.ID, *snapshot.WorkflowDefinitionID)
	event, err := tx.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID), outboxevent.TenantIDEQ(tenantID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) && !ent.IsNotSingular(err) {
		return nil, workitemcreation.NewInfrastructureUnavailable("could not load completed intake graph", err)
	}
	if err != nil {
		return nil, workitemcreation.NewInternalFailure("completed work item is missing its workflow start event", err)
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

func workflowStartEventID(workItemID, definitionID int) string {
	return "workflow-start:" + strconv.Itoa(workItemID) + ":" + strconv.Itoa(definitionID)
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

var _ workitemcreation.Application = (*Service)(nil)

func validateProfessional(ctx context.Context, tx *ent.Tx, item *ent.Ticket, reference *workitemcreation.ProfessionalReference) error {
	if reference == nil {
		return workitemcreation.NewInternalFailure("creator returned no professional reference", nil)
	}
	if item.RecordClass == workitemcreation.RecordClassGeneric {
		if *reference != (workitemcreation.ProfessionalReference{}) {
			return workitemcreation.NewDomainValidationFailed("generic work item must not have an extension", nil)
		}
		return nil
	}
	if reference.ID <= 0 {
		return workitemcreation.NewDomainValidationFailed("professional reference is required", nil)
	}
	var ok bool
	var err error
	switch item.RecordClass {
	case workitemcreation.RecordClassIncident:
		if reference.Type != "incident" {
			break
		}
		ok, err = tx.Incident.Query().Where(incident.IDEQ(reference.ID), incident.WorkItemIDEQ(item.ID)).Exist(ctx)
	case workitemcreation.RecordClassProblem:
		if reference.Type != "problem" {
			break
		}
		ok, err = tx.Problem.Query().Where(problem.IDEQ(reference.ID), problem.WorkItemIDEQ(item.ID)).Exist(ctx)
	case workitemcreation.RecordClassChangeRequest:
		if reference.Type != "change" {
			break
		}
		ok, err = tx.Change.Query().Where(change.IDEQ(reference.ID), change.WorkItemIDEQ(item.ID)).Exist(ctx)
	case workitemcreation.RecordClassServiceRequestItem:
		if reference.Type != "service_request" {
			break
		}
		ok, err = tx.ServiceRequest.Query().Where(servicerequest.IDEQ(reference.ID), servicerequest.TicketIDEQ(item.ID), servicerequest.TenantIDEQ(item.TenantID)).Exist(ctx)
	}
	if err != nil {
		return workitemcreation.NewInfrastructureUnavailable("could not query intake reference", err)
	}
	if !ok {
		return workitemcreation.NewReferenceNotFound("professional extension does not belong to work item", err)
	}
	return nil
}

func missingDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func retryableTransactionConflict(err error) bool {
	var state interface{ SQLState() string }
	if !errors.As(err, &state) {
		return false
	}
	return state.SQLState() == "40001" || state.SQLState() == "40P01"
}
