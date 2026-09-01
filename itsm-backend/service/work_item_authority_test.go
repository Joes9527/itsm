package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

func requireIncidentWorkItem(t *testing.T, client *ent.Client, entity *ent.Incident) *ent.Ticket {
	t.Helper()
	workItem, err := client.Ticket.Get(context.Background(), entity.WorkItemID)
	require.NoError(t, err)
	return workItem
}

func requireChangeWorkItem(t *testing.T, client *ent.Client, entity *ent.Change) *ent.Ticket {
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
