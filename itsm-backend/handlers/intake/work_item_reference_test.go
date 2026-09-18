package intake

import (
	"context"
	"encoding/json"
	"testing"

	"itsm-backend/authorization"
	"itsm-backend/ent"
	changeowner "itsm-backend/handlers/change"
	problemowner "itsm-backend/handlers/problem"
	requestowner "itsm-backend/handlers/service_request"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
)

func TestReferenceURLUsesConfiguredFrontend(t *testing.T) {
	got, err := ticketReferenceURL("https://support.example.test", 37)
	require.NoError(t, err)
	require.Equal(t, "https://support.example.test/tickets/37", got)
	_, err = ticketReferenceURL("javascript:alert(1)", 37)
	require.Error(t, err)
}

func TestReferenceURLRejectsInvalidOriginAndIdentity(t *testing.T) {
	for _, origin := range []string{"", "//support.example.test", "ftp://support.example.test", "https:///missing", "https://user:password@support.example.test", "https://support.example.test?q=1", "https://support.example.test?", "https://support.example.test#secret", "https://support.example.test#", "https://support.example.test/subpath", "https://:443"} {
		t.Run(origin, func(t *testing.T) {
			_, err := ticketReferenceURL(origin, 37)
			require.Error(t, err)
		})
	}
	for _, id := range []int{0, -1} {
		_, err := ticketReferenceURL("https://support.example.test", id)
		require.Error(t, err)
	}
	got, err := ticketReferenceURL("http://support.example.test:3000/", 37)
	require.NoError(t, err)
	require.Equal(t, "http://support.example.test:3000/tickets/37", got)
}

func TestWorkItemReferenceExcludesDetails(t *testing.T) {
	item := &ent.Ticket{ID: 37, TicketNumber: "T-37", Status: "pending", Title: "private", Description: "secret", RequesterID: 9, TenantID: 2}
	got, err := projectWorkItemReference("https://support.example.test", item)
	require.NoError(t, err)
	raw, err := json.Marshal(WorkItemReferencePage{Items: []WorkItemReference{got}})
	require.NoError(t, err)
	require.JSONEq(t, `{"items":[{"workItemId":37,"number":"T-37","status":"pending","url":"https://support.example.test/tickets/37"}],"nextCursor":null}`, string(raw))
	_, err = projectWorkItemReference("https://support.example.test", nil)
	require.Error(t, err)
	_, err = projectWorkItemReference("invalid", item)
	require.Error(t, err)
}

func referenceLifecycleOwners() map[string]authorization.WorkItemLifecycleReader {
	return map[string]authorization.WorkItemLifecycleReader{
		"ticket": &service.TicketService{}, "incident": &service.IncidentService{},
		"problem": &problemowner.Service{}, "change": &changeowner.Service{}, "service_request": &requestowner.Service{},
	}
}

// These expectations come from each owner's transitions and completion rules.
// In particular Change failed still requires recovery; Problem has no cancelled
// state, and generic rejected can reopen while Requested Item rejected is done.
func TestRequesterLifecycleUsesProfessionalOwners(t *testing.T) {
	reader := NewRequesterLifecycleReader(referenceLifecycleOwners())
	for _, tc := range []struct {
		class                     string
		active, finished, invalid []string
	}{
		{"generic", []string{"new", "open", "assigned", "in_progress", "pending", "approved", "rejected"}, []string{"resolved", "closed", "cancelled"}, []string{"unknown", "", "completed"}},
		{"incident", []string{"new", "acknowledged", "assigned", "in_progress", "triaged", "escalated", "on_hold"}, []string{"resolved", "closed", "cancelled"}, []string{"unknown", "", "pending"}},
		{"problem", []string{"open", "investigating", "identified", "in_progress"}, []string{"resolved", "closed"}, []string{"unknown", "", "cancelled", "new"}},
		{"change_request", []string{"draft", "submitted", "approved", "scheduled", "in_progress", "failed"}, []string{"rejected", "completed", "cancelled", "rolled_back"}, []string{"unknown", "", "resolved", "closed"}},
		{"service_request_item", []string{"new", "open", "assigned", "pending", "in_progress"}, []string{"resolved", "closed", "cancelled", "rejected"}, []string{"unknown", "", "completed"}},
	} {
		t.Run(tc.class, func(t *testing.T) {
			for _, state := range tc.active {
				got, err := reader.IsUnfinished(context.Background(), nil, &ent.Ticket{RecordClass: tc.class, Status: state})
				require.NoError(t, err, state)
				require.True(t, got, state)
			}
			for _, state := range tc.finished {
				got, err := reader.IsUnfinished(context.Background(), nil, &ent.Ticket{RecordClass: tc.class, Status: state})
				require.NoError(t, err, state)
				require.False(t, got, state)
			}
			for _, state := range tc.invalid {
				_, err := reader.IsUnfinished(context.Background(), nil, &ent.Ticket{RecordClass: tc.class, Status: state})
				require.Error(t, err, state)
			}
		})
	}
}

func TestRequesterLifecycleFailsClosed(t *testing.T) {
	reader := NewRequesterLifecycleReader(referenceLifecycleOwners())
	for _, item := range []*ent.Ticket{nil, {RecordClass: "unknown", Status: "open"}, {RecordClass: "catalog_task", Status: "open"}} {
		_, err := reader.IsUnfinished(context.Background(), nil, item)
		require.Error(t, err)
	}
	for _, owner := range []authorization.WorkItemLifecycleReader{nil, (*service.TicketService)(nil)} {
		reader := NewRequesterLifecycleReader(map[string]authorization.WorkItemLifecycleReader{"ticket": owner})
		_, err := reader.IsUnfinished(context.Background(), nil, &ent.Ticket{RecordClass: "generic", Status: "open"})
		require.Error(t, err)
	}
	for _, owner := range referenceLifecycleOwners() {
		_, err := owner.IsUnfinished(context.Background(), nil, nil)
		require.Error(t, err)
		_, err = owner.IsUnfinished(context.Background(), nil, &ent.Ticket{RecordClass: "unknown", Status: "open"})
		require.Error(t, err)
	}
}
