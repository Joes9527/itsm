package intake

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/handlers/common/workitemcreation"
	"testing"
)

func TestBaseWriterSupportsPreparedClassesWithoutLifecycleDefaults(t *testing.T) {
	for _, class := range []string{"generic", "incident", "problem", "change_request", "service_request_item"} {
		t.Run(class, func(t *testing.T) {
			client, _, identity, command, allocator, creator := intakeFixture(t)
			ctx := context.Background()
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			authorized, err := authorization.AuthorizeWorkItemCreation(ctx, tx, tx.Client(), identity, command)
			require.NoError(t, err)
			identity = authorized.Identity()
			plan, err := creator.Prepare(ctx, tx, workitemcreation.ResolvedIntake{Identity: identity, Command: command, RecordClass: class})
			require.NoError(t, err)
			plan.WorkItem.Status = "prepared-domain-state"
			plan.WorkItem.Priority = "prepared-priority"
			item, err := NewWorkItemCreator(allocator).CreateBase(ctx, tx, plan, authorized)
			require.NoError(t, err)
			require.Equal(t, class, item.RecordClass)
			require.Equal(t, "prepared-domain-state", item.Status)
			require.Equal(t, "prepared-priority", item.Priority)
			require.Equal(t, "TKT-TEST-000001", item.TicketNumber)
			require.Equal(t, class, item.RecordClass)
		})
	}
}
