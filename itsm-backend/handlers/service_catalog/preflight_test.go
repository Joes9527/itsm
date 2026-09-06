package service_catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"testing"
	"time"
)

func TestPublicationDraftRepairVersionAndRollback(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	tenant := client.Tenant.Create().SetName("Draft").SetCode(t.Name()).SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("admin").SetName("Admin").SetEmail("admin@example.test").SetPasswordHash("unused").SetRole("super_admin").SaveX(ctx)
	svc := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), sameTransactionDirectory{})
	draft, err := svc.Create(ctx, tenant.ID, dto.CreateServiceCatalogRequest{Name: "Incomplete", Category: "IT", TargetClass: "not_configured", Fields: []map[string]interface{}{{"name": "amount", "label": "Amount", "type": "select", "options": []interface{}{map[string]any{"label": "Exact", "value": json.Number("9007199254740993")}, map[string]any{"label": "Adjacent", "value": json.Number("9007199254740992")}}}}})
	require.NoError(t, err)
	identity := creation.Identity{TenantID: tenant.ID, ActorID: actor.ID, Role: actor.Role}
	read, err := svc.Read(ctx, identity, draft.ID)
	require.NoError(t, err)
	require.Equal(t, draft.CatalogVersion, read.CatalogVersion)
	require.Equal(t, json.Number("9007199254740993"), read.Fields[0].Options[0].(map[string]any)["value"], "storage must retain exact JSON numbers")
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	_, _, err = svc.ResolveCreationCatalog(ctx, tx, identity, draft.ID)
	require.Error(t, err)
	require.NoError(t, tx.Rollback())
	_, err = svc.Update(ctx, tenant.ID, draft.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: draft.CatalogVersion, Status: scPtr("enabled")})
	require.Error(t, err)
	require.Equal(t, "disabled", client.ServiceCatalog.GetX(ctx, draft.ID).Status)
	client.ProcessBinding.Create().SetTenantID(tenant.ID).SetBusinessType("ticket").SetProcessDefinitionKey("none").SetConditions(map[string]interface{}{"no_process": true}).SaveX(ctx)
	published, err := svc.Update(ctx, tenant.ID, draft.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: draft.CatalogVersion, TargetClass: scPtr("generic"), Status: scPtr("enabled")})
	require.NoError(t, err)
	require.Equal(t, "generic", published.TargetClass)
	require.NotEqual(t, draft.CatalogVersion, published.CatalogVersion)
	_, err = svc.Update(ctx, tenant.ID, draft.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: draft.CatalogVersion, Name: scPtr("stale")})
	require.ErrorIs(t, err, creation.ErrCatalogVersionConflict)
	_, err = svc.Update(ctx, tenant.ID, draft.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: published.CatalogVersion, Name: scPtr("must rollback"), Fields: []map[string]interface{}{{"name": "broken", "label": "Broken", "type": "alien"}}})
	require.Error(t, err)
	require.Equal(t, "Incomplete", client.ServiceCatalog.GetX(ctx, draft.ID).Name)
	current, err := svc.Read(ctx, identity, draft.ID)
	require.NoError(t, err)
	require.Equal(t, published.CatalogVersion, current.CatalogVersion)
}

func TestPublicationDeclaredWorkflowValidation(t *testing.T) {
	for _, tc := range []struct {
		name, xml string
		approval  bool
		ok        bool
	}{
		{"generic", `<userTask id="task" assignee="${requester_id}"/>`, false, true},
		{"unknown_role", `<userTask id="task" assigneeRole="missing_role"/>`, false, false},
		{"missing_candidate", `<userTask id="task"/>`, false, false},
		{"approval_missing", `<userTask id="task" assignee="${requester_id}"/>`, true, false},
		{"required_unknown", `<serviceTask id="task"><extensionElements><metaData name="service_task_type">external_grant</metaData><metaData name="action">grant</metaData></extensionElements></serviceTask>`, false, false},
		{"unknown_action", `<serviceTask id="task"><extensionElements><metaData name="service_task_type">ticket_task</metaData><metaData name="action">unknown</metaData></extensionElements></serviceTask>`, false, false},
		{"kaf_missing_config", `<serviceTask id="task"><extensionElements><metaData name="service_task_type">kaf_delegate</metaData><metaData name="allowed_actions">complete_bpmn_task</metaData></extensionElements></serviceTask>`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
			defer client.Close()
			dep := client.ProcessDeployment.Create().SetTenantID(1).SetDeploymentID(t.Name()).SetDeploymentName("Publication").SaveX(ctx)
			xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="flow" isExecutable="true"><startEvent id="start"/>` + tc.xml + `<endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="task"/><sequenceFlow id="b" sourceRef="task" targetRef="end"/></process></definitions>`
			definition := client.ProcessDefinition.Create().SetTenantID(1).SetDeploymentID(dep.ID).SetKey("flow").SetName("Publication").SetVersion("1.2.0").SetIsLatest(true).SetIsActive(true).SetBpmnXML([]byte(xml)).SaveX(ctx)
			svc := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), nil)
			svc.SetPublicationEngine(service.NewCustomProcessEngine(client, zap.NewNop().Sugar()).(*service.CustomProcessEngine))
			input := dto.CreateServiceCatalogRequest{Name: "Consultation", Category: "IT", Status: "enabled", TargetClass: "generic", ProcessDefinitionKey: "flow", RequiresApproval: tc.approval}
			catalog, err := svc.Create(ctx, 1, input)
			if !tc.ok {
				require.Error(t, err)
				require.Zero(t, client.ServiceCatalog.Query().CountX(ctx))
				return
			}
			require.NoError(t, err)
			client.ProcessDefinition.UpdateOneID(definition.ID).SetName("display only").SetDeployedAt(time.Now().Add(time.Hour)).ExecX(ctx)
			// Exact definition identity is unchanged by display/deployment clocks.
			result, err := svc.Update(ctx, 1, catalog.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: catalog.CatalogVersion, Description: scPtr("")})
			require.NoError(t, err)
			require.Equal(t, catalog.CatalogVersion, result.CatalogVersion)
		})
	}
}

func TestPublicationSLAOwnershipAndUnrelatedRevision(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	binding := client.ProcessBinding.Create().SetTenantID(1).SetBusinessType("ticket").SetProcessDefinitionKey("none").SetConditions(map[string]interface{}{"no_process": true}).SaveX(ctx)
	svc := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), nil)
	catalog, err := svc.Create(ctx, 1, dto.CreateServiceCatalogRequest{Name: "Hardware", Category: "IT", Status: "enabled", TargetClass: "generic"})
	require.NoError(t, err)
	client.SLADefinition.Create().SetTenantID(1).SetName("Unrelated").SaveX(ctx)
	unchanged, err := svc.Update(ctx, 1, catalog.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: catalog.CatalogVersion})
	require.NoError(t, err)
	require.Equal(t, catalog.CatalogVersion, unchanged.CatalogVersion)
	foreign := client.SLADefinition.Create().SetTenantID(2).SetName("Foreign").SaveX(ctx)
	client.ProcessBinding.UpdateOneID(binding.ID).SetSLAPolicyID(fmt.Sprint(foreign.ID)).ExecX(ctx)
	_, err = svc.Create(ctx, 1, dto.CreateServiceCatalogRequest{Name: "Invalid SLA", Category: "IT", Status: "enabled", TargetClass: "generic"})
	require.Error(t, err)
}
