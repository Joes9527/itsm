package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

func TestBPMNBoundTaskUsesStructuredBusinessIdentity(t *testing.T) {
	response := ToBPMNTaskResponse(&ent.ProcessTask{AssigneeSource: "work_item_assignee"}, &ent.ProcessInstance{
		BusinessType: "service_request", BusinessID: 41, BusinessKey: "incident:99",
	})
	require.Equal(t, "service_request", response.BusinessType)
	require.Equal(t, 41, response.BusinessID)
	require.Equal(t, "work_item_assignee", response.AssigneeSource)
}
