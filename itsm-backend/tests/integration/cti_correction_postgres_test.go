//go:build integration_postgres

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	problem "itsm-backend/handlers/problem"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
)

// B1 的真实 PostgreSQL 证据：专业分类纠正必须带原因、目标必须是可用路径、
// 证据与写入同事务，且纠正不得改写归属/SLA/目录默认分类。
// 复用 problem_lifecycle 夹具（含 RLS 与真实 problem 拥有者），未配置显式目标指纹时失败。
func TestCTICorrectionPostgres(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	categories := service.NewTicketCategoryService(f.client)

	tree := func(prefix string) [3]int {
		ids := [3]int{}
		parent := 0
		for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
			record, err := categories.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: f.tenant.ID})
			require.NoError(t, err)
			ids[index] = record.ID
			parent = record.ID
		}
		return ids
	}
	primary := tree("correct")
	alternative := tree("alternative")
	inactiveRoot := createCategoryInTenant(t, categories, f.ctx, f.tenant.ID, "inactive-root", false)

	// 起始分类：一级节点（专业纠正允许部分分类，完整度是完成门禁的要求）。
	original := primary[0]
	f.client.Ticket.UpdateOneID(f.p.WorkItemID).SetCategoryID(original).ExecX(f.ctx)
	assignee := f.actor.ID
	f.client.Ticket.UpdateOneID(f.p.WorkItemID).SetAssigneeID(assignee).ExecX(f.ctx)
	catalog := f.client.ServiceCatalog.Create().SetTenantID(f.tenant.ID).SetName("Corrected catalog").
		SetTargetClass("problem").SetDefaultTicketCategoryID(primary[2]).SaveX(f.ctx)

	snapshot := func() (int, int, string) {
		item := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
		catalogRow := f.client.ServiceCatalog.GetX(f.ctx, catalog.ID)
		return item.Version, item.AssigneeID, fmt.Sprintf("%v|%v|%v", item.ResolvedAt, item.ClosedAt, catalogRow.DefaultTicketCategoryID)
	}

	correct := func(target *int, reason, key string) error {
		item := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
		_, err := f.owner.ApplyMetadata(f.ctx, problem.MetadataCommand{
			Meta:      workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: key},
			ProblemID: f.p.ID,
			Patch:     dto.UpdateProblemRequest{CategoryID: target, ClassificationReason: reason},
		})
		return err
	}

	t.Run("missing_reason_is_rejected_without_side_effects", func(t *testing.T) {
		beforeVersion, beforeAssignee, beforeSLA := snapshot()
		err := correct(&primary[2], "   ", "cti-correction-noreason")
		require.ErrorContains(t, err, "reason is required")
		afterVersion, afterAssignee, afterSLA := snapshot()
		require.Equal(t, beforeVersion, afterVersion)
		require.Equal(t, beforeAssignee, afterAssignee)
		require.Equal(t, beforeSLA, afterSLA)
		require.Zero(t, f.client.AuditLog.Query().Where(auditlog.ResourceEQ("work_item_classification")).CountX(f.ctx))
	})

	t.Run("complete_correction_records_evidence_and_preserves_derived_state", func(t *testing.T) {
		beforeVersion, beforeAssignee, beforeSLA := snapshot()
		expected := beforeVersion
		target := primary[2]
		replay := func() error {
			_, err := f.owner.ApplyMetadata(f.ctx, problem.MetadataCommand{
				Meta:      workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: expected, Source: "http", OperationID: "cti-correction-ok"},
				ProblemID: f.p.ID,
				Patch:     dto.UpdateProblemRequest{CategoryID: &target, ClassificationReason: "misrouted at intake"},
			})
			return err
		}
		require.NoError(t, replay())
		afterVersion, afterAssignee, afterSLA := snapshot()
		require.Equal(t, beforeVersion+1, afterVersion, "correction must bump the WorkItem version exactly once")
		require.Equal(t, beforeAssignee, afterAssignee, "classification correction must never reassign")
		require.Equal(t, beforeSLA, afterSLA, "classification correction must not touch SLA or catalog defaults")

		receipt := f.client.AuditLog.Query().
			Where(auditlog.TenantIDEQ(f.tenant.ID), auditlog.ResourceEQ("work_item")).
			Order(ent.Desc(auditlog.FieldID)).FirstX(f.ctx)
		require.NotNil(t, receipt.RequestBody)
		require.Contains(t, *receipt.RequestBody, `"classificationReason":"misrouted at intake"`)
		require.Contains(t, *receipt.RequestBody, `"classificationBefore"`)
		require.Contains(t, *receipt.RequestBody, `"classificationAfter"`)
		require.Contains(t, *receipt.RequestBody, fmt.Sprintf(`"id":%d`, primary[2]))
		require.Contains(t, *receipt.RequestBody, fmt.Sprintf(`"id":%d`, original))

		// 完全相同的重试必须复用既有回执，不重复写审计。
		auditsBefore := f.client.AuditLog.Query().Where(auditlog.TenantIDEQ(f.tenant.ID), auditlog.ResourceEQ("work_item")).CountX(f.ctx)
		require.NoError(t, replay())
		require.Equal(t, auditsBefore, f.client.AuditLog.Query().Where(auditlog.TenantIDEQ(f.tenant.ID), auditlog.ResourceEQ("work_item")).CountX(f.ctx))
		unchangedVersion, _, _ := snapshot()
		require.Equal(t, afterVersion, unchangedVersion)
	})

	t.Run("unusable_targets_are_rejected", func(t *testing.T) {
		for _, scenario := range []struct {
			name   string
			target int
		}{
			{"inactive", inactiveRoot},
			{"unknown", 999999},
			{"foreign", createForeignCategory(t, f, categories)},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				beforeVersion, _, _ := snapshot()
				target := scenario.target
				err := correct(&target, "probe rejected target", "cti-correction-reject-"+scenario.name)
				require.Error(t, err)
				afterVersion, _, _ := snapshot()
				require.Equal(t, beforeVersion, afterVersion)
			})
		}
	})

	t.Run("concurrent_corrections_allow_exactly_one_version", func(t *testing.T) {
		// 基线移到 alternative[2]，随后两个竞争者分别指向两个与基线不同且合法的路径。
		require.NoError(t, correct(&alternative[2], "baseline before race", "cti-correction-baseline"))
		item := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
		expected := item.Version

		start := make(chan struct{})
		var ready sync.WaitGroup
		ready.Add(2)
		results := make(chan error, 2)
		targets := []int{primary[2], alternative[1]}
		keys := []string{"cti-correction-race-a", "cti-correction-race-b"}
		for index := 0; index < 2; index++ {
			go func(index int) {
				ready.Done()
				<-start
				target := targets[index]
				_, err := f.owner.ApplyMetadata(f.ctx, problem.MetadataCommand{
					Meta:      workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: expected, Source: "http", OperationID: keys[index]},
					ProblemID: f.p.ID,
					Patch:     dto.UpdateProblemRequest{CategoryID: &target, ClassificationReason: "concurrent correction"},
				})
				results <- err
			}(index)
		}
		ready.Wait()
		close(start)
		successes := 0
		for index := 0; index < 2; index++ {
			if err := <-results; err == nil {
				successes++
			}
		}
		require.Equal(t, 1, successes, "only one correction may win the expected version")
		after := f.client.Ticket.GetX(f.ctx, f.p.WorkItemID)
		require.Equal(t, expected+1, after.Version, "the losing correction must not bump the version")
	})

	// 诊断输出证明真实执行身份与目标，便于审阅者核对该结果不是 skip。
	var identity string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT current_user").Scan(&identity))
	t.Logf("CTI correction PostgreSQL evidence: tenant=%d user=%s", f.tenant.ID, identity)
}

func createCategoryInTenant(t *testing.T, categories *service.TicketCategoryService, ctx context.Context, tenantID int, code string, active bool) int {
	t.Helper()
	record, err := categories.CreateCategory(ctx, &service.CreateCategoryRequest{Name: code, Code: code, IsActive: active, TenantID: tenantID})
	require.NoError(t, err)
	return record.ID
}

func createForeignCategory(t *testing.T, f *problemLifecycleFixture, categories *service.TicketCategoryService) int {
	t.Helper()
	foreign := f.client.Tenant.Create().SetCode("cti-correction-foreign").SetName("foreign").SaveX(f.ctx)
	record, err := categories.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: "foreign", Code: "foreign", IsActive: true, TenantID: foreign.ID})
	require.NoError(t, err)
	return record.ID
}
