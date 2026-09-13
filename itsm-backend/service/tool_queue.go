package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"io"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/toolinvocation"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"strconv"
	"sync"
	"time"
)

type ToolJob struct {
	InvocationID int
	TenantID     int
	RequestID    string
}

var ErrToolQueueClosed = errors.New("tool queue is stopping or stopped")

type toolQueueState uint8

const (
	toolQueueCreated toolQueueState = iota
	toolQueueAccepting
	toolQueueStopping
	toolQueueStopped
)

type ToolQueue struct {
	admit      func(context.Context, ToolJob) error
	admissions sync.WaitGroup
	execution  *database.ExecutionPolicy
	mu         sync.Mutex
	cond       *sync.Cond
	jobs       []ToolJob
	capacity   int
	state      toolQueueState
	stopping   chan struct{}
	stopped    chan struct{}
	closeOnce  sync.Once
	process    func(context.Context, ToolJob) error
	client     *ent.Client
	tools      *ToolRegistry
	creation   creation.Application
	tickets    *TicketService
	logger     *zap.SugaredLogger
	workerCtx  context.Context
	cancel     context.CancelFunc
	stopParent func() bool
}

func NewToolQueue(client *ent.Client, tools *ToolRegistry, app creation.Application, tickets *TicketService, capacity int, logger *zap.SugaredLogger, execution *database.ExecutionPolicy) *ToolQueue {
	if client == nil || app == nil || execution == nil {
		panic("tool queue requires the shared creation application and tenant client")
	}
	q := &ToolQueue{execution: execution, client: client, tools: tools, creation: app, tickets: tickets}
	q.initialize(capacity, logger, q.ProcessJob, func(ctx context.Context, job ToolJob) error { _, _, err := q.loadApprovedTool(ctx, job); return err })
	return q
}

func (q *ToolQueue) initialize(capacity int, logger *zap.SugaredLogger, process func(context.Context, ToolJob) error, admit func(context.Context, ToolJob) error) {
	if capacity <= 0 {
		capacity = 100
	}
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	if process == nil || admit == nil {
		panic("tool queue requires a job processor")
	}
	q.capacity = capacity
	q.jobs = make([]ToolJob, 0, capacity)
	q.state = toolQueueCreated
	q.stopping = make(chan struct{})
	q.stopped = make(chan struct{})
	q.process = process
	q.admit = admit
	q.logger = logger
	q.cond = sync.NewCond(&q.mu)
}

// Start explicitly starts the only worker. Construction never processes jobs.
func (q *ToolQueue) Start(ctx context.Context) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cond == nil || q.state != toolQueueCreated || ctx == nil {
		return fmt.Errorf("tool queue cannot start in its current state")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	q.workerCtx, q.cancel = context.WithCancel(ctx)
	q.state = toolQueueAccepting
	q.stopParent = context.AfterFunc(ctx, q.Close)
	go q.worker()
	return nil
}

func (q *ToolQueue) Close() {
	q.closeOnce.Do(func() {
		q.mu.Lock()
		wasCreated := q.state == toolQueueCreated
		q.state = toolQueueStopping
		if q.stopParent != nil {
			q.stopParent()
		}
		if q.cancel != nil {
			q.cancel()
		}
		close(q.stopping)
		q.cond.Broadcast()
		if wasCreated {
			q.state = toolQueueStopped
			close(q.stopped)
		}
		q.mu.Unlock()
	})
	<-q.stopped
	q.admissions.Wait()
}

func (q *ToolQueue) Enqueue(job ToolJob) error {
	if job.TenantID <= 0 || job.InvocationID <= 0 {
		return fmt.Errorf("tool invocation identity is required")
	}
	q.mu.Lock()
	if q.state != toolQueueAccepting {
		q.mu.Unlock()
		return fmt.Errorf("%w; approved invocation remains pending", ErrToolQueueClosed)
	}
	q.admissions.Add(1)
	parent := q.workerCtx
	q.mu.Unlock()
	defer q.admissions.Done()
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if err := q.admit(ctx, job); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.state != toolQueueAccepting {
		return fmt.Errorf("%w; approved invocation remains pending", ErrToolQueueClosed)
	}
	if len(q.jobs) >= q.capacity {
		return fmt.Errorf("tool queue is full; approved invocation remains pending")
	}
	q.jobs = append(q.jobs, job)
	q.cond.Signal()
	return nil
}

