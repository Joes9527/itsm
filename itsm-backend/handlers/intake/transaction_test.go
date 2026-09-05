package intake

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/repository/workitemnumber"
)

type graphResolver struct{ catalogID, workflowID int }

func (r graphResolver) Resolve(_ context.Context, _ *ent.Tx, i workitemcreation.Identity, c workitemcreation.CreateWorkItemCommand) (*workitemcreation.ResolvedIntake, error) {
	return &workitemcreation.ResolvedIntake{Identity: i, Command: c, RecordClass: c.RecordClass, Catalog: &workitemcreation.ResolvedCatalog{ID: r.catalogID, Version: "1", FormSchemaVersion: "1"}, Workflow: workitemcreation.ResolvedWorkflowBinding{DefinitionID: &r.workflowID, DefinitionKey: "test-process", DefinitionVersion: "1"}, ResolverVersion: "test-v1"}, nil
}

type graphCreator struct{ preparedCreator }

func (*graphCreator) RecordClass() string { return "incident" }
func (*graphCreator) CreateExtension(ctx context.Context, tx *ent.Tx, item *ent.Ticket, _ *workitemcreation.CreationPlan) (*workitemcreation.ProfessionalReference, error) {
	extension, err := tx.Incident.Create().SetWorkItemID(item.ID).SetIncidentNumber(item.TicketNumber).Save(ctx)
	if err != nil {
		return nil, err
	}
	return &workitemcreation.ProfessionalReference{Type: "incident", ID: extension.ID}, nil
}
func graphFixture(t *testing.T) (*ent.Client, *Service, workitemcreation.Identity, workitemcreation.CreateWorkItemCommand) {
	client, s, i, c, _, _ := intakeFixture(t)
	ctx := context.Background()
	catalog := client.ServiceCatalog.Create().SetName("VPN").SetTenantID(i.TenantID).SaveX(ctx)
	deployment := client.ProcessDeployment.Create().SetDeploymentID("test").SetDeploymentName("test").SetTenantID(i.TenantID).SaveX(ctx)
	workflow := client.ProcessDefinition.Create().SetKey("test-process").SetName("Test").SetVersion("1").SetTenantID(i.TenantID).SetDeploymentID(deployment.ID).SetBpmnXML([]byte("<definitions/> ")).SaveX(ctx)
	client.FieldDefinition.Create().SetTenantID(i.TenantID).SetEntityType("service_catalog").SetEntityID(catalog.ID).SetName("location").SetLabel("Location").SetFieldType("text").SaveX(ctx)
	s.resolver = graphResolver{catalog.ID, workflow.ID}
	s.registry = NewCreatorRegistry()
	require.NoError(t, s.registry.Register(&graphCreator{}))
	s.workItems = NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator())
	c.RecordClass = "incident"
	c.IntakeKind = "catalog_item"
	c.CatalogItemID = &catalog.ID
	c.CatalogVersion = "1"
	c.FormSchemaVersion = "1"
	c.FormValues = map[string]any{"location": "Shanghai"}
	return client, s, i, c
}
func TestApplicationEachWriteStageFailureRollsBackEntireGraph(t *testing.T) {
	for _, stage := range []string{"base", "extension", "field", "snapshot", "audit", "outbox", "complete"} {
		t.Run(stage, func(t *testing.T) {
			client, s, i, c := graphFixture(t)
			ctx := context.Background()
			reached := false
			injected := errors.New("injected " + stage)
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					value, err := next.Mutate(ctx, m)
					if err != nil {
						return value, err
					}
					reached = true
					return value, injected
				})
			}
			switch stage {
			case "base":
				client.Ticket.Use(hook)
			case "extension":
				client.Incident.Use(hook)
			case "field":
				client.FieldValue.Use(hook)
			case "snapshot":
				client.IntakeResolutionSnapshot.Use(hook)
			case "audit":
				client.AuditLog.Use(hook)
			case "outbox":
				client.OutboxEvent.Use(hook)
			case "complete":
				client.IntakeRequest.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if m.Op().Is(ent.OpUpdate) {
							return hook(next).Mutate(ctx, m)
						}
						return next.Mutate(ctx, m)
					})
				})
			}
			_, err := s.Create(ctx, i, c)
			require.Error(t, err)
			require.True(t, reached, "must reach the injected persistence stage: %v", err)
			require.Zero(t, client.Ticket.Query().CountX(ctx))
			require.Zero(t, client.Incident.Query().CountX(ctx))
			require.Zero(t, client.FieldValue.Query().CountX(ctx))
			require.Zero(t, client.IntakeResolutionSnapshot.Query().CountX(ctx))
			require.Zero(t, client.AuditLog.Query().CountX(ctx))
			require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
			require.Zero(t, client.IntakeRequest.Query().CountX(ctx))
			require.Zero(t, client.WorkItemNumberSequence.Query().CountX(ctx))
		})
	}
}
func TestApplicationGraphCommitsFieldsSnapshotAndOutbox(t *testing.T) {
	client, s, i, c := graphFixture(t)
	ctx := context.Background()
	result, err := s.Create(ctx, i, c)
	require.NoError(t, err)
	require.Equal(t, "pending", result.WorkflowStartStatus)
	require.Equal(t, 1, client.FieldValue.Query().CountX(ctx))
	require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx))
	require.Equal(t, 1, client.Incident.Query().CountX(ctx))
	snapshot := client.IntakeResolutionSnapshot.Query().OnlyX(ctx)
	require.Equal(t, "1", snapshot.CatalogVersion)
	client.ServiceCatalog.UpdateOneID(*c.CatalogItemID).SetName("changed later").ExecX(ctx)
	replay, err := s.Create(ctx, i, c)
	require.NoError(t, err)
	require.Equal(t, result.ProfessionalReference, replay.ProfessionalReference)
	require.Equal(t, result.Number, replay.Number)
	require.Equal(t, "1", client.IntakeResolutionSnapshot.Query().OnlyX(ctx).CatalogVersion)
}

func (graphResolver) ResolveWorkflow(context.Context, *ent.Tx, *workitemcreation.CreationPlan) error {
	return nil
}
