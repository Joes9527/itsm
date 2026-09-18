package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent"
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
	resp, err := svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{Version: client.Ticket.GetX(ctx, wi.ID).Version, Title: &title}, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, original.ID, resp.CategoryID)
	// B1 契约：分类变化必须带原因，否则拒绝。
	beforeReason, err := client.Ticket.Get(ctx, wi.ID)
	require.NoError(t, err)
	_, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{Version: beforeReason.Version, CategoryID: &selected.ID}, tenant.ID)
	require.ErrorContains(t, err, "reason is required")
	unchanged, err := client.Ticket.Get(ctx, wi.ID)
	require.NoError(t, err)
	require.Equal(t, original.ID, unchanged.CategoryID)
	require.Equal(t, beforeReason.Version, unchanged.Version)

	resp, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{Version: client.Ticket.GetX(ctx, wi.ID).Version, CategoryID: &selected.ID, ClassificationReason: "misrouted at intake"}, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, selected.ID, resp.CategoryID)
	// 证据落在本域时间线：事件类型 + 原因 + 前后路径快照。
	event := client.IncidentEvent.Query().Order(ent.Desc("id")).FirstX(ctx)
	require.Equal(t, "classification_change", event.EventType)
	require.Equal(t, "misrouted at intake", event.Metadata["classificationReason"])
	beforePath, ok := event.Metadata["classificationBefore"].([]any)
	require.True(t, ok)
	require.Len(t, beforePath, 1)
	require.Equal(t, float64(original.ID), beforePath[0].(map[string]any)["id"])
	afterPath, ok := event.Metadata["classificationAfter"].([]any)
	require.True(t, ok)
	require.Len(t, afterPath, 1)
	require.Equal(t, float64(selected.ID), afterPath[0].(map[string]any)["id"])
	require.Equal(t, "Duplicate display name", afterPath[0].(map[string]any)["name"])
	for _, id := range []int{inactive.ID, other.ID, -1, 999999} {
		before, err := client.Ticket.Get(ctx, wi.ID)
		require.NoError(t, err)
		rejected := "Must not persist"
		_, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{Version: client.Ticket.GetX(ctx, wi.ID).Version, CategoryID: &id, ClassificationReason: "rejected target probe", Title: &rejected}, tenant.ID)
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
	_, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{Version: client.Ticket.GetX(ctx, wi.ID).Version, Title: &title}, tenant.ID)
	require.NoError(t, err)
	zero := 0
	resp, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{Version: client.Ticket.GetX(ctx, wi.ID).Version, CategoryID: &zero, ClassificationReason: "明确清空分类"}, tenant.ID)
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
	_, err = svc.UpdateIncident(ctx, incident.ID, &dto.UpdateIncidentRequest{Version: client.Ticket.GetX(ctx, wi.ID).Version, CategoryID: &original.ID, ClassificationReason: "rollback probe"}, tenant.ID)
	require.Error(t, err)
	after, err := client.Ticket.Get(ctx, wi.ID)
	require.NoError(t, err)
	require.Equal(t, before.CategoryID, after.CategoryID)
	require.Equal(t, before.Version, after.Version)
}
