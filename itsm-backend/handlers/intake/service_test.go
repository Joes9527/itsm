package intake

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/ent/schema"
	creation "itsm-backend/handlers/common/workitemcreation"
	cataloghandler "itsm-backend/handlers/service_catalog"
	srhandler "itsm-backend/handlers/service_request"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
	"strconv"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type resolverFixture struct {
	client  *ent.Client
	actor   creation.Identity
	catalog *ent.ServiceCatalog
	app     *Service
}

func newResolverFixture(t *testing.T) *resolverFixture {
	client, _, identity, _, _, _ := intakeFixture(t)
	return resolverFixtureWithClient(t, client, identity)
}
func resolverFixtureWithClient(t *testing.T, client *ent.Client, identity creation.Identity) *resolverFixture {
	t.Helper()
	ctx := context.Background()
	catalog := client.ServiceCatalog.Create().SetTenantID(identity.TenantID).SetName("VPN").SetTargetClass("service_request_item").SetServiceType("access").SaveX(ctx)
	client.FieldDefinition.Create().SetTenantID(identity.TenantID).SetEntityType("service_catalog").SetEntityID(catalog.ID).SetName("device_count").SetLabel("Devices").SetFieldType("number").SetRequired(true).SaveX(ctx)
	sla := client.SLADefinition.Create().SetTenantID(identity.TenantID).SetName("Access SLA").SetResponseTime(30).SetResolutionTime(240).SaveX(ctx)
	deployment := client.ProcessDeployment.Create().SetTenantID(identity.TenantID).SetDeploymentID("workflow-" + strconv.Itoa(identity.TenantID)).SetDeploymentName("Workflow").SaveX(ctx)
	client.ProcessDefinition.Create().SetTenantID(identity.TenantID).SetDeploymentID(deployment.ID).SetKey("vpn").SetName("VPN").SetVersion("1").SetIsActive(true).SetIsLatest(true).SetBpmnXML([]byte("<definitions/>")).SaveX(ctx)
	client.ProcessBinding.Create().SetTenantID(identity.TenantID).SetBusinessType("service_request").SetIsDefault(true).SetProcessDefinitionKey("vpn").SetSLAPolicyID(strconv.Itoa(sla.ID)).SaveX(ctx)
	logger := zap.NewNop().Sugar()
	resolver := NewResolver(cataloghandler.NewService(nil, client, logger, nil), service.NewProcessBindingService(client), service.NewConfigurationItemService(client, logger, nil, nil), service.NewTicketCategoryService(client))
	registry := NewCreatorRegistry()
	domain := srhandler.NewService(nil, client, logger, service.NewApprovalChainResolver(client, logger))
	require.NoError(t, registry.Register(domain))
	return &resolverFixture{client: client, actor: identity, catalog: catalog, app: NewService(client, resolver, registry, NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), sameTransactionDirectory{})}
}
func (f *resolverFixture) catalogCommand(t *testing.T) creation.CreateWorkItemCommand {
	t.Helper()
	ctx := context.Background()
	tx, err := f.client.Tx(ctx)
	require.NoError(t, err)
	owner := cataloghandler.NewService(nil, f.client, zap.NewNop().Sugar(), nil)
	catalog, _, err := owner.ResolveCreationCatalog(ctx, tx, f.actor, f.catalog.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	return creation.CreateWorkItemCommand{RecordClass: "service_request_item", IntakeKind: "catalog_item", Confirmation: "confirmed", CatalogItemID: &f.catalog.ID, CatalogVersion: catalog.Version, FormSchemaVersion: catalog.FormSchemaVersion, Title: "VPN request", IdempotencyKey: "one", FormValues: map[string]any{"device_count": json.Number("2")}}
}
func installIntakeMutationFailure(client *ent.Client, stage string) *bool {
	reached := new(bool)
	hook := func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, m)
			if err != nil {
				return value, err
			}
			*reached = true
			return value, errors.New("injected " + stage + " failure")
		})
	}
	switch stage {
	case "base":
		client.Ticket.Use(hook)
	case "extension":
		client.ServiceRequest.Use(hook)
	case "field value":
		client.FieldValue.Use(hook)
	case "SLA":
		client.Ticket.Use(func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if m.Op().Is(ent.OpUpdateOne) {
					return hook(next).Mutate(ctx, m)
				}
				return next.Mutate(ctx, m)
			})
		})
	case "audit":
		client.AuditLog.Use(hook)
	case "snapshot":
		client.IntakeResolutionSnapshot.Use(hook)
	case "workflow start":
		client.OutboxEvent.Use(hook)
	}
	return reached
}
func assertNoIntakeGraph(t *testing.T, client *ent.Client) {
	t.Helper()
	ctx := context.Background()
	require.Zero(t, client.Ticket.Query().CountX(ctx))
	require.Zero(t, client.ServiceRequest.Query().CountX(ctx))
	require.Zero(t, client.Incident.Query().CountX(ctx))
	require.Zero(t, client.Problem.Query().CountX(ctx))
	require.Zero(t, client.Change.Query().CountX(ctx))
	require.Zero(t, client.FieldValue.Query().CountX(ctx))
	require.Zero(t, client.AuditLog.Query().CountX(ctx))
	require.Zero(t, client.IntakeResolutionSnapshot.Query().CountX(ctx))
	require.Zero(t, client.IntakeRequest.Query().CountX(ctx))
	require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
	require.Zero(t, client.WorkItemNumberSequence.Query().CountX(ctx))
}
func TestServiceCreateRollbackFaultMatrix(t *testing.T) {
	for _, stage := range []string{"base", "extension", "field value", "SLA", "audit", "snapshot", "workflow start"} {
		t.Run(stage, func(t *testing.T) {
			f := newResolverFixture(t)
			command := f.catalogCommand(t)
			reached := installIntakeMutationFailure(f.client, stage)
			_, err := f.app.Create(context.Background(), f.actor, command)
			require.Error(t, err)
			require.True(t, *reached, "fault must reach real mutation: %v", err)
			var cause error = err
			for errors.Unwrap(cause) != nil {
				cause = errors.Unwrap(cause)
			}
			require.Contains(t, cause.Error(), "injected "+stage+" failure")
			assertNoIntakeGraph(t, f.client)
		})
	}
}
func TestServiceRequestFieldFailureRollsBackCreation(t *testing.T) {
	f := newResolverFixture(t)
	command := f.catalogCommand(t)
	reached := installIntakeMutationFailure(f.client, "field value")
	_, err := f.app.Create(context.Background(), f.actor, command)
	require.Error(t, err)
	require.True(t, *reached)
	require.Contains(t, errors.Unwrap(err).Error(), "injected field value failure")
	assertNoIntakeGraph(t, f.client)
}
func TestAuthoritativeGraphIncludesSLAAndRejectsUnknownFields(t *testing.T) {
	f := newResolverFixture(t)
	command := f.catalogCommand(t)
	result, err := f.app.Create(context.Background(), f.actor, command)
	require.NoError(t, err)
	item := f.client.Ticket.GetX(context.Background(), result.WorkItemID)
	require.False(t, item.SLAResponseDeadline.IsZero())
	require.False(t, item.SLAResolutionDeadline.IsZero())
	require.Equal(t, 1, f.client.FieldValue.Query().CountX(context.Background()))
	require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(context.Background()))
	command.IdempotencyKey = "other"
	command.FormValues["undeclared"] = "lost"
	_, err = f.app.Create(context.Background(), f.actor, command)
	require.ErrorIs(t, err, creation.ErrDomainValidationFailed)
	require.Equal(t, 1, f.client.Ticket.Query().CountX(context.Background()))
}

