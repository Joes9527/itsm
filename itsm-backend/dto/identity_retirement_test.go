package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

func TestIncidentNumberProjectsOwningWorkItem(t *testing.T) {
	response := ToIncidentResponse(&ent.Incident{ID: 9}, &ent.Ticket{ID: 17, TicketNumber: "TKT-owned-17"})
	require.Equal(t, "TKT-owned-17", response.IncidentNumber)
	require.Nil(t, ToIncidentResponse(&ent.Incident{ID: 9}, nil))
}

func TestIncidentPublicIdentityPreservesProfessionalAndWorkItemIDs(t *testing.T) {
	response := ToIncidentResponse(&ent.Incident{ID: 4, WorkItemID: 91}, &ent.Ticket{ID: 91, TicketNumber: "TKT-0091", Version: 7})
	require.Equal(t, 4, response.ID)
	require.NotNil(t, response.WorkItemID)
	require.Equal(t, 91, *response.WorkItemID)
	require.Equal(t, "TKT-0091", response.Number)
	require.Equal(t, 7, response.Version)
}
