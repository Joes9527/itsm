package service

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/incident"
)

var incidentTestNumber atomic.Int64

// incidentTestBuilder keeps legacy test setup concise while honoring the
// production aggregate boundary: public fields are written only to Ticket and
// the Incident row contains professional fields plus work_item_id.
type incidentTestBuilder struct {
	client       *ent.Client
	professional *ent.IncidentCreate
	workItemID   int
	title        string
	description  string
	status       string
	priority     string
	reporterID   int
	tenantID     int
	createdAt    *time.Time
	updatedAt    *time.Time
}

func newIncidentTestBuilder(client *ent.Client) *incidentTestBuilder {
	return &incidentTestBuilder{client: client, professional: client.Incident.Create(), status: "new", priority: "medium"}
}

func (b *incidentTestBuilder) SetTitle(v string) *incidentTestBuilder { b.title = v; return b }
func (b *incidentTestBuilder) SetDescription(v string) *incidentTestBuilder {
	b.description = v
	return b
}
func (b *incidentTestBuilder) SetStatus(v string) *incidentTestBuilder   { b.status = v; return b }
func (b *incidentTestBuilder) SetPriority(v string) *incidentTestBuilder { b.priority = v; return b }
func (b *incidentTestBuilder) SetReporterID(v int) *incidentTestBuilder  { b.reporterID = v; return b }
func (b *incidentTestBuilder) SetTenantID(v int) *incidentTestBuilder    { b.tenantID = v; return b }
func (b *incidentTestBuilder) SetCreatedAt(v time.Time) *incidentTestBuilder {
	b.createdAt = &v
	return b
}
func (b *incidentTestBuilder) SetUpdatedAt(v time.Time) *incidentTestBuilder {
	b.updatedAt = &v
	return b
}
func (b *incidentTestBuilder) SetWorkItemID(v int) *incidentTestBuilder { b.workItemID = v; return b }

func (b *incidentTestBuilder) SetType(v string) *incidentTestBuilder {
	b.professional.SetType(v)
	return b
}
func (b *incidentTestBuilder) SetSeverity(v string) *incidentTestBuilder {
	b.professional.SetSeverity(v)
	return b
}
func (b *incidentTestBuilder) SetImpact(v string) *incidentTestBuilder {
	b.professional.SetImpact(v)
	return b
}
func (b *incidentTestBuilder) SetUrgency(v string) *incidentTestBuilder {
	b.professional.SetUrgency(v)
	return b
}
func (b *incidentTestBuilder) SetIncidentNumber(v string) *incidentTestBuilder {
	b.professional.SetIncidentNumber(v)
	return b
}
func (b *incidentTestBuilder) SetAssigneeID(v int) *incidentTestBuilder {
	b.professional.SetAssigneeID(v)
	return b
}
func (b *incidentTestBuilder) SetNillableAssigneeID(v *int) *incidentTestBuilder {
	b.professional.SetNillableAssigneeID(v)
	return b
}
func (b *incidentTestBuilder) SetConfigurationItemID(v int) *incidentTestBuilder {
	b.professional.SetConfigurationItemID(v)
	return b
}
func (b *incidentTestBuilder) SetCategory(v string) *incidentTestBuilder {
	b.professional.SetCategory(v)
	return b
}
func (b *incidentTestBuilder) SetSubcategory(v string) *incidentTestBuilder {
	b.professional.SetSubcategory(v)
	return b
}
func (b *incidentTestBuilder) SetImpactAnalysis(v map[string]interface{}) *incidentTestBuilder {
	b.professional.SetImpactAnalysis(v)
	return b
}
func (b *incidentTestBuilder) SetRootCause(v map[string]interface{}) *incidentTestBuilder {
	b.professional.SetRootCause(v)
	return b
}
func (b *incidentTestBuilder) SetResolutionSteps(v []map[string]interface{}) *incidentTestBuilder {
	b.professional.SetResolutionSteps(v)
	return b
}
func (b *incidentTestBuilder) SetDetectedAt(v time.Time) *incidentTestBuilder {
	b.professional.SetDetectedAt(v)
	return b
}
func (b *incidentTestBuilder) SetResolvedAt(v time.Time) *incidentTestBuilder {
	b.professional.SetResolvedAt(v)
	return b
}
func (b *incidentTestBuilder) SetClosedAt(v time.Time) *incidentTestBuilder {
	b.professional.SetClosedAt(v)
	return b
}
func (b *incidentTestBuilder) SetEscalatedAt(v time.Time) *incidentTestBuilder {
	b.professional.SetEscalatedAt(v)
	return b
}
func (b *incidentTestBuilder) SetEscalationLevel(v int) *incidentTestBuilder {
	b.professional.SetEscalationLevel(v)
	return b
}
func (b *incidentTestBuilder) SetIsAutomated(v bool) *incidentTestBuilder {
	b.professional.SetIsAutomated(v)
	return b
}
func (b *incidentTestBuilder) SetIsMajorIncident(v bool) *incidentTestBuilder {
	b.professional.SetIsMajorIncident(v)
	return b
}
func (b *incidentTestBuilder) SetSource(v string) *incidentTestBuilder {
	b.professional.SetSource(v)
	return b
}
func (b *incidentTestBuilder) SetMetadata(v map[string]interface{}) *incidentTestBuilder {
	b.professional.SetMetadata(v)
	return b
}
func (b *incidentTestBuilder) SetVersion(v int) *incidentTestBuilder {
	b.professional.SetVersion(v)
	return b
}
func (b *incidentTestBuilder) SetDeletedAt(v time.Time) *incidentTestBuilder {
	b.professional.SetDeletedAt(v)
	return b
}
func (b *incidentTestBuilder) AddConfigurationItemIDs(v ...int) *incidentTestBuilder {
	b.professional.AddConfigurationItemIDs(v...)
	return b
}

