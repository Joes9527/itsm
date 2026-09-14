package service_request

import (
	"context"
	"errors"
	"fmt"

	"itsm-backend/common"
	"itsm-backend/common/executionscope"
	"itsm-backend/database"
	"itsm-backend/ent"
)

// The caller owns business authorization and this same write transaction.
func requireRequestExecutionTx(ctx context.Context, tx *ent.Tx, policy *database.ExecutionPolicy, tenantID, workItemID int) error {
	failure := func(err error) error {
		if err == nil {
			return nil
		}
		if errors.Is(err, executionscope.ErrDenied) {
			return common.NewForbiddenError("requested item execution scope denied")
		}
		return fmt.Errorf("requested item execution scope: %w", err)
	}
	if err := policy.BindEnt(ctx, tx, tenantID); err != nil {
		return failure(err)
	}
	return failure(policy.RequireEntMembers(ctx, tx, tenantID, workItemID))
}