func TestRealResolverReplayPreservesConfirmedCatalogSnapshot(t *testing.T) {
	f := newResolverFixture(t)
	command := f.catalogCommand(t)
	first, err := f.app.Create(context.Background(), f.actor, command)
	require.NoError(t, err)
	f.client.ServiceCatalog.UpdateOneID(f.catalog.ID).SetDescription("New definition").ExecX(context.Background())
	replay, err := f.app.Create(context.Background(), f.actor, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	command.IdempotencyKey = "changed"
	_, err = f.app.Create(context.Background(), f.actor, command)
	require.ErrorIs(t, err, creation.ErrCatalogVersionConflict)
	require.Equal(t, 1, f.client.Ticket.Query().CountX(context.Background()))
	require.Equal(t, command.CatalogVersion, f.client.IntakeResolutionSnapshot.Query().OnlyX(context.Background()).CatalogVersion)
}

func TestRealResolverMissingRequiredFieldFailsBeforeAllocation(t *testing.T) {
	f := newResolverFixture(t)
	command := f.catalogCommand(t)
	command.FormValues = nil
	_, err := f.app.Create(context.Background(), f.actor, command)
	require.ErrorIs(t, err, creation.ErrDomainValidationFailed)
	assertNoIntakeGraph(t, f.client)
}

func TestRealResolverRoutingUsesRequestAmount(t *testing.T) {
	f := newResolverFixture(t)
	ctx := context.Background()
	f.client.ProcessBinding.Update().SetConditions(map[string]any{"min_amount": 100}).ExecX(ctx)
	command := f.catalogCommand(t)
	command.ServiceRequest = &creation.ServiceRequestInput{Amount: json.Number("100.0000000000000001")}
	_, err := f.app.Create(ctx, f.actor, command)
	require.NoError(t, err)
	command.IdempotencyKey = "under"
	command.ServiceRequest.Amount = json.Number("99.9999999999999999")
	_, err = f.app.Create(ctx, f.actor, command)
	require.ErrorIs(t, err, creation.ErrWorkflowBindingRequired)
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
}

func TestRealResolverUnknownRoutingOperatorCannotFallThrough(t *testing.T) {
	f := newResolverFixture(t)
	ctx := context.Background()
	f.client.ProcessBinding.Create().SetTenantID(f.actor.TenantID).SetBusinessType("service_request").SetProcessDefinitionKey("vpn").SetPriority(1000).SetConditions(map[string]any{"amount": map[string]any{"unknown": 1}}).SaveX(ctx)
	command := f.catalogCommand(t)
	_, err := f.app.Create(ctx, f.actor, command)
	require.ErrorIs(t, err, creation.ErrDomainValidationFailed)
	assertNoIntakeGraph(t, f.client)
}

func TestRealResolverOutagesStayRetryable(t *testing.T) {
	for _, target := range []string{"routing", "catalog", "SLA"} {
		t.Run(target, func(t *testing.T) {
			f := newResolverFixture(t)
			command := f.catalogCommand(t)
			reached := false
			f.client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
					match := false
					switch q.(type) {
					case *ent.ProcessBindingQuery:
						match = target == "routing"
					case *ent.ServiceCatalogQuery:
						match = target == "catalog"
					case *ent.SLADefinitionQuery:
						match = target == "SLA"
					}
					if match {
						reached = true
						return nil, errors.New("database unavailable")
					}
					return next.Query(ctx, q)
				})
			}))
			_, err := f.app.Create(context.Background(), f.actor, command)
			require.True(t, reached)
			require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
			var policy *creation.IntakeError
			require.ErrorAs(t, err, &policy)
			require.Equal(t, 503, policy.HTTPStatus)
			require.True(t, policy.Retryable)
		})
	}
}

