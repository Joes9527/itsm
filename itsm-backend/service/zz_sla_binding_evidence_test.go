package service

import (
	"context"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent/enttest"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"
	"testing"
)

func TestEvidenceIncidentPriorityBindingsOutrankDefault(t *testing.T) {
	c := enttest.Open(t, "sqlite3", testDSN())
	defer c.Close()
	ctx := context.Background()
	// Earliest default also has the greatest numeric priority: specificity must win.
	c.ProcessBinding.Create().SetTenantID(1).SetBusinessType("incident").SetProcessDefinitionKey("incident_emergency_flow").SetIsDefault(true).SetPriority(9999).SetSLAPolicyID("999").SaveX(ctx)
	for i, p := range []string{"critical", "high", "medium", "low"} {
		c.ProcessBinding.Create().SetTenantID(1).SetBusinessType("incident").SetProcessDefinitionKey("incident_emergency_flow").SetConditions(map[string]interface{}{"priority": p}).SetSLAPolicyID(strconv.Itoa(i + 101)).SaveX(ctx)
	}
	router := NewProcessRoutingService(c, zap.NewNop().Sugar())
	for i, p := range []string{"critical", "high", "medium", "low"} {
		t.Run(p, func(t *testing.T) {
			r, err := router.FindBestRoute(ctx, &RoutingContext{TenantID: 1, BusinessType: "incident", BusinessSubType: "incident", Variables: map[string]interface{}{"priority": p}})
			require.NoError(t, err)
			require.NotNil(t, r)
			require.Equal(t, strconv.Itoa(i+101), r.SLAPolicyID)
			require.Equal(t, "incident_emergency_flow", r.ProcessDefinitionKey)
		})
	}
}

func TestEvidenceChangeSubtypeOutranksDefault(t *testing.T) {
	c := enttest.Open(t, "sqlite3", testDSN())
	defer c.Close()
	ctx := context.Background()
	c.ProcessBinding.Create().SetTenantID(1).SetBusinessType("change_request").SetProcessDefinitionKey("change_normal_flow").SetIsDefault(true).SetPriority(9999).SetSLAPolicyID("201").SaveX(ctx)
	c.ProcessBinding.Create().SetTenantID(1).SetBusinessType("change_request").SetBusinessSubType("emergency").SetProcessDefinitionKey("change_emergency_flow").SetSLAPolicyID("202").SaveX(ctx)
	router := NewProcessRoutingService(c, zap.NewNop().Sugar())
	for _, tc := range []struct{ sub, policy, key string }{{"normal", "201", "change_normal_flow"}, {"emergency", "202", "change_emergency_flow"}} {
		t.Run(tc.sub, func(t *testing.T) {
			r, err := router.FindBestRoute(ctx, &RoutingContext{TenantID: 1, BusinessType: "change_request", BusinessSubType: tc.sub, Variables: map[string]interface{}{"priority": "critical"}})
			require.NoError(t, err)
			require.NotNil(t, r)
			require.Equal(t, tc.policy, r.SLAPolicyID)
			require.Equal(t, tc.key, r.ProcessDefinitionKey)
		})
	}
}

func TestEvidenceExplicitProcessKeySkipsBindingSLA(t *testing.T) {
	c := enttest.Open(t, "sqlite3", testDSN())
	defer c.Close()
	ctx := context.Background()
	c.Tenant.Create().SetName("Fixture").SetCode("fixture").SetDomain("fixture.invalid").SetStatus("active").SaveX(ctx)
	u := c.User.Create().SetTenantID(1).SetUsername("fixture").SetEmail("fixture@example.invalid").SetName("Fixture").SetPasswordHash("unused").SetRole("end_user").SetActive(true).SaveX(ctx)
	c.ProcessBinding.Create().SetTenantID(1).SetBusinessType("incident").SetProcessDefinitionKey("direct").SetConditions(map[string]interface{}{"no_process": true}).SetSLAPolicyID("301").SaveX(ctx)
	d := c.ProcessDeployment.Create().SetTenantID(1).SetDeploymentID("direct-v1").SetDeploymentName("Direct").SaveX(ctx)
	definition := c.ProcessDefinition.Create().SetTenantID(1).SetDeploymentID(d.ID).SetKey("direct").SetName("Direct").SetVersion("1").SetIsActive(true).SetIsLatest(true).SetBpmnXML([]byte("<definitions/>")).SaveX(ctx)
	in := creation.ResolvedIntake{RecordClass: "incident", Identity: creation.Identity{TenantID: 1, ActorID: u.ID, RequesterID: u.ID}}
	plan := creation.NewPlan(in, "new", "critical", "manual")
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	owner := NewProcessBindingService(c)
	implicit, policy, err := owner.ResolveCreationWorkflow(ctx, tx, plan, "")
	require.NoError(t, err)
	require.True(t, implicit.NoProcess)
	require.NotNil(t, policy)
	require.Equal(t, 301, *policy)
	explicit, policy, err := owner.ResolveCreationWorkflow(ctx, tx, plan, "direct")
	require.NoError(t, err)
	require.NotNil(t, explicit.DefinitionID)
	require.Equal(t, definition.ID, *explicit.DefinitionID)
	require.Nil(t, policy)
}