func (q *ToolQueue) worker() {
	for {
		q.mu.Lock()
		for q.state == toolQueueAccepting && len(q.jobs) == 0 {
			q.cond.Wait()
		}
		if q.state != toolQueueAccepting {
			buffered := append([]ToolJob(nil), q.jobs...)
			q.jobs = nil
			q.state = toolQueueStopped
			q.mu.Unlock()
			for _, job := range buffered {
				q.logger.Warnw("Approved tool job remains pending after queue shutdown", "tenant_id", job.TenantID, "invocation_id", job.InvocationID)
			}
			close(q.stopped)
			return
		}
		job := q.jobs[0]
		q.jobs[0] = ToolJob{}
		q.jobs = q.jobs[1:]
		q.mu.Unlock()

		ctx, cancel := context.WithTimeout(q.workerCtx, 30*time.Second)
		if err := q.process(ctx, job); err != nil {
			q.logger.Warnw("Approved tool job did not complete", "tenant_id", job.TenantID, "invocation_id", job.InvocationID)
		}
		cancel()
	}
}

// ProcessJob rechecks the persisted approval and original caller at the execution
// boundary. The invocation ID is the stable source identity across lost acks.
// loadApprovedTool reads current source, approval and identities in one transaction.
// Both enqueue and execution call it; business writes must still revalidate in their own transaction.
func (q *ToolQueue) loadApprovedTool(ctx context.Context, job ToolJob) (inv *ent.ToolInvocation, actor *ent.User, err error) {
	if job.TenantID <= 0 || job.InvocationID <= 0 {
		return nil, nil, creation.NewInvalidCommand("tool invocation identity is required", creation.FieldError{}, nil)
	}
	if ctx == nil || tenantctx.IsSystemBypass(ctx) {
		return nil, nil, fmt.Errorf("explicit tool execution context required")
	}
	if tenantID, ok := tenantctx.TenantID(ctx); ok && tenantID != job.TenantID {
		return nil, nil, fmt.Errorf("tool execution tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, job.TenantID)
	if q.execution == nil || q.client == nil {
		return nil, nil, fmt.Errorf("tool execution dependencies required")
	}
	tx, err := q.client.Tx(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if closeErr := tx.Rollback(); closeErr != nil {
			inv = nil
			actor = nil
			err = errors.Join(err, closeErr)
		}
	}()
	if err = q.execution.BindEnt(ctx, tx, job.TenantID); err != nil {
		return nil, nil, err
	}
	if err = q.execution.RequireEntToolInvocation(ctx, tx, job.TenantID, job.InvocationID); err != nil {
		return nil, nil, err
	}
	inv, err = tx.ToolInvocation.Query().Where(toolinvocation.IDEQ(job.InvocationID), toolinvocation.TenantIDEQ(job.TenantID)).Only(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !inv.NeedsApproval || inv.ApprovalState != "approved" || inv.ApprovedBy <= 0 || inv.ApprovedAt.IsZero() || inv.UserID <= 0 || inv.DryRun {
		return nil, nil, creation.NewPermissionDenied("approved invocation and original actor are required", nil)
	}
	actor, err = tx.User.Query().Where(user.IDEQ(inv.UserID), user.TenantIDEQ(inv.TenantID), user.ActiveEQ(true)).Only(ctx)
	if err != nil {
		return nil, nil, creation.NewPermissionDenied("tool actor is unavailable", err)
	}
	approver, err := tx.User.Query().Where(user.IDEQ(inv.ApprovedBy), user.TenantIDEQ(inv.TenantID), user.ActiveEQ(true)).Only(ctx)
	if err != nil {
		return nil, nil, creation.NewPermissionDenied("tool approver is unavailable", err)
	}
	if err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: inv.TenantID, ActorID: approver.ID, RequesterID: approver.ID, Role: approver.Role}, "ai", "write"); err != nil {
		return nil, nil, err
	}
	return inv, actor, nil
}

func (q *ToolQueue) ProcessJob(ctx context.Context, job ToolJob) error {
	inv, actor, err := q.loadApprovedTool(ctx, job)
	if err != nil {
		return err
	}
	ctx = tenantctx.WithTenantID(ctx, job.TenantID)
	var result any
	switch inv.ToolName {
	case "create_ticket":
		var command creation.CreateWorkItemCommand
		var requester int
		command, requester, err = toolCreationCommand(inv.Arguments, inv.ID, actor.ID)
		if err == nil {
			result, err = q.creation.Create(ctx, creation.Identity{TenantID: inv.TenantID, ActorID: actor.ID, RequesterID: requester, Role: authorization.EffectiveSessionRole(actor), Channel: "ai_tool", Provider: "tool_queue"}, command)
		}
	case "update_ticket":
		if q.tickets == nil {
			err = creation.NewInternalFailure("ticket owner is unavailable", nil)
			break
		}
		var command dto.TicketEditCommand
		command, err = toolEditCommand(inv.Arguments, inv.ID, actor.ID, inv.TenantID)
		if err == nil {
			result, err = q.tickets.UpdateTicket(ctx, command)
		}

	default:
		if q.tools == nil {
			err = fmt.Errorf("tool registry is unavailable")
			break
		}
		var args map[string]any
		d := json.NewDecoder(bytes.NewBufferString(inv.Arguments))
		d.UseNumber()
		err = d.Decode(&args)
		if err == nil {
			result, err = q.tools.Execute(ctx, inv.TenantID, inv.ToolName, args)
		}
	}
	if err != nil {
		if inv.Status != "done" {
			message := "tool execution failed"
			var typed *creation.IntakeError
			if errors.As(err, &typed) {
				message = typed.Message
			}
			_, writeErr := q.client.ToolInvocation.UpdateOneID(inv.ID).SetStatus("failed").SetError(message).Save(ctx)
			if writeErr != nil {
				return errors.Join(err, writeErr)
			}
		}
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = q.client.ToolInvocation.UpdateOneID(inv.ID).SetStatus("done").ClearError().SetResult(string(encoded)).Save(ctx)
	return err
}
func positiveToolInteger(raw any) (int, error) {
	number, ok := raw.(json.Number)
	if !ok {
		return 0, fmt.Errorf("integer argument is required")
	}
	value, err := strconv.Atoi(string(number))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("positive integer argument is required")
	}
	return value, nil
}
func toolCreationCommand(raw string, invocationID, actorID int) (creation.CreateWorkItemCommand, int, error) {
	command := creation.CreateWorkItemCommand{RecordClass: "generic", IntakeKind: "generic", Confirmation: "confirmed", IdempotencyKey: fmt.Sprintf("tool-invocation:%d", invocationID), Generic: &creation.GenericInput{Source: "ai"}, SourceReference: &creation.SourceReference{Provider: "tool_queue", EventID: strconv.Itoa(invocationID)}}
	requester := actorID
	invalid := func() (creation.CreateWorkItemCommand, int, error) {
		return command, 0, creation.NewInvalidCommand("invalid create_ticket arguments", creation.FieldError{Field: "arguments", Message: "only title, description, priority and requester_id are accepted"}, nil)
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.UseNumber()
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return invalid()
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return invalid()
		}
		seen[key] = true
		var value any
		if err = d.Decode(&value); err != nil {
			return invalid()
		}
		switch key {
		case "title", "description", "priority":
			text, ok := value.(string)
			if !ok {
				return invalid()
			}
			switch key {
			case "title":
				command.Title = text
			case "description":
				command.Description = text
			case "priority":
				command.Priority = text
			}
		case "requester_id":
			requester, err = positiveToolInteger(value)
			if err != nil {
				return invalid()
			}
		default:
			return invalid()
		}
	}
	if _, err = d.Token(); err != nil {
		return invalid()
	}
	if _, err = d.Token(); err != io.EOF {
		return invalid()
	}
	return command, requester, nil
}

