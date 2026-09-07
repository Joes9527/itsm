package service_request_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/kaftaskcompletionreceipt"
	"itsm-backend/ent/processtask"
	"itsm-backend/handlers/common/accessgrant"
	sr "itsm-backend/handlers/service_request"
	svc "itsm-backend/service"
)

func verifiedAccessFixture(t *testing.T) (*sslvpnDelegationFixture, *ent.ProcessTask, int, svc.KafActionRequest) {
	t.Helper()
	fx := newSSLVPNDelegationFixture(t)
	security := fx.client.User.Create().SetTenantID(fx.tenant.ID).SetUsername("security").SetEmail("security@example.test").SetName("Security").SetPasswordHash("unused").SetRole("security_approver").SaveX(fx.ctx)
	deploySSLVPNDefinition(t, fx, "verified_access", fmt.Sprintf(sslvpnApprovalNodes, fx.approver.ID, security.ID), sslvpnApprovalFlows)
	request := createSSLVPNServiceRequestForDefinition(t, fx, "verified_access")
	instance := awaitSSLVPNInstance(t, fx, "service_request", request.TicketID)
	// Real ordered BPMN decisions from different assigned actors.
	require.NoError(t, completeSSLVPNApproval(t, fx, instance, "Approval_1"))
	assertNoSSLVPNDelegation(t, fx, instance)
	fx.approver = security
	require.NoError(t, completeSSLVPNApproval(t, fx, instance, "Approval_2"))
	task := assertOneSSLVPNDelegation(t, fx, instance)
	fx.client.ExternalIdentity.Create().SetTenantID(fx.tenant.ID).SetUserID(fx.requester.ID).SetProvider("graph").SetWorkspace("directory").SetSubject("approved-subject").SaveX(fx.ctx)
	policy := fx.client.CatalogAccessPolicy.Create().SetCatalogID(request.CatalogID).SetProvider("graph").SetExternalSystem("directory").SetGroupID("approved-group").SetDurationField("duration").SetDurationOptions([]accessgrant.DurationOption{{Key: "month", Label: "Month", Seconds: 2592000}}).SaveX(fx.ctx)
	fx.client.ServiceRequestAccessSnapshot.Create().SetWorkItemID(request.TicketID).SetPolicyID(policy.ID).SetPolicyVersion(1).SetProvider("graph").SetExternalSystem("directory").SetSubjectID("approved-subject").SetGroupID("approved-group").SetDurationKey("month").SetDurationSeconds(2592000).SaveX(fx.ctx)
	task = fx.client.ProcessTask.UpdateOne(task).SetCallbackAction(accessgrant.Capability).SetCallbackConfigRef(fmt.Sprint(policy.ID)).SaveX(fx.ctx)
	owner := sr.NewService(sr.NewEntRepository(fx.client), fx.client, zap.NewNop().Sugar(), nil)
	fx.delegation.SetApprovedAccessReader(owner)
	fx.engine.(*svc.CustomProcessEngine).SetAccessCompletionContributor(owner)
	projected, err := fx.delegation.GetTaskContext(fx.ctx, task.TaskID)
	require.NoError(t, err)
	require.Len(t, projected.ApprovedAccess.Approvals, 2)
	require.NotEqual(t, projected.ApprovedAccess.Approvals[0].ActorID, projected.ApprovedAccess.Approvals[1].ActorID)
	req := sslvpnCompletionRequest(fx, task.TaskID, projected, "verified-run", "finish")
	req.Payload.AccessResult = json.RawMessage(fmt.Sprintf(`{"outcome":"granted","provider":"graph","subjectId":"approved-subject","groupId":"approved-group","baseline":"not_member","verifiedAt":%q,"evidenceRef":%q}`, time.Now().UTC().Format(time.RFC3339Nano), req.Execution.IdempotencyKey))
	return fx, task, request.TicketID, req
}

