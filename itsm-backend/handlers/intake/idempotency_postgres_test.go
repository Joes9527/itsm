//go:build integration_postgres

package intake

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestIdempotencyPostgresConcurrentClaimCreatesOneReceipt(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("INTAKE_POSTGRES_TEST_DSN not set")
	}
	client, err := ent.Open("postgres", dsn)
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, client.Schema.Create(ctx), "INTAKE_POSTGRES_TEST_DSN must point to a disposable test database")
	key := fmt.Sprintf("intake-concurrent-%d", time.Now().UnixNano())
	identity := testIdentity(910001)
	repo := NewIdempotencyRepository()

	t.Cleanup(func() {
		_, _ = client.IntakeRequest.Delete().Where(
			intakerequest.TenantIDEQ(identity.TenantID),
			intakerequest.IdempotencyKeyEQ(key),
		).Exec(context.Background())
	})

	type result struct {
		outcome ClaimOutcome
		err     error
	}
	results := make(chan result, 20)
	var ready sync.WaitGroup
	ready.Add(20)
	start := make(chan struct{})
	for range 20 {
		go func() {
			ready.Done()
			<-start
			tx, err := client.Tx(ctx)
			if err != nil {
				results <- result{err: err}
				return
			}
			receipt, outcome, err := repo.Claim(ctx, tx, identity, key, "digest-a", CanonicalDigestVersion)
			if err != nil {
				_ = tx.Rollback()
				results <- result{err: err}
				return
			}
			if outcome == ClaimInserted {
				err = repo.Complete(ctx, tx, identity.TenantID, receipt.ID, 501)
			}
			if err == nil && outcome == ClaimInserted {
				err = tx.Commit()
			} else {
				rollbackErr := tx.Rollback()
				if err == nil {
					err = rollbackErr
				}
			}
			results <- result{outcome: outcome, err: err}
		}()
	}
	ready.Wait()
	close(start)

	inserted, replayed := 0, 0
	for range 20 {
		result := <-results
		require.NoError(t, result.err)
		switch result.outcome {
		case ClaimInserted:
			inserted++
		case ClaimReplay:
			replayed++
		default:
			t.Fatalf("unexpected claim outcome %q", result.outcome)
		}
	}
	require.Equal(t, 1, inserted)
	require.Equal(t, 19, replayed)

	count, err := client.IntakeRequest.Query().Where(
		intakerequest.TenantIDEQ(identity.TenantID),
		intakerequest.IdempotencyKeyEQ(key),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
