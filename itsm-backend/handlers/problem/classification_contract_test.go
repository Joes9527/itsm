package problem_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func TestProblemHTTPClassificationIDContract(t *testing.T) {
	router, _, svc, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()
	tenant := createProblemHandlerTenant(t, ctx, client, "classification")
	foreign := createProblemHandlerTenant(t, ctx, client, "foreign-classification")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "classification")
	original := createProblemHandlerCategory(t, ctx, client, tenant.ID, "Original")
	selected := createProblemHandlerCategory(t, ctx, client, tenant.ID, "Selected")
	inactive := createProblemHandlerCategory(t, ctx, client, tenant.ID, "Inactive")
	other := createProblemHandlerCategory(t, ctx, client, foreign.ID, "Foreign")
	_, err := client.TicketCategory.UpdateOneID(inactive.ID).SetIsActive(false).Save(ctx)
	require.NoError(t, err)
	_, err = client.TicketCategory.UpdateOneID(selected.ID).SetName(original.Name).Save(ctx)
	require.NoError(t, err)
	p := createProblemHandlerProblem(t, ctx, svc, tenant.ID, user.ID)
	_, err = client.Ticket.UpdateOneID(*p.WorkItemID).SetCategoryID(original.ID).Save(ctx)
	require.NoError(t, err)
	path := fmt.Sprintf("/api/v1/problems/%d", p.ID)
	// B1 契约：分类确实变化时必须给出原因，因此统一在这里补上。
	update := func(req dto.UpdateProblemRequest, want int) {
		if req.CategoryID != nil {
			req.ClassificationReason = "reclassified during triage"
		}
		req.Version = client.Ticket.GetX(ctx, *p.WorkItemID).Version
		w := performProblemRequest(router, http.MethodPut, path, req, tenant.ID, user.ID)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response common.Response
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Zero(t, response.Code, w.Body.String())
		require.Equal(t, float64(want), response.Data.(map[string]interface{})["categoryId"])
	}
	update(dto.UpdateProblemRequest{OperationID: fmt.Sprintf("metadata-%d", time.Now().UnixNano()), Title: strPtr("Edited without classification")}, original.ID)
	update(dto.UpdateProblemRequest{OperationID: fmt.Sprintf("metadata-%d", time.Now().UnixNano()), CategoryID: &selected.ID}, selected.ID)
	for _, id := range []int{inactive.ID, other.ID, -1, 999999} {
		before, err := client.Ticket.Get(ctx, *p.WorkItemID)
		require.NoError(t, err)
		w := performProblemRequest(router, http.MethodPut, path, dto.UpdateProblemRequest{OperationID: fmt.Sprintf("metadata-%d", time.Now().UnixNano()), Version: before.Version, CategoryID: &id, ClassificationReason: "reclassified during triage", Title: strPtr("Must not persist")}, tenant.ID, user.ID)
		var response common.Response
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.NotZero(t, response.Code)
		after, err := client.Ticket.Get(ctx, *p.WorkItemID)
		require.NoError(t, err)
		require.Equal(t, before.Title, after.Title)
		require.Equal(t, before.Version, after.Version)
		require.Equal(t, selected.ID, after.CategoryID)
	}
	// 有变化但缺原因：必须拒绝，且不改变分类、不留下纠正审计。
	beforeReason, err := client.Ticket.Get(ctx, *p.WorkItemID)
	require.NoError(t, err)
	reasonRecorder := performProblemRequest(router, http.MethodPut, path, dto.UpdateProblemRequest{OperationID: fmt.Sprintf("metadata-%d", time.Now().UnixNano()), Version: beforeReason.Version, CategoryID: &original.ID}, tenant.ID, user.ID)
	var reasonResponse common.Response
	require.NoError(t, json.Unmarshal(reasonRecorder.Body.Bytes(), &reasonResponse))
	require.NotZero(t, reasonResponse.Code)
	afterReason, err := client.Ticket.Get(ctx, *p.WorkItemID)
	require.NoError(t, err)
	require.Equal(t, selected.ID, afterReason.CategoryID)
	require.Equal(t, beforeReason.Version, afterReason.Version)
	require.Zero(t, client.AuditLog.Query().Where(auditlog.ResourceEQ("work_item_classification")).CountX(ctx), "rejected correction must not leave audit evidence")

	// 纠正证据必须落在既有操作回执里：原因 + 前后完整路径快照。
	receipt := client.AuditLog.Query().Where(auditlog.ResourceEQ("work_item")).Order(ent.Desc(auditlog.FieldID)).FirstX(ctx)
	require.NotNil(t, receipt.RequestBody)
	require.Contains(t, *receipt.RequestBody, `"classificationReason":"reclassified during triage"`)
	require.Contains(t, *receipt.RequestBody, `"classificationBefore"`)
	require.Contains(t, *receipt.RequestBody, `"classificationAfter"`)
	require.Contains(t, *receipt.RequestBody, fmt.Sprintf(`"id":%d`, selected.ID))

	_, err = client.TicketCategory.UpdateOneID(selected.ID).SetIsActive(false).Save(ctx)
	require.NoError(t, err)
	update(dto.UpdateProblemRequest{OperationID: fmt.Sprintf("metadata-%d", time.Now().UnixNano()), Title: strPtr("Retain inactive existing classification")}, selected.ID)
	zero := 0
	update(dto.UpdateProblemRequest{OperationID: fmt.Sprintf("metadata-%d", time.Now().UnixNano()), CategoryID: &zero}, 0)
	client.Problem.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpUpdateOne) {
				return nil, errors.New("injected extension failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	before, err := client.Ticket.Get(ctx, *p.WorkItemID)
	require.NoError(t, err)
	w := performProblemRequest(router, http.MethodPut, path, dto.UpdateProblemRequest{OperationID: fmt.Sprintf("metadata-%d", time.Now().UnixNano()), Version: before.Version, CategoryID: &original.ID, ClassificationReason: "reclassified during triage"}, tenant.ID, user.ID)
	var response common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.NotZero(t, response.Code)
	after, err := client.Ticket.Get(ctx, *p.WorkItemID)
	require.NoError(t, err)
	require.Equal(t, before.CategoryID, after.CategoryID)
	require.Equal(t, before.Version, after.Version)
}

func TestProblemHTTPCreationCTI(t *testing.T) {
	router, _, _, client := setupProblemHTTPHandlerTest(t)
	defer client.Close()
	ctx := context.Background()
	tenant := createProblemHandlerTenant(t, ctx, client, "create-cti")
	user := createProblemHandlerUser(t, ctx, client, tenant.ID, "create-cti")
	root := createProblemHandlerCategory(t, ctx, client, tenant.ID, "root")
	child := createProblemHandlerCategory(t, ctx, client, tenant.ID, "child")
	_, err := client.TicketCategory.UpdateOneID(child.ID).SetParentID(root.ID).SetName(root.Name).Save(ctx)
	require.NoError(t, err)
	req := dto.CreateProblemRequest{Title: "CTI problem", Description: "Classified problem description", Priority: "high", CTI: &creation.CTIInput{CategoryID: &root.ID, TypeID: &child.ID}}
	w := performProblemRequest(router, http.MethodPost, "/api/v1/problems", req, tenant.ID, user.ID)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var response common.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Zero(t, response.Code)
	wiID := int(response.Data.(map[string]interface{})["workItemId"].(float64))
	wi, err := client.Ticket.Get(ctx, wiID)
	require.NoError(t, err)
	require.Equal(t, child.ID, wi.CategoryID)
	for _, scenario := range []string{"inactive", "foreign", "hierarchy", "missing-parent"} {
		_, err = client.TicketCategory.UpdateOneID(child.ID).SetIsActive(true).SetTenantID(tenant.ID).SetParentID(root.ID).Save(ctx)
		require.NoError(t, err)
		req.CTI.CategoryID = &root.ID
		switch scenario {
		case "inactive":
			client.TicketCategory.UpdateOneID(child.ID).SetIsActive(false).SaveX(ctx)
		case "foreign":
			client.TicketCategory.UpdateOneID(child.ID).SetTenantID(tenant.ID + 999).SaveX(ctx)
		case "hierarchy":
			client.TicketCategory.UpdateOneID(child.ID).ClearParentID().SaveX(ctx)
		case "missing-parent":
			req.CTI.CategoryID = nil
		}
		w = performProblemRequest(router, http.MethodPost, "/api/v1/problems", req, tenant.ID, user.ID)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.NotZero(t, response.Code, scenario)
		require.Equal(t, 1, client.Problem.Query().CountX(ctx))
		require.Equal(t, 1, client.Ticket.Query().CountX(ctx))
	}
}
