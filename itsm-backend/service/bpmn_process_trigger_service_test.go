package service

import (
	"context"
	stdErrors "errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/service/bpmn"

	"entgo.io/ent/dialect"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestTriggerProcess_PopulatesStructuredBusinessIdentity(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:trigger_business_identity?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Trigger Identity Tenant").
		SetCode("trigger-identity").
		SetDomain("trigger-identity.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	logger := zaptest.NewLogger(t).Sugar()
	engine := NewCustomProcessEngine(client, logger)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	tenantCtx = WithTrustedBPMNTenantContext(tenantCtx, tenant.ID)

	deploySvc := NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := NewProcessTriggerService(client, engine)
	resp, err := trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           42,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": false},
		TriggeredBy:          "system",
		TenantID:             tenant.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "change:42", resp.BusinessKey)

	// dto.ProcessTriggerResponse.ProcessInstanceID is the ent row's integer primary
	// key (instance.ID), not the string BPMN engine id (instance.ProcessInstanceID) —
	// confirmed against the response construction in TriggerProcess itself
	// (service/bpmn_process_trigger_service.go: "ProcessInstanceID: instance.ID").
	instance, err := client.ProcessInstance.Get(ctx, resp.ProcessInstanceID)
	require.NoError(t, err)
	require.Equal(t, "change", instance.BusinessType)
	require.Equal(t, 42, instance.BusinessID)
}

func TestTriggerProcessScopeOverridesRequestTriggeredBy(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:trigger_authenticated_actor?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Authenticated trigger tenant").
		SetCode("trigger-authenticated").
		SetDomain("trigger-authenticated.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().
		SetUsername("authenticated.trigger.actor").
		SetEmail("authenticated.trigger.actor@example.test").
		SetName("Authenticated Trigger Actor").
		SetPasswordHash("test").
		SetRole("service_agent").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	workflowCtx := WithBPMNAccessScope(ctx, BPMNAccessScope{UserID: actor.ID, TenantID: tenant.ID})
	deploySvc := NewBPMNTemplateService(client)
	_, err = deploySvc.LoadAndDeployTemplates(workflowCtx, tenant.ID)
	require.NoError(t, err)

	resp, err := NewProcessTriggerService(client, NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar())).TriggerProcess(workflowCtx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           43,
		ProcessDefinitionKey: "change_normal_flow",
		TriggeredBy:          "system",
		TenantID:             tenant.ID,
	})
	require.NoError(t, err)

	instance := client.ProcessInstance.GetX(ctx, resp.ProcessInstanceID)
	require.Equal(t, strconv.Itoa(actor.ID), instance.Variables["triggered_by"])
	audit := client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionProcessStarted),
	).OnlyX(ctx)
	require.Equal(t, actor.ID, audit.UserID)
	require.Equal(t, actor.Name, audit.UserName)
}

func TestProcessTriggerResponseLooksUpDefinitionWithinInstanceTenant(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:trigger_response_tenant_%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	foreignTenant, err := client.Tenant.Create().
		SetName("Foreign response tenant").
		SetCode(fmt.Sprintf("foreign-response-%d", time.Now().UnixNano())).
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	instanceTenant, err := client.Tenant.Create().
		SetName("Instance response tenant").
		SetCode(fmt.Sprintf("instance-response-%d", time.Now().UnixNano())).
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	const definitionKey = "shared-response-definition"
	createProcessTriggerResponseDefinition(t, client, ctx, foreignTenant.ID, definitionKey, "Foreign definition")
	instanceDefinition := createProcessTriggerResponseDefinition(t, client, ctx, instanceTenant.ID, definitionKey, "Instance tenant definition")

	response, err := NewProcessTriggerService(client, nil).toProcessTriggerResponse(ctx, &ent.ProcessInstance{
		ProcessDefinitionID:  instanceDefinition.ID,
		ProcessDefinitionKey: definitionKey,
		TenantID:             instanceTenant.ID,
		Status:               "running",
	})
	require.NoError(t, err)
	require.Equal(t, "Instance tenant definition", response.ProcessDefinitionName)
}

func TestProcessTriggerResponseFallsBackToDefinitionKeyOnlyWhenDefinitionIsAbsent(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:trigger_response_not_found_%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	response, err := NewProcessTriggerService(client, nil).toProcessTriggerResponse(context.Background(), &ent.ProcessInstance{
		ProcessDefinitionKey: "missing-definition",
		TenantID:             1,
		Status:               "running",
	})
	require.NoError(t, err)
	require.Equal(t, "missing-definition", response.ProcessDefinitionName)
}

func TestProcessTriggerResponsePropagatesDefinitionLookupErrors(t *testing.T) {
	queryErr := stdErrors.New("definition database unavailable")
	trigger := &ProcessTriggerService{client: ent.NewClient(ent.Driver(&processTriggerDefinitionQueryErrorDriver{err: queryErr}))}

	response, err := trigger.toProcessTriggerResponse(context.Background(), &ent.ProcessInstance{
		ProcessDefinitionKey: "definition-with-query-error",
		TenantID:             1,
		Status:               "running",
	})
	require.Nil(t, response)
	require.ErrorIs(t, err, queryErr)
}

func createProcessTriggerResponseDefinition(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, key, name string) *ent.ProcessDefinition {
	t.Helper()
	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID(fmt.Sprintf("trigger-response-deployment-%d", time.Now().UnixNano())).
		SetDeploymentName(name + " deployment").
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().
		SetKey(key).
		SetName(name).
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return definition
}

type processTriggerDefinitionQueryErrorDriver struct {
	err error
}

func (d *processTriggerDefinitionQueryErrorDriver) Dialect() string { return dialect.SQLite }
func (d *processTriggerDefinitionQueryErrorDriver) Close() error    { return nil }
func (d *processTriggerDefinitionQueryErrorDriver) Tx(context.Context) (dialect.Tx, error) {
	return nil, d.err
}
func (d *processTriggerDefinitionQueryErrorDriver) Exec(context.Context, string, any, any) error {
	return d.err
}
func (d *processTriggerDefinitionQueryErrorDriver) Query(context.Context, string, any, any) error {
	return d.err
}

var _ dialect.Driver = (*processTriggerDefinitionQueryErrorDriver)(nil)
