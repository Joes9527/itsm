package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/workitemidentity"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/ticket"
	"itsm-backend/service/bpmn"
)

type submittedWorkItemStartKey struct{}
type submittedWorkItemStart struct{ tenantID, workItemID int }

// SubmittedWorkItemProcessStarter belongs to the professional submit command.
// It uses the caller transaction and immutable creation evidence, never current routing.
type SubmittedWorkItemProcessStarter interface {
	StartSubmittedWorkItemProcessTx(context.Context, *ent.Tx, int, map[string]interface{}) (*ent.ProcessInstance, error)
}

func (e *CustomProcessEngine) StartSubmittedWorkItemProcessTx(ctx context.Context, tx *ent.Tx, workItemID int, professional map[string]interface{}) (*ent.ProcessInstance, error) {
	if tx == nil || e.transactionBound {
		return nil, common.NewValidationError("professional start requires owning transaction", nil)
	}
	if err := e.requireActorSnapshot(ctx, tx); err != nil {
		return nil, err
	}
	tenantID, err := bpmnAuthorizedTenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	item, err := tx.Ticket.Query().Where(ticket.ID(workItemID), ticket.TenantID(tenantID), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := authorization.ResolveWorkItemPolicy(item.RecordClass)
	if err != nil {
		return nil, err
	}
	if policy.WorkflowStartTiming != authorization.WorkflowStartOnSubmit || item.Status != "submitted" {
		return nil, common.NewValidationError("professional workflow requires submitted WorkItem", nil)
	}
	snapshot, err := tx.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemID(item.ID), intakeresolutionsnapshot.TenantID(tenantID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, common.NewValidationError("frozen workflow evidence is missing", nil)
	}
	if err != nil {
		return nil, err
	}
	if snapshot.NoProcess {
		return nil, common.NewValidationError("Change submission requires an approval workflow; configured no_process cannot authorize submission", nil)
	}
	if snapshot.RecordClass != item.RecordClass || snapshot.WorkflowDefinitionID == nil || len(snapshot.WorkflowDefinitionDigest) != 64 || len(snapshot.WorkflowVariables) == 0 {
		return nil, common.NewValidationError("frozen workflow evidence is incomplete", nil)
	}
	receipt, err := tx.IntakeRequest.Query().Where(intakerequest.ID(snapshot.IntakeRequestID), intakerequest.TenantID(tenantID), intakerequest.WorkItemID(item.ID), intakerequest.Status("completed")).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, common.NewValidationError("frozen workflow creation receipt is unavailable", nil)
	}
	if err != nil {
		return nil, err
	}
	if receipt.RequestDigest != snapshot.RequestDigest {
		return nil, common.NewValidationError("frozen workflow creation receipt conflicts", nil)
	}
	definition, err := tx.ProcessDefinition.Query().Where(processdefinition.ID(*snapshot.WorkflowDefinitionID), processdefinition.TenantID(tenantID), processdefinition.Key(snapshot.WorkflowDefinitionKey), processdefinition.Version(snapshot.WorkflowDefinitionVersion), processdefinition.IsActive(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, common.NewValidationError("frozen process definition unavailable or changed", nil)
	}
	if err != nil {
		return nil, err
	}
	frozen := FreezeProcessDefinition(definition)
	if frozen.Digest != snapshot.WorkflowDefinitionDigest {
		return nil, common.NewValidationError("frozen process definition unavailable or changed", nil)
	}
	variables := map[string]interface{}{}
	decoder := json.NewDecoder(bytes.NewReader(snapshot.WorkflowVariables))
	decoder.UseNumber()
	if err = decoder.Decode(&variables); err != nil || variables == nil {
		return nil, common.NewValidationError("frozen workflow variables invalid", nil)
	}
	if frozenWorkflowInteger(variables["work_item_id"]) != item.ID || frozenWorkflowInteger(variables["tenant_id"]) != tenantID || variables["record_class"] != item.RecordClass || frozenWorkflowInteger(variables["requester_id"]) != receipt.RequesterID || fmt.Sprint(variables["triggered_by"]) != fmt.Sprint(receipt.ActorID) {
		return nil, common.NewValidationError("frozen workflow identity variables conflict with creation receipt", nil)
	}
	for key, value := range professional {
		variables[key] = value
	}
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	variables["work_item_id"], variables["record_class"], variables["tenant_id"] = item.ID, item.RecordClass, item.TenantID
	variables["requester_id"], variables["triggered_by"] = item.RequesterID, fmt.Sprint(actorID)
	variables["version"], variables["status"] = item.Version, item.Status
	businessKey, err := dto.WorkItemBusinessKey(item.RecordClass, item.ID)
	if err != nil {
		return nil, err
	}
	existing, err := tx.ProcessInstance.Query().Where(processinstance.TenantID(tenantID), processinstance.BusinessKey(businessKey)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if existing {
		return nil, common.NewValidationError("professional workflow start conflicts with an existing process instance", nil)
	}
	startKey := fmt.Sprintf("workflow-submit:%d:%d", item.ID, definition.ID)
	identity := fmt.Sprintf("PI-start-%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%s", tenantID, startKey))))
	raw, err := json.Marshal(variables)
	if err != nil {
		return nil, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(append([]byte(frozen.Digest), raw...)))
	ctx = context.WithValue(ctx, submittedWorkItemStartKey{}, submittedWorkItemStart{tenantID, item.ID})
	keys := []string{}
	instance, err := e.forClient(tx.Client(), &keys, tx).startResolvedProcess(ctx, definition, businessKey, string(policy.BusinessType), item.ID, variables, identity, digest)
	if err != nil {
		return nil, err
	}
	tx.OnCommit(func(next ent.Committer) ent.Committer {
		return ent.CommitFunc(func(commitCtx context.Context, tx *ent.Tx) error {
			if err := next.Commit(commitCtx, tx); err != nil {
				return err
			}
			instance.Unwrap()
			e.processCommittedCallbackKeys(ctx, tenantID, keys)
			return nil
		})
	})
	return instance, nil
}

