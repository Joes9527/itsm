package intake

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/handlers/common/workitemcreation"
	"testing"
)

func assertIntakePolicy(t *testing.T, err error, code workitemcreation.ErrorCode, status int, retry bool) {
	t.Helper()
	var detail *workitemcreation.IntakeError
	require.ErrorAs(t, err, &detail)
	require.Equal(t, code, detail.Code)
	require.Equal(t, status, detail.HTTPStatus)
	require.Equal(t, retry, detail.Retryable)
}
func queryFailure(reached *bool, cause error) ent.Interceptor {
	return ent.InterceptFunc(func(ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) { *reached = true; return nil, cause })
	})
}
func TestApplicationDefinedFieldValidationIsNotRetryable(t *testing.T) {
	for _, typ := range []string{"number", "select", "multiselect"} {
		t.Run(typ, func(t *testing.T) {
			client, s, i, c := graphFixture(t)
			ctx := context.Background()
			definition := client.FieldDefinition.Query().OnlyX(ctx)
			client.FieldDefinition.UpdateOneID(definition.ID).SetFieldType(typ).SetOptions([]interface{}{map[string]interface{}{"value": "allowed"}}).SaveX(ctx)
			_, err := s.Create(ctx, i, c)
			assertIntakePolicy(t, err, workitemcreation.DomainValidationFailed, 400, false)
			var detail *workitemcreation.IntakeError
			require.ErrorAs(t, err, &detail)
			require.NotEmpty(t, detail.FieldErrors)
			require.Equal(t, "formValues.location", detail.FieldErrors[0].Field)
			require.Zero(t, client.IntakeRequest.Query().CountX(ctx))
			require.Zero(t, client.Ticket.Query().CountX(ctx))
			require.Zero(t, client.Incident.Query().CountX(ctx))
			require.Zero(t, client.FieldValue.Query().CountX(ctx))
			require.Zero(t, client.IntakeResolutionSnapshot.Query().CountX(ctx))
			require.Zero(t, client.AuditLog.Query().CountX(ctx))
			require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
			require.Zero(t, client.WorkItemNumberSequence.Query().CountX(ctx))
		})
	}
}
func TestApplicationFieldPersistenceIsRetryable(t *testing.T) {
	client, s, i, c := graphFixture(t)
	reached := false
	cause := errors.New("field write outage")
	client.FieldValue.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			v, err := next.Mutate(ctx, m)
			if err != nil {
				return nil, err
			}
			reached = true
			return v, cause
		})
	})
	_, err := s.Create(context.Background(), i, c)
	require.True(t, reached)
	require.ErrorIs(t, err, cause)
	assertIntakePolicy(t, err, workitemcreation.InfrastructureUnavailable, 503, true)
	require.Zero(t, client.Ticket.Query().CountX(context.Background()))
}
func TestInfrastructureReferenceQueryFailuresAreRetryable(t *testing.T) {
	for _, stage := range []string{"base", "receipt", "audit", "snapshot", "snapshot_receipt", "snapshot_catalog", "snapshot_workflow", "snapshot_sla", "snapshot_ci", "snapshot_cti", "professional"} {
		t.Run(stage, func(t *testing.T) {
			client, s, i, c := graphFixture(t)
			ctx := context.Background()
			result, err := s.Create(ctx, i, c)
			require.NoError(t, err)
			receipt := client.IntakeRequest.Query().OnlyX(ctx)
			item := client.Ticket.GetX(ctx, result.WorkItemID)
			reached := false
			cause := errors.New("query outage")
			inject := queryFailure(&reached, cause)
			input := SnapshotInput{TenantID: i.TenantID, IntakeRequestID: receipt.ID, WorkItemID: item.ID, Channel: i.Channel, RecordClass: item.RecordClass, NoProcess: true, ResolverVersion: "test", RequestDigest: "test"}
			id := 999999
			switch stage {
			case "base":
				client.User.Intercept(inject)
			case "receipt", "audit", "snapshot":
				client.Ticket.Intercept(inject)
			case "snapshot_receipt":
				client.IntakeRequest.Intercept(inject)
			case "snapshot_catalog":
				input.CatalogItemID = &id
				client.ServiceCatalog.Intercept(inject)
			case "snapshot_workflow":
				input.NoProcess = false
				input.WorkflowDefinitionID = &id
				input.WorkflowDefinitionKey = "test"
				input.WorkflowDefinitionVersion = "1"
				client.ProcessDefinition.Intercept(inject)
			case "snapshot_sla":
				input.SLADefinitionID = &id
				client.SLADefinition.Intercept(inject)
			case "snapshot_ci":
				input.CIIDs = []int{id}
				client.ConfigurationItem.Intercept(inject)
			case "snapshot_cti":
				input.CTISnapshot = map[string]any{"categoryId": id}
				client.TicketCategory.Intercept(inject)
			case "professional":
				client.Incident.Intercept(inject)
			}
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			switch stage {
			case "base":
				err = validateDraftReferences(ctx, tx, &workitemcreation.WorkItemDraft{TenantID: i.TenantID, ActorID: i.ActorID, RequesterID: i.RequesterID})
			case "receipt":
				err = NewIdempotencyRepository().Complete(ctx, tx, i.TenantID, receipt.ID, item.ID)
			case "audit":
				err = NewAuditRepository().RecordCreated(ctx, tx, CreatedAuditInput{TenantID: i.TenantID, UserID: i.ActorID, WorkItemID: item.ID, RequestID: "test", Path: "/test", Method: "POST"})
			case "professional":
				err = validateProfessional(ctx, tx, item, &result.ProfessionalReference)
			default:
				_, err = NewSnapshotRepository().Create(ctx, tx, input)
			}
			require.True(t, reached, "query was not reached: %v", err)
			require.ErrorIs(t, err, cause)
			assertIntakePolicy(t, err, workitemcreation.InfrastructureUnavailable, 503, true)
		})
	}
}
func TestReplayQueryFailuresAreRetryable(t *testing.T) {
	for _, stage := range []string{"work_item", "incident", "snapshot", "outbox"} {
		t.Run(stage, func(t *testing.T) {
			client, s, i, c := graphFixture(t)
			ctx := context.Background()
			_, err := s.Create(ctx, i, c)
			require.NoError(t, err)
			reached := false
			cause := errors.New("replay query outage")
			inject := queryFailure(&reached, cause)
			switch stage {
			case "work_item":
				client.Ticket.Intercept(inject)
			case "incident":
				client.Incident.Intercept(inject)
			case "snapshot":
				client.IntakeResolutionSnapshot.Intercept(inject)
			case "outbox":
				client.OutboxEvent.Intercept(inject)
			}
			_, err = s.Create(ctx, i, c)
			require.True(t, reached)
			require.ErrorIs(t, err, cause)
			assertIntakePolicy(t, err, workitemcreation.InfrastructureUnavailable, 503, true)
		})
	}
}

