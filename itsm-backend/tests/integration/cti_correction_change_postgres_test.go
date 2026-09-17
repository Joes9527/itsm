//go:build integration_postgres

package integration

import (
	"sync"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	change "itsm-backend/handlers/change"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// 变更单分类纠正的真实 PostgreSQL 证据：复用本域既有元数据命令（PUT /changes/:id 的
// 应用服务），原因仅在分类确实变化时必填，目标必须是可用的租户内路径，
// 证据写进既有操作回执，且纠正不改写归属。
func TestCTICorrectionChangePostgres(t *testing.T) {
	f := newCTIStructureFixture(t)
	categories := service.NewTicketCategoryService(f.client)
	owner := change.NewService(change.NewEntRepository(f.client, nil), f.client, zap.NewNop().Sugar(), executionfixture.Standard())

	tenant := f.tenant(t, "cti-change-correction")
	actor := f.client.User.Create().SetTenantID(tenant).SetUsername("cti-change-admin").SetName("Admin").
		SetRole("super_admin").SetEmail("cti-change-admin@example.test").SetPasswordHash("x").SaveX(f.ctx)

	tree := func(prefix string) [3]int {
		ids := [3]int{}
		parent := 0
		for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
			record, err := categories.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenant})
			require.NoError(t, err)
			ids[index] = record.ID
			parent = record.ID
		}
		return ids
	}
	primary := tree("chg-pg")
	alternative := tree("chg-pg-alt")
	inactive := f.category(t, categories, tenant, 0, "chg-pg-inactive")
	f.client.TicketCategory.UpdateOneID(inactive).SetIsActive(false).ExecX(f.ctx)
	foreignTenant := f.tenant(t, "cti-change-foreign")
	foreign := f.category(t, categories, foreignTenant, 0, "chg-pg-foreign")

	item := f.client.Ticket.Create().SetTenantID(tenant).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).
		SetTitle("Change").SetTicketNumber("CHG-PG-1").SetRecordClass("change_request").
		SetStatus("draft").SetPriority("medium").SetAssigneeID(actor.ID).SetCategoryID(primary[2]).SaveX(f.ctx)
	record := f.client.Change.Create().SetWorkItemID(item.ID).SetType("normal").
		SetImplementationPlan("deploy").SetRollbackPlan("restore").SaveX(f.ctx)

	correct := func(target *int, reason, key string) error {
		current := f.client.Ticket.GetX(f.ctx, item.ID)
		_, err := owner.ApplyMetadata(f.ctx, change.MetadataCommand{
			Meta:     workitemmutation.Meta{TenantID: tenant, ActorID: actor.ID, ExpectedVersion: current.Version, Source: "http", OperationID: key},
			ChangeID: record.ID,
			Patch:    dto.UpdateChangeRequest{CategoryID: target, ClassificationReason: reason},
		})
		return err
	}
	target := func(id int) *int { return &id }
	snapshot := func() (int, int, int) {
		row := f.client.Ticket.GetX(f.ctx, item.ID)
		return row.CategoryID, row.Version, row.AssigneeID
	}

	t.Run("correction_records_evidence_without_touching_ownership", func(t *testing.T) {
		beforeCategory, beforeVersion, beforeAssignee := snapshot()
		require.Equal(t, primary[2], beforeCategory)
		require.NoError(t, correct(target(alternative[2]), "变更范围归错类", "chg-cti-ok"))
		category, version, assignee := snapshot()
		require.Equal(t, alternative[2], category)
		require.Equal(t, beforeVersion+1, version)
		require.Equal(t, beforeAssignee, assignee)

		receipt := f.client.AuditLog.Query().
			Where(auditlog.TenantIDEQ(tenant), auditlog.ResourceEQ("work_item")).
			Order(ent.Desc(auditlog.FieldID)).FirstX(f.ctx)
		require.NotNil(t, receipt.RequestBody)
		require.Contains(t, *receipt.RequestBody, `"classificationReason":"变更范围归错类"`)
		require.Contains(t, *receipt.RequestBody, `"classificationBefore"`)
		require.Contains(t, *receipt.RequestBody, `"classificationAfter"`)
	})

	t.Run("missing_reason_is_rejected_without_side_effects", func(t *testing.T) {
		_, version, _ := snapshot()
		require.ErrorContains(t, correct(target(primary[2]), "   ", "chg-cti-noreason"), "reason is required")
		category, after, _ := snapshot()
		require.Equal(t, alternative[2], category)
		require.Equal(t, version, after)
	})

	t.Run("unusable_targets_are_rejected", func(t *testing.T) {
		for _, scenario := range []struct {
			name   string
			target int
		}{
			{"inactive", inactive},
			{"unknown", 999999},
			{"foreign", foreign},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				_, version, _ := snapshot()
				require.Error(t, correct(target(scenario.target), "probe rejected target", "chg-cti-reject-"+scenario.name))
				category, after, _ := snapshot()
				require.Equal(t, alternative[2], category)
				require.Equal(t, version, after)
			})
		}
	})

	t.Run("terminal_change_has_no_classification_channel", func(t *testing.T) {
		f.client.Ticket.UpdateOneID(item.ID).SetStatus("cancelled").ExecX(f.ctx)
		require.ErrorContains(t, correct(target(primary[2]), "终态尝试修改", "chg-cti-terminal"), "locked")
		category, _, _ := snapshot()
		require.Equal(t, alternative[2], category)
		f.client.Ticket.UpdateOneID(item.ID).SetStatus("draft").ExecX(f.ctx)
	})

	t.Run("concurrent_corrections_allow_exactly_one_version", func(t *testing.T) {
		_, expected, _ := snapshot()
		start := make(chan struct{})
		var ready sync.WaitGroup
		ready.Add(2)
		results := make(chan error, 2)
		targets := []int{primary[2], alternative[1]}
		keys := []string{"chg-cti-race-a", "chg-cti-race-b"}
		for index := 0; index < 2; index++ {
			go func(index int) {
				ready.Done()
				<-start
				id := targets[index]
				results <- correct(&id, "concurrent correction", keys[index])
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
		_, version, _ := snapshot()
		require.Equal(t, expected+1, version)
	})
}
