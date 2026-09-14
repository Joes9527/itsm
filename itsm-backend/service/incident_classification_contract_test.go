package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"testing"
	"time"
)

func TestIncidentUpdateClassificationIDContract(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "classification")
	require.NoError(t, err)
	foreign, err := createIncidentTestTenant(ctx, client, "foreign-classification")
	require.NoError(t, err)
	user, err := createIncidentTestUser(ctx, client, tenant.ID, "classification")
	require.NoError(t, err)
	cat := func(tenantID int, active bool) *ent.TicketCategory {
		c, err := client.TicketCategory.Create().SetTenantID(tenantID).SetCode(fmt.Sprintf("cat-%d", time.Now().UnixNano())).SetName("Duplicate display name").SetIsActive(active).Save(ctx)
		require.NoError(t, err)
		return c
	}
	original, selected, inactive, other := cat(tenant.ID, true), cat(tenant.ID, true), cat(tenant.ID, false), cat(foreign.ID, true)
	wi := createIncidentTestWorkItem(t, ctx, client, tenant.ID, user.ID, "Original title", "new", "medium")
	_, err = client.Ticket.UpdateOneID(wi.ID).SetCategoryID(original.ID).Save(ctx)
	require.NoError(t, err)
	incident, err := client.Incident.Create().SetWorkItemID(wi.ID).SetSeverity("medium").SetDetectedAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	title := "Updated title"
	resp, err := svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{Title: &title}, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, original.ID, resp.CategoryID)
	resp, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{CategoryID: &selected.ID}, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, selected.ID, resp.CategoryID)
	for _, id := range []int{inactive.ID, other.ID, -1, 999999} {
		before, err := client.Ticket.Get(ctx, wi.ID)
		require.NoError(t, err)
		rejected := "Must not persist"
		_, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{CategoryID: &id, Title: &rejected}, tenant.ID)
		require.Error(t, err)
		after, err := client.Ticket.Get(ctx, wi.ID)
		require.NoError(t, err)
		require.Equal(t, before.Title, after.Title)
		require.Equal(t, before.Version, after.Version)
		require.Equal(t, selected.ID, after.CategoryID)
	}
	// Omission preserves a classification that was deactivated after selection.
	_, err = client.TicketCategory.UpdateOneID(selected.ID).SetIsActive(false).Save(ctx)
	require.NoError(t, err)
	_, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{Title: &title}, tenant.ID)
	require.NoError(t, err)
	zero := 0
	resp, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{CategoryID: &zero}, tenant.ID)
	require.NoError(t, err)
	require.Zero(t, resp.CategoryID)
	require.Empty(t, resp.Category)
	// A failing extension write must roll back the already-written WorkItem.
	client.Incident.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpUpdateOne) {
				return nil, errors.New("injected extension failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	before, err := client.Ticket.Get(ctx, wi.ID)
	require.NoError(t, err)
	_, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{CategoryID: &original.ID}, tenant.ID)
	require.Error(t, err)
	after, err := client.Ticket.Get(ctx, wi.ID)
	require.NoError(t, err)
	require.Equal(t, before.CategoryID, after.CategoryID)
	require.Equal(t, before.Version, after.Version)
}