func TestServiceRequestCloudCatalogRequiresCITypeBeforeAllocation(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			f := newResolverFixture(t)
			ctx := context.Background()
			update := f.client.ServiceCatalog.UpdateOne(f.catalog).SetCloudServiceID(42)
			if configured {
				ciType := f.client.CIType.Create().SetTenantID(f.actor.TenantID).SetName("Provisioned service").SaveX(ctx)
				update.SetCiTypeID(ciType.ID)
			}
			update.ExecX(ctx)
			command := f.catalogCommand(t)
			allocator := &testAllocator{}
			f.app.workItems = NewWorkItemCreator(allocator)
			_, err := f.app.Create(ctx, f.actor, command)
			if configured {
				require.NoError(t, err)
				require.Equal(t, 1, allocator.calls)
				return
			}
			require.ErrorIs(t, err, creation.ErrDomainValidationFailed)
			require.Zero(t, allocator.calls)
			assertNoIntakeGraph(t, f.client)
		})
	}
}

func TestServiceRequestApprovalConfigurationAndStorageErrors(t *testing.T) {
	for _, malformed := range []bool{true, false} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			f := newResolverFixture(t)
			ctx := context.Background()
			chain := f.client.ApprovalChain.Create().SetTenantID(f.actor.TenantID).SetName("Approval").SetEntityType("service_request").SetChain([]schema.ApprovalChainStep{}).SaveX(ctx)
			command := f.catalogCommand(t)
			if malformed {
				db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
				require.NoError(t, err)
				defer db.Close()
				_, err = db.ExecContext(ctx, `UPDATE approval_chains SET chain = '{}' WHERE id = ?`, chain.ID)
				require.NoError(t, err)
			} else {
				f.client.ApprovalChain.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
					return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) { return nil, errors.New("approval storage outage") })
				}))
			}
			allocator := &testAllocator{}
			f.app.workItems = NewWorkItemCreator(allocator)
			_, err := f.app.Create(ctx, f.actor, command)
			var policy *creation.IntakeError
			require.ErrorAs(t, err, &policy)
			if malformed {
				require.Equal(t, 400, policy.HTTPStatus)
				require.False(t, policy.Retryable)
			} else {
				require.Equal(t, 503, policy.HTTPStatus)
				require.True(t, policy.Retryable)
			}
			require.Zero(t, allocator.calls)
			assertNoIntakeGraph(t, f.client)
		})
	}
}

