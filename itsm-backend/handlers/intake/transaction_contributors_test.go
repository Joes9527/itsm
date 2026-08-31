package intake

import (
	"context"
	"testing"

	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/intakeresolutionsnapshot"

	"github.com/stretchr/testify/require"
)

func TestSnapshotRepositoryCreatesOnlyFrozenResolutionEvidence(t *testing.T) {
	client := newIntakeRepositoryClient(t)
	defer client.Close()
	ctx := context.Background()
	tx := beginIntakeTestTx(t, client)
	catalogID, workflowID, slaID := 11, 22, 33

	repo := NewSnapshotRepository()
	created, err := repo.Create(ctx, tx, SnapshotInput{
		TenantID:                  1,
		IntakeRequestID:           101,
		WorkItemID:                501,
		Channel:                   "kaf_web",
		SourceProvider:            "teams",
		SourceEventID:             "event-1",
		SourceConversationID:      "conversation-1",
		CatalogItemID:             &catalogID,
		CatalogVersion:            "2026-08-31T10:00:00Z",
		RecordClass:               RecordClassIncident,
		CTISnapshot:               map[string]any{"categoryId": 7, "typeId": 8},
		CIIDs:                     []int{3, 9},
		FormSchemaVersion:         "schema-v3",
		WorkflowDefinitionID:      &workflowID,
		WorkflowDefinitionKey:     "incident-default",
		WorkflowDefinitionVersion: "4",
		SLADefinitionID:           &slaID,
		ResolverVersion:           "resolver-v1",
		RequestDigest:             "digest-a",
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	stored, err := client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.IDEQ(created.ID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "teams", stored.SourceProvider)
	require.Equal(t, []int{3, 9}, stored.CiIds)
	require.Equal(t, "resolver-v1", stored.ResolverVersion)
	require.False(t, stored.NoProcess)
}

func TestSnapshotRepositoryRejectsSensitiveOrAuthoritativeMetadata(t *testing.T) {
	client := newIntakeRepositoryClient(t)
	defer client.Close()
	ctx := context.Background()
	repo := NewSnapshotRepository()

	for _, forbidden := range []string{"title", "description", "requester", "formValues", "token", "secret", "authorization", "password"} {
		t.Run(forbidden, func(t *testing.T) {
			tx := beginIntakeTestTx(t, client)
			_, err := repo.Create(ctx, tx, SnapshotInput{
				TenantID: 1, IntakeRequestID: 101, WorkItemID: 501, Channel: "itsm_web",
				RecordClass: RecordClassIncident, CTISnapshot: map[string]any{forbidden: "must-not-persist"},
				NoProcess: true, ResolverVersion: "resolver-v1", RequestDigest: "digest-a",
			})
			require.ErrorIs(t, err, ErrInvalidCommand)
			require.NoError(t, tx.Rollback())
		})
	}
}

func TestSnapshotRepositoryRequiresFrozenWorkflowOrExplicitNoProcess(t *testing.T) {
	client := newIntakeRepositoryClient(t)
	defer client.Close()
	ctx := context.Background()
	tx := beginIntakeTestTx(t, client)

	_, err := NewSnapshotRepository().Create(ctx, tx, SnapshotInput{
		TenantID: 1, IntakeRequestID: 101, WorkItemID: 501, Channel: "itsm_web",
		RecordClass: RecordClassIncident, ResolverVersion: "resolver-v1", RequestDigest: "digest-a",
	})
	require.ErrorIs(t, err, ErrWorkflowBindingRequired)
	require.NoError(t, tx.Rollback())
}

func TestAuditRepositoryRecordsCreatedWithoutRequestBody(t *testing.T) {
	client := newIntakeRepositoryClient(t)
	defer client.Close()
	ctx := context.Background()
	tx := beginIntakeTestTx(t, client)

	repo := NewAuditRepository()
	err := repo.RecordCreated(ctx, tx, CreatedAuditInput{
		TenantID: 1, UserID: 20, WorkItemID: 501, RequestID: "request-1",
		Path: "/api/v1/intake/work-items", Method: "POST", StatusCode: 201,
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	stored, err := client.AuditLog.Query().Where(auditlog.RequestIDEQ("request-1")).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "work_item:501", stored.Resource)
	require.Equal(t, "intake.created", stored.Action)
	require.Nil(t, stored.RequestBody)
}
