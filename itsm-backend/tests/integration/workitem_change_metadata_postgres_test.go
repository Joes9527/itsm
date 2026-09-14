//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	changedomain "itsm-backend/handlers/change"
)

func TestWorkItemChangeMetadataOwner(t *testing.T) {
	for _, mode := range []string{"write", "stale", "terminal", "governed", "rollback", "replay", "tenant", "revoked", "empty", "unchanged", "invalid_enum"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			title := "Revised title"
			plan := "Changed deployment plan"
			cmd := changedomain.MetadataCommand{Meta: f.command("metadata", "metadata").Meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{Title: &title}}
			item := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			switch mode {
			case "invalid_enum":
				invalid := dto.ChangeType("not-a-change-type")
				cmd.Patch.Type = &invalid
			case "empty":
				cmd.Patch = dto.UpdateChangeRequest{}
			case "unchanged":
				title = item.Title
			case "stale":
				cmd.Meta.ExpectedVersion++
			case "terminal":
				item.Update().SetStatus("completed").ExecX(f.ctx)
			case "governed":
				item.Update().SetStatus("approved").ExecX(f.ctx)
				cmd.Patch.ImplementationPlan = &plan
			case "tenant":
				cmd.Meta.TenantID++
			case "revoked":
				f.actor.Update().SetActive(false).ExecX(f.ctx)
			case "rollback":
				f.runtime.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if _, ok := m.(*ent.AuditLogMutation); ok {
							return nil, errors.New("metadata audit unavailable")
						}
						return next.Mutate(ctx, m)
					})
				})
			}
			result, err := f.owner.ApplyMetadata(f.ctx, cmd)
			if mode != "write" && mode != "replay" {
				require.Error(t, err)
				if mode == "empty" || mode == "unchanged" {
					require.ErrorContains(t, err, "new metadata facts")
				}
				require.Equal(t, item.Title, f.client.Ticket.GetX(f.ctx, item.ID).Title)
				require.Equal(t, item.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
				return
			}
			require.NoError(t, err)
			require.Equal(t, item.Version+1, result.Version)
			stored := f.client.Ticket.GetX(f.ctx, item.ID)
			require.Equal(t, title, stored.Title)
			require.Equal(t, item.Status, stored.Status)
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
			if mode == "replay" {
				again, err := f.owner.ApplyMetadata(f.ctx, cmd)
				require.NoError(t, err)
				require.True(t, again.Replayed)
				require.Equal(t, result.Version, again.Version)
				changed := "Other"
				cmd.Patch.Title = &changed
				_, err = f.owner.ApplyMetadata(f.ctx, cmd)
				require.Error(t, err)
				require.Equal(t, result.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
			}
		})
	}
}

func TestWorkItemChangeMetadataConcurrentReceipt(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			title := "Concurrent title"
			cmd := changedomain.MetadataCommand{Meta: f.command("metadata", "concurrent").Meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{Title: &title}}
			synchronizeChangeOwnerReads(t, f)
			start := make(chan struct{})
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for i := range 2 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					copy := cmd
					if !same {
						copy.Meta.OperationID += fmt.Sprint(i)
					}
					_, err := f.owner.ApplyMetadata(f.ctx, copy)
					results <- err
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				}
			}
			if same {
				require.Equal(t, 2, success, "same-key contender resolves committed receipt")
			} else {
				require.Equal(t, 1, success)
			}
			require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
		})
	}
}

// Both contenders must establish their RR read snapshot before either writes.
func synchronizeChangeOwnerReads(t *testing.T, f *changeLifecycleFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	t.Cleanup(cancel)
	f.ctx = ctx
	ready := make(chan struct{})
	var arrived atomic.Int32
	f.runtime.Change.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, q)
			if err != nil {
				return value, err
			}
			if _, ok := value.([]*ent.Change); ok {
				n := arrived.Add(1)
				if n == 2 {
					close(ready)
				}
				if n <= 2 {
					select {
					case <-ready:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}
			return value, nil
		})
	}))
}
