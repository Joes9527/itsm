package service

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestWorkItemLifecycleTypedAccess(t *testing.T) {
	xml := lifecycleXML(lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v1"), lifecycleMetadata("workItemPrerequisite", "resolved"), lifecycleOwnerAttrs, "")
	parsed, err := NewBPMNParser().ParseXML(xml)
	require.NoError(t, err)
	contract, err := parsed.Processes[0].WorkItemLifecycleContract()
	require.NoError(t, err)
	require.Equal(t, GenericFulfillmentV1, contract)
	prerequisite, err := parsed.Processes[0].UserTasks[0].WorkItemPrerequisite()
	require.NoError(t, err)
	require.Equal(t, WorkItemPrerequisiteResolved, prerequisite)
	require.NoError(t, ValidateWorkItemLifecycleRecordClass(parsed, "generic"))
	for _, class := range []string{"", "incident", "problem", "change_request", "service_request_item", "catalog_task"} {
		require.Error(t, ValidateWorkItemLifecycleRecordClass(parsed, class))
	}
	flags, err := ReadGenericFulfillmentConfig(parsed, nil)
	require.NoError(t, err)
	require.Equal(t, &GenericFulfillmentConfig{}, flags)
	flags, err = ReadGenericFulfillmentConfig(parsed, map[string]interface{}{"approval_required": true, "need_escalate": true})
	require.NoError(t, err)
	require.True(t, flags.ApprovalRequired)
	require.True(t, flags.NeedEscalate)
	legacy := strings.Replace(string(lifecycleXML("", "", "", "")), "<extensionElements></extensionElements>", "", -1)
	parsed, err = NewBPMNParser().ParseXML([]byte(legacy))
	require.NoError(t, err)
	require.Nil(t, parsed.Processes[0].ExtensionElements)
	flags, err = ReadGenericFulfillmentConfig(parsed, map[string]interface{}{"approval_required": nil})
	require.NoError(t, err)
	require.Nil(t, flags)
	require.NoError(t, ValidateWorkItemLifecycleRecordClass(parsed, "incident"))
}
func TestWorkItemLifecycleMetadataPlacement(t *testing.T) {
	contract := lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v1")
	prereq := lifecycleMetadata("workItemPrerequisite", "assigned")
	base := string(lifecycleXML(contract, prereq, lifecycleOwnerAttrs, ""))
	for name, xml := range map[string]string{
		"service task prerequisite": strings.ReplaceAll(base, "userTask", "serviceTask"),
		"task contract":             string(lifecycleXML("", contract, lifecycleOwnerAttrs, "")),
		"process prerequisite":      string(lifecycleXML(prereq, "", lifecycleOwnerAttrs, "")),
		"nested task":               strings.ReplaceAll(strings.ReplaceAll(base, "<userTask", `<subProcess id="sub"><userTask`), "</userTask>", "</userTask></subProcess>"),
		"unknown task location":     strings.ReplaceAll(base, "userTask", "manualTask"),
		"contract attribute":        strings.Replace(string(lifecycleXML("", "", "", "")), `id="contract"`, `id="contract" workItemLifecycleContract="generic_fulfillment_v1"`, 1),
	} {
		t.Run(name, func(t *testing.T) { _, err := NewBPMNParser().ParseXML([]byte(xml)); require.Error(t, err) })
	}
}
func TestWorkItemLifecycleContractRejectsUnsupportedExecution(t *testing.T) {
	contract := lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v1")
	for _, node := range []string{
		`<subProcess id="sub"/>`,
		`<callActivity id="call"/>`,
		`<serviceTask id="service"/>`,
		`<scriptTask id="script"/>`,
		`<businessRuleTask id="rule"/>`,
		`<manualTask id="manual"/>`,
	} {
		xml := strings.Replace(string(lifecycleXML(contract, "", lifecycleOwnerAttrs, "")), "</process>", node+"</process>", 1)
		_, err := NewBPMNParser().ParseXML([]byte(xml))
		require.Error(t, err, node)
	}
	_, err := NewBPMNParser().ParseXML(lifecycleXML(contract, "", lifecycleOwnerAttrs, lifecycleMetadata("action", "resolve")))
	require.Error(t, err)
}
