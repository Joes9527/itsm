package service

import (
	"context"
	"database/sql"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"errors"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/ticket"
	"strconv"
	"time"
)

// A response owns one RR snapshot. Caches are bounded to one task chunk, hold
// successful reads only, and are never installed on mutation contexts.
const bpmnTaskReadBatchSize = 128

type bpmnTaskReadKey struct{}
type bpmnReadItem struct {
	item   *ent.Ticket
	policy authorization.WorkItemPolicy
}
type bpmnTaskReadSnapshot struct {
	pageAssignments map[int]BPMNTaskAssignment

	now time.Time

	terminal map[int]BPMNTaskAssignment

	client         *ent.Client
	tx             *ent.Tx
	directory      *ent.Client
	closeDirectory func() error
	users          map[[2]int]*ent.User
	items          map[[2]int]bpmnReadItem
	visibility     map[[3]int]bool
}

func taskReadSnapshot(ctx context.Context, client *ent.Client) *bpmnTaskReadSnapshot {
	view, _ := ctx.Value(bpmnTaskReadKey{}).(*bpmnTaskReadSnapshot)
	if view != nil && view.client == client {
		return view
	}
	return nil
}
func (v *bpmnTaskReadSnapshot) resetChunk() {
	v.terminal = make(map[int]BPMNTaskAssignment)

	v.users = make(map[[2]int]*ent.User)
	v.items = make(map[[2]int]bpmnReadItem)
	v.visibility = make(map[[3]int]bool)
}
func (e *CustomProcessEngine) withTaskReadSnapshot(ctx context.Context, run func(context.Context, *CustomProcessEngine) error) error {
	if taskReadSnapshot(ctx, e.client) != nil {
		return run(ctx, e)
	}
	return withBPMNTaskReadSnapshot(ctx, e.client, func(ctx context.Context, tx *ent.Tx) error { return run(ctx, e.forTransaction(tx, nil)) })
}
func withBPMNTaskReadSnapshot(ctx context.Context, client *ent.Client, run func(context.Context, *ent.Tx) error) (err error) {
	tx, err := client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	view := &bpmnTaskReadSnapshot{client: tx.Client(), tx: tx, now: time.Now()}
	view.resetChunk()
	err = run(context.WithValue(ctx, bpmnTaskReadKey{}, view), tx)
	if view.closeDirectory != nil {
		err = errors.Join(err, view.closeDirectory())
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func boundWorkItemVisible(ctx context.Context, client *ent.Client, item *ent.Ticket, actor *ent.User, tenantID int) (bool, error) {
	view := taskReadSnapshot(ctx, client)
	key := [3]int{tenantID, item.ID, actor.ID}
	if view != nil {
		if visible, ok := view.visibility[key]; ok {
			return visible, nil
		}
	}
	visible, err := client.Ticket.Query().Where(ticket.ID(item.ID), ticket.TenantID(tenantID), authorization.WorkItemRowScope(actor.ID, authorization.EffectiveSessionRole(actor))).Exist(ctx)
	if err == nil && view != nil {
		view.visibility[key] = visible
	}
	return visible, err
}

// Both task and audit materialization are chunk bounded. At most two matching
// records per task are retained; two already establishes unavailable ambiguity.
func (v *bpmnTaskReadSnapshot) loadTerminalAssignments(ctx context.Context, tasks []*ent.ProcessTask) error {
	predicates := make([]predicate.ProcessAuditLog, 0)
	terminalTasks := make(map[int]*ent.ProcessTask)
	evidence := make(map[int][]*ent.ProcessAuditLog)
	for _, task := range tasks {
		if task.AssigneeSource != BPMNAssigneeSourceWorkItem || (task.Status != "completed" && task.Status != "cancelled") {
			continue
		}
		action := AuditActionTaskCompleted
		if task.Status == "cancelled" {
			action = AuditActionTaskCancelled
		}
		id := task.ID
		predicates = append(predicates, processauditlog.And(processauditlog.TenantID(task.TenantID), processauditlog.ProcessInstanceID(task.ProcessInstanceID), processauditlog.ActivityID(task.TaskDefinitionKey), processauditlog.Action(action), processauditlog.ActivityType(ActivityTypeUserTask), func(s *entsql.Selector) {
			// JSON containment avoids PostgreSQL integer casts on untrusted audit
			// metadata and accepts integral JSON numbers; canonical strings are
			// also supported by the existing exact evidence matcher.
			s.Where(entsql.Or(sqljson.ValueContains(processauditlog.FieldMetadata, id, sqljson.Path("taskId")), sqljson.ValueEQ(processauditlog.FieldMetadata, strconv.Itoa(id), sqljson.Path("taskId"))))
		}))
		terminalTasks[id] = task
	}
	if len(predicates) == 0 {
		return nil
	}
	lastID := 0
	for {
		logs, err := v.client.ProcessAuditLog.Query().Where(processauditlog.Or(predicates...), processauditlog.IDGT(lastID)).Order(ent.Asc(processauditlog.FieldID)).Limit(bpmnTaskReadBatchSize).All(ctx)
		if err != nil {
			return err
		}
		if len(logs) == 0 {
			break
		}
		for _, entry := range logs {
			id, ok := numericInt(entry.Metadata["taskId"])
			if !ok {
				continue
			}
			task := terminalTasks[id]
			if task == nil || len(evidence[id]) >= 2 {
				continue
			}
			if terminalTaskAssignment(task, []*ent.ProcessAuditLog{entry}).State == "terminal" {
				evidence[id] = append(evidence[id], entry)
			}
		}
		lastID = logs[len(logs)-1].ID
		if len(logs) < bpmnTaskReadBatchSize {
			break
		}
	}
	for id, task := range terminalTasks {
		v.terminal[id] = terminalTaskAssignment(task, evidence[id])
	}
	return nil
}
