package intake

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processbinding"
	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

type resolverFixture struct {
	client          *ent.Client
	tenant          *ent.Tenant
	otherTenant     *ent.Tenant
	user            *ent.User
	serviceCatalog  *ent.ServiceCatalog
	incidentCatalog *ent.ServiceCatalog
	changeCatalog   *ent.ServiceCatalog
	otherCatalog    *ent.ServiceCatalog
	disabledCatalog *ent.ServiceCatalog
	categoryID      int
	typeID          int
	itemID          int
	ciID            int
	otherCIID       int
}

func newResolverFixture(t *testing.T) *resolverFixture {
	t.Helper()
	ctx := context.Background()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", name))
	t.Cleanup(func() { client.Close() })
	tenant, err := client.Tenant.Create().SetName(name).SetCode(name).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	otherTenant, err := client.Tenant.Create().SetName(name + "-other").SetCode(name + "-other").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().SetUsername(name).SetEmail(name + "@example.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	seedResolverDefinition(t, client, tenant.ID, "incident-flow", "5")
	seedResolverDefinition(t, client, tenant.ID, "service-request-flow", "9")
	_, err = client.ProcessBinding.Create().SetBusinessType("ticket").SetBusinessSubType("incident").
		SetProcessDefinitionKey("incident-flow").SetProcessVersion(5).SetPriority(100).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.ProcessBinding.Create().SetBusinessType("ticket").SetBusinessSubType("service_request").
		SetProcessDefinitionKey("service-request-flow").SetProcessVersion(9).SetPriority(100).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	seedResolverDefinition(t, client, tenant.ID, "change-flow", "3")
	_, err = client.ProcessBinding.Create().SetBusinessType("ticket").SetBusinessSubType("change").
		SetProcessDefinitionKey("change-flow").SetProcessVersion(3).SetPriority(100).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	serviceCatalog, err := client.ServiceCatalog.Create().SetName("Access request").SetStatus("enabled").SetIsActive(true).
		SetTenantID(tenant.ID).SetTargetClass(RecordClassServiceRequestItem).Save(ctx)
	require.NoError(t, err)
	incidentCatalog, err := client.ServiceCatalog.Create().SetName("Report outage").SetStatus("active").SetIsActive(true).
		SetTenantID(tenant.ID).SetTargetClass(RecordClassIncident).Save(ctx)
	require.NoError(t, err)
	changeCatalog, err := client.ServiceCatalog.Create().SetName("Planned network change").SetStatus("active").SetIsActive(true).
		SetTenantID(tenant.ID).SetTargetClass(RecordClassChangeRequest).Save(ctx)
	require.NoError(t, err)
	otherCatalog, err := client.ServiceCatalog.Create().SetName("Other tenant request").SetStatus("active").SetIsActive(true).
		SetTenantID(otherTenant.ID).SetTargetClass(RecordClassServiceRequestItem).Save(ctx)
	require.NoError(t, err)
	disabledCatalog, err := client.ServiceCatalog.Create().SetName("Disabled request").SetStatus("disabled").SetIsActive(false).
		SetTenantID(tenant.ID).SetTargetClass(RecordClassServiceRequestItem).Save(ctx)
	require.NoError(t, err)
	_, err = client.FieldDefinition.Create().SetTenantID(tenant.ID).SetEntityType("service_catalog").
		SetEntityID(serviceCatalog.ID).SetName("device_count").SetLabel("Device count").
		SetFieldType("number").SetRequired(true).SetIsActive(true).Save(ctx)
	require.NoError(t, err)

	category, err := client.TicketCategory.Create().SetName("Category").SetCode(name + "-cat").SetLevel(1).
		SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	typeCategory, err := client.TicketCategory.Create().SetName("Type").SetCode(name + "-type").SetLevel(2).
		SetParentID(category.ID).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	item, err := client.TicketCategory.Create().SetName("Item").SetCode(name + "-item").SetLevel(3).
		SetParentID(typeCategory.ID).SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	ciType, err := client.CIType.Create().SetName(name + "-ci-type").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	ci, err := client.ConfigurationItem.Create().SetName("Database").SetCiTypeID(ciType.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	otherCIType, err := client.CIType.Create().SetName(name + "-other-ci-type").SetTenantID(otherTenant.ID).Save(ctx)
	require.NoError(t, err)
	otherCI, err := client.ConfigurationItem.Create().SetName("Other database").SetCiTypeID(otherCIType.ID).SetTenantID(otherTenant.ID).Save(ctx)
	require.NoError(t, err)

	return &resolverFixture{
		client: client, tenant: tenant, otherTenant: otherTenant, user: user,
		serviceCatalog: serviceCatalog, incidentCatalog: incidentCatalog, changeCatalog: changeCatalog, otherCatalog: otherCatalog, disabledCatalog: disabledCatalog,
		categoryID: category.ID, typeID: typeCategory.ID, itemID: item.ID,
		ciID: ci.ID, otherCIID: otherCI.ID,
	}
}

func seedResolverDefinition(t *testing.T, client *ent.Client, tenantID int, key, version string) {
	t.Helper()
	ctx := context.Background()
	deployment, err := client.ProcessDeployment.Create().SetDeploymentID(fmt.Sprintf("%s-%d", key, tenantID)).
		SetDeploymentName(key).SetTenantID(tenantID).Save(ctx)
	require.NoError(t, err)
	_, err = client.ProcessDefinition.Create().SetKey(key).SetName(key).SetVersion(version).
		SetBpmnXML([]byte("<definitions/>")).SetIsActive(true).SetIsLatest(true).
		SetDeploymentID(deployment.ID).SetTenantID(tenantID).Save(ctx)
	require.NoError(t, err)
}

func (f *resolverFixture) identity() Identity {
	return Identity{TenantID: f.tenant.ID, ActorID: f.user.ID, RequesterID: f.user.ID, Role: "end_user", Channel: "itsm_web"}
}

func (f *resolverFixture) resolver(check PermissionCheckFunc) *Resolver {
	if check == nil {
		check = func(_ *ent.Client, _ Identity, _, _ string) bool { return true }
	}
	return NewResolver(service.NewProcessBindingService(f.client), check)
}

func (f *resolverFixture) catalogCommand(catalogID int) CreateWorkItemCommand {
	return CreateWorkItemCommand{
		IdempotencyKey: "key-1", IntakeKind: IntakeKindCatalogItem, Title: "Create request",
		CatalogItemID: &catalogID, FormValues: map[string]any{"device_count": 2},
	}
}

func TestResolverResolvesDirectAndCatalogTargets(t *testing.T) {
	for _, test := range []struct {
		name        string
		command     func(*resolverFixture) CreateWorkItemCommand
		recordClass string
	}{
		{name: "direct incident", command: func(f *resolverFixture) CreateWorkItemCommand { return validIncidentCommand("key-1", nil) }, recordClass: RecordClassIncident},
		{name: "service request catalog", command: func(f *resolverFixture) CreateWorkItemCommand { return f.catalogCommand(f.serviceCatalog.ID) }, recordClass: RecordClassServiceRequestItem},
		{name: "incident catalog", command: func(f *resolverFixture) CreateWorkItemCommand {
			cmd := f.catalogCommand(f.incidentCatalog.ID)
			cmd.FormValues = nil
			return cmd
		}, recordClass: RecordClassIncident},
		{name: "change catalog", command: func(f *resolverFixture) CreateWorkItemCommand {
			cmd := f.catalogCommand(f.changeCatalog.ID)
			cmd.FormValues = nil
			return cmd
		}, recordClass: RecordClassChangeRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newResolverFixture(t)
			tx := beginIntakeTestTx(t, fixture.client)
			resolved, err := fixture.resolver(nil).Resolve(context.Background(), tx, fixture.identity(), test.command(fixture))
			require.NoError(t, err)
			require.Equal(t, test.recordClass, resolved.RecordClass)
			require.NotNil(t, resolved.Workflow.DefinitionID)
			require.NotEmpty(t, resolved.Workflow.DefinitionVersion)
			require.NoError(t, tx.Rollback())
		})
	}
}

func TestResolverValidatesCTIAndCITenantVisibility(t *testing.T) {
	fixture := newResolverFixture(t)
	ctx := context.Background()

	t.Run("valid chain", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		cmd := validIncidentCommand("key-1", []int{fixture.ciID})
		cmd.CTI = &CTIInput{CategoryID: &fixture.categoryID, TypeID: &fixture.typeID, ItemID: &fixture.itemID}
		resolved, err := fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), cmd)
		require.NoError(t, err)
		require.Equal(t, fixture.itemID, *resolved.CTI.ItemID)
		require.Equal(t, []int{fixture.ciID}, resolved.CIIDs)
		require.Len(t, resolved.ConfigurationItems, 1)
		require.Equal(t, fixture.ciID, resolved.ConfigurationItems[0].ID)
		require.NoError(t, tx.Rollback())
	})

	t.Run("incomplete chain", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		cmd := validIncidentCommand("key-2", nil)
		cmd.CTI = &CTIInput{CategoryID: &fixture.categoryID}
		_, err := fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), cmd)
		require.ErrorIs(t, err, ErrDomainValidationFailed)
		require.NoError(t, tx.Rollback())
	})

	t.Run("cross tenant CI", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		cmd := validIncidentCommand("key-3", []int{fixture.otherCIID})
		_, err := fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), cmd)
		require.ErrorIs(t, err, ErrReferenceNotFound)
		require.NoError(t, tx.Rollback())
	})
}

