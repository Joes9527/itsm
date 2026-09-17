package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticket"
)

const WorkItemCompletionNote = "workItemCompletionNote"

type genericWorkflowFacts struct {
	OwnerID                                                                   int
	Status, Resolution                                                        string
	Running, Approved, Handled, Escalated, Resolved, Closed, ResolveConfirmed bool
	Waiting                                                                   WorkItemPrerequisite
}

func genericGateError(reason string) error {
	return common.NewValidationError("generic fulfillment: "+reason, nil)
}

func evaluateGenericWorkflowPrerequisite(prerequisite WorkItemPrerequisite, f genericWorkflowFacts, note string, requireNote bool) error {
	switch prerequisite {
	case "":
		return nil
	case WorkItemPrerequisiteAssigned:
		if f.OwnerID <= 0 {
			return genericGateError("assign a current WorkItem owner first")
		}
	case WorkItemPrerequisiteInProgress:
		if f.Status != "in_progress" {
			return genericGateError("WorkItem must be in progress")
		}
		if requireNote && (strings.TrimSpace(note) == "" || utf8.RuneCountInString(strings.TrimSpace(note)) > 4000) {
			return genericGateError("handling evidence must contain 1 to 4000 characters")
		}
	case WorkItemPrerequisiteEscalated:
		if !f.Escalated {
			return genericGateError("successful escalation receipt is required for this stage")
		}
	case WorkItemPrerequisiteResolved:
		if f.Status != "resolved" || strings.TrimSpace(f.Resolution) == "" || !f.Resolved {
			return genericGateError("resolved WorkItem and this stage's resolution receipt are required")
		}
	case WorkItemPrerequisiteClosed:
		if f.Status != "closed" || !f.Closed {
			return genericGateError("closed WorkItem and this stage's closure receipt are required")
		}
	default:
		return genericGateError("unsupported prerequisite")
	}
	return nil
}

func evaluateGenericWorkflowTransition(f genericWorkflowFacts, c GenericFulfillmentConfig, status string) error {
	switch status {
	case "in_progress", "resolved", "closed", "manual_escalation":
		if !f.Running {
			return genericGateError("workflow is not running or is awaiting creation")
		}
	default:
		return nil
	}
	switch status {
	case "in_progress":
		if f.Waiting != WorkItemPrerequisiteInProgress || (c.ApprovalRequired && !f.Approved) {
			return genericGateError("handling stage and approval evidence are required")
		}
	case "manual_escalation":
		if !c.NeedEscalate || f.Waiting != WorkItemPrerequisiteEscalated || !f.Handled {
			return genericGateError("escalation stage after handling is required")
		}
	case "resolved":
		if f.Waiting != WorkItemPrerequisiteResolved || !f.Handled || (c.NeedEscalate && !f.Escalated) {
			return genericGateError("completed handling and required escalation must precede resolution")
		}
	case "closed":
		if f.Waiting != WorkItemPrerequisiteClosed || !f.ResolveConfirmed {
			return genericGateError("resolution confirmation must precede closure")
		}
	}
	return nil
}

type genericWorkflowGate struct {
	item       *ent.Ticket
	instance   *ent.ProcessInstance
	definition *ent.ProcessDefinition
	process    *BPMNProcess
	config     *GenericFulfillmentConfig
	tasks      []*ent.ProcessTask
	facts      genericWorkflowFacts
}

