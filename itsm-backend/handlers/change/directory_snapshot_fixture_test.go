package change

import (
	"context"
	"itsm-backend/ent"
)

// sameTransactionDirectory is a test-only view for single-database fixtures.
// It provides no evidence of PostgreSQL cross-role snapshot isolation.
type sameTransactionDirectory struct{}

func (sameTransactionDirectory) Open(_ context.Context, tx *ent.Tx, _ int) (*ent.Client, func() error, error) {
	return tx.Client(), func() error { return nil }, nil
}
