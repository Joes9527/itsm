package service

import (
	"testing"

	"itsm-backend/ent/processauditlog"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestBPMNCallbackAuditStoresOnlyAllowlistedCallbackMetadata(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "callback-audit")
	row := f.client.ProcessCallbackOutbox.Create().
		SetExecutionKey("callback-audit-key").
		SetTenantID(f.tenant.ID).
		SetProcessInstanceID(task.ProcessInstanceID).
		SetProcessTaskID(task.ID).
		SetTaskID(task.TaskID).
		SetCallbackKind("service_task").
		SetHandlerID("callback_audit_handler").
		SetTaskType("callback_audit_task").
		SetElementID(task.TaskDefinitionKey).
		SetAction("notify").
		SetConfigRef("connector-secret-ref").
		SetVariables(map[string]interface{}{"secret": "must-not-be-audited"}).
		SaveX(f.userCtx)

	audit := NewBPMNAuditService(f.client, zaptest.NewLogger(t).Sugar())
	require.NoError(t, audit.RecordCallbackBlocked(f.userCtx, row, bpmn.CallbackBlockTargetMissing))
	require.Error(t, audit.RecordCallbackSkippedOptional(f.userCtx, row, bpmn.CallbackBlockTargetMissing))
	optionalRow := f.client.ProcessCallbackOutbox.UpdateOne(row).SetOptionalDeclared(true).SaveX(f.userCtx)
	require.NoError(t, audit.RecordCallbackSkippedOptional(f.userCtx, optionalRow, bpmn.CallbackBlockTargetMissing))

	logs := f.client.ProcessAuditLog.Query().
		Where(processauditlog.ProcessInstanceID(task.ProcessInstanceID)).
		Order(processauditlog.ByAction()).
		AllX(f.userCtx)
	require.Len(t, logs, 2)
	for _, log := range logs {
		assert.Contains(t, []string{bpmn.CallbackAuditActionBlocked, bpmn.CallbackAuditActionSkippedOptional}, log.Action)
		assert.Empty(t, log.Comment)
		assert.Empty(t, log.VariablesBefore)
		assert.Empty(t, log.VariablesAfter)
		assert.Equal(t, "callback_audit_handler", log.Metadata["handler_id"])
		assert.Equal(t, "notify", log.Metadata["action"])
		assert.Equal(t, string(bpmn.CallbackBlockTargetMissing), log.Metadata["block_code"])
		assert.Equal(t, log.Action == bpmn.CallbackAuditActionSkippedOptional, log.Metadata["optional_declared"])
		assert.NotContains(t, log.Metadata, "payload")
		assert.NotContains(t, log.Metadata, "config_ref")
		assert.NotContains(t, log.Metadata, "error")
	}
}
