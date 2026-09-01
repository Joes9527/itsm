package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	feishuConn "itsm-backend/connector/builtin/feishu"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/feishuticketsync"
	"itsm-backend/ent/ticket"
	"itsm-backend/repository/workitemnumber"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestFeishuSyncService_UsesWorkItemNumberAllocator(t *testing.T) {
	testName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", testName))
	defer client.Close()

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Feishu Number Tenant").
		SetCode("feishu-number").
		SetDomain("feishu-number.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.User.Create().
		SetUsername("feishu-requester").
		SetEmail("feishu-requester@example.com").
		SetName("Feishu Requester").
		SetPasswordHash("hash").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	fixedIssuedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	svc := NewFeishuSyncService(client, zaptest.NewLogger(t).Sugar(), fixedIssuedAtAllocator{
		issuedAt: fixedIssuedAt,
		delegate: workitemnumber.NewPostgreSQLAllocator(),
	})
	client.FeishuTicketSync.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if syncMutation, ok := mutation.(*ent.FeishuTicketSyncMutation); ok && mutation.Op().Is(ent.OpCreate) {
				if taskID, exists := syncMutation.FeishuTaskID(); exists && taskID == "feishu-task-fail" {
					return nil, fmt.Errorf("injected feishu sync mapping failure")
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})

	_, _, err = svc.SyncFeishuTaskToTicket(ctx, tenant.ID, &feishuConn.FeishuTask{
		GUID: "feishu-task-fail",
		Name: "Feishu task with failed mapping",
	})
	require.ErrorContains(t, err, "injected feishu sync mapping failure")
	ticketCount, err := client.Ticket.Query().Where(ticket.TenantID(tenant.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, ticketCount, "mapping failure must roll back the WorkItem")
	sequenceCount, err := client.WorkItemNumberSequence.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, sequenceCount, "mapping failure must roll back the allocated number")

	first, action, err := svc.SyncFeishuTaskToTicket(ctx, tenant.ID, &feishuConn.FeishuTask{
		GUID: "feishu-task-1",
		Name: "First Feishu task",
	})
	require.NoError(t, err)
	require.Equal(t, "created", action)
	second, action, err := svc.SyncFeishuTaskToTicket(ctx, tenant.ID, &feishuConn.FeishuTask{
		GUID: "feishu-task-2",
		Name: "Second Feishu task",
	})
	require.NoError(t, err)
	require.Equal(t, "created", action)

	require.Equal(t, "TKT-202609-000001", first.TicketNumber)
	require.Equal(t, "TKT-202609-000002", second.TicketNumber)

	tickets, err := client.Ticket.Query().
		Where(ticket.TenantID(tenant.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, tickets, 2)
	for _, synced := range []struct {
		ticketID int
		taskGUID string
	}{
		{ticketID: first.TicketID, taskGUID: "feishu-task-1"},
		{ticketID: second.TicketID, taskGUID: "feishu-task-2"},
	} {
		record, err := client.FeishuTicketSync.Query().
			Where(
				feishuticketsync.TenantID(tenant.ID),
				feishuticketsync.TicketID(synced.ticketID),
				feishuticketsync.FeishuTaskID(synced.taskGUID),
			).
			Only(ctx)
		require.NoError(t, err)
		require.Equal(t, "synced", record.SyncStatus)
		require.Equal(t, "feishu_to_itsm", record.LastSyncDirection)
	}
}

type fixedIssuedAtAllocator struct {
	issuedAt time.Time
	delegate workitemnumber.Allocator
}

func (a fixedIssuedAtAllocator) Allocate(
	ctx context.Context,
	client *ent.Client,
	tenantID int,
	_ time.Time,
) (string, error) {
	return a.delegate.Allocate(ctx, client, tenantID, a.issuedAt)
}
