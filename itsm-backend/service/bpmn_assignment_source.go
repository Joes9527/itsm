package service

import (
	"fmt"
	"strings"
)

const BPMNAssigneeSourceWorkItem = "work_item_assignee"

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
