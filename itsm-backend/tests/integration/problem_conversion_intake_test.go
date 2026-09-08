package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func TestIntakeProblemConversionOwnsWholeGraph(t *testing.T) {
	for _, failure := range []string{"", "relation", "timeline", "audit"} {
		t.Run("failure="+failure, func(t *testing.T) {
			ctx := context.Background()
			f := newUnifiedIntakeFixture(t)
			client, app, identity, command := f.client, f.app, f.identity, f.command
			// Domain Prepare/CreateExtension must retain the source association inside
			// the same Intake transaction, including all audit/timeline records.

			source := client.Ticket.Create().SetTenantID(identity.TenantID).SetRequesterID(identity.ActorID).SetTitle("VPN outage").SetDescription("Connection loss").SetPriority("high").SetStatus("new").SetTicketNumber("TKT-SOURCE").SetRecordClass("incident").SaveX(ctx)
			incident := client.Incident.Create().SetWorkItemID(source.ID).SetSeverity("high").SetImpact("high").SetUrgency("high").SaveX(ctx)
			command.RecordClass, command.IntakeKind, command.Title = "problem", "problem", ""
			require.NoError(t, json.Unmarshal([]byte(fmt.Sprintf(`{"sourceIncidentId":%d,"rootCause":"sensitive diagnosis"}`, incident.ID)), &command.Problem))
			reached := false
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					value, err := next.Mutate(ctx, m)
					if err != nil {
						return value, err
					}
					reached = true
					return nil, errors.New("injected conversion " + failure)
				})
			}
			switch failure {
			case "relation":
				client.WorkItemRelation.Use(hook)
			case "timeline":
				client.IncidentEvent.Use(hook)
			case "audit":
				client.AuditLog.Use(hook)
			}
			result, err := app.Create(ctx, identity, command)
			if failure != "" {
				require.Error(t, err)
				require.True(t, reached)
				require.Equal(t, 1, client.Ticket.Query().CountX(ctx))
				require.Zero(t, client.Problem.Query().CountX(ctx))
				require.Zero(t, client.WorkItemRelation.Query().CountX(ctx))
				require.Zero(t, client.IncidentEvent.Query().CountX(ctx))
				require.Zero(t, client.AuditLog.Query().CountX(ctx))
				require.Zero(t, client.IntakeRequest.Query().CountX(ctx))
				return
			}
			require.NoError(t, err)
			created := client.Ticket.GetX(ctx, result.WorkItemID)
			require.Equal(t, "问题-VPN outage", created.Title)
			require.Equal(t, "Connection loss", created.Description)
			require.Equal(t, "high", created.Priority)
			require.Equal(t, 1, client.WorkItemRelation.Query().CountX(ctx))
			relation := client.WorkItemRelation.Query().OnlyX(ctx)
			require.Equal(t, source.ID, relation.SourceWorkItemID)
			require.Equal(t, result.WorkItemID, relation.TargetWorkItemID)
			require.Equal(t, "investigated_by", relation.RelationType)
			event := client.IncidentEvent.Query().OnlyX(ctx)
			require.Equal(t, incident.ID, event.IncidentID)
			require.Equal(t, 2, client.AuditLog.Query().CountX(ctx))
			audits := client.AuditLog.Query().AllX(ctx)
			for _, audit := range audits {
				if audit.RequestBody != nil {
					require.NotContains(t, *audit.RequestBody, "sensitive diagnosis")
				}
			}
			replay, err := app.Create(ctx, identity, command)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, result.WorkItemID, replay.WorkItemID)
			require.Equal(t, 1, client.WorkItemRelation.Query().CountX(ctx))
			require.Equal(t, 1, client.IncidentEvent.Query().CountX(ctx))
			require.Equal(t, "incident", client.Ticket.GetX(ctx, source.ID).RecordClass)
			command.IdempotencyKey = "another"
			_, err = app.Create(ctx, identity, command)
			require.ErrorIs(t, err, creation.ErrDomainValidationFailed)
			require.Equal(t, 1, client.Ticket.Query().Where(ticket.RecordClassEQ("problem")).CountX(ctx))
			command.IdempotencyKey = "one"
			// The caller retains Problem permissions, but loses Incident access. Even
			// a completed target receipt may no longer disclose the source graph.
			client.Permission.Update().SetResource("problem").ExecX(ctx)
			_, err = app.Create(ctx, identity, command)
			require.ErrorIs(t, err, creation.ErrPermissionDenied)
		})
	}
}
