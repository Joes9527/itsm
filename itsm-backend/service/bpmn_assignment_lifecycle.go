package service

import (
	"context"
	"fmt"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticket"
)

func bpmnLifecycleLock(s *sql.Selector) {
	if s.Dialect() != dialect.SQLite {
		s.ForUpdate()
	}
}

// Called before any instance or task write, including advancement from a
// legacy task into a bound task. Release and unbound legacy instances keep
// their existing identity semantics; bound nodes validate identity separately.
func lockBPMNBusinessItem(ctx context.Context, client *ent.Client, tenantID, businessID int, businessType string) error {
	if businessID <= 0 || businessType == "release" {
		return nil
	}
	_, err := client.Ticket.Query().Where(ticket.ID(businessID), ticket.TenantID(tenantID), ticket.DeletedAtIsNil(), bpmnLifecycleLock).Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	return err
}

// A new instance is a phantom for an assignment transaction whose repeatable
// read snapshot predates this start. A row lock alone cannot invalidate that
// snapshot. Rewrite only existing values so PostgreSQL rejects that stale
// assignment with 40001, without changing the WorkItem aggregate version or
// public update timestamp. The caller still owns rollback and retry policy.
func fenceBPMNBoundCreation(ctx context.Context, client *ent.Client, tenantID, businessID int) error {
	item, err := client.Ticket.Query().Where(ticket.ID(businessID), ticket.TenantID(tenantID), ticket.DeletedAtIsNil(), bpmnLifecycleLock).Only(ctx)
	if err != nil {
		return err
	}
	_, err = client.Ticket.UpdateOne(item).SetVersion(item.Version).SetUpdatedAt(item.UpdatedAt).Save(ctx)
	return err
}

func lockBPMNInstanceLifecycle(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance) (*ent.ProcessInstance, error) {
	if err := lockBPMNBusinessItem(ctx, client, instance.TenantID, instance.BusinessID, instance.BusinessType); err != nil {
		return nil, err
	}
	locked, err := client.ProcessInstance.Query().Where(processinstance.ID(instance.ID), processinstance.TenantID(instance.TenantID), bpmnLifecycleLock).Only(ctx)
	if err != nil {
		return nil, err
	}
	if locked.BusinessID != instance.BusinessID || locked.BusinessType != instance.BusinessType {
		return nil, fmt.Errorf("process business identity changed during lifecycle command")
	}
	return locked, nil
}

func lockBPMNTaskLifecycle(ctx context.Context, client *ent.Client, task *ent.ProcessTask) (*ent.ProcessTask, error) {
	instance, err := client.ProcessInstance.Query().Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(task.TenantID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = lockBPMNInstanceLifecycle(ctx, client, instance); err != nil {
		return nil, err
	}
	return client.ProcessTask.Query().Where(processtask.ID(task.ID), processtask.TenantID(task.TenantID), processtask.ProcessInstanceID(instance.ID), bpmnLifecycleLock).Only(ctx)
}

// Responsibility is captured while the owning WorkItem lock is held. It must
// never be copied into the task's persisted assignee or candidate fields.
func boundTaskResponsibleID(ctx context.Context, client *ent.Client, task *ent.ProcessTask) (int, error) {
	if task.AssigneeSource == "" {
		return 0, nil
	}
	if task.AssigneeSource != BPMNAssigneeSourceWorkItem {
		return 0, fmt.Errorf("unsupported task assignment source")
	}
	item, _, err := resolveBoundTaskWorkItem(ctx, client, task)
	if err != nil {
		return 0, err
	}
	return item.AssigneeID, nil
}

func (s *BPMNAuditService) recordBoundTaskTerminal(ctx context.Context, task *ent.ProcessTask, responsibleID, actorID int, actorName, source, reason string, before, after, metadata map[string]interface{}) error {
	// Bound tasks currently have authenticated user commands only. A future
	// system command must add its own verified provenance boundary first.
	if actorID <= 0 || source == "" {
		return fmt.Errorf("bound terminal audit requires authenticated actor and command source")
	}
	audit, err := s.taskAuditContext(ctx, task, actorID, actorName)
	if err != nil {
		return err
	}
	audit.Action = AuditActionTaskCompleted
	if task.Status == "cancelled" {
		audit.Action = AuditActionTaskCancelled
	} else if task.Status != "completed" {
		return fmt.Errorf("bound task terminal audit requires terminal status")
	}
	audit.ActivityType = ActivityTypeUserTask
	audit.AssigneeID = responsibleID
	audit.Comment = reason
	audit.VariablesBefore = before
	audit.VariablesAfter = after
	audit.Metadata = make(map[string]interface{}, len(metadata)+4)
	for key, value := range metadata {
		audit.Metadata[key] = value
	}
	audit.Metadata["taskId"] = task.ID
	audit.Metadata["taskVersion"] = task.AggregationVersion
	audit.Metadata["terminalStatus"] = task.Status
	audit.Metadata["assigneeSource"] = BPMNAssigneeSourceWorkItem
	audit.Metadata["source"] = source
	return s.RecordAudit(ctx, audit)
}

// Resolve policy here only to keep the identity check beside creation locks.
func validateBoundProcessBusiness(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance) error {
	_, policy, err := authorization.ResolveWorkItemIdentity(ctx, client, instance.BusinessID, instance.TenantID)
	if err != nil {
		return err
	}
	if string(policy.BusinessType) != instance.BusinessType {
		return fmt.Errorf("bound process business identity mismatch")
	}
	return nil
}
