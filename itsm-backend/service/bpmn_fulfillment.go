package service

import (
	"context"
	"itsm-backend/ent"
	"itsm-backend/ent/kaftaskactionledger"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/handlers/common/accessgrant"
	"itsm-backend/service/bpmn"
)

type WorkflowFulfillment struct {
	State           string
	DelegatedTaskID int
	Approvals       []accessgrant.ApprovalEvidence
}

// ReadWorkflowFulfillment is the workflow owner's projection for Requested Items.
// Process/transport completion never establishes verified external fulfillment.
func ReadWorkflowFulfillment(ctx context.Context, client *ent.Client, tenantID, itemID int) (WorkflowFulfillment, error) {
	result := WorkflowFulfillment{State: "unknown"}
	instances, err := client.ProcessInstance.Query().Where(processinstance.TenantIDEQ(tenantID), processinstance.BusinessTypeEQ("service_request"), processinstance.BusinessIDEQ(itemID)).All(ctx)
	if err != nil {
		return result, err
	}
	if len(instances) != 1 {
		return result, nil
	}
	instance := instances[0]
	decisions, err := client.ProcessApprovalDecision.Query().Where(processapprovaldecision.TenantIDEQ(tenantID), processapprovaldecision.ProcessInstanceIDEQ(instance.ID)).Order(ent.Asc(processapprovaldecision.FieldID)).All(ctx)
	if err != nil {
		return result, err
	}
	for _, d := range decisions {
		if d.Decision == "rejected" {
			result.State = "rejected"
			return result, nil
		}
		if d.Decision == "approved" && d.Action == "approve" {
			result.Approvals = append(result.Approvals, accessgrant.ApprovalEvidence{DecisionID: d.ID, TaskID: d.TaskID, ActorID: d.ActorID, Decision: d.Decision})
		}
	}
	if instance.Status == "terminated" || instance.Status == "cancelled" {
		result.State = "cancelled"
		return result, nil
	}
	if instance.Status != "running" {
		return result, nil
	}
	tasks, err := client.ProcessTask.Query().Where(processtask.TenantIDEQ(tenantID), processtask.ProcessInstanceIDEQ(instance.ID)).All(ctx)
	if err != nil {
		return result, err
	}
	for _, task := range tasks {
		if task.Status == "created" || task.Status == "assigned" || task.Status == "started" {
			if task.TaskType == "user_task" && task.TaskVariables["taskPurpose"] == "approval" {
				result.State = "awaiting_approval"
				return result, nil
			}
		}
	}
	for _, task := range tasks {
		if task.TaskType == bpmn.KafDelegateTaskType && task.Status == "delegated" {
			// A recorded uncertain/failed execution remains unknown even while its task
			// is still delegated. Claim delivery and process start are not fulfillment.
			ledgers, err := client.KafTaskActionLedger.Query().Where(kaftaskactionledger.TenantIDEQ(tenantID), kaftaskactionledger.TaskIDEQ(task.TaskID)).All(ctx)
			if err != nil {
				return result, err
			}
			for _, ledger := range ledgers {
				if ledger.ResultStatus == "failed_terminal" || ledger.ResultStatus == "failed_retryable" {
					return WorkflowFulfillment{State: "unknown"}, nil
				}
			}
			if result.DelegatedTaskID != 0 {
				return WorkflowFulfillment{State: "unknown"}, nil
			}
			result.DelegatedTaskID = task.ID
			result.State = "fulfilling"
		}
	}
	return result, nil
}
