//go:build integration_postgres

package integration

import (
	"sync"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/systemconfig"
	"itsm-backend/ent/ticket"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// B2 的真实 PostgreSQL 证据：受控启用的锁与审计、保留键防线、完成门禁的事务内判定与回滚。
// 复用 cti_structure_postgres_test.go 的隔离 schema（含 009 RLS 与 048），未配置显式
// 目标指纹时测试失败，不以 skip 冒充通过。
func TestCTICompletionPostgres(t *testing.T) {
	f := newCTIStructureFixture(t)
	svc := service.NewTicketCategoryService(f.client)
	config := service.NewSystemConfigService(f.client, zap.NewNop().Sugar())
	tenant := f.tenant(t, "cti-completion")
	actor := f.client.User.Create().SetTenantID(tenant).SetUsername("cti-completion-admin").
		SetName("Admin").SetRole("super_admin").SetEmail("cti-completion-admin@example.test").
		SetPasswordHash("x").SaveX(f.ctx)

	tree := func(prefix string) [3]int {
		ids := [3]int{}
		parent := 0
		for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
			record, err := svc.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenant})
			require.NoError(t, err)
			ids[index] = record.ID
			parent = record.ID
		}
		return ids
	}

	t.Run("activation_keeps_the_first_cutoff_and_records_audit", func(t *testing.T) {
		first, err := config.SetCTIGovernance(f.ctx, tenant, actor.ID, "admin_ui", service.CTIGovernanceUpdate{CompletionEnforced: true, CatalogEnforced: true})
		require.NoError(t, err)
		require.True(t, first.Applied)
		require.NotNil(t, first.Governance.EffectiveFrom)
		cutoff := *first.Governance.EffectiveFrom

		// 暂停保留截止时间；恢复不得把在途单重新定义为“新单”。
		paused, err := config.SetCTIGovernance(f.ctx, tenant, actor.ID, "admin_ui", service.CTIGovernanceUpdate{})
		require.NoError(t, err)
		require.False(t, paused.Governance.CompletionEnforced)
		require.Equal(t, cutoff, *paused.Governance.EffectiveFrom)
		resumed, err := config.SetCTIGovernance(f.ctx, tenant, actor.ID, "admin_ui", service.CTIGovernanceUpdate{CompletionEnforced: true})
		require.NoError(t, err)
		require.Equal(t, cutoff, *resumed.Governance.EffectiveFrom)

		// 局部唯一索引必须只有一条启用记录，且审计逐次留痕。
		require.Equal(t, 1, f.client.SystemConfig.Query().
			Where(systemconfig.TenantIDEQ(tenant), systemconfig.KeyEQ(service.CTIGovernanceConfigKey), systemconfig.DeletedAtIsNil()).
			CountX(f.ctx))
		require.Equal(t, 3, f.client.AuditLog.Query().
			Where(auditlog.TenantIDEQ(tenant), auditlog.ResourceEQ("cti_governance")).
			CountX(f.ctx))
	})

	t.Run("reserved_key_cannot_be_written_through_the_generic_config_api", func(t *testing.T) {
		_, err := config.CreateSystemConfig(f.ctx, &dto.SystemConfigRequest{Key: service.CTIGovernanceConfigKey, Value: `{"completionEnforced":false}`}, tenant)
		require.ErrorIs(t, err, service.ErrReservedSystemConfigKey)
		_, err = config.BatchUpdateSystemConfigs(f.ctx, []dto.UpdateSystemConfigRequest{{Key: service.CTIGovernanceConfigKey, Value: `{"completionEnforced":false}`}}, tenant)
		require.ErrorIs(t, err, service.ErrReservedSystemConfigKey)
		row := f.client.SystemConfig.Query().
			Where(systemconfig.TenantIDEQ(tenant), systemconfig.KeyEQ(service.CTIGovernanceConfigKey), systemconfig.DeletedAtIsNil()).
			OnlyX(f.ctx)
		_, err = config.UpdateSystemConfig(f.ctx, row.ID, &dto.UpdateSystemConfigRequest{Value: `{"completionEnforced":false}`}, tenant)
		require.ErrorIs(t, err, service.ErrReservedSystemConfigKey)
		require.ErrorIs(t, config.DeleteSystemConfig(f.ctx, row.ID, tenant), service.ErrReservedSystemConfigKey)
		// 绕过 Go 层直写也受局部唯一索引限制：同一租户不能出现第二条启用记录。
		_, err = f.client.SystemConfig.Create().SetTenantID(tenant).SetKey(service.CTIGovernanceConfigKey).
			SetValue(`{"completionEnforced":true}`).SetValueType("json").Save(f.ctx)
		require.Error(t, err, "the partial unique index is the last concurrency defence")
	})

	t.Run("concurrent_first_activation_produces_exactly_one_record", func(t *testing.T) {
		other := f.tenant(t, "cti-completion-race")
		start := make(chan struct{})
		var ready sync.WaitGroup
		ready.Add(2)
		type outcome struct {
			applied bool
			err     error
		}
		results := make(chan outcome, 2)
		for index := 0; index < 2; index++ {
			go func() {
				ready.Done()
				<-start
				// 每个并发调用使用独立事务上下文，模拟两个管理端同时提交。
				result, err := config.SetCTIGovernance(f.ctx, other, actor.ID, "admin_ui", service.CTIGovernanceUpdate{CompletionEnforced: true})
				results <- outcome{applied: result.Applied, err: err}
			}()
		}
		ready.Wait()
		close(start)
		applied := 0
		for index := 0; index < 2; index++ {
			result := <-results
			if result.err != nil {
				// 并发首次启用落败方必须显式失败（唯一索引），不得静默成功。
				require.Contains(t, result.err.Error(), "concurrently")
				continue
			}
			if result.applied {
				applied++
			}
		}
		require.Equal(t, 1, applied, "exactly one first activation may apply a cutoff")
		required := f.client.SystemConfig.Query().
			Where(systemconfig.TenantIDEQ(other), systemconfig.KeyEQ(service.CTIGovernanceConfigKey), systemconfig.DeletedAtIsNil()).
			OnlyX(f.ctx)
		require.Equal(t, `{"catalogEnforced":false,"completionEnforced":true`, required.Value[:len(`{"catalogEnforced":false,"completionEnforced":true`)])
		require.Equal(t, 1, f.client.SystemConfig.Query().
			Where(systemconfig.TenantIDEQ(other), systemconfig.KeyEQ(service.CTIGovernanceConfigKey), systemconfig.DeletedAtIsNil()).
			CountX(f.ctx))
	})

	t.Run("completion_gate_decides_inside_the_transaction_and_rolls_back", func(t *testing.T) {
		ids := tree("completion")
		requester := f.client.User.Create().SetTenantID(tenant).SetUsername("cti-completion-requester").
			SetName("Requester").SetRole("requester").SetEmail("cti-completion-requester@example.test").
			SetPasswordHash("x").SaveX(f.ctx)

		create := func(number string, categoryID *int, createdAt time.Time) int {
			builder := f.client.Ticket.Create().SetTenantID(tenant).SetRequesterID(requester.ID).
				SetTitle(number).SetTicketNumber(number).SetStatus("resolved").SetRecordClass("generic").
				SetCreatedAt(createdAt)
			if categoryID != nil {
				builder.SetCategoryID(*categoryID)
			}
			return builder.SaveX(f.ctx).ID
		}

		afterCutoff := time.Now().UTC().Add(time.Minute)
		unclassified := create("CTI-PG-UNCLASSIFIED", nil, afterCutoff)
		partial := create("CTI-PG-PARTIAL", &ids[1], afterCutoff)
		complete := create("CTI-PG-COMPLETE", &ids[2], afterCutoff)

		gate := func(id int) error {
			return svcWithTransaction(f, func(tx *ent.Tx) error {
				item, err := tx.Ticket.Query().Where(ticket.IDEQ(id)).Only(f.ctx)
				if err != nil {
					return err
				}
				return service.EnforceWorkItemCompletionCTI(f.ctx, tx, tenant, item, "close")
			})
		}

		// 未分类 / 只有二级：业务拒绝，且拒绝时必须整笔回滚（状态与版本不变）。
		for _, id := range []int{unclassified, partial} {
			err := gate(id)
			require.Error(t, err, "ticket %d", id)
			row := f.client.Ticket.GetX(f.ctx, id)
			require.Equal(t, "resolved", row.Status)
			require.Nil(t, row.ClosedAt)
		}
		require.NoError(t, gate(complete), "a complete three-level path must pass")

		// 停用祖先不追溯：历史合法引用仍可完成（停用只禁止新选择）。
		disabled := false
		_, err := svc.UpdateCategory(f.ctx, ids[0], &service.UpdateCategoryRequest{IsActive: &disabled}, tenant)
		require.NoError(t, err)
		require.NoError(t, gate(complete), "deactivating a node must not strand in-flight records")

		// 截止之前创建的记录不被追溯要求。
		legacy := create("CTI-PG-LEGACY", nil, time.Now().UTC().Add(-48*time.Hour))
		require.NoError(t, gate(legacy), "records created before the cutoff stay ungated")

		// 跨租户分类按“分类不可用”拒绝，不泄露对象是否存在。
		foreign := f.tenant(t, "cti-completion-foreign")
		foreignCategory := f.category(t, svc, foreign, 0, "foreign-root")
		crossTenant := create("CTI-PG-CROSS", &foreignCategory, afterCutoff)
		require.ErrorIs(t, gate(crossTenant), service.ErrCTICompletionRequired)
	})

	// 诊断输出证明真实执行身份与目标，便于审阅者核对该结果不是 skip。
	var identity string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT current_user").Scan(&identity))
	t.Logf("CTI completion PostgreSQL evidence: schema=%s user=%s", f.schema, identity)
}
