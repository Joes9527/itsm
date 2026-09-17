package change

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

// 变更单分类纠正契约（B1）：原因仅在分类确实变化时必填，目标必须是同租户、
// 启用、父链连续的路径；证据写进既有操作回执；终态变更不允许再改分类。
func TestChangeClassificationCorrectionContract(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	categories := service.NewTicketCategoryService(f.client)

	tree := func(prefix string) [3]int {
		ids := [3]int{}
		parent := 0
		for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
			record, err := categories.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: f.tenant})
			require.NoError(t, err)
			ids[index] = record.ID
			parent = record.ID
		}
		return ids
	}
	primary := tree("chg")
	alternative := tree("chg-alt")
	inactive := func() int {
		record, err := categories.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: "chg-inactive", Code: "chg-inactive", IsActive: false, TenantID: f.tenant})
		require.NoError(t, err)
		return record.ID
	}()
	foreignTenant := f.client.Tenant.Create().SetName("foreign").SetCode("chg-foreign").SaveX(f.ctx)
	foreign, err := categories.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: "chg-foreign", Code: "chg-foreign", IsActive: true, TenantID: foreignTenant.ID})
	require.NoError(t, err)

	assignee := f.requester
	f.client.Ticket.UpdateOneID(f.record.WorkItemID).SetCategoryID(primary[2]).SetAssigneeID(assignee).ExecX(f.ctx)

	correct := func(target *int, reason, key string) error {
		item := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID)
		_, err := f.svc.ApplyMetadata(f.ctx, MetadataCommand{
			Meta:     workitemmutation.Meta{TenantID: f.tenant, ActorID: f.requester, ExpectedVersion: item.Version, Source: "http", OperationID: key},
			ChangeID: f.record.ID,
			Patch:    dto.UpdateChangeRequest{CategoryID: target, ClassificationReason: reason},
		})
		return err
	}
	target := func(id int) *int { return &id }

	// 完整 → 完整：允许，版本 +1，回执含原因与前后路径，归属不变。
	before := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID)
	require.NoError(t, correct(target(alternative[2]), "变更范围归错类", "chg-cti-ok"))
	after := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID)
	require.Equal(t, alternative[2], after.CategoryID)
	require.Equal(t, before.Version+1, after.Version)
	require.Equal(t, assignee, after.AssigneeID, "classification correction must never reassign")

	receipt := f.client.AuditLog.Query().
		Where(auditlog.TenantIDEQ(f.tenant), auditlog.ResourceEQ("work_item")).
		Order(ent.Desc(auditlog.FieldID)).FirstX(f.ctx)
	require.NotNil(t, receipt.RequestBody)
	require.Contains(t, *receipt.RequestBody, `"classificationReason":"变更范围归错类"`)
	require.Contains(t, *receipt.RequestBody, `"classificationBefore"`)
	require.Contains(t, *receipt.RequestBody, `"classificationAfter"`)
	require.Contains(t, *receipt.RequestBody, `"id":`)

	// 缺原因：拒绝且零副作用。
	version := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Version
	require.ErrorContains(t, correct(target(primary[2]), "   ", "chg-cti-noreason"), "reason is required")
	current := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID)
	require.Equal(t, alternative[2], current.CategoryID)
	require.Equal(t, version, current.Version)

	// 停用 / 未知 / 跨租户目标：拒绝且不落库。
	for _, scenario := range []struct {
		name   string
		target int
	}{
		{"inactive", inactive},
		{"unknown", 999999},
		{"foreign", foreign.ID},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			version := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Version
			require.Error(t, correct(target(scenario.target), "非法目标探测", "chg-cti-reject-"+scenario.name))
			after := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID)
			require.Equal(t, alternative[2], after.CategoryID)
			require.Equal(t, version, after.Version)
		})
	}

	// 过期版本：版本冲突且不写入。
	require.True(t, func() bool {
		_, err := f.svc.ApplyMetadata(f.ctx, MetadataCommand{
			Meta:     workitemmutation.Meta{TenantID: f.tenant, ActorID: f.requester, ExpectedVersion: version + 5, Source: "http", OperationID: "chg-cti-stale"},
			ChangeID: f.record.ID,
			Patch:    dto.UpdateChangeRequest{CategoryID: target(primary[2]), ClassificationReason: "过期版本"},
		})
		return err != nil
	}())

	// 显式清空：本域保留既有语义，但必须带原因并留痕。
	version = f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Version
	require.NoError(t, correct(target(0), "确认与任何分类无关", "chg-cti-clear"))
	cleared := f.client.Ticket.GetX(f.ctx, f.record.WorkItemID)
	require.Zero(t, cleared.CategoryID)
	require.Equal(t, version+1, cleared.Version)

	// 终态：不再提供分类修改通道。
	f.client.Ticket.UpdateOneID(f.record.WorkItemID).SetStatus("cancelled").ExecX(f.ctx)
	require.ErrorContains(t, correct(target(alternative[2]), "终态尝试修改", "chg-cti-terminal"), "locked")
}
