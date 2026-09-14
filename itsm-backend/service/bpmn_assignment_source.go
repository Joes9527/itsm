package service

import (
	"context"
	"fmt"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processinstance"
	"strconv"
	"strings"
)

const BPMNAssigneeSourceWorkItem = common.BPMNAssigneeSourceWorkItem

func validateBPMNAssigneeSource(task *BPMNUserTask) error {
	if task == nil {
		return fmt.Errorf("BPMN user task is required")
	}
	source := task.AssigneeSource
	if source == "" {
		return nil
	}
	if source != BPMNAssigneeSourceWorkItem {
		return fmt.Errorf("task %q has unsupported assignee source %q", task.ID, source)
	}
	if task.TaskPurpose != "fulfillment" {
		return fmt.Errorf("task %q assignee source %q requires fulfillment purpose", task.ID, source)
	}
	if strings.TrimSpace(task.Assignee) != "" || strings.TrimSpace(task.CandidateUsers) != "" || strings.TrimSpace(task.CandidateGroups) != "" {
		return fmt.Errorf("task %q assignee source %q conflicts with explicit assignee or candidates", task.ID, source)
	}
	if task.ApprovalMode != "" || task.ApprovalThreshold != 0 || task.RejectStrategy != "" || task.TimeoutAction != "" ||
		task.AllowDelegate || task.AllowAddApprover || task.CommentRequiredOnReject || strings.TrimSpace(task.AssigneeRole) != "" ||
		task.AssigneeDeptId != 0 || task.AssigneeTeamId != 0 || task.AssigneeProjectId != 0 || task.AssigneeTempTeamId != 0 || task.AssigneeGmChain {
		return fmt.Errorf("task %q assignee source %q conflicts with approval assignment configuration", task.ID, source)
	}
	if task.ServiceTaskType() != "" {
		return fmt.Errorf("task %q assignee source %q cannot use a delegated handler", task.ID, source)
	}
	return nil
}

// BPMNTaskAssignment is a read projection. It is never written to ProcessTask.
type BPMNTaskAssignment struct {
	Assignee, Source, State    string
	ResponsibleUserID, ActorID int
}

func resolveBoundTaskWorkItem(ctx context.Context, client *ent.Client, task *ent.ProcessTask) (*ent.Ticket, authorization.WorkItemPolicy, error) {
	view := taskReadSnapshot(ctx, client)
	key := [2]int{task.TenantID, task.ProcessInstanceID}
	if view != nil {
		if cached, ok := view.items[key]; ok {
			return cached.item, cached.policy, nil
		}
	}

	instance, err := client.ProcessInstance.Query().Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(task.TenantID)).Only(ctx)
	if err != nil {
		return nil, authorization.WorkItemPolicy{}, fmt.Errorf("resolve bound task instance: %w", err)
	}
	item, policy, err := authorization.ResolveWorkItemIdentity(ctx, client, instance.BusinessID, task.TenantID)
	if err != nil {
		return nil, policy, err
	}
	if string(policy.BusinessType) != instance.BusinessType {
		return nil, policy, common.NewForbiddenError("bound task business identity mismatch")
	}
	if view != nil {
		view.items[key] = bpmnReadItem{item: item, policy: policy}
	}
	return item, policy, nil
}

func (e *CustomProcessEngine) resolveTaskAssignment(ctx context.Context, client *ent.Client, task *ent.ProcessTask) (BPMNTaskAssignment, error) {
	if task == nil {
		return BPMNTaskAssignment{}, fmt.Errorf("task is required")
	}
	if view := taskReadSnapshot(ctx, client); view != nil {
		if assignment, ok := view.pageAssignments[task.ID]; ok {
			return assignment, nil
		}
	}
	assignment := BPMNTaskAssignment{Source: task.AssigneeSource, State: "unavailable"}
	if task.AssigneeSource == "" {
		assignment.Assignee = task.Assignee
		assignment.State = "unassigned"
		if task.Assignee != "" && task.Assignee != "0" {
			assignment.State = "assigned"
		}
		assignment.ResponsibleUserID, _ = strconv.Atoi(task.Assignee)
		return assignment, nil
	}
	if task.AssigneeSource != BPMNAssigneeSourceWorkItem {
		return assignment, fmt.Errorf("unsupported task assignee source %q", task.AssigneeSource)
	}
	if task.Status == common.ProcessTaskStatusCompleted || task.Status == common.ProcessTaskStatusCancelled {
		if view := taskReadSnapshot(ctx, client); view != nil {
			if assignment, ok := view.terminal[task.ID]; ok {
				return assignment, nil
			}
		}
		// Envelope supplies tenant/instance/node/action and responsible/actual actor.
		// Metadata supplies exact task identity and post-transition version. Ambiguous
		// or missing evidence must never fall back to the current WorkItem owner.
		action := AuditActionTaskCompleted
		if task.Status == common.ProcessTaskStatusCancelled {
			action = AuditActionTaskCancelled
		}
		logs, err := client.ProcessAuditLog.Query().Where(
			processauditlog.TenantID(task.TenantID), processauditlog.ProcessInstanceID(task.ProcessInstanceID),
			processauditlog.ActivityID(task.TaskDefinitionKey), processauditlog.Action(action), processauditlog.ActivityType(ActivityTypeUserTask),
		).All(ctx)
		if err != nil {
			return assignment, err
		}
		return terminalTaskAssignment(task, logs), nil
	}

	if ValidateBPMNTaskLifecycle(BPMNTaskCommandComplete, task.Status) != nil {
		return assignment, fmt.Errorf("unsupported bound task lifecycle %q", task.Status)
	}
	item, _, err := resolveBoundTaskWorkItem(ctx, client, task)
	if err != nil {
		return assignment, err
	}
	if item.AssigneeID <= 0 {
		assignment.State = "unassigned"
		return assignment, nil
	}
	owner, err := e.resolveAssignmentUser(ctx, client, item.AssigneeID, task.TenantID)
	if unavailableBPMNIdentity(err) {
		return assignment, nil
	}
	if err != nil {
		return assignment, err
	}
	assignment.Assignee = strconv.Itoa(owner.ID)
	assignment.ResponsibleUserID = owner.ID
	assignment.State = "assigned"
	return assignment, nil
}

// The single evidence matcher is shared by bounded read batches and fresh command/detail reads.
func terminalTaskAssignment(task *ent.ProcessTask, logs []*ent.ProcessAuditLog) BPMNTaskAssignment {
	assignment := BPMNTaskAssignment{Source: task.AssigneeSource, State: "unavailable"}
	var match *ent.ProcessAuditLog
	for _, entry := range logs {
		id, idOK := numericInt(entry.Metadata["taskId"])
		version, versionOK := numericInt(entry.Metadata["taskVersion"])
		if !idOK || !versionOK || id != task.ID || version != task.AggregationVersion ||
			entry.Metadata["terminalStatus"] != task.Status || entry.Metadata["assigneeSource"] != BPMNAssigneeSourceWorkItem {
			continue
		}
		if match != nil {
			return assignment
		}
		match = entry
	}
	if match != nil {
		assignment.State = "terminal"
		assignment.ResponsibleUserID = match.AssigneeID
		assignment.ActorID = match.UserID
		if match.AssigneeID > 0 {
			assignment.Assignee = strconv.Itoa(match.AssigneeID)
		}
	}
	return assignment
}
