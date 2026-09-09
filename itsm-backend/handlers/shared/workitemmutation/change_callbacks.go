package workitemmutation

import (
	"context"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
)

// Mutation owners pair this RR read with their Ticket write fence. A mere
// snapshot read or row lock would miss a concurrently accepted callback.
func RequireSettledChangeCallbacks(ctx context.Context, tx *ent.Tx, tenantID, itemID int) error {
	ids, err := tx.ProcessInstance.Query().Where(processinstance.TenantID(tenantID), processinstance.BusinessType("change"), processinstance.BusinessID(itemID), processinstance.BusinessKey(fmt.Sprintf("change:%d", itemID))).IDs(ctx)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	unresolved, err := tx.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.TenantID(tenantID), processcallbackoutbox.ProcessInstanceIDIn(ids...), processcallbackoutbox.StatusNEQ("completed")).Exist(ctx)
	if err != nil {
		return err
	}
	if unresolved {
		return &UnresolvedChangeCallbackError{}
	}
	return nil
}

type UnresolvedChangeCallbackError struct{}

func (*UnresolvedChangeCallbackError) Error() string { return "prior callback is unresolved" }