func TestResolverRejectsHiddenCatalogInvalidFormAndPermissionDenial(t *testing.T) {
	fixture := newResolverFixture(t)
	ctx := context.Background()

	for name, catalogID := range map[string]int{
		"cross tenant catalog": fixture.otherCatalog.ID,
		"disabled catalog":     fixture.disabledCatalog.ID,
	} {
		t.Run(name, func(t *testing.T) {
			tx := beginIntakeTestTx(t, fixture.client)
			cmd := fixture.catalogCommand(catalogID)
			_, err := fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), cmd)
			require.ErrorIs(t, err, ErrReferenceNotFound)
			require.NoError(t, tx.Rollback())
		})
	}

	t.Run("required form field", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		cmd := fixture.catalogCommand(fixture.serviceCatalog.ID)
		cmd.FormValues = nil
		_, err := fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), cmd)
		require.ErrorIs(t, err, ErrDomainValidationFailed)
		require.NoError(t, tx.Rollback())
	})

	t.Run("invalid form field type", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		cmd := fixture.catalogCommand(fixture.serviceCatalog.ID)
		cmd.FormValues["device_count"] = "many"
		_, err := fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), cmd)
		require.ErrorIs(t, err, ErrDomainValidationFailed)
		require.NoError(t, tx.Rollback())
	})

	t.Run("professional service request fields", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		cmd := fixture.catalogCommand(fixture.serviceCatalog.ID)
		cmd.FormValues["contact_name"] = "Requester"
		cmd.FormValues["quantity"] = 2
		resolved, err := fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), cmd)
		require.NoError(t, err)
		require.Equal(t, "Requester", resolved.Command.FormValues["contact_name"])
		require.NoError(t, tx.Rollback())
	})

	t.Run("unknown form field", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		cmd := fixture.catalogCommand(fixture.serviceCatalog.ID)
		cmd.FormValues["unexpected"] = true
		_, err := fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), cmd)
		require.ErrorIs(t, err, ErrDomainValidationFailed)
		require.NoError(t, tx.Rollback())
	})

	t.Run("target permission", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		checker := PermissionCheckFunc(func(_ *ent.Client, _ Identity, resource, action string) bool {
			return !(resource == "incident" && action == "write")
		})
		_, err := fixture.resolver(checker).Resolve(ctx, tx, fixture.identity(), validIncidentCommand("key-4", nil))
		require.ErrorIs(t, err, ErrPermissionDenied)
		require.NoError(t, tx.Rollback())
	})

	t.Run("change target permission", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		checker := PermissionCheckFunc(func(_ *ent.Client, _ Identity, resource, action string) bool {
			return !(resource == "change" && action == "write")
		})
		cmd := fixture.catalogCommand(fixture.changeCatalog.ID)
		cmd.FormValues = nil
		_, err := fixture.resolver(checker).Resolve(ctx, tx, fixture.identity(), cmd)
		require.ErrorIs(t, err, ErrPermissionDenied)
		require.NoError(t, tx.Rollback())
	})
}

