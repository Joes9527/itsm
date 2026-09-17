//go:build integration_postgres

package integration

import (
	"sync"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	servicerequest "itsm-backend/handlers/service_request"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Requested Item 分类纠正的真实 PostgreSQL 证据：
// 目录已声明完整三级，因此申请项既不允许清空、也不接受部分分类；
// 原因必填、版本 CAS、证据与写入同事务，且只动分类。
func TestCTICorrectionServiceRequestPostgres(t *testing.T) {
	f := newCTIStructureFixture(t)
	categories := service.NewTicketCategoryService(f.client)
	owner := servicerequest.NewService(servicerequest.NewEntRepository(f.client, executionfixture.Standard()), f.client, zap.NewNop().Sugar(), nil, executionfixture.Standard())

	tenant := f.tenant(t, "cti-sr-correction")
	actor := f.client.User.Create().SetTenantID(tenant).SetUsername("cti-sr-admin").SetName("Admin").
		SetRole("super_admin").SetEmail("cti-sr-admin@example.test").SetPasswordHash("x").SaveX(f.ctx)
	requester := f.client.User.Create().SetTenantID(tenant).SetUsername("cti-sr-requester").SetName("Requester").
		SetRole("end_user").SetEmail("cti-sr-requester@example.test").SetPasswordHash("x").SaveX(f.ctx)

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
	primary := tree("sr-pg")
	alternative := tree("sr-pg-alt")
	inactive := f.category(t, categories, tenant, 0, "sr-pg-inactive")
	f.client.TicketCategory.UpdateOneID(inactive).SetIsActive(false).ExecX(f.ctx)
	foreignTenant := f.tenant(t, "cti-sr-foreign")
	foreign := f.category(t, categories, foreignTenant, 0, "sr-pg-foreign")

	catalog := f.client.ServiceCatalog.Create().SetTenantID(tenant).SetName("Requested item correction").
		SetTargetClass("service_request_item").SetDefaultTicketCategoryID(primary[2]).SaveX(f.ctx)
	item := f.client.Ticket.Create().SetTenantID(tenant).SetRequesterID(requester.ID).SetOpenedByID(requester.ID).
		SetTitle("Requested item").SetTicketNumber("SR-PG-1").SetRecordClass("service_request_item").
		SetStatus("open").SetPriority("medium").SetAssigneeID(actor.ID).SetCategoryID(primary[2]).SaveX(f.ctx)
	request := f.client.ServiceRequest.Create().SetWorkItemID(item.ID).SetCatalogID(catalog.ID).SaveX(f.ctx)

	correct := func(target, version int, reason, key string) error {
		_, err := owner.CorrectClassification(f.ctx, servicerequest.ClassificationCorrection{
			Meta:             workitemmutation.Meta{TenantID: tenant, ActorID: actor.ID, Source: "http", ExpectedVersion: version, OperationID: key},
			ServiceRequestID: request.ID,
			TargetCategoryID: target,
			Reason:           reason,
			ActorRole:        "super_admin",
		})
		return err
	}
	snapshot := func() (int, int, int) {
		row := f.client.Ticket.GetX(f.ctx, item.ID)
		return row.CategoryID, row.Version, row.AssigneeID
	}

	t.Run("complete_to_complete_records_evidence", func(t *testing.T) {
		beforeCategory, beforeVersion, beforeAssignee := snapshot()
		require.Equal(t, primary[2], beforeCategory)
		require.NoError(t, correct(alternative[2], beforeVersion, "申请入口选错了服务项", "sr-cti-ok"))
		category, version, assignee := snapshot()
		require.Equal(t, alternative[2], category)
		require.Equal(t, beforeVersion+1, version)
		require.Equal(t, beforeAssignee, assignee, "classification correction must never reassign")

		receipt := f.client.AuditLog.Query().
			Where(auditlog.TenantIDEQ(tenant), auditlog.ResourceEQ("work_item_classification")).
			Order(ent.Desc(auditlog.FieldID)).FirstX(f.ctx)
		require.NotNil(t, receipt.RequestBody)
		require.Contains(t, *receipt.RequestBody, `"classificationReason":"申请入口选错了服务项"`)
		require.Contains(t, *receipt.RequestBody, `"classificationBefore"`)
		require.Contains(t, *receipt.RequestBody, `"classificationAfter"`)
	})

	t.Run("clearing_and_partial_targets_are_rejected", func(t *testing.T) {
		_, version, _ := snapshot()
		require.Error(t, correct(0, version, "试图清空分类", "sr-cti-clear"))
		require.ErrorContains(t, correct(alternative[1], version, "试图降级到二级", "sr-cti-partial"), "complete three-level path")
		category, after, _ := snapshot()
		require.Equal(t, alternative[2], category)
		require.Equal(t, version, after)
	})

	t.Run("missing_reason_is_rejected_without_side_effects", func(t *testing.T) {
		_, version, _ := snapshot()
		require.ErrorContains(t, correct(primary[2], version, "   ", "sr-cti-noreason"), "reason is required")
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
				require.Error(t, correct(scenario.target, version, "probe rejected target", "sr-cti-reject-"+scenario.name))
				category, after, _ := snapshot()
				require.Equal(t, alternative[2], category)
				require.Equal(t, version, after)
			})
		}
	})

	t.Run("concurrent_corrections_allow_exactly_one_version", func(t *testing.T) {
		_, expected, _ := snapshot()
		start := make(chan struct{})
		var ready sync.WaitGroup
		ready.Add(2)
		results := make(chan error, 2)
		targets := []int{primary[2], alternative[2]}
		keys := []string{"sr-cti-race-a", "sr-cti-race-b"}
		for index := 0; index < 2; index++ {
			go func(index int) {
				ready.Done()
				<-start
				results <- correct(targets[index], expected, "concurrent correction", keys[index])
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