func (b *incidentTestBuilder) Save(ctx context.Context) (*ent.Incident, error) {
	if b.workItemID == 0 {
		workItemCreate := b.client.Ticket.Create().
			SetTicketNumber(fmt.Sprintf("TKT-INC-TEST-%d", incidentTestNumber.Add(1))).
			SetTitle(b.title).
			SetDescription(b.description).
			SetStatus(b.status).
			SetType("incident").
			SetRecordClass("incident").
			SetPriority(b.priority).
			SetRequesterID(b.reporterID).
			SetTenantID(b.tenantID)
		if b.createdAt != nil {
			workItemCreate.SetCreatedAt(*b.createdAt)
		}
		if b.updatedAt != nil {
			workItemCreate.SetUpdatedAt(*b.updatedAt)
		}
		workItem, err := workItemCreate.Save(ctx)
		if err != nil {
			return nil, err
		}
		b.workItemID = workItem.ID
	}
	return b.professional.SetWorkItemID(b.workItemID).Save(ctx)
}

func (b *incidentTestBuilder) SaveX(ctx context.Context) *ent.Incident {
	entity, err := b.Save(ctx)
	if err != nil {
		panic(err)
	}
	return entity
}

func incidentTestWorkItem(ctx context.Context, client *ent.Client, incidentID int) (*ent.Ticket, error) {
	incidentEntity, err := client.Incident.Query().Where(incident.IDEQ(incidentID)).WithWorkItem().Only(ctx)
	if err != nil {
		return nil, err
	}
	if incidentEntity.Edges.WorkItem == nil {
		return nil, fmt.Errorf("incident %d is missing its work item", incidentID)
	}
	return incidentEntity.Edges.WorkItem, nil
}

func setIncidentTestStatus(ctx context.Context, client *ent.Client, incidentID int, status string) error {
	workItem, err := incidentTestWorkItem(ctx, client, incidentID)
	if err != nil {
		return err
	}
	return client.Ticket.UpdateOneID(workItem.ID).SetStatus(status).Exec(ctx)
}

func mustIncidentTestWorkItem(t testing.TB, ctx context.Context, client *ent.Client, incidentID int) *ent.Ticket {
	t.Helper()
	workItem, err := incidentTestWorkItem(ctx, client, incidentID)
	if err != nil {
		t.Fatalf("load incident work item: %v", err)
	}
	return workItem
}
