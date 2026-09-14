// Package workitemassignment owns shared owner mutation, never professional lifecycle.
package workitemassignment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticket"
)

const EventType = "work_item.assigned"

var (
	ErrInvalidCommand  = errors.New("invalid WorkItem assignment command")
	ErrVersionConflict = errors.New("WorkItem assignment version conflict")
)

type Command struct {
	WorkItemID      int    `json:"workItemId"`
	TenantID        int    `json:"tenantId"`
	ActorTenantID   int    `json:"actorTenantId"`
	ActorID         int    `json:"actorId"`
	AssigneeID      int    `json:"assigneeId"`
	ExpectedVersion int    `json:"expectedVersion"`
	Source          string `json:"source"`
	Reason          string `json:"reason"`
}
type Event struct {
	Command            Command `json:"command"`
	PreviousAssigneeID int     `json:"previousAssigneeId"`
	Version            int     `json:"version"`
	EventID            string  `json:"eventId"`
}
type Enqueue func(context.Context, *ent.Client, Event) error

// IdentityValidator uses the owning caller's verified directory/session snapshot.
// It must validate actor provenance and every nonzero assignee against the
// selected target tenant using the existing directory/allocation policy.
// Row visibility and professional assignment permission remain caller obligations.
type IdentityValidator func(context.Context, *ent.Client, Command) error
type Writer struct {
	enqueue          Enqueue
	validateIdentity IdentityValidator
}

func NewWriter(enqueue Enqueue, validateIdentity IdentityValidator) *Writer {
	return &Writer{enqueue: enqueue, validateIdentity: validateIdentity}
}
func (c Command) Validate() error {
	if c.WorkItemID <= 0 || c.TenantID <= 0 || c.ActorTenantID <= 0 || c.ActorID <= 0 || c.AssigneeID < 0 || c.ExpectedVersion <= 0 ||
		strings.TrimSpace(c.Source) != c.Source || c.Source == "" || len(c.Source) > 128 || strings.ContainsFunc(c.Source, unicode.IsControl) || len(c.Reason) > 4096 {
		return ErrInvalidCommand
	}
	return nil
}
func EventID(tenantID, workItemID, version int) string {
	return fmt.Sprintf("work-item-assigned:%d:%d:%d", tenantID, workItemID, version)
}

// Apply requires a caller-owned transaction. On ANY error the caller rolls back.
// A changed owner increments the aggregate version exactly once; mixed field
// updates in that transaction must not increment it a second time. Zero clears
// the owner. A same-owner command validates identity/version but creates no audit
// or event and does not increment the version.
func (w *Writer) Apply(ctx context.Context, client *ent.Client, cmd Command) (*ent.Ticket, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	if w == nil || w.enqueue == nil || w.validateIdentity == nil || client == nil {
		return nil, fmt.Errorf("assignment dependencies are required")
	}
	if scope, ok := tenantctx.TenantID(ctx); ok && scope != cmd.TenantID {
		return nil, fmt.Errorf("assignment tenant context mismatch")
	}
	// Ent explicitly rejects nested transactions. Probe before touching domain
	// rows so accidentally passing a pool client can never partially commit.
	probe, err := client.Tx(ctx)
	if !errors.Is(err, ent.ErrTxStarted) {
		if probe != nil {
			_ = probe.Rollback()
		}
		return nil, fmt.Errorf("assignment requires caller transaction")
	}
	if err = w.validateIdentity(ctx, client, cmd); err != nil {
		return nil, err
	}
	item, err := client.Ticket.Query().Where(ticket.ID(cmd.WorkItemID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(), lock).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock assignment WorkItem: %w", err)
	}
	if item.Version != cmd.ExpectedVersion {
		return nil, ErrVersionConflict
	}
	if item.AssigneeID == cmd.AssigneeID {
		return item, nil
	}
	policy, err := authorization.ResolveWorkItemPolicy(item.RecordClass)
	if err != nil {
		return nil, err
	}
	// All instance locks precede all task locks; ordered IDs stabilize the audit
	// snapshot and agree with terminal execution's WorkItem -> instance -> task order.
	instances, err := client.ProcessInstance.Query().Where(processinstance.BusinessID(item.ID), processinstance.TenantID(cmd.TenantID), processinstance.BusinessType(string(policy.BusinessType)), lock).Order(ent.Asc(processinstance.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	instanceIDs := make([]int, 0, len(instances))
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}
	taskIDs := []int{}
	if len(instanceIDs) > 0 {
		tasks, err := client.ProcessTask.Query().Where(processtask.ProcessInstanceIDIn(instanceIDs...), processtask.TenantID(cmd.TenantID), processtask.AssigneeSource(common.BPMNAssigneeSourceWorkItem), processtask.StatusNotIn(common.ProcessTaskStatusCompleted, common.ProcessTaskStatusCancelled), lock).Order(ent.Asc(processtask.FieldID)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			taskIDs = append(taskIDs, task.ID)
		}
	}
	update := client.Ticket.Update().Where(ticket.ID(item.ID), ticket.TenantID(cmd.TenantID), ticket.Version(cmd.ExpectedVersion)).AddVersion(1)
	if cmd.AssigneeID == 0 {
		update.ClearAssigneeID()
	} else {
		update.SetAssigneeID(cmd.AssigneeID)
	}
	count, err := update.Save(ctx)
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrVersionConflict
	}
	event := Event{Command: cmd, PreviousAssigneeID: item.AssigneeID, Version: item.Version + 1, EventID: EventID(cmd.TenantID, item.ID, item.Version+1)}
	err = client.TicketWorkflowRecord.Create().SetTenantID(cmd.TenantID).SetTicketID(item.ID).SetAction("assign").SetOperatorID(cmd.ActorID).
		SetFromUserID(item.AssigneeID).SetToUserID(cmd.AssigneeID).SetReason(cmd.Reason).
		SetMetadata(map[string]any{"source": cmd.Source, "actorTenantId": cmd.ActorTenantID, "eventId": event.EventID, "version": event.Version, "previousAssigneeId": item.AssigneeID, "assigneeId": cmd.AssigneeID, "affectedTaskIds": taskIDs}).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("assignment audit: %w", err)
	}
	if err = w.enqueue(ctx, client, event); err != nil {
		return nil, fmt.Errorf("assignment outbox: %w", err)
	}
	return client.Ticket.Get(ctx, item.ID)
}
func lock(selector *sql.Selector) {
	if selector.Dialect() != dialect.SQLite {
		selector.ForUpdate()
	}
}