// Every engine entry shares the same timing policy, including generic HTTP triggers.
func validateWorkItemStartTiming(ctx context.Context, client *ent.Client, businessKey, businessType string, tenantID, workItemID int, variables map[string]interface{}) error {
	if businessType == "" && workItemID == 0 {
		if _, _, err := workitemidentity.ParseBusinessKey(businessKey); err == nil {
			return common.NewValidationError("independent workflow cannot use a WorkItem business key", nil)
		}
		for _, key := range []string{"work_item_id", "ticket_id", "change_id", "record_class", "business_type", "business_id", "business_key"} {
			if _, present := variables[key]; present {
				return common.NewValidationError("independent workflow cannot supply WorkItem identity variables", nil)
			}
		}
		return nil
	}
	if businessType == string(dto.BusinessTypeRelease) {
		return nil
	}
	_, policy, err := authorization.ResolveWorkItemIdentity(ctx, client, workItemID, tenantID)
	if err != nil {
		return err
	}
	if string(policy.BusinessType) != businessType {
		return common.NewValidationError("workflow business identity conflicts with WorkItem", nil)
	}
	switch policy.WorkflowStartTiming {
	case authorization.WorkflowStartOnCreation:
		return nil
	case authorization.WorkflowStartOnSubmit:
		scope, ok := ctx.Value(submittedWorkItemStartKey{}).(submittedWorkItemStart)
		if ok && scope.tenantID == tenantID && scope.workItemID == workItemID {
			return nil
		}
		return common.NewValidationError("workflow must be started by the professional submit command", nil)
	default:
		return common.NewValidationError("unsupported workflow start timing", nil)
	}
}

func frozenWorkflowInteger(value interface{}) int {
	n, ok := value.(json.Number)
	if !ok {
		return 0
	}
	i, err := n.Int64()
	if err != nil {
		return 0
	}
	return int(i)
}
