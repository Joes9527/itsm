package service

import (
	"context"
	"fmt"
	"testing"

	entschema "itsm-backend/ent/schema"

	"github.com/stretchr/testify/require"
)

func TestBPMNAssignmentSourceSchemaIsImmutable(t *testing.T) {
	for _, candidate := range (entschema.ProcessTask{}).Fields() {
		descriptor := candidate.Descriptor()
		if descriptor.Name == "assignee_source" {
			require.True(t, descriptor.Immutable, "assignment source must not have generated update setters")
			return
		}
	}
	require.Fail(t, "assignee_source field is missing")
}

func TestBPMNAssignmentSourceValidation(t *testing.T) {
	valid := &BPMNUserTask{TaskPurpose: "fulfillment", AssigneeSource: BPMNAssigneeSourceWorkItem}
	require.NoError(t, validateBPMNAssigneeSource(valid))
	require.NoError(t, validateBPMNAssigneeSource(&BPMNUserTask{}), "empty source preserves legacy routing")

	cases := map[string]func(*BPMNUserTask){
		"unknown source":             func(task *BPMNUserTask) { task.AssigneeSource = "unknown" },
		"expression source":          func(task *BPMNUserTask) { task.AssigneeSource = "${assignee_id}" },
		"whitespace padded source":   func(task *BPMNUserTask) { task.AssigneeSource = " work_item_assignee " },
		"non fulfillment purpose":    func(task *BPMNUserTask) { task.TaskPurpose = "approval" },
		"explicit assignee":          func(task *BPMNUserTask) { task.Assignee = "operator" },
		"candidate users":            func(task *BPMNUserTask) { task.CandidateUsers = "operator" },
		"candidate groups":           func(task *BPMNUserTask) { task.CandidateGroups = "helpdesk" },
		"approval mode":              func(task *BPMNUserTask) { task.ApprovalMode = "single" },
		"approval threshold":         func(task *BPMNUserTask) { task.ApprovalThreshold = 1 },
		"reject strategy":            func(task *BPMNUserTask) { task.RejectStrategy = "stop" },
		"timeout action":             func(task *BPMNUserTask) { task.TimeoutAction = "reject" },
		"delegate approval":          func(task *BPMNUserTask) { task.AllowDelegate = true },
		"add approver":               func(task *BPMNUserTask) { task.AllowAddApprover = true },
		"reject comment requirement": func(task *BPMNUserTask) { task.CommentRequiredOnReject = true },
		"role selector":              func(task *BPMNUserTask) { task.AssigneeRole = "manager" },
		"department selector":        func(task *BPMNUserTask) { task.AssigneeDeptId = 1 },
		"team selector":              func(task *BPMNUserTask) { task.AssigneeTeamId = 1 },
		"project selector":           func(task *BPMNUserTask) { task.AssigneeProjectId = 1 },
		"temporary team selector":    func(task *BPMNUserTask) { task.AssigneeTempTeamId = 1 },
		"management chain selector":  func(task *BPMNUserTask) { task.AssigneeGmChain = true },
		"delegated handler": func(task *BPMNUserTask) {
			task.ExtensionElements = &BPMNExtensionElements{MetaData: []BPMNMetaData{{Name: bpmnMetaDataServiceTaskType, Value: "external.delegate"}}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			task := *valid
			mutate(&task)
			require.Error(t, validateBPMNAssigneeSource(&task))
		})
	}
}

func TestBPMNAssignmentSourceNamespacedXMLDecoding(t *testing.T) {
	parsed, err := NewBPMNParser().ParseXML([]byte(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="bound" isExecutable="true">
	<bpmn:startEvent id="start" />
    <bpmn:userTask id="fulfill" taskPurpose="fulfillment" assigneeSource="work_item_assignee" />
	<bpmn:endEvent id="end" />
	<bpmn:sequenceFlow id="to-fulfill" sourceRef="start" targetRef="fulfill" />
	<bpmn:sequenceFlow id="to-end" sourceRef="fulfill" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`))
	require.NoError(t, err)
	require.Equal(t, "work_item_assignee", parsed.Processes[0].UserTasks[0].AssigneeSource)
}

func TestBPMNAssignmentSourcePublicationRejectsInvalidSource(t *testing.T) {
	f := newApprovalAssignmentFixture(t)
	for _, source := range []string{"unknown", "${assignee_id}"} {
		t.Run(source, func(t *testing.T) {
			xml := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="invalid-source" isExecutable="true">
	<bpmn:startEvent id="start" />
    <bpmn:userTask id="fulfill" taskPurpose="fulfillment" assigneeSource="%s" />
	<bpmn:endEvent id="end" />
	<bpmn:sequenceFlow id="to-fulfill" sourceRef="start" targetRef="fulfill" />
	<bpmn:sequenceFlow id="to-end" sourceRef="fulfill" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`, source)
			definition := f.def.Update().SetBpmnXML([]byte(xml)).SaveX(context.Background())
			require.ErrorContains(t, f.engine.ValidateDefinitionForPublication(f.ctx, f.client, f.tenant.ID, definition, false), "unsupported assignee source")
		})
	}
}

func TestBPMNAssignmentSourceCreationPersistsBindingWithoutParticipantSnapshot(t *testing.T) {
	f := newApprovalAssignmentFixture(t)
	requester := f.createUser(t, "bound-requester", 0)
	instance := f.createInstance(t, "bound-source", map[string]interface{}{
		"requester_id": requester.ID,
		"assignee_id":  requester.ID,
	})
	item := f.client.Ticket.Create().SetTitle("Bound").SetTicketNumber("BOUND-CREATE").SetRequesterID(requester.ID).SetTenantID(instance.TenantID).SaveX(f.ctx)
	instance = instance.Update().SetBusinessID(item.ID).SetBusinessType("ticket").SaveX(f.ctx)
	require.NoError(t, f.engine.createUserTask(f.ctx, instance, &BPMNUserTask{
		ID: "fulfill", Name: "Fulfill", TaskPurpose: "fulfillment", AssigneeSource: BPMNAssigneeSourceWorkItem,
	}))

	task := f.getCreatedTask(t, instance.ID, "fulfill")
	require.Equal(t, "work_item_assignee", task.AssigneeSource)
	require.Empty(t, task.Assignee)
	require.Empty(t, task.CandidateUsers)
	require.Empty(t, task.CandidateGroups)
}

func TestBPMNAssignmentSourceCreationRejectsUnsupportedRuntimeSource(t *testing.T) {
	f := newApprovalAssignmentFixture(t)
	instance := f.createInstance(t, "invalid-runtime-source", map[string]interface{}{"requester_id": 1})
	err := f.engine.createUserTask(f.ctx, instance, &BPMNUserTask{
		ID: "fulfill", Name: "Fulfill", TaskPurpose: "fulfillment", AssigneeSource: "external",
	})
	require.Error(t, err)
}