func TestProfessionalReplayQueryFailureAndAbsenceAreDistinct(t *testing.T) {
	for _, class := range []string{"problem", "change_request", "service_request_item"} {
		for _, outage := range []bool{false, true} {
			name := class + "/absent"
			if outage {
				name = class + "/outage"
			}
			t.Run(name, func(t *testing.T) {
				client, s, i, c, n, creator := intakeFixture(t)
				ctx := context.Background()
				tx, err := client.Tx(ctx)
				require.NoError(t, err)
				plan, err := creator.Prepare(ctx, tx, workitemcreation.ResolvedIntake{Identity: i, Command: c, RecordClass: class})
				require.NoError(t, err)
				item, err := NewWorkItemCreator(n).CreateBase(ctx, tx, plan)
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
				reached := false
				cause := errors.New("extension query outage")
				if outage {
					inject := queryFailure(&reached, cause)
					switch class {
					case "problem":
						client.Problem.Intercept(inject)
					case "change_request":
						client.Change.Intercept(inject)
					case "service_request_item":
						client.ServiceRequest.Intercept(inject)
					}
				}
				tx, err = client.Tx(ctx)
				require.NoError(t, err)
				defer tx.Rollback()
				_, err = s.loadResult(ctx, tx, i.TenantID, item.ID, true)
				if outage {
					require.True(t, reached)
					require.ErrorIs(t, err, cause)
					assertIntakePolicy(t, err, workitemcreation.InfrastructureUnavailable, 503, true)
				} else {
					assertIntakePolicy(t, err, workitemcreation.InternalFailure, 500, false)
				}
			})
		}
	}
}
func TestReplayMissingGraphIsNotRetryable(t *testing.T) {
	for _, stage := range []string{"incident", "snapshot", "outbox"} {
		t.Run(stage, func(t *testing.T) {
			client, s, i, c := graphFixture(t)
			ctx := context.Background()
			_, err := s.Create(ctx, i, c)
			require.NoError(t, err)
			switch stage {
			case "incident":
				_, err = client.Incident.Delete().Exec(ctx)
			case "snapshot":
				_, err = client.IntakeResolutionSnapshot.Delete().Exec(ctx)
			case "outbox":
				_, err = client.OutboxEvent.Delete().Exec(ctx)
			}
			require.NoError(t, err)
			_, err = s.Create(ctx, i, c)
			assertIntakePolicy(t, err, workitemcreation.InternalFailure, 500, false)
		})
	}
}

func TestApplicationFieldDefinitionQueryIsRetryable(t *testing.T) {
	client, s, i, c := graphFixture(t)
	reached := false
	cause := errors.New("field definition query outage")
	client.FieldDefinition.Intercept(queryFailure(&reached, cause))
	_, err := s.Create(context.Background(), i, c)
	require.True(t, reached)
	require.ErrorIs(t, err, cause)
	assertIntakePolicy(t, err, workitemcreation.InfrastructureUnavailable, 503, true)
	require.Zero(t, client.Ticket.Query().CountX(context.Background()))
}
