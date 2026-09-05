package intake

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/handlers/common/workitemcreation"
	"testing"
)

func TestReceiptScopesAndCrossTenantCompletion(t *testing.T) {
	client, s, i, c, _, _ := intakeFixture(t)
	ctx := context.Background()
	result, err := s.Create(ctx, i, c)
	require.NoError(t, err)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	repo := NewIdempotencyRepository()
	receipt, err := repo.LoadCompleted(ctx, tx, i, c.IdempotencyKey)
	require.NoError(t, err)
	for _, scope := range []string{"tenant", "actor", "channel"} {
		other := i
		switch scope {
		case "tenant":
			other.TenantID++
		case "actor":
			other.ActorID++
		case "channel":
			other.Channel = "itsm_api"
		}
		_, err = repo.LoadCompleted(ctx, tx, other, c.IdempotencyKey)
		require.ErrorIs(t, err, workitemcreation.ErrReferenceNotFound)
	}
	require.ErrorIs(t, repo.Complete(ctx, tx, i.TenantID+1, receipt.ID, result.WorkItemID), workitemcreation.ErrReferenceNotFound)
	other := i
	other.TenantID++
	claimed, _, err := repo.Claim(ctx, tx, other, "other", "digest", workitemcreation.CanonicalDigestVersion)
	require.NoError(t, err)
	require.ErrorIs(t, repo.Complete(ctx, tx, other.TenantID, claimed.ID, result.WorkItemID), workitemcreation.ErrReferenceNotFound)
}
func TestSnapshotRejectsCrossTenantAssociationsAndSensitiveEvidence(t *testing.T) {
	client, s, i, c, _, _ := intakeFixture(t)
	ctx := context.Background()
	result, err := s.Create(ctx, i, c)
	require.NoError(t, err)
	receipt := client.IntakeRequest.Query().OnlyX(ctx)
	for _, scenario := range []string{"tenant", "receipt", "work_item", "sensitive", "catalog", "workflow", "ci", "sla", "cti"} {
		t.Run(scenario, func(t *testing.T) {
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			id := 999999
			input := SnapshotInput{TenantID: i.TenantID, IntakeRequestID: receipt.ID, WorkItemID: result.WorkItemID, Channel: i.Channel, RecordClass: "generic", NoProcess: true, ResolverVersion: "test", RequestDigest: "digest"}
			switch scenario {
			case "tenant":
				input.TenantID++
			case "receipt":
				input.IntakeRequestID = id
			case "work_item":
				input.WorkItemID = id
			case "sensitive":
				input.CTISnapshot = map[string]any{"password": "secret"}
			case "catalog":
				input.CatalogItemID = &id
			case "workflow":
				input.NoProcess = false
				input.WorkflowDefinitionID = &id
				input.WorkflowDefinitionKey = "missing"
				input.WorkflowDefinitionVersion = "1"
			case "ci":
				input.CIIDs = []int{id}
			case "sla":
				input.SLADefinitionID = &id
			case "cti":
				input.CTISnapshot = map[string]any{"categoryId": id}
			}
			_, err = NewSnapshotRepository().Create(ctx, tx, input)
			if scenario == "sensitive" {
				require.ErrorIs(t, err, workitemcreation.ErrInvalidCommand)
			} else {
				require.ErrorIs(t, err, workitemcreation.ErrReferenceNotFound)
			}
			require.Equal(t, 1, tx.IntakeResolutionSnapshot.Query().CountX(ctx))
		})
	}
}
func TestBaseWriterRejectsCrossTenantAssignee(t *testing.T) {
	client, s, i, c, _, p := intakeFixture(t)
	ctx := context.Background()
	other := client.Tenant.Create().SetName("Other").SetCode("other").SaveX(ctx)
	user := client.User.Create().SetTenantID(other.ID).SetUsername("other").SetName("Other").SetEmail("other@example.test").SetPasswordHash("test").SaveX(ctx)
	p.mutate = func(plan *workitemcreation.CreationPlan) { plan.WorkItem.AssigneeID = &user.ID }
	_, err := s.Create(ctx, i, c)
	require.ErrorIs(t, err, workitemcreation.ErrReferenceNotFound)
	require.Zero(t, client.Ticket.Query().CountX(ctx))
}