func TestPreparedApprovalRequirementIsCheckedAfterRouting(t *testing.T) {
	for _, noProcess := range []bool{true, false} {
		t.Run(fmt.Sprint(noProcess), func(t *testing.T) {
			f := newResolverFixture(t)
			ctx := context.Background()
			f.client.ServiceCatalog.UpdateOne(f.catalog).SetRequiresApproval(false).ExecX(ctx)
			f.client.ProcessBinding.Update().SetConditions(map[string]any{"no_process": noProcess}).ExecX(ctx)
			f.client.ApprovalChain.Create().SetTenantID(f.actor.TenantID).SetName("Required approval").SetEntityType("service_request").SetChain([]schema.ApprovalChainStep{{Level: 1, Role: "manager", IsRequired: true}}).SaveX(ctx)
			command := f.catalogCommand(t)
			allocator := &testAllocator{}
			f.app.workItems = NewWorkItemCreator(allocator)
			result, err := f.app.Create(ctx, f.actor, command)
			if noProcess {
				require.ErrorIs(t, err, creation.ErrWorkflowBindingRequired)
				require.Zero(t, allocator.calls)
				assertNoIntakeGraph(t, f.client)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "pending", result.WorkflowStartStatus)
			require.Contains(t, f.client.ServiceRequest.Query().OnlyX(ctx).FormData, "_approval_chain")
		})
	}
}

func TestSerializationRetryRollsBackTheWholeCreationAttempt(t *testing.T) {
	for _, failCount := range []int{2, 3} {
		t.Run(fmt.Sprint(failCount), func(t *testing.T) {
			f := newResolverFixture(t)
			ctx := context.Background()
			command := f.catalogCommand(t)
			attempts := 0
			f.client.Ticket.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					value, err := next.Mutate(ctx, m)
					if err != nil || !m.Op().Is(ent.OpCreate) {
						return value, err
					}
					attempts++
					if attempts <= failCount {
						return value, &pq.Error{Code: "40001", Message: "injected serialization failure after base mutation"}
					}
					return value, nil
				})
			})
			_, err := f.app.Create(ctx, f.actor, command)
			require.Equal(t, 3, attempts)
			if failCount == 3 {
				require.ErrorIs(t, err, creation.ErrInfrastructureUnavailable)
				var policy *creation.IntakeError
				require.ErrorAs(t, err, &policy)
				require.Equal(t, 503, policy.HTTPStatus)
				require.True(t, policy.Retryable)
				assertNoIntakeGraph(t, f.client)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
			require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
			require.Equal(t, 1, f.client.ServiceRequest.Query().CountX(ctx))
			require.Equal(t, int64(1), f.client.WorkItemNumberSequence.Query().OnlyX(ctx).LastValue)
		})
	}
}

func TestSerializationRetryRechecksRevokedPermission(t *testing.T) {
	f := newResolverFixture(t)
	ctx := context.Background()
	command := f.catalogCommand(t)
	baseWrites := 0
	revoked := false
	f.client.Ticket.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, m)
			if err != nil || !m.Op().Is(ent.OpCreate) {
				return value, err
			}
			baseWrites++
			tx, err := m.(*ent.TicketMutation).Tx()
			if err != nil {
				return nil, err
			}
			tx.OnRollback(func(next ent.Rollbacker) ent.Rollbacker {
				return ent.RollbackFunc(func(ctx context.Context, tx *ent.Tx) error {
					if err := next.Rollback(ctx, tx); err != nil {
						return err
					}
					_, err := f.client.RolePermission.Delete().Exec(ctx)
					revoked = err == nil
					return err
				})
			})
			return value, &pq.Error{Code: "40001", Message: "injected transaction conflict"}
		})
	})
	_, err := f.app.Create(ctx, f.actor, command)
	require.True(t, revoked, "revoke committed after first attempt rolled back")
	require.ErrorIs(t, err, creation.ErrPermissionDenied)
	require.Equal(t, 1, baseWrites, "second attempt must authorize before allocating/writing")
	assertNoIntakeGraph(t, f.client)
}
