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
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/toolinvocation"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"
	"sync"
	"time"
)

type ToolJob struct {
	InvocationID int
	TenantID     int
	RequestID    string
}
type ToolQueue struct {
	jobs      chan ToolJob
	done      chan struct{}
	closeOnce sync.Once
	client    *ent.Client
	tools     *ToolRegistry
	creation  creation.Application
	tickets   *TicketService
	logger    *zap.SugaredLogger
}

func NewToolQueue(client *ent.Client, tools *ToolRegistry, app creation.Application, tickets *TicketService, capacity int, logger *zap.SugaredLogger) *ToolQueue {
	if client == nil || app == nil {
		panic("tool queue requires the shared creation application and tenant client")
	}
	if capacity <= 0 {
		capacity = 100
	}
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	q := &ToolQueue{jobs: make(chan ToolJob, capacity), done: make(chan struct{}), client: client, tools: tools, creation: app, tickets: tickets, logger: logger}
	go q.worker()
	return q
}
func (q *ToolQueue) Close() { q.closeOnce.Do(func() { close(q.done) }) }
func (q *ToolQueue) Enqueue(job ToolJob) error {
	if job.TenantID <= 0 || job.InvocationID <= 0 {
		return fmt.Errorf("tool invocation identity is required")
	}
	select {
	case <-q.done:
		return fmt.Errorf("tool queue is closed")
	default:
	}
	select {
	case q.jobs <- job:
		return nil
	default:
		return fmt.Errorf("tool queue is full; approved invocation remains pending")
	}
}
func (q *ToolQueue) worker() {
	for {
		select {
		case <-q.done:
			return
		case job := <-q.jobs:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := q.ProcessJob(ctx, job); err != nil {
				q.logger.Warnw("Approved tool job did not complete", "tenant_id", job.TenantID, "invocation_id", job.InvocationID)
			}
			cancel()
		}
	}
}

// ProcessJob rechecks the persisted approval and original caller at the execution
// boundary. The invocation ID is the stable source identity across lost acks.
func (q *ToolQueue) ProcessJob(ctx context.Context, job ToolJob) error {
	if job.TenantID <= 0 || job.InvocationID <= 0 {
		return creation.NewInvalidCommand("tool invocation identity is required", creation.FieldError{}, nil)
	}
	ctx = tenantctx.WithTenantID(ctx, job.TenantID)
	inv, err := q.client.ToolInvocation.Query().Where(toolinvocation.IDEQ(job.InvocationID), toolinvocation.TenantIDEQ(job.TenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if !inv.NeedsApproval || inv.ApprovalState != "approved" || inv.ApprovedBy <= 0 || inv.ApprovedAt.IsZero() || inv.UserID <= 0 || inv.DryRun {
		return creation.NewPermissionDenied("approved invocation and original actor are required", nil)
	}
	actor, err := q.client.User.Query().Where(user.IDEQ(inv.UserID), user.TenantIDEQ(inv.TenantID), user.ActiveEQ(true)).Only(ctx)
	if err != nil {
		return creation.NewPermissionDenied("tool actor is unavailable", err)
	}
	approver, err := q.client.User.Query().Where(user.IDEQ(inv.ApprovedBy), user.TenantIDEQ(inv.TenantID), user.ActiveEQ(true)).Only(ctx)
	if err != nil {
		return creation.NewPermissionDenied("tool approver is unavailable", err)
	}
	tx, err := q.client.Tx(ctx)
	if err != nil {
		return err
	}
	err = authorization.RequireCurrentPermission(ctx, tx, creation.Identity{TenantID: inv.TenantID, ActorID: approver.ID, RequesterID: approver.ID, Role: approver.Role}, "ai", "write")
	_ = tx.Rollback()
	if err != nil {
		return err
	}
	var result any
	switch inv.ToolName {
	case "create_ticket":
		var command creation.CreateWorkItemCommand
		var requester int
		command, requester, err = toolCreationCommand(inv.Arguments, inv.ID, actor.ID)
		if err == nil {
			result, err = q.creation.Create(ctx, creation.Identity{TenantID: inv.TenantID, ActorID: actor.ID, RequesterID: requester, Role: actor.Role, Channel: "ai_tool", Provider: "tool_queue"}, command)
		}
	case "update_ticket":
		if q.tickets == nil {
			err = creation.NewInternalFailure("ticket owner is unavailable", nil)
			break
		}
		var args map[string]any
		d := json.NewDecoder(bytes.NewBufferString(inv.Arguments))
		d.UseNumber()
		err = d.Decode(&args)
		if err != nil {
			break
		}
		id, idErr := positiveToolInteger(args["ticket_id"])
		if idErr != nil {
			err = idErr
			break
		}
		assignee := 0
		if raw, ok := args["assignee_id"]; ok {
			assignee, err = positiveToolInteger(raw)
			if err != nil {
				break
			}
		}
		status, _ := args["status"].(string)
		result, err = q.tickets.UpdateTicket(ctx, id, &dto.UpdateTicketRequest{Status: status, AssigneeID: assignee, UserID: actor.ID}, inv.TenantID)
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
