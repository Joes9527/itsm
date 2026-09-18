package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

func genericStartXML() []byte {
	return []byte(strings.Replace(string(lifecycleXML(lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v1"), lifecycleMetadata("workItemPrerequisite", "assigned"), lifecycleOwnerAttrs, "")), `id="work"`, `id="work" name="Confirm assignment"`, 1))
}

func genericStartFixture(t *testing.T, flags map[string]interface{}) (*bpmnAuthorizationFixture, *ent.OutboxEvent) {
	t.Helper()
	f, event := workflowStartFixture(t, genericStartXML())
	f.definition = f.definition.Update().SetProcessVariables(flags).SaveX(context.Background())
	var payload workflowStartPayload
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	payload.Variables["approval_required"], payload.Variables["need_escalate"] = false, false
	if v, ok := flags["approval_required"]; ok {
		payload.Variables["approval_required"] = v
	}
	if v, ok := flags["need_escalate"]; ok {
		payload.Variables["need_escalate"] = v
	}
	event.Payload, _ = json.Marshal(payload)
	frozen, _ := json.Marshal(payload.Variables)
	replaceGenericSnapshot(t, f, FreezeProcessDefinition(f.definition).Digest, frozen, "generic")
	return f, event
}

func TestGenericStartRejectsPublicEnrollmentAndInputs(t *testing.T) {
	for _, key := range []string{"", "approval_required", "need_escalate", "approvalResult", "workItemCompletionNote"} {
		t.Run(key, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			f.definition = f.definition.Update().SetBpmnXML(genericStartXML()).SaveX(context.Background())
			item := f.workItem(t, 90)
			vars := map[string]interface{}{}
			if key != "" {
				vars[key] = false
			}
			_, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, fmt.Sprintf("generic:%d", item.ID), "generic", item.ID, vars)
			if key != "" {
				require.ErrorContains(t, err, key)
			} else {
				require.ErrorContains(t, err, "creation")
			}
			require.Zero(t, f.client.ProcessInstance.Query().CountX(context.Background()))
			_, err = f.engine.StartProcessByDefinitionID(startProcessContext(f), FreezeProcessDefinition(f.definition), fmt.Sprintf("generic:%d", item.ID), "generic", item.ID, vars, "new-contract-public")
			require.Error(t, err)
			require.Zero(t, f.client.ProcessInstance.Query().CountX(context.Background()))
		})
	}
}

func TestGenericStartTrustedSnapshotPinsFlagsAndVersion(t *testing.T) {
	for _, flags := range []map[string]interface{}{nil, {"approval_required": true, "need_escalate": true}} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			f, event := genericStartFixture(t, flags)
			// Current latest configuration must not replace the creation snapshot's definition.
			f.client.ProcessDefinition.Create().SetTenantID(f.tenant.ID).SetKey(f.definition.Key).SetVersion("2.0.0").SetName("later").SetDeploymentID(f.definition.DeploymentID).SetBpmnXML(genericStartXML()).SetIsActive(true).SetIsLatest(true).SetProcessVariables(map[string]interface{}{"approval_required": false, "need_escalate": false}).SaveX(context.Background())
			handler := NewWorkflowStartOutboxHandler(f.client, f.engine, f.client)
			require.NoError(t, handler.Deliver(context.Background(), event))
			instance := f.client.ProcessInstance.Query().OnlyX(context.Background())
			require.Equal(t, f.definition.ID, instance.ProcessDefinitionID)
			require.Equal(t, flags != nil, instance.Variables["approval_required"])
			require.Equal(t, flags != nil, instance.Variables["need_escalate"])
			require.NoError(t, handler.Deliver(context.Background(), event))
			require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(context.Background()))
		})
	}
}

func TestGenericStartRejectsSnapshotMismatch(t *testing.T) {
	for _, mutation := range []string{"digest", "variables", "flags", "missing", "class"} {
		t.Run(mutation, func(t *testing.T) {
			f, event := genericStartFixture(t, nil)
			snapshot := f.client.IntakeResolutionSnapshot.Query().OnlyX(context.Background())
			switch mutation {
			case "digest":
				replaceGenericSnapshot(t, f, "wrong", snapshot.WorkflowVariables, snapshot.RecordClass)
			case "variables":
				replaceGenericSnapshot(t, f, snapshot.WorkflowDefinitionDigest, []byte("{}"), snapshot.RecordClass)
			case "flags":
				var payload workflowStartPayload
				require.NoError(t, json.Unmarshal(event.Payload, &payload))
				payload.Variables["need_escalate"] = true
				event.Payload, _ = json.Marshal(payload)
				raw, _ := json.Marshal(payload.Variables)
				replaceGenericSnapshot(t, f, snapshot.WorkflowDefinitionDigest, raw, snapshot.RecordClass)
			case "missing":
				f.client.IntakeResolutionSnapshot.DeleteOneID(snapshot.ID).ExecX(context.Background())
			case "class":
				replaceGenericSnapshot(t, f, snapshot.WorkflowDefinitionDigest, snapshot.WorkflowVariables, "incident")
			}
			require.Error(t, NewWorkflowStartOutboxHandler(f.client, f.engine, f.client).Deliver(context.Background(), event))
			require.Zero(t, f.client.ProcessInstance.Query().CountX(context.Background()))
		})
	}
}

// Recreate immutable fixture evidence instead of bypassing the snapshot schema.
func replaceGenericSnapshot(t *testing.T, f *bpmnAuthorizationFixture, digest string, vars []byte, class string) {
	t.Helper()
	ctx := context.Background()
	old := f.client.IntakeResolutionSnapshot.Query().OnlyX(ctx)
	f.client.IntakeResolutionSnapshot.DeleteOneID(old.ID).ExecX(ctx)
	f.client.IntakeResolutionSnapshot.Create().SetTenantID(old.TenantID).SetIntakeRequestID(old.IntakeRequestID).SetWorkItemID(old.WorkItemID).SetChannel(old.Channel).SetSourceProvider(old.SourceProvider).SetRecordClass(class).SetWorkflowDefinitionID(*old.WorkflowDefinitionID).SetWorkflowDefinitionKey(old.WorkflowDefinitionKey).SetWorkflowDefinitionVersion(old.WorkflowDefinitionVersion).SetWorkflowDefinitionDigest(digest).SetWorkflowVariables(vars).SetResolverVersion(old.ResolverVersion).SetRequestDigest(old.RequestDigest).SaveX(ctx)
}
