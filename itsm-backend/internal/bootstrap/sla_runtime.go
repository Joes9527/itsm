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
	ids := app.executionPolicy.CandidateTenantIDs()
	if !app.executionPolicy.IsCandidate() {
		if app.systemClient == nil {
			return fmt.Errorf("SLA tenant discovery client is required")
		}
		var err error
		ids, err = app.systemClient.Tenant.Query().Select(tenant.FieldID).Ints(tenantctx.SystemContext(ctx, "runtime:sla-discovery", "discover tenants for SLA monitoring"))
		if err != nil {
			return fmt.Errorf("SLA tenant discovery: %w", err)
		}
	}
	var failures []error
	for _, id := range ids {
		if _, err := app.slaMonitor.CheckSLAViolations(tenantctx.WithTenantID(ctx, id), id); err != nil {
			failures = append(failures, fmt.Errorf("SLA tenant %d: %w", id, err))
		}
	}
	return errors.Join(failures...)
}
