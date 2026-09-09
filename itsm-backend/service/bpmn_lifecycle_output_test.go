package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service/bpmn"
	"testing"
)

func TestChangeLifecycleOutputUsesCanonicalClass(t *testing.T) {
	h := bpmn.NewChangeServiceTaskHandler(nil, zap.NewNop().Sugar())
	row := &ent.ProcessCallbackOutbox{Action: "assess_risk", Variables: map[string]interface{}{"version": 2}}
	effect := &bpmn.CallbackEffect{Status: bpmn.CallbackEffectApplied, LifecycleResult: &workitemmutation.Result{WorkItemID: 7, Version: 3, Status: "submitted"}}
	for _, kind := range []string{"change", "change_request"} {
		out, err := callbackContinuationOutputs(h, row, &ent.ProcessInstance{BusinessType: kind, BusinessID: 7}, effect)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"version": 3, "status": "submitted"}, out)
	}
	for _, kind := range []string{"incident", "unknown", ""} {
		_, err := callbackContinuationOutputs(h, row, &ent.ProcessInstance{BusinessType: kind, BusinessID: 7}, effect)
		require.Error(t, err)
	}
	effect.OutputVars = map[string]interface{}{"untrusted": true}
	_, err := callbackContinuationOutputs(h, row, &ent.ProcessInstance{BusinessType: "change", BusinessID: 7}, effect)
	require.Error(t, err)
}
func TestLifecycleOutputRequiresActualTenantSourceClass(t *testing.T) {
	for _, mismatch := range []string{"class", "tenant"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			class := "incident"
			if mismatch == "tenant" {
				class = "change_request"
			}
			item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetTitle("class guard").SetTicketNumber("OUTPUT-CLASS").SetRecordClass(class).SetStatus("new").SaveX(f.userCtx)
			row := &ent.ProcessCallbackOutbox{TenantID: f.tenant.ID, Action: "assess_risk", Variables: map[string]interface{}{"version": 1}}
			if mismatch == "tenant" {

				row.TenantID = f.tenant.ID + 999
			}
			instance := &ent.ProcessInstance{BusinessType: "change", BusinessID: item.ID, TenantID: row.TenantID}
			effect := &bpmn.CallbackEffect{Status: bpmn.CallbackEffectApplied, LifecycleResult: &workitemmutation.Result{WorkItemID: item.ID, Version: 2, Status: "submitted"}}
			row.ActorID = f.actor.ID
			row.ExecutionKey = "class-guard"
			receiptTx, err := f.client.Tx(f.userCtx)
			require.NoError(t, err)
			require.NoError(t, workitemmutation.RecordTx(f.userCtx, receiptTx, workitemmutation.Meta{TenantID: row.TenantID, ActorID: f.actor.ID, OperationID: row.ExecutionKey}, *effect.LifecycleResult, "change.assess", "test-digest", nil))
			require.NoError(t, receiptTx.Commit())
			tx, err := f.client.Tx(context.Background())
			require.NoError(t, err)
			defer tx.Rollback()
			err = persistCallbackOutputs(f.userCtx, tx, bpmn.NewChangeServiceTaskHandler(nil, zap.NewNop().Sugar()), row, instance, effect)
			require.Error(t, err)
			var callbackErr *bpmnCallbackExecutionError
			require.True(t, errors.As(err, &callbackErr))
			require.Equal(t, "handler_error", callbackErr.errorClass, "source class/tenant must fail before continuation CAS")
		})
	}
}