// toolEditCommand decodes exactly the persisted, approved edit arguments. It
// never substitutes a newly read version or a new operation identity on retry.
func toolEditCommand(raw string, invocationID, actorID, tenantID int) (dto.TicketEditCommand, error) {
	cmd := dto.TicketEditCommand{Meta: workitemmutation.Meta{TenantID: tenantID, ActorID: actorID, Source: "ai_tool", OperationID: fmt.Sprintf("tool:update_ticket:%d", invocationID)}}
	invalid := func() (dto.TicketEditCommand, error) {
		return dto.TicketEditCommand{}, creation.NewInvalidCommand("invalid update_ticket arguments", creation.FieldError{Field: "arguments", Message: "ticket_id, expectedVersion and status or assignee_id are required; unknown or duplicate fields are rejected"}, nil)
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.UseNumber()
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return invalid()
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return invalid()
		}
		seen[key] = true
		var value any
		if err = d.Decode(&value); err != nil {
			return invalid()
		}
		switch key {
		case "ticket_id", "expectedVersion", "assignee_id":
			n, err := positiveToolInteger(value)
			if err != nil {
				return invalid()
			}
			switch key {
			case "ticket_id":
				cmd.WorkItemID = n
			case "expectedVersion":
				cmd.Meta.ExpectedVersion = n
			case "assignee_id":
				cmd.Fields.AssigneeID = n
			}
		case "status":
			status, ok := value.(string)
			if !ok || status == "" {
				return invalid()
			}
			cmd.Fields.Status = status
		default:
			return invalid()
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return invalid()
	}
	if _, err = d.Token(); err != io.EOF {
		return invalid()
	}
	if invocationID <= 0 || actorID <= 0 || tenantID <= 0 || cmd.WorkItemID <= 0 || cmd.Meta.ExpectedVersion <= 0 || (!seen["status"] && !seen["assignee_id"]) {
		return invalid()
	}
	return cmd, nil
}
