package dto

import (
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"testing"
)

func TestIncidentNumberProjectsOwningWorkItem(t *testing.T) {
	response := ToIncidentResponse(&ent.Incident{ID: 9}, &ent.Ticket{ID: 17, TicketNumber: "TKT-owned-17"})
	require.Equal(t, "TKT-owned-17", response.IncidentNumber)
	require.Nil(t, ToIncidentResponse(&ent.Incident{ID: 9}, nil))
}
