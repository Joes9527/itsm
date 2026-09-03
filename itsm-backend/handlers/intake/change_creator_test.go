package intake

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChangeCreatorCreatesRealChangeExtension(t *testing.T) {
	client, tenant, requester := newCreatorFixture(t)
	ctx := context.Background()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	creator := NewChangeCreator()
	plan, err := creator.Prepare(ctx, tx, ResolvedIntake{
		Identity: Identity{TenantID: tenant.ID, ActorID: requester.ID, RequesterID: requester.ID},
		Command: CreateWorkItemCommand{
			Title:  "Upgrade router firmware",
			Change: &ChangeInput{Type: "normal", RiskLevel: "medium", ImpactScope: "low"},
		},
		RecordClass: RecordClassChangeRequest,
	})
	require.NoError(t, err)
	assert.Empty(t, plan.WorkItem.TicketNumber)

	workItemCreator := NewWorkItemCreator(&stubTicketAllocator{number: "TKT-202609-000099"})
	workItem, err := workItemCreator.CreateBase(ctx, tx, plan)
	require.NoError(t, err)
	assert.Equal(t, RecordClassChangeRequest, workItem.RecordClass)
	assert.Equal(t, "change", workItem.Type)

	ref, err := creator.CreateExtension(ctx, tx, workItem, plan)
	require.NoError(t, err)
	assert.Equal(t, "change", ref.Type)

	saved, err := tx.Change.Get(ctx, ref.ID)
	require.NoError(t, err)
	assert.Equal(t, "medium", saved.RiskLevel)
	assert.Equal(t, workItem.ID, saved.WorkItemID)
}