func readGenericWorkflowDefinition(ctx context.Context, client *ent.Client, id, tenantID int) (*ent.ProcessDefinition, *BPMNProcess, *GenericFulfillmentConfig, error) {
	def, err := client.ProcessDefinition.Query().Where(processdefinition.ID(id), processdefinition.TenantID(tenantID)).Only(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	parsed, err := NewBPMNParser().ParseXML(def.BpmnXML)
	if err != nil {
		return nil, nil, nil, err
	}
	config, err := ReadGenericFulfillmentConfig(parsed, def.ProcessVariables)
	if err != nil {
		return nil, nil, nil, err
	}
	return def, parsed.Processes[0], config, nil
}

// Caller owns the transaction. Mutation callers lock WorkItem before instances
// and tasks; read-only UI callers pass lock=false and never repair missing data.
func loadGenericWorkflowGate(ctx context.Context, client *ent.Client, tenantID, workItemID int, lock bool) (*genericWorkflowGate, error) {
	q := client.Ticket.Query().Where(ticket.ID(workItemID), ticket.TenantID(tenantID), ticket.DeletedAtIsNil())
	if lock {
		q.Where(bpmnLifecycleLock)
	}
	item, err := q.Only(ctx)
	if err != nil {
		return nil, err
	}
	if item.RecordClass != "generic" {
		return nil, nil
	}
	iq := client.ProcessInstance.Query().Where(processinstance.TenantID(tenantID), processinstance.BusinessID(workItemID), processinstance.BusinessType("generic"))
	if lock {
		iq.Where(bpmnLifecycleLock)
	}
	instances, err := iq.All(ctx)
	if err != nil {
		return nil, err
	}
	var gate *genericWorkflowGate
	for _, instance := range instances {
		def, p, c, err := readGenericWorkflowDefinition(ctx, client, instance.ProcessDefinitionID, tenantID)
		if err != nil {
			return nil, err
		}
		if c == nil {
			continue
		}
		if gate != nil {
			return nil, genericGateError("multiple lifecycle contract instances are ambiguous")
		}
		gate = &genericWorkflowGate{item: item, instance: instance, definition: def, process: p, config: c}
	}
	if gate == nil {
		// Intake freezes routing before the async start. A pending opted-in start
		// cannot authorize direct lifecycle transitions merely because no instance exists.
		snapshot, err := client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.TenantID(tenantID), intakeresolutionsnapshot.WorkItemID(workItemID)).Only(ctx)
		if ent.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if snapshot.NoProcess {
			return nil, nil
		}
		if snapshot.WorkflowDefinitionID == nil {
			return nil, genericGateError("frozen workflow definition is missing")
		}
		def, p, c, err := readGenericWorkflowDefinition(ctx, client, *snapshot.WorkflowDefinitionID, tenantID)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, nil
		}
		receipt, err := client.IntakeRequest.Query().Where(intakerequest.ID(snapshot.IntakeRequestID), intakerequest.TenantID(tenantID), intakerequest.WorkItemID(workItemID), intakerequest.Status("completed")).Only(ctx)
		if err != nil {
			return nil, err
		}
		if snapshot.RecordClass != item.RecordClass || snapshot.WorkflowDefinitionKey != def.Key || snapshot.WorkflowDefinitionVersion != def.Version || snapshot.WorkflowDefinitionDigest != FreezeProcessDefinition(def).Digest || snapshot.RequestDigest != receipt.RequestDigest {
			return nil, genericGateError("frozen intake evidence conflicts")
		}
		gate = &genericWorkflowGate{item: item, definition: def, process: p, config: c}
	}
	gate.facts = genericWorkflowFacts{OwnerID: item.AssigneeID, Status: item.Status, Resolution: item.Resolution}
	if gate.instance == nil {
		return gate, nil
	}
	tq := client.ProcessTask.Query().Where(processtask.TenantID(tenantID), processtask.ProcessInstanceID(gate.instance.ID))
	if lock {
		tq.Where(bpmnLifecycleLock)
	}
	gate.tasks, err = tq.Order(ent.Asc(processtask.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	gate.facts.Running = gate.instance.Status == "running"
	for _, task := range gate.tasks {
		node := genericWorkflowTaskNode(gate.process, task.TaskDefinitionKey)
		if node == nil {
			continue
		}
		pre, err := node.WorkItemPrerequisite()
		if err != nil {
			return nil, err
		}
		if task.Status == "completed" {
			if pre == WorkItemPrerequisiteInProgress {
				note, _ := task.TaskVariables[WorkItemCompletionNote].(string)
				gate.facts.Handled = strings.TrimSpace(note) != ""
			}
			if pre == WorkItemPrerequisiteResolved {
				gate.facts.ResolveConfirmed = true
			}
		} else if task.Status != "cancelled" && pre != "" {
			if gate.facts.Waiting != "" {
				return nil, genericGateError("multiple active fulfillment stages are ambiguous")
			}
			gate.facts.Waiting = pre
		}
		since := task.CreatedTime
		if task.CreatedAt.After(since) {
			since = task.CreatedAt
		}
		if gate.instance.StartTime.After(since) {
			since = gate.instance.StartTime
		}
		switch pre {
		case WorkItemPrerequisiteEscalated:
			gate.facts.Escalated, err = genericWorkflowReceipt(ctx, client, item, "work_item.escalation.manual", "in_progress", since)
		case WorkItemPrerequisiteResolved:
			gate.facts.Resolved, err = genericWorkflowReceipt(ctx, client, item, "work_item.edit", "resolved", since)
		case WorkItemPrerequisiteClosed:
			gate.facts.Closed, err = genericWorkflowReceipt(ctx, client, item, "work_item.edit", "closed", since)
		}
		if err != nil {
			return nil, err
		}
	}
	decisions, err := client.ProcessApprovalDecision.Query().Where(processapprovaldecision.TenantID(tenantID), processapprovaldecision.ProcessInstanceID(gate.instance.ID)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, decision := range decisions {
		for _, task := range gate.tasks {
			node := genericWorkflowTaskNode(gate.process, task.TaskDefinitionKey)
			if task.ID != decision.ProcessTaskID || node == nil || node.TaskPurpose != "approval" || task.Status != "completed" {
				continue
			}
			if decision.Decision == "rejected" {
				gate.facts.Running = false
				gate.facts.Approved = false
				return gate, nil
			}
			if decision.Decision == "approved" && decision.Action == "approve" {
				gate.facts.Approved = true
			}
		}
	}
	return gate, nil
}

func genericWorkflowTaskNode(p *BPMNProcess, key string) *BPMNUserTask {
	for _, task := range p.UserTasks {
		if task.ID == key {
			return task
		}
	}
	return nil
}

func genericWorkflowReceipt(ctx context.Context, client *ent.Client, item *ent.Ticket, action, status string, since time.Time) (bool, error) {
	rows, err := client.AuditLog.Query().Where(auditlog.TenantID(item.TenantID), auditlog.Resource("work_item"), auditlog.Path(strconv.Itoa(item.ID)), auditlog.Action(action), auditlog.StatusCode(200), auditlog.CreatedAtGTE(since), auditlog.ResultStatus(status)).All(ctx)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.OperationID == nil || strings.TrimSpace(*row.OperationID) == "" || row.RequestDigest == nil || len(*row.RequestDigest) != 64 || row.ResultVersion == nil || *row.ResultVersion <= 0 || row.UserID <= 0 || row.RequestBody == nil {
			continue
		}
		var facts map[string]interface{}
		if json.Unmarshal([]byte(*row.RequestBody), &facts) != nil {
			continue
		}
		previous, _ := facts["previousStatus"].(string)
		if action == "work_item.edit" && (previous == "" || previous == status) {
			continue
		}
		if action == "work_item.escalation.manual" {
			prior, _ := facts["previousPriority"].(string)
			next, _ := facts["priority"].(string)
			reason, _ := facts["reason"].(string)
			if prior == "" || next == "" || prior == next || strings.TrimSpace(reason) == "" {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

// EnforceGenericWorkflowTransitionTx runs inside the owning versioned command.
// It is also available to legacy owners to detect and reject opted-in commands.
func EnforceGenericWorkflowTransitionTx(ctx context.Context, client *ent.Client, tenantID, workItemID int, status string) error {
	gate, err := loadGenericWorkflowGate(ctx, client, tenantID, workItemID, true)
	if err != nil || gate == nil {
		return err
	}
	return evaluateGenericWorkflowTransition(gate.facts, *gate.config, status)
}

func RejectGenericWorkflowLegacyMutationTx(ctx context.Context, client *ent.Client, tenantID, workItemID int) error {
	gate, err := loadGenericWorkflowGate(ctx, client, tenantID, workItemID, true)
	if err != nil {
		return err
	}
	if gate != nil {
		return genericGateError("use the versioned WorkItem command")
	}
	return nil
}

// GenericWorkflowTaskGate is a read-only projection of the command prerequisites.
// Missing handling text is represented as a required input, not a permanently disabled action.
func GenericWorkflowTaskGate(ctx context.Context, client *ent.Client, task *ent.ProcessTask) (reason string, completionNoteRequired bool, err error) {
	instance, err := client.ProcessInstance.Query().Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(task.TenantID)).Only(ctx)
	if err != nil {
		return "", false, err
	}
	if instance.BusinessType != "generic" {
		return "", false, nil
	}
	gate, err := loadGenericWorkflowGate(ctx, client, task.TenantID, instance.BusinessID, false)
	if err != nil || gate == nil {
		return "", false, err
	}
	if gate.instance == nil || gate.instance.ID != instance.ID {
		return "", false, genericGateError("task does not belong to authoritative lifecycle instance")
	}
	node := genericWorkflowTaskNode(gate.process, task.TaskDefinitionKey)
	if node == nil {
		return "", false, genericGateError("task is missing from immutable definition")
	}
	pre, err := node.WorkItemPrerequisite()
	if err != nil {
		return "", false, err
	}
	if !gate.facts.Running {
		return "流程未运行，不能完成任务", pre == WorkItemPrerequisiteInProgress, nil
	}
	err = evaluateGenericWorkflowPrerequisite(pre, gate.facts, "", false)
	if err != nil {
		return err.Error(), pre == WorkItemPrerequisiteInProgress, nil
	}
	return "", pre == WorkItemPrerequisiteInProgress, nil
}
