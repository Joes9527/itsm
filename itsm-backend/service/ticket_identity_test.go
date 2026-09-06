package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"testing"
)

func TestSearchTicketIdentityProjectionUsesRecordClass(t *testing.T) {
	service := &TicketService{}
	for _, tc := range []struct{ class, subtype, want string }{{"incident", "", "incident"}, {"change_request", "", "change"}, {"generic", "improvement", "improvement"}} {
		domain := service.entToDomain(&ent.Ticket{ID: 1, RecordClass: tc.class, GenericSubtype: tc.subtype})
		require.Equal(t, tc.want, ToTicketResponse(context.Background(), domain).Type)
	}
}
