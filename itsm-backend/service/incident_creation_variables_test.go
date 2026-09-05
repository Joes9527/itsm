package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func TestIncidentPreparedWorkflowCategoryUsesResolvedClassification(t *testing.T) {
	svc := &IncidentService{}
	in := creation.ResolvedIntake{RecordClass: creation.RecordClassIncident, Identity: creation.Identity{TenantID: 1, ActorID: 2, RequesterID: 3}, Command: creation.CreateWorkItemCommand{Title: "Connectivity", Priority: "high", Incident: &creation.IncidentInput{Category: "unresolved input", Severity: "critical"}}, CTI: creation.ResolvedCTI{CategoryName: "Resolved network category"}}
	plan, err := svc.Prepare(context.Background(), nil, in)
	require.NoError(t, err)
	require.Equal(t, "Resolved network category", plan.WorkflowVariables["category"])
	require.Equal(t, "critical", plan.WorkflowVariables["severity"])
	require.Equal(t, 3, plan.WorkflowVariables["reporter_id"])
}