func TestKafAccessCompletionAtomicAndImmutableReplay(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			fx, task, itemID, req := verifiedAccessFixture(t)
			if present {
				req.Payload.AccessResult = json.RawMessage(strings.ReplaceAll(strings.ReplaceAll(string(req.Payload.AccessResult), "granted", "already_present"), "not_member", "member"))
			}
			result, err := fx.delegation.ExecuteAction(fx.ctx, task.TaskID, req, fx.engine)
			require.NoError(t, err)
			require.Equal(t, svc.KafActionApplied, result.ResultStatus)
			saved := fx.client.ServiceRequestAccessResult.Query().OnlyX(fx.ctx)
			replayOwner := sr.NewService(sr.NewEntRepository(fx.client), fx.client, zap.NewNop().Sugar(), nil)
			require.NoError(t, replayOwner.ValidateAccessCompletionReplay(fx.ctx, fx.client, fx.client.ProcessTask.GetX(fx.ctx, task.ID), fx.client.KafTaskActionLedger.Query().OnlyX(fx.ctx)))
			require.Equal(t, task.ID, saved.ProcessTaskID)
			require.Equal(t, itemID, saved.WorkItemID)
			if present {
				require.Nil(t, saved.ExpiresAt)
			} else {
				require.Equal(t, saved.VerifiedAt.Add(30*24*time.Hour), *saved.ExpiresAt)
			}
			require.Equal(t, "resolved", fx.client.Ticket.GetX(fx.ctx, itemID).Status)
			require.Equal(t, "completed", fx.client.ProcessTask.GetX(fx.ctx, task.ID).Status)
			require.Equal(t, 1, fx.client.KafTaskCompletionReceipt.Query().Where(kaftaskcompletionreceipt.StatusEQ("callback_succeeded")).CountX(fx.ctx))
			require.Equal(t, 1, fx.client.AuditLog.Query().Where(auditlog.ActionEQ("service_request.access_verified")).CountX(fx.ctx))
			replay, err := fx.delegation.ExecuteAction(fx.ctx, task.TaskID, req, fx.engine)
			require.NoError(t, err)
			require.Equal(t, svc.KafActionAlreadyApplied, replay.ResultStatus)
			require.Equal(t, saved.VerifiedAt, fx.client.ServiceRequestAccessResult.Query().OnlyX(fx.ctx).VerifiedAt)
			// Canonical object ordering replays, while a changed first verification
			// or expected version conflicts with the immutable action digest.
			var canonical map[string]interface{}
			require.NoError(t, json.Unmarshal(req.Payload.AccessResult, &canonical))
			req.Payload.AccessResult, _ = json.Marshal(canonical)
			_, err = fx.delegation.ExecuteAction(fx.ctx, task.TaskID, req, fx.engine)
			require.NoError(t, err)
			for _, changed := range []string{"time", "version"} {
				mismatch := req
				if changed == "version" {
					mismatch.ExpectedVersion++
				} else {
					canonical["verifiedAt"] = time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
					mismatch.Payload.AccessResult, _ = json.Marshal(canonical)
				}
				_, err = fx.delegation.ExecuteAction(fx.ctx, task.TaskID, mismatch, fx.engine)
				require.ErrorIs(t, err, svc.ErrKafActionConflict)
			}
			req.Payload.AccessResult = json.RawMessage(strings.Replace(string(req.Payload.AccessResult), "approved-group", "different-group", 1))
			_, err = fx.delegation.ExecuteAction(fx.ctx, task.TaskID, req, fx.engine)
			require.ErrorIs(t, err, svc.ErrKafActionConflict)
		})
	}
}

