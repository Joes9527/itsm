package service_catalog

import (
	"context"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/common/accessgrant"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"testing"
)

func TestIntakeCatalogDiscoverySkipsInvalidAcrossPages(t *testing.T) {
	c := enttest.Open(t, "sqlite3", "file:discovery_pages?mode=memory&cache=shared&_fk=1")
	defer c.Close()
	ctx := context.Background()
	dep := c.ProcessDeployment.Create().SetDeploymentID("discovery").SetDeploymentName("Discovery").SetTenantID(1).SaveX(ctx)
	for _, bad := range []bool{false, true} {
		key := "valid"
		task := ""
		if bad {
			key = "invalid"
			task = `<userTask id="unassigned"/>`
		}
		xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="` + key + `" isExecutable="true"><startEvent id="start"/>` + task + `<endEvent id="end"/></process></definitions>`
		c.ProcessDefinition.Create().SetKey(key).SetName(key).SetVersion("1.0.0").SetTenantID(1).SetDeploymentID(dep.ID).SetBpmnXML([]byte(xml)).SetIsActive(true).SetIsLatest(true).SaveX(ctx)
	}
	grantXML := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="legacy-grant" isExecutable="true"><startEvent id="start"/><serviceTask id="grant"><extensionElements><metaData name="service_task_type">kaf_delegate</metaData><metaData name="action">external_group_grant</metaData><metaData name="callback_config_ref">999</metaData></extensionElements></serviceTask><endEvent id="end"/></process></definitions>`
	c.ProcessDefinition.Create().SetKey("legacy-grant").SetName("legacy-grant").SetVersion("1.0.0").SetTenantID(1).SetDeploymentID(dep.ID).SetBpmnXML([]byte(grantXML)).SetIsActive(true).SetIsLatest(true).SaveX(ctx)
	add := func(key string) *ent.ServiceCatalog {
		return c.ServiceCatalog.Create().SetName(key).SetTenantID(1).SetTargetClass("generic").SetProcessDefinitionKey(key).SetStatus("enabled").SetIsActive(true).SetRequiresApproval(false).SaveX(ctx)
	}
	for i := 0; i < 60; i++ {
		add("invalid")
	}
	legacy := add("legacy-grant")
	valid := []int{}
	for i := 0; i < 52; i++ {
		valid = append(valid, add("valid").ID)
		add("invalid")
	}
	for i := 0; i < 60; i++ {
		add("invalid")
	}
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core).Sugar()
	svc := newCatalogPublisher(NewEntRepository(c), c, logger, nil)
	svc.SetPublicationEngine(service.NewCustomProcessEngine(c, logger, executionfixture.Standard()).(*service.CustomProcessEngine))
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	snapshot := &authorization.SessionSnapshot{Tx: tx, Identity: creation.Identity{TenantID: 1, ActorID: 1, Role: "super_admin"}}
	first, err := svc.ListAvailableForIntake(ctx, snapshot, 0, "", 51)
	require.NoError(t, err)
	require.Len(t, first, 51)
	require.Equal(t, valid[0], first[0].ID)
	require.Equal(t, valid[50], first[50].ID)
	second, err := svc.ListAvailableForIntake(ctx, snapshot, first[49].ID, "", 51)
	require.NoError(t, err)
	require.Len(t, second, 2)
	require.Equal(t, first[50].ID, second[0].ID)
	require.Equal(t, valid[51], second[1].ID)
	_, err = svc.ReadAvailableForIntake(ctx, snapshot, legacy.ID)
	require.ErrorContains(t, err, "access capability binding is incomplete")
	_, err = svc.ReadAvailableForIntake(ctx, snapshot, 1)
	require.Error(t, err)
	require.Greater(t, logs.Len(), 0)
	for _, entry := range logs.All() {
		require.Contains(t, entry.ContextMap(), "catalog_id")
		require.Contains(t, entry.ContextMap(), "tenant_id")
		require.Equal(t, "publication_configuration", entry.ContextMap()["error_class"])
		require.NotContains(t, entry.ContextMap(), "error")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = svc.ListAvailableForIntake(cancelled, snapshot, 0, "", 51)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	snapshot.Identity.Role = "unassigned"
	_, err = svc.ListAvailableForIntake(ctx, snapshot, 0, "", 51)
	require.ErrorIs(t, err, creation.ErrPermissionDenied)
	snapshot.Identity.Role = "super_admin"
	binder := service.NewProcessBindingService(tx.Client())
	for _, policy := range []*accessgrant.Policy{nil, {ID: 123}} {
		err = binder.ValidateAccessPolicyBinding(ctx, tx, 1, "generic", "legacy-grant", policy)
		require.True(t, isUnavailableIntakeCatalog(err))
	}
	require.NoError(t, tx.Rollback())
	err = binder.ValidateAccessPolicyBinding(ctx, tx, 1, "generic", "legacy-grant", nil)
	require.Error(t, err)
	require.False(t, isUnavailableIntakeCatalog(err))

	_, err = svc.ListAvailableForIntake(ctx, snapshot, 0, "", 51)
	require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
}
func TestCatalogDiscoveryErrorClassification(t *testing.T) {
	marker := &bpmn.PublicationConfigurationError{Message: "missing candidates"}
	require.True(t, isUnavailableIntakeCatalog(marker))
	require.True(t, isUnavailableIntakeCatalog(creation.NewDomainValidationFailed("invalid publication", marker)))
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("database failure"), creation.NewDomainValidationFailed("invalid", errors.New("driver failure")), creation.NewInfrastructureUnavailable("down", marker), creation.NewPermissionDenied("denied", marker), creation.NewAuthenticationRequired("login", marker), errors.Join(marker, errors.New("database failure")), fmt.Errorf("wrapped: %w", context.Canceled)} {
		require.False(t, isUnavailableIntakeCatalog(err), "%v", err)
	}
}

// The policy owner marks known missing configuration, never a database failure.
func TestIntakeCatalogPolicyOwnerErrorBoundary(t *testing.T) {
	c := enttest.Open(t, "sqlite3", "file:discovery_policy?mode=memory&cache=shared&_fk=1")
	defer c.Close()
	svc := newCatalogPublisher(NewEntRepository(c), c, zap.NewNop().Sugar(), nil)
	err := svc.ValidatePublicationConfiguration(context.Background(), c, 1, accessgrant.Capability, "999")
	require.True(t, isUnavailableIntakeCatalog(err))
	require.NoError(t, c.Close())
	err = svc.ValidatePublicationConfiguration(context.Background(), c, 1, accessgrant.Capability, "999")
	require.Error(t, err)
	require.False(t, isUnavailableIntakeCatalog(err))
}
