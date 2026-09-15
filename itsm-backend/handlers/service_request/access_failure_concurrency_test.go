package service_request_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/kaftaskactionledger"
	svc "itsm-backend/service"
)

type c3ClaimBarrierKey struct{}

func TestC3PreclaimedFailureCannotContributeAfterOriginal(t *testing.T) {
	for _, mode := range []string{"retry", "resume", "success"} {
		t.Run(mode, func(t *testing.T) {
			fx, task, _, req := verifiedAccessFixture(t)
			assertC3PreclaimedFailure(t, fx, task, req, mode)
		})
	}
}

func assertC3PreclaimedFailure(t *testing.T, fx *sslvpnDelegationFixture, task *ent.ProcessTask, req svc.KafActionRequest, mode string) {
	t.Helper()
	task = fx.client.ProcessTask.UpdateOne(task).SetTaskVariables(map[string]interface{}{"allowed_actions": "complete_bpmn_task,record_execution_failure"}).SaveX(fx.ctx)
	original := req
	original.Action = "record_execution_failure"
	original.Payload.AccessResult = nil
	original.Payload.FailureSummary = "access_result_unknown_manual_review_required"
	contender := original
	contender.Execution.RunID += "-contender"
	contender.Execution.IdempotencyKey = fmt.Sprintf("%d:%s:%s:%s", fx.tenant.ID, task.TaskID, contender.Execution.RunID, contender.Execution.StepID)
	contender.ExpectedVersion++
	blocked := original
	if mode != "retry" {
		blocked = contender
	}
	reached, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var paused atomic.Bool
	fx.client.ProcessInstance.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			if ctx.Value(c3ClaimBarrierKey{}) == true && !paused.Load() {
				// Inspect only: the ledger was produced by normal ExecuteAction/Claim.
				executing, err := fx.client.KafTaskActionLedger.Query().Where(kaftaskactionledger.IdempotencyKeyEQ(blocked.Execution.IdempotencyKey), kaftaskactionledger.ResultStatusEQ("executing")).Exist(ctx)
				if err != nil {
					return nil, err
				}
				if executing && paused.CompareAndSwap(false, true) {
					close(reached)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}
			return next.Query(ctx, query)
		})
	}))
	type outcome struct {
		result *svc.KafActionResult
		err    error
	}
	finished := make(chan outcome, 1)
	callCtx, cancel := context.WithTimeout(context.WithValue(fx.ctx, c3ClaimBarrierKey{}, true), 10*time.Second)
	defer cancel()
	go func() {
		result, err := fx.delegation.ExecuteAction(callCtx, task.TaskID, blocked, fx.engine)
		finished <- outcome{result, err}
	}()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("normal claim did not reach instance-read barrier")
	}
	if mode == "retry" {
		_, err := fx.delegation.ExecuteAction(fx.ctx, task.TaskID, contender, fx.engine)
		require.ErrorContains(t, err, "expected", "future-version contender must fail normally before original applies")
		row := fx.client.KafTaskActionLedger.Query().Where(kaftaskactionledger.IdempotencyKeyEQ(contender.Execution.IdempotencyKey)).OnlyX(fx.ctx)
		require.Equal(t, "failed_retryable", row.ResultStatus)
		unblock()
		done := <-finished
		require.NoError(t, done.err)
		require.Equal(t, svc.KafActionApplied, done.result.ResultStatus)
	} else {
		winner := original
		if mode == "success" {
			winner = req
		}
		applied, err := fx.delegation.ExecuteAction(fx.ctx, task.TaskID, winner, fx.engine)
		require.NoError(t, err)
		require.Equal(t, svc.KafActionApplied, applied.ResultStatus)
	}
	version := fx.client.ProcessInstance.GetX(fx.ctx, task.ProcessInstanceID).Version
	comments := fx.client.TicketComment.Query().CountX(fx.ctx)
	audits := fx.client.AuditLog.Query().CountX(fx.ctx)
	ledgers := fx.client.KafTaskActionLedger.Query().CountX(fx.ctx)
	if mode == "retry" {
		_, err := fx.delegation.ExecuteAction(fx.ctx, task.TaskID, contender, fx.engine)
		require.Error(t, err, "pre-created failed_retryable is not an applied original report")
	} else {
		unblock()
		done := <-finished
		require.Error(t, done.err, "preclaimed first invocation must not make a second contribution")
	}
	require.Equal(t, version, fx.client.ProcessInstance.GetX(fx.ctx, task.ProcessInstanceID).Version)
	require.Equal(t, comments, fx.client.TicketComment.Query().CountX(fx.ctx))
	require.Equal(t, audits, fx.client.AuditLog.Query().CountX(fx.ctx))
	require.Equal(t, ledgers, fx.client.KafTaskActionLedger.Query().CountX(fx.ctx))
	contenderLedger := fx.client.KafTaskActionLedger.Query().Where(kaftaskactionledger.IdempotencyKeyEQ(contender.Execution.IdempotencyKey)).OnlyX(fx.ctx)
	require.NotEqual(t, "applied", contenderLedger.ResultStatus)
	appliedFailures := fx.client.KafTaskActionLedger.Query().Where(kaftaskactionledger.ActionEQ("record_execution_failure"), kaftaskactionledger.ResultStatusEQ("applied")).CountX(fx.ctx)
	if mode == "success" {
		require.Zero(t, appliedFailures)
	} else {
		require.Equal(t, 1, appliedFailures)
	}
	winner := original
	if mode == "success" {
		winner = req
	}
	replay, err := fx.delegation.ExecuteAction(fx.ctx, task.TaskID, winner, fx.engine)
	require.NoError(t, err)
	require.Equal(t, svc.KafActionAlreadyApplied, replay.ResultStatus)
}
