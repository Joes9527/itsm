package service

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

func TestVoteRetriesOnlyConfirmedRolledBackSerializationFailures(t *testing.T) {
	for _, mode := range []string{"recover", "bounded", "ordinary", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			instance := f.createProcessInstance(t, f.tenant, "retry-vote")
			task := f.createProcessTask(t, instance, f.tenant.ID, "retry-vote", strconv.Itoa(f.actor.ID), "", "")
			task.Update().SetStatus("assigned").ExecX(f.userCtx)
			attempts := 0
			ordinary := errors.New("audit unavailable")
			ctx, cancel := context.WithCancel(f.scopedCtx(false, false, false, false))
			defer cancel()
			f.client.ProcessAuditLog.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					attempts++
					if mode == "ordinary" {
						return nil, ordinary
					}
					if mode == "cancelled" {
						cancel()
					}
					if mode != "recover" || attempts <= 2 {
						return nil, &pq.Error{Code: "40001", Message: "injected serialization failure"}
					}
					return next.Mutate(ctx, m)
				})
			})
			err := f.engine.TaskService().Vote(ctx, task.TaskID, &VoteRequest{Approved: true, Comment: "reviewed"})
			if mode == "recover" {
				require.NoError(t, err)
				require.Equal(t, 3, attempts)
				require.Equal(t, "completed", f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
				require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.userCtx))
				return
			}
			require.Error(t, err)
			if mode == "bounded" {
				require.Equal(t, 50, attempts)
			} else {
				require.Equal(t, 1, attempts)
			}
			if mode == "ordinary" {
				require.ErrorIs(t, err, ordinary)
			}
			require.Equal(t, "assigned", f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
			require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.userCtx))
			require.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
		})
	}
}