func TestKafAccessCompletionRejectsUnverifiedOrUnboundReceipt(t *testing.T) {
	for _, replacement := range []string{"missing", "subject", "group", "evidence", "future", "closed"} {
		t.Run(replacement, func(t *testing.T) {
			fx, task, itemID, req := verifiedAccessFixture(t)
			switch replacement {
			case "missing":
				req.Payload.AccessResult = nil
			case "closed":
				fx.client.Ticket.UpdateOneID(itemID).SetStatus("closed").SaveX(fx.ctx)
			case "subject":
				req.Payload.AccessResult = json.RawMessage(strings.Replace(string(req.Payload.AccessResult), "approved-subject", "other-subject", 1))
			case "group":
				req.Payload.AccessResult = json.RawMessage(strings.Replace(string(req.Payload.AccessResult), "approved-group", "other-group", 1))
			case "evidence":
				req.Payload.AccessResult = json.RawMessage(strings.Replace(string(req.Payload.AccessResult), req.Execution.IdempotencyKey, "other-task", 1))
			case "future":
				var raw map[string]interface{}
				require.NoError(t, json.Unmarshal(req.Payload.AccessResult, &raw))
				raw["verifiedAt"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
				req.Payload.AccessResult, _ = json.Marshal(raw)
			}
			_, err := fx.delegation.ExecuteAction(fx.ctx, task.TaskID, req, fx.engine)
			require.Error(t, err)
			require.Zero(t, fx.client.ServiceRequestAccessResult.Query().CountX(fx.ctx))
			require.NotEqual(t, "resolved", fx.client.Ticket.GetX(fx.ctx, itemID).Status)
			require.Equal(t, "delegated", fx.client.ProcessTask.GetX(fx.ctx, task.ID).Status)
			require.Zero(t, fx.client.KafTaskCompletionReceipt.Query().CountX(fx.ctx))
		})
	}
}

func TestKafAccessCompletionAuditFailureRollsBackAllEffects(t *testing.T) {
	fx, task, itemID, req := verifiedAccessFixture(t)
	fx.client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if audit, ok := mutation.(*ent.AuditLogMutation); ok {
				if action, _ := audit.Action(); action == "service_request.access_verified" {
					return nil, fmt.Errorf("injected result audit failure")
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})
	_, err := fx.delegation.ExecuteAction(fx.ctx, task.TaskID, req, fx.engine)
	require.ErrorContains(t, err, "injected result audit failure")
	require.Zero(t, fx.client.ServiceRequestAccessResult.Query().CountX(fx.ctx))
	require.Equal(t, "delegated", fx.client.ProcessTask.Query().Where(processtask.IDEQ(task.ID)).OnlyX(fx.ctx).Status)
	require.NotEqual(t, "resolved", fx.client.Ticket.GetX(fx.ctx, itemID).Status)
	require.Zero(t, fx.client.KafTaskCompletionReceipt.Query().CountX(fx.ctx))
}

func TestKafAccessLegacyCompletedTaskCannotInventVerifiedResult(t *testing.T) {
	fx, task, _, req := verifiedAccessFixture(t)
	ledger, claimed, err := fx.delegation.ClaimKafAction(fx.ctx, task, req)
	require.NoError(t, err)
	require.True(t, claimed)
	fx.client.ProcessTask.UpdateOne(task).SetStatus("completed").SaveX(fx.ctx)
	err = fx.engine.(*svc.CustomProcessEngine).CompleteKafDelegatedTask(fx.ctx, ledger.ID, ledger.LeaseOwner, task.TaskID, map[string]interface{}{})
	require.ErrorContains(t, err, "verified access replay evidence unavailable")
	require.Zero(t, fx.client.ServiceRequestAccessResult.Query().CountX(fx.ctx))
	require.Zero(t, fx.client.KafTaskCompletionReceipt.Query().CountX(fx.ctx))
}

func TestKafAccessManualProvisioningUsesDomainOwner(t *testing.T) {
	fx, _, itemID, _ := verifiedAccessFixture(t)
	owner := sr.NewService(sr.NewEntRepository(fx.client), fx.client, zap.NewNop().Sugar(), nil)
	manual := svc.NewProvisioningService(fx.client, zap.NewNop().Sugar())
	manual.SetManualProvisioningGuard(owner)
	request := fx.client.ServiceRequest.Query().OnlyX(fx.ctx)
	_, err := manual.CreateTaskFromServiceRequest(fx.ctx, request.ID, fx.tenant.ID, fx.approver.ID, "security_approver")
	require.ErrorContains(t, err, "managed_access_requires_verified_delegation")
	require.Zero(t, fx.client.ProvisioningTask.Query().CountX(fx.ctx))
	task := fx.client.ProvisioningTask.Create().SetTenantID(fx.tenant.ID).SetServiceRequestID(request.ID).SetProvider("alicloud").SetResourceType("ecs").SetStatus("pending").SaveX(fx.ctx)
	_, err = manual.ExecuteTask(fx.ctx, task.ID, fx.tenant.ID, fx.approver.ID, "security_approver")
	require.ErrorContains(t, err, "managed_access_requires_verified_delegation")
	require.Equal(t, "pending", fx.client.ProvisioningTask.GetX(fx.ctx, task.ID).Status)
	// A legacy missing snapshot cannot evade its current catalog policy either.
	fx.client.ServiceRequestAccessSnapshot.Delete().ExecX(fx.ctx)
	require.ErrorContains(t, owner.ValidateManualProvisioning(fx.ctx, fx.client, fx.tenant.ID, itemID), "access_snapshot_unavailable")
}
