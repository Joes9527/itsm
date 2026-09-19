package service

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenericV2Template(t *testing.T) {
	old, err := os.ReadFile("bpmn/ticket_general_flow.bpmn")
	require.NoError(t, err)
	require.Equal(t, "124a7faf660f23e82ba89c0302e900171cbd64ca932df091514f2adbcb5cc2d3", fmt.Sprintf("%x", sha256.Sum256(old)))
	xml, err := os.ReadFile("bpmn/ticket_general_flow_v2.bpmn")
	require.NoError(t, err)
	defs, err := NewBPMNParser().ParseXML(xml)
	require.NoError(t, err)
	require.Len(t, defs.Processes, 1)
	p := defs.Processes[0]
	require.Equal(t, "ticket_general_flow_v2", p.ID)
	contract, err := p.WorkItemLifecycleContract()
	require.NoError(t, err)
	require.Equal(t, GenericFulfillmentV1, contract)
	required := map[string]WorkItemPrerequisite{"Activity_Assign": WorkItemPrerequisiteAssigned, "Activity_Handle": WorkItemPrerequisiteInProgress, "Activity_Escalate": WorkItemPrerequisiteEscalated, "Activity_Resolve": WorkItemPrerequisiteResolved, "Activity_Close": WorkItemPrerequisiteClosed}
	for id, expected := range required {
		task := genericWorkflowTaskNode(p, id)
		require.NotNil(t, task)
		actual, err := task.WorkItemPrerequisite()
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
	require.NotContains(t, string(xml), "Activity_NotifyRequester")
	require.NotContains(t, string(xml), "service_task_type")
	require.NotContains(t, string(xml), "<bpmn:body>")
	require.NotContains(t, string(xml), "${")
	f := newBPMNAuthorizationFixture(t)
	conditions := 0
	for _, flow := range p.SequenceFlows {
		if flow.ID == "Flow_Reject" {
			require.Equal(t, "EndEvent_Rejected", flow.TargetRef)
		}
		if flow.ConditionExpression != nil {
			conditions++
			require.NotEmpty(t, strings.TrimSpace(flow.ConditionExpression.Expression))
			for _, enabled := range []bool{false, true} {
				variables := map[string]interface{}{"approval_required": enabled, "need_escalate": enabled, "approvalResult": "rejected"}
				if enabled {
					variables["approvalResult"] = "approved"
				}
				got, err := f.engine.evaluateCondition(flow, variables)
				require.NoError(t, err)
				positive := flow.ID == "Flow_ApprovalYes" || flow.ID == "Flow_Approved" || flow.ID == "Flow_4"
				require.Equal(t, enabled == positive, got, flow.ID)
			}
		}
	}
	require.Equal(t, 6, conditions)
}
