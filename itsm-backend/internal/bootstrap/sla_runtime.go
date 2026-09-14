package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"itsm-backend/common/tenantctx"
	"itsm-backend/ent/tenant"
	"itsm-backend/service"
)

type slaViolationMonitor interface {
	CheckSLAViolations(context.Context, int) (*service.SLACheckStats, error)
}

// runSLACycle is shared by the timer and direct lifecycle verification. System
// discovery never becomes the authority of a tenant's business transaction.
func (app *Application) runSLACycle(ctx context.Context) error {
	if app.slaMonitor == nil || app.executionPolicy == nil {
		return fmt.Errorf("SLA runtime dependencies are required")
	}
	ids, err := app.scheduledTenantIDs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if _, err := app.slaMonitor.CheckSLAViolations(tenantctx.WithTenantID(ctx, id), id); err != nil {
			failures = append(failures, fmt.Errorf("SLA tenant %d: %w", id, err))
		}
	}
	return errors.Join(failures...)
}

// Both scheduled domains share frozen discovery, never an unscoped write client.
func (app *Application) scheduledTenantIDs(ctx context.Context) ([]int, error) {
	if app.executionPolicy == nil {
		return nil, fmt.Errorf("scheduled execution policy is required")
	}
	ids := app.executionPolicy.CandidateTenantIDs()
	if !app.executionPolicy.IsCandidate() {
		if app.systemClient == nil {
			return nil, fmt.Errorf("scheduled tenant discovery client is required")
		}
		var err error
		ids, err = app.systemClient.Tenant.Query().Select(tenant.FieldID).Ints(tenantctx.SystemContext(ctx, "runtime:scheduled-discovery", "discover tenants for scheduled work"))
		if err != nil {
			return nil, fmt.Errorf("scheduled tenant discovery: %w", err)
		}
	}
	return ids, nil
}

type escalationProcessor interface {
	ProcessEscalations(context.Context, int) error
}

func (app *Application) runEscalationCycle(ctx context.Context) error {
	if app.escalationService == nil {
		return fmt.Errorf("escalation runtime is required")
	}
	ids, err := app.scheduledTenantIDs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if err := app.escalationService.ProcessEscalations(tenantctx.WithTenantID(ctx, id), id); err != nil {
			failures = append(failures, fmt.Errorf("escalation tenant %d: %w", id, err))
		}
	}
	return errors.Join(failures...)
}
