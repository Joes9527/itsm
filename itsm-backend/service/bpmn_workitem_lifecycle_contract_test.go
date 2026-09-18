package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

func lifecycleMetadata(name, value string) string {
	return fmt.Sprintf("<metaData name=%q>%s</metaData>", name, value)
}

func lifecycleXML(contract, prerequisite, attrs, extra string) []byte {
	return []byte(`<definitions><process id="contract" isExecutable="true"><extensionElements>` + contract + `</extensionElements><startEvent id="start"/><userTask id="work" ` + attrs + `><extensionElements>` + prerequisite + extra + `</extensionElements></userTask><endEvent id="end"/><sequenceFlow id="first" sourceRef="start" targetRef="work"/><sequenceFlow id="last" sourceRef="work" targetRef="end"/></process></definitions>`)
}

const lifecycleOwnerAttrs = `taskPurpose="fulfillment" assigneeSource="work_item_assignee"`

func TestWorkItemLifecycleXMLDeclarationValidation(t *testing.T) {
	contract := lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v1")
	prereq := lifecycleMetadata("workItemPrerequisite", "assigned")
	for _, tc := range []struct {
		name, contract, prerequisite, attrs, extra string
		invalid                                    bool
	}{
		{"legacy absent", "", "", "", "", false},
		{"legacy descriptive", lifecycleMetadata("service_task_type", "ticket_task"), "", "", "", false},
		{"known", contract, prereq, lifecycleOwnerAttrs, "", false},
		{"empty contract", lifecycleMetadata("workItemLifecycleContract", ""), "", "", "", true},
		{"unknown contract", lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v2"), "", "", "", true},
		{"duplicate contract", contract + contract, "", "", "", true},
		{"empty prerequisite", contract, lifecycleMetadata("workItemPrerequisite", ""), lifecycleOwnerAttrs, "", true},
		{"unknown prerequisite", contract, lifecycleMetadata("workItemPrerequisite", "resolve"), lifecycleOwnerAttrs, "", true},
		{"duplicate prerequisite", contract, prereq + prereq, lifecycleOwnerAttrs, "", true},
		{"orphan prerequisite", "", prereq, lifecycleOwnerAttrs, "", true},
		{"wrong purpose", contract, prereq, `taskPurpose="approval" assigneeSource="work_item_assignee"`, "", true},
		{"missing binding", contract, prereq, `taskPurpose="fulfillment"`, "", true},
		{"handler", contract, prereq, lifecycleOwnerAttrs, lifecycleMetadata("service_task_type", "ticket_task"), true},
		{"empty handler", contract, prereq, lifecycleOwnerAttrs, lifecycleMetadata("service_task_type", ""), true},
		{"action only", contract, prereq, lifecycleOwnerAttrs, lifecycleMetadata("action", "resolve"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewBPMNParser().ParseXML(lifecycleXML(tc.contract, tc.prerequisite, tc.attrs, tc.extra))
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
	for _, value := range []string{"assigned", "in_progress", "escalated", "resolved", "closed"} {
		t.Run(value, func(t *testing.T) {
			_, err := NewBPMNParser().ParseXML(lifecycleXML(contract, lifecycleMetadata("workItemPrerequisite", value), lifecycleOwnerAttrs, ""))
			require.NoError(t, err)
		})
	}
}

func TestWorkItemLifecyclePublicationFlags(t *testing.T) {
	contract := lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v1")
	for _, key := range []string{"approval_required", "need_escalate"} {
		for _, value := range []interface{}{nil, "true", 1, []interface{}{true}, map[string]interface{}{"value": true}} {
			t.Run(fmt.Sprintf("%s_%T", key, value), func(t *testing.T) {
				def := &ent.ProcessDefinition{BpmnXML: lifecycleXML(contract, "", lifecycleOwnerAttrs, ""), ProcessVariables: map[string]interface{}{key: value}}
				err := (&CustomProcessEngine{}).ValidateDefinitionForPublication(context.Background(), nil, 1, def, false)
				require.ErrorContains(t, err, key)
			})
		}
	}
	for _, vars := range []map[string]interface{}{nil, {"approval_required": false, "need_escalate": true}} {
		def := &ent.ProcessDefinition{BpmnXML: lifecycleXML(contract, "", lifecycleOwnerAttrs, ""), ProcessVariables: vars}
		require.NoError(t, (&CustomProcessEngine{}).ValidateDefinitionForPublication(context.Background(), nil, 1, def, false))
	}
}
