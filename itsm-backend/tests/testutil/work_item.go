package testutil

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"itsm-backend/ent"
)

var workItemFixtureSequence atomic.Int64

// IncidentFixture describes the public WorkItem fields and the professional
// Incident fields needed by tests outside the incident service package.
type IncidentFixture struct {
	Number      string
	Title       string
	Description string
	Status      string
	Priority    string
	Type        string
	Severity    string
}

// CreateIncident creates a valid WorkItem aggregate. Tests must not write
// public fields to the Incident extension because those columns no longer
// exist after migration 021.
func CreateIncident(t testing.TB, ctx context.Context, client *ent.Client, tenantID, requesterID int, fixture IncidentFixture) *ent.Incident {
	t.Helper()
	if fixture.Title == "" {
		fixture.Title = fixture.Number
	}
	if fixture.Status == "" {
		fixture.Status = "open"
	}
	if fixture.Priority == "" {
		fixture.Priority = "medium"
	}
	if fixture.Type == "" {
		fixture.Type = "incident"
	}
	if fixture.Severity == "" {
		fixture.Severity = "medium"
	}

	workItem, err := client.Ticket.Create().
		SetTitle(fixture.Title).
		SetDescription(fixture.Description).
		SetStatus(fixture.Status).
		SetPriority(fixture.Priority).
		SetType("incident").
		SetRecordClass("incident").
		SetTicketNumber(fmt.Sprintf("WI-FIXTURE-%d", workItemFixtureSequence.Add(1))).
		SetRequesterID(requesterID).
		SetTenantID(tenantID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create incident WorkItem fixture: %v", err)
	}

	created, err := client.Incident.Create().
		SetIncidentNumber(fixture.Number).
		SetType(fixture.Type).
		SetSeverity(fixture.Severity).
		SetWorkItemID(workItem.ID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create incident extension fixture: %v", err)
	}
	return created
}
