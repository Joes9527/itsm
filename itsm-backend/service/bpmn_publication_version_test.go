package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"strings"
	"testing"
)

func TestPublicationWaitingInstanceRetainsDefinitionVersion(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := startProcessContext(f)
	xml := strings.Replace(string(startProcessUserTaskXML()), `name="Approval"`, `name="Approval" assignee="bpmn.actor"`, 1)
	f.definition = f.definition.Update().SetBpmnXML([]byte(xml)).SetCategory("maintenance").SetProcessVariables(map[string]interface{}{"quota": "exact"}).SaveX(ctx)
	original := FreezeProcessDefinition(f.definition)
	instance, err := f.engine.StartProcessByDefinitionID(ctx, original, "ticket:91", "ticket", 91, nil, "version-waiting")
	require.NoError(t, err)
	task := f.client.ProcessTask.Query().OnlyX(ctx)
	changedXML := strings.ReplaceAll(xml, `id="end"`, `id="new_end"`)
	changedXML = strings.ReplaceAll(changedXML, `targetRef="end"`, `targetRef="new_end"`)
	_, err = f.engine.ProcessDefinitionService().UpdateProcessDefinition(ctx, f.definition.Key, f.definition.Version, &UpdateProcessDefinitionRequest{BPMNXML: changedXML})
	require.ErrorContains(t, err, "new process version")
	_, err = f.engine.ProcessDefinitionService().UpdateProcessDefinition(ctx, f.definition.Key, f.definition.Version, &UpdateProcessDefinitionRequest{ProcessVariables: map[string]interface{}{"changed": true}})
	require.ErrorContains(t, err, "new process version")
	_, err = f.engine.ProcessDefinitionService().UpdateProcessDefinition(ctx, f.definition.Key, f.definition.Version, &UpdateProcessDefinitionRequest{Name: "Display changed", BPMNXML: xml, ProcessVariables: f.definition.ProcessVariables})
	require.NoError(t, err)
	versions := NewBPMNVersionService(f.client, zap.NewNop().Sugar())
	next, err := versions.CreateVersion(ctx, &CreateVersionRequest{ProcessDefinitionKey: f.definition.Key, BaseVersion: f.definition.Version, Name: "New version", BPMNXML: changedXML, TenantID: f.tenant.ID})
	require.NoError(t, err)
	require.NotEqual(t, f.definition.Version, next.Version)
	newDefinition, err := f.engine.ProcessDefinitionService().GetProcessDefinition(ctx, f.definition.Key, next.Version)
	require.NoError(t, err)
	require.Equal(t, f.definition.Category, newDefinition.Category)
	require.Equal(t, f.definition.ProcessVariables, newDefinition.ProcessVariables)
	newCategory := "new category"
	latest, err := versions.CreateVersion(ctx, &CreateVersionRequest{ProcessDefinitionKey: f.definition.Key, BaseVersion: next.Version, Name: "Edited settings", BPMNXML: changedXML, Category: &newCategory, ProcessVariables: map[string]interface{}{"quota": "changed"}, TenantID: f.tenant.ID})
	require.NoError(t, err)
	branch, err := versions.CreateVersion(ctx, &CreateVersionRequest{ProcessDefinitionKey: f.definition.Key, BaseVersion: f.definition.Version, Name: "Branch older definition", BPMNXML: changedXML, TenantID: f.tenant.ID})
	require.NoError(t, err)
	require.NotEqual(t, latest.Version, branch.Version)
	branchedDefinition, err := f.engine.ProcessDefinitionService().GetProcessDefinition(ctx, f.definition.Key, branch.Version)
	require.NoError(t, err)
	require.Equal(t, f.definition.Category, branchedDefinition.Category)
	require.Equal(t, f.definition.ProcessVariables, branchedDefinition.ProcessVariables)
	require.Equal(t, changedXML, string(branchedDefinition.BpmnXML))
	f.actor.Update().SetActive(false).ExecX(context.Background())
	require.Error(t, f.engine.CompleteTask(ctx, task.TaskID, nil), "authorization is checked live after publication")
	f.actor.Update().SetActive(true).ExecX(context.Background())
	require.NoError(t, f.engine.CompleteTask(ctx, task.TaskID, nil))
	finished := f.client.ProcessInstance.GetX(ctx, instance.ID)
	require.Equal(t, f.definition.ID, finished.ProcessDefinitionID)
	require.Equal(t, "completed", finished.Status)
	require.Equal(t, original, FreezeProcessDefinition(f.client.ProcessDefinition.GetX(ctx, f.definition.ID)))
}
