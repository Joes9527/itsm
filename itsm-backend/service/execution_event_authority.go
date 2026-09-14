package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/pkg/eventbus"
)

// ExecutionEventAuthority verifies the persistent producer behind a Stream message.
// This read transaction does not replace the consumer's own write transaction.
type ExecutionEventAuthority struct {
	client *ent.Client
	policy *database.ExecutionPolicy
}

func NewExecutionEventAuthority(client *ent.Client, policy *database.ExecutionPolicy) *ExecutionEventAuthority {
	return &ExecutionEventAuthority{client: client, policy: policy}
}
func (a *ExecutionEventAuthority) ValidateEvent(ctx context.Context, ref executionscope.Ref, env eventbus.Envelope) error {
	if a == nil || a.client == nil || ctx == nil {
		return executionscope.ErrDenied
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != ref.TenantID {
		return executionscope.ErrDenied
	}
	if tenantctx.IsSystemBypass(ctx) {
		return executionscope.ErrDenied
	}
	ctx = tenantctx.WithTenantID(ctx, ref.TenantID)
	tx, err := a.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return a.ValidateEventTx(ctx, tx, ref, env)
}

// ValidateEventTx uses only the caller's transaction. The consuming owner must
// use this again in the same transaction as its durable write and receipt.
func (a *ExecutionEventAuthority) ValidateEventTx(ctx context.Context, tx *ent.Tx, ref executionscope.Ref, env eventbus.Envelope) error {
	if a == nil || a.client == nil || a.policy == nil || ctx == nil || env.Execution == nil {
		return executionscope.ErrDenied
	}
	frozen, err := a.policy.EventRef(ref.TenantID)
	if err != nil || frozen != ref || env.TenantID != strconv.Itoa(ref.TenantID) || env.Execution.ScopeID != ref.ScopeID || env.Execution.DeploymentID != ref.DeploymentID {
		return executionscope.ErrDenied
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != ref.TenantID {
		return executionscope.ErrDenied
	}
	if tenantctx.IsSystemBypass(ctx) {
		return executionscope.ErrDenied
	}
	if tx == nil {
		return executionscope.ErrDenied
	}
	if tenant, ok := tenantctx.TenantID(ctx); !ok || tenant != ref.TenantID {
		return executionscope.ErrDenied
	}
	if err := a.policy.BindEnt(ctx, tx, ref.TenantID); err != nil {
		return err
	}
	if err := a.policy.RequireEntMembers(ctx, tx, ref.TenantID, env.Execution.WorkItemID); err != nil {
		return err
	}
	row, err := tx.OutboxEvent.Query().Where(outboxevent.EventIDEQ(env.EventID), outboxevent.TenantIDEQ(ref.TenantID), outboxevent.ExecutionWorkItemIDEQ(env.Execution.WorkItemID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("persistent event source unavailable: %w", err)
	}
	// SLA is currently the sole Stream producer with a persistent WorkItem source.
	// Other declared event types fail closed until their owning contract exists.
	if env.EventType != slaBreachEventType {
		return fmt.Errorf("unsupported persistent event type")
	}
	expected, err := persistedSLABreach(row)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, env.Payload); err != nil {
		return err
	}
	if !bytes.Equal(payload, compact.Bytes()) || !env.OccurredAt.Equal(expected.OccurredAt()) {
		return fmt.Errorf("event differs from persistent producer fact")
	}
	return nil
}
