package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntakeSourceRelationOwnsVersion(t *testing.T) {
	ctx := context.Background()
	f := newUnifiedIntakeFixture(t)
	source := f.client.Ticket.Create().SetTenantID(f.identity.TenantID).SetRequesterID(f.identity.ActorID).SetTitle("source").SetTicketNumber("REL-INTAKE-SOURCE").SetRecordClass("incident").SetStatus("new").SetPriority("high").SaveX(ctx)
	f.client.Incident.Create().SetWorkItemID(source.ID).SetSeverity("high").SetImpact("high").SetUrgency("high").SaveX(ctx)
	command := f.command
	command.RecordClass, command.IntakeKind, command.Title = "problem", "problem", "investigation"
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"sourceRelations":[{"sourceWorkItemId":%d,"relationType":"investigated_by","expectedVersion":%d}]}`, source.ID, source.Version)), &command))
	result, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)
	require.Equal(t, source.Version+1, f.client.Ticket.GetX(ctx, source.ID).Version)
	require.Equal(t, 1, f.client.WorkItemRelation.Query().CountX(ctx))
	replay, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, result.WorkItemID, replay.WorkItemID)
	require.Equal(t, source.Version+1, f.client.Ticket.GetX(ctx, source.ID).Version)
}
