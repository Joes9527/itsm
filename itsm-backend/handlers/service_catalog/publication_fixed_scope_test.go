package service_catalog

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"itsm-backend/service"
	"testing"
)

func TestA5FixPublicationFixedScopeCandidates(t *testing.T) {
	for _, scope := range []string{"assigneeDeptId", "assigneeTeamId", "assigneeProjectId", "assigneeTempTeamId"} {
		for _, state := range []string{"valid", "missing", "foreign", "no_manager", "inactive_manager"} {
			t.Run(scope+"/"+state, func(t *testing.T) {
				ctx := context.Background()
				client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
				defer client.Close()
				tenant := client.Tenant.Create().SetName("Native").SetCode("native").SaveX(ctx)
				foreign := client.Tenant.Create().SetName("Foreign").SetCode("foreign").SaveX(ctx)
				manager := client.User.Create().SetTenantID(tenant.ID).SetName("Manager").SetUsername("manager").SetEmail("manager@example.test").SetPasswordHash("unused").SetActive(state != "inactive_manager").SaveX(ctx)
				ownerTenant := tenant.ID
				if state == "foreign" {
					ownerTenant = foreign.ID
				}
				managerID := manager.ID
				if state == "no_manager" {
					managerID = 0
				}
				id := 999999
				if state != "missing" {
					switch scope {
					case "assigneeDeptId":
						id = client.Department.Create().SetTenantID(ownerTenant).SetName("Department").SetCode("dept").SetManagerID(managerID).SaveX(ctx).ID
					case "assigneeTeamId", "assigneeTempTeamId":
						id = client.Team.Create().SetTenantID(ownerTenant).SetName("Team").SetCode("team").SetManagerID(managerID).SaveX(ctx).ID
					case "assigneeProjectId":
						id = client.Project.Create().SetTenantID(ownerTenant).SetName("Project").SetCode("project").SetManagerID(managerID).SaveX(ctx).ID
					}
				}
				dep := client.ProcessDeployment.Create().SetTenantID(tenant.ID).SetDeploymentID(t.Name()).SetDeploymentName("Scope").SaveX(ctx)
				xml := fmt.Sprintf(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="scope" isExecutable="true"><startEvent id="start"/><userTask id="approval" taskPurpose="approval" %s="%d"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="approval"/><sequenceFlow id="b" sourceRef="approval" targetRef="end"/></process></definitions>`, scope, id)
				client.ProcessDefinition.Create().SetTenantID(tenant.ID).SetDeploymentID(dep.ID).SetKey("scope").SetName("Scope").SetBpmnXML([]byte(xml)).SaveX(ctx)
				owner := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), nil)
				owner.SetPublicationEngine(service.NewCustomProcessEngine(client, zap.NewNop().Sugar()).(*service.CustomProcessEngine))
				_, err := owner.Create(ctx, tenant.ID, dto.CreateServiceCatalogRequest{Name: "Scope", Category: "IT", TargetClass: "generic", Status: "enabled", RequiresApproval: true, ProcessDefinitionKey: "scope"})
				if state == "valid" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					require.Zero(t, client.ServiceCatalog.Query().CountX(ctx))
				}
			})
		}
	}
}

func TestA5FixPublicationRejectsInvalidSLAConfiguration(t *testing.T) {
	for _, kind := range []string{"calendar", "escalation"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
			defer client.Close()
			builder := client.SLADefinition.Create().SetTenantID(1).SetName("Invalid configuration")
			if kind == "calendar" {
				builder.SetBusinessHours(map[string]interface{}{"work_days": []interface{}{}})
			} else {
				builder.SetEscalationRules(map[string]interface{}{"high": []interface{}{map[string]interface{}{"level": 1.5, "afterMinutes": 30}}})
			}
			sla := builder.SaveX(ctx)
			client.ProcessBinding.Create().SetTenantID(1).SetBusinessType("ticket").SetProcessDefinitionKey("none").SetConditions(map[string]interface{}{"no_process": true}).SetSLAPolicyID(fmt.Sprint(sla.ID)).SaveX(ctx)
			owner := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), nil)
			draft, err := owner.Create(ctx, 1, dto.CreateServiceCatalogRequest{Name: "Calendar", Category: "IT", TargetClass: "generic"})
			require.NoError(t, err)
			_, err = owner.Update(ctx, 1, draft.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: draft.CatalogVersion, Status: scPtr("enabled")})
			require.Error(t, err)
			current, err := owner.Get(ctx, 1, draft.ID)
			require.NoError(t, err)
			require.Equal(t, "disabled", current.Status)
		})
	}
}
