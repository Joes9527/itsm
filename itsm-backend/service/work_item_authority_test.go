package service

import (
	"context"
	"testing"

	"itsm-backend/ent"

	"github.com/stretchr/testify/require"
)

func requireIncidentWorkItem(t *testing.T, client *ent.Client, entity *ent.Incident) *ent.Ticket {
	t.Helper()
	workItem, err := client.Ticket.Get(context.Background(), entity.WorkItemID)
	require.NoError(t, err)
	return workItem
}

func incidentEntityWithStatus(status string, workItemIDs ...int) *ent.Incident {
	entity := &ent.Incident{Edges: ent.IncidentEdges{WorkItem: &ent.Ticket{Status: status}}}
	if len(workItemIDs) > 0 {
		entity.WorkItemID = workItemIDs[0]
	}
	return entity
}
