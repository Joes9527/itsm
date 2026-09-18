package workflowcallback

import "context"

// Claim is transport metadata supplied only by the callback executor. Consumers
// must match every field against a live persisted claim in their own transaction.
type Claim struct {
	ID, TenantID, AttemptCount int
	ExecutionKey, LeaseOwner   string
}
type claimKey struct{}

func WithClaim(ctx context.Context, claim Claim) context.Context {
	return context.WithValue(ctx, claimKey{}, claim)
}

func CurrentClaim(ctx context.Context) (Claim, bool) {
	if ctx == nil {
		return Claim{}, false
	}
	claim, ok := ctx.Value(claimKey{}).(Claim)
	return claim, ok && claim.ID > 0 && claim.TenantID > 0 && claim.AttemptCount > 0 && claim.ExecutionKey != "" && claim.LeaseOwner != ""
}
