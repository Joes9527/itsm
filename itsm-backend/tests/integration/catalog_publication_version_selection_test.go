package integration

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/processdefinition"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	catalog "itsm-backend/handlers/service_catalog"
	"itsm-backend/service"
	"testing"
)

func TestA5FixCatalogKeepsExecutableVersionWhenSavingDraft(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := service.WithBPMNAccessScope(context.Background(), service.BPMNAccessScope{TenantID: f.identity.TenantID, UserID: f.identity.ActorID})
	logger := zap.NewNop().Sugar()
	engine := service.NewCustomProcessEngine(f.client, logger).(*service.CustomProcessEngine)
	xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="flow" isExecutable="true"><startEvent id="start"/><userTask id="work" assignee="${requester_id}"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="work"/><sequenceFlow id="b" sourceRef="work" targetRef="end"/></process></definitions>`
	definition, err := engine.ProcessDefinitionService().CreateProcessDefinition(ctx, &service.CreateProcessDefinitionRequest{Key: "catalog-flow", Name: "Flow", BPMNXML: xml, TenantID: f.identity.TenantID})
	require.NoError(t, err)
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(&service.TicketService{}))
	owner := catalog.NewService(catalog.NewEntRepository(f.client), f.client, logger, sameTransactionDirectory{})
	owner.SetCreatorRegistry(registry)
	owner.SetPublicationEngine(engine)
	published, err := owner.Create(ctx, f.identity.TenantID, dto.CreateServiceCatalogRequest{Name: "Consultation", Category: "IT", TargetClass: "generic", Status: "enabled", ProcessDefinitionKey: definition.Key})
	require.NoError(t, err)
	versions := service.NewBPMNVersionService(f.client, logger)
	draft, err := versions.CreateVersion(ctx, &service.CreateVersionRequest{ProcessDefinitionKey: definition.Key, BaseVersion: definition.Version, Name: "Draft", BPMNXML: xml, TenantID: f.identity.TenantID})
	require.NoError(t, err)
	afterSave, err := owner.Get(ctx, f.identity.TenantID, published.ID)
	require.NoError(t, err)
	require.Equal(t, published.CatalogVersion, afterSave.CatalogVersion, "saving inactive draft cannot change the executable contract")
	require.NoError(t, owner.ValidateForPublication(ctx, f.identity.TenantID, afterSave))
	create := func(key string, current *catalog.ServiceCatalog, wantVersion string) {
		cmd := creation.CreateWorkItemCommand{IntakeKind: creation.IntakeKindCatalogItem, RecordClass: "generic", CatalogItemID: &published.ID, CatalogVersion: current.CatalogVersion, FormSchemaVersion: current.FormSchemaVersion, Title: "Consultation", IdempotencyKey: key, Confirmation: "confirmed"}
		result, err := f.app.Create(ctx, f.identity, cmd)
		require.NoError(t, err)
		snapshot := f.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemIDEQ(result.WorkItemID)).OnlyX(ctx)
		require.Equal(t, wantVersion, snapshot.WorkflowDefinitionVersion)
	}
	create("after-save", afterSave, definition.Version)
	failActivation := true
	f.client.ProcessDefinition.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if active, ok := m.Field("is_active"); ok && active == true && failActivation {
				return nil, errors.New("injected activation failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	require.Error(t, engine.ProcessDefinitionService().SetProcessDefinitionActive(ctx, definition.Key, draft.Version, true))
	require.True(t, f.client.ProcessDefinition.GetX(ctx, definition.ID).IsActive)
	afterFailure, err := owner.Get(ctx, f.identity.TenantID, published.ID)
	require.NoError(t, err)
	require.Equal(t, published.CatalogVersion, afterFailure.CatalogVersion)
	create("after-failed-deploy", afterFailure, definition.Version)
	failActivation = false
	require.NoError(t, engine.ProcessDefinitionService().SetProcessDefinitionActive(ctx, definition.Key, draft.Version, true))
	require.Equal(t, 1, f.client.ProcessDefinition.Query().Where(processdefinition.KeyEQ(definition.Key), processdefinition.IsActiveEQ(true)).CountX(ctx))
	changed, err := owner.Get(ctx, f.identity.TenantID, published.ID)
	require.NoError(t, err)
	require.NotEqual(t, published.CatalogVersion, changed.CatalogVersion)
	old := creation.CreateWorkItemCommand{IntakeKind: creation.IntakeKindCatalogItem, RecordClass: "generic", CatalogItemID: &published.ID, CatalogVersion: published.CatalogVersion, FormSchemaVersion: published.FormSchemaVersion, Title: "Consultation", IdempotencyKey: "stale-after-activation", Confirmation: "confirmed"}
	_, err = f.app.Create(ctx, f.identity, old)
	require.ErrorIs(t, err, creation.ErrCatalogVersionConflict)
	create("after-activation", changed, draft.Version)
}