func TestResolverMapsExplicitNoProcessAndMissingBinding(t *testing.T) {
	fixture := newResolverFixture(t)
	ctx := context.Background()

	t.Run("explicit no process", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		_, err := tx.ProcessBinding.Update().Where(
			processbinding.TenantIDEQ(fixture.tenant.ID),
			processbinding.BusinessSubTypeEQ("incident"),
		).SetConditions(map[string]any{"no_process": true}).Save(ctx)
		require.NoError(t, err)
		resolved, err := fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), validIncidentCommand("key-5", nil))
		require.NoError(t, err)
		require.True(t, resolved.Workflow.NoProcess)
		require.Nil(t, resolved.Workflow.DefinitionID)
		require.NoError(t, tx.Rollback())
	})

	t.Run("missing binding", func(t *testing.T) {
		tx := beginIntakeTestTx(t, fixture.client)
		_, err := tx.ProcessBinding.Delete().Where(
			processbinding.TenantIDEQ(fixture.tenant.ID),
			processbinding.BusinessSubTypeEQ("incident"),
		).Exec(ctx)
		require.NoError(t, err)
		_, err = fixture.resolver(nil).Resolve(ctx, tx, fixture.identity(), validIncidentCommand("key-6", nil))
		require.ErrorIs(t, err, ErrWorkflowBindingRequired)
		require.NoError(t, tx.Rollback())
	})
}
