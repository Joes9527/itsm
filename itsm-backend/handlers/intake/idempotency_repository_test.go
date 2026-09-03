package intake

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/intakerequest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func newIntakeRepositoryClient(t *testing.T) *ent.Client {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	return enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", name))
}

func beginIntakeTestTx(t *testing.T, client *ent.Client) *ent.Tx {
	t.Helper()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	return tx
}

func testIdentity(tenantID int) Identity {
	return Identity{TenantID: tenantID, ActorID: 20, RequesterID: 20, Role: "requester", Channel: "itsm_web", TokenID: "jti-test"}
}

func TestIdempotencyClaimReplaysOnlyCompletedMatchingDigest(t *testing.T) {
	client := newIntakeRepositoryClient(t)
	defer client.Close()
	repo := NewIdempotencyRepository()
	ctx := context.Background()
	identity := testIdentity(1)

	tx := beginIntakeTestTx(t, client)
	receipt, outcome, err := repo.Claim(ctx, tx, identity, "same-key", "digest-a", CanonicalDigestVersion)
	require.NoError(t, err)
	require.Equal(t, ClaimInserted, outcome)
	require.NoError(t, repo.Complete(ctx, tx, identity.TenantID, receipt.ID, 501))
	require.NoError(t, tx.Commit())

	tx2 := beginIntakeTestTx(t, client)
	loaded, err := repo.LoadCompleted(ctx, tx2, identity, "same-key")
	require.NoError(t, err)
	require.NotNil(t, loaded.WorkItemID)
	require.Equal(t, 501, *loaded.WorkItemID)
	replayed, outcome, err := repo.Claim(ctx, tx2, identity, "same-key", "digest-a", CanonicalDigestVersion)
	require.NoError(t, err)
	require.Equal(t, ClaimReplay, outcome)
	require.NotNil(t, replayed.WorkItemID)
	require.Equal(t, 501, *replayed.WorkItemID)
	require.NoError(t, tx2.Rollback())
}

func TestIdempotencyClaimRejectsDifferentDigestOrVersion(t *testing.T) {
	client := newIntakeRepositoryClient(t)
	defer client.Close()
	repo := NewIdempotencyRepository()
	ctx := context.Background()
	identity := testIdentity(1)

	tx := beginIntakeTestTx(t, client)
	receipt, outcome, err := repo.Claim(ctx, tx, identity, "same-key", "digest-a", CanonicalDigestVersion)
	require.NoError(t, err)
	require.Equal(t, ClaimInserted, outcome)
	require.NoError(t, repo.Complete(ctx, tx, identity.TenantID, receipt.ID, 501))
	require.NoError(t, tx.Commit())

	for _, conflict := range []struct {
		name    string
		digest  string
		version string
	}{
		{name: "digest", digest: "digest-b", version: CanonicalDigestVersion},
		{name: "version", digest: "digest-a", version: "intake-v1"},
	} {
		t.Run(conflict.name, func(t *testing.T) {
			tx2 := beginIntakeTestTx(t, client)
			_, _, err := repo.Claim(ctx, tx2, identity, "same-key", conflict.digest, conflict.version)
			require.ErrorIs(t, err, ErrIdempotencyConflict)
			require.NoError(t, tx2.Rollback())
		})
	}
}

func TestIdempotencyClaimRollsBackWithCallerTransaction(t *testing.T) {
	client := newIntakeRepositoryClient(t)
	defer client.Close()
	repo := NewIdempotencyRepository()
	ctx := context.Background()
	identity := testIdentity(1)

	tx := beginIntakeTestTx(t, client)
	_, outcome, err := repo.Claim(ctx, tx, identity, "rollback-key", "digest-a", CanonicalDigestVersion)
	require.NoError(t, err)
	require.Equal(t, ClaimInserted, outcome)
	require.NoError(t, tx.Rollback())

	count, err := client.IntakeRequest.Query().Where(intakerequest.IdempotencyKeyEQ("rollback-key")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)

	tx2 := beginIntakeTestTx(t, client)
	_, outcome, err = repo.Claim(ctx, tx2, identity, "rollback-key", "digest-a", CanonicalDigestVersion)
	require.NoError(t, err)
	require.Equal(t, ClaimInserted, outcome)
	require.NoError(t, tx2.Rollback())
}

func TestIdempotencyCompleteRequiresPositiveWorkItemAndTenantOwnership(t *testing.T) {
	client := newIntakeRepositoryClient(t)
	defer client.Close()
	repo := NewIdempotencyRepository()
	ctx := context.Background()
	identity := testIdentity(1)

	tx := beginIntakeTestTx(t, client)
	receipt, _, err := repo.Claim(ctx, tx, identity, "complete-key", "digest-a", CanonicalDigestVersion)
	require.NoError(t, err)
	require.ErrorIs(t, repo.Complete(ctx, tx, identity.TenantID, receipt.ID, 0), ErrInternalFailure)
	require.ErrorIs(t, repo.Complete(ctx, tx, 2, receipt.ID, 501), ErrReferenceNotFound)
	require.NoError(t, tx.Rollback())
}

func TestIdempotencyClaimScopeIncludesTenantActorAndChannel(t *testing.T) {
	client := newIntakeRepositoryClient(t)
	defer client.Close()
	repo := NewIdempotencyRepository()
	ctx := context.Background()

	for index, identity := range []Identity{
		testIdentity(1),
		testIdentity(2),
		{TenantID: 1, ActorID: 21, RequesterID: 21, Role: "requester", Channel: "itsm_web"},
		{TenantID: 1, ActorID: 20, RequesterID: 20, Role: "requester", Channel: "kaf_web", Provider: "teams"},
	} {
		tx := beginIntakeTestTx(t, client)
		receipt, outcome, err := repo.Claim(ctx, tx, identity, "scoped-key", "digest-a", CanonicalDigestVersion)
		require.NoError(t, err)
		require.Equal(t, ClaimInserted, outcome)
		require.NoError(t, repo.Complete(ctx, tx, identity.TenantID, receipt.ID, 600+index))
		require.NoError(t, tx.Commit())
	}

	count, err := client.IntakeRequest.Query().Where(intakerequest.IdempotencyKeyEQ("scoped-key")).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, count)
}
