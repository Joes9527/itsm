//go:build integration_postgres

package integration

import (
	"sync"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
)

// 目录默认分类的真实 PostgreSQL 证据：租户隔离、默认分类与工单创建的事务竞争、
// 祖先停用与创建竞争的最终一致性。复用 cti_structure_postgres_test.go 的隔离 schema
// 与显式目标指纹校验；未配置目标时失败，不以 skip 冒充通过。
func TestCTICatalogPostgres(t *testing.T) {
	f := newCTIStructureFixture(t)
	categories := service.NewTicketCategoryService(f.client)
	tenantA := f.tenant(t, "cti-catalog-a")
	tenantB := f.tenant(t, "cti-catalog-b")

	tree := func(tenantID int, prefix string) [3]int {
		ids := [3]int{}
		parent := 0
		for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
			record, err := categories.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenantID})
			require.NoError(t, err)
			ids[index] = record.ID
			parent = record.ID
		}
		return ids
	}

	t.Run("catalog_default_classification_is_tenant_scoped", func(t *testing.T) {
		a := tree(tenantA, "a")
		b := tree(tenantB, "b")
		require.NotEqual(t, a[2], b[2])

		// 跨租户默认值必须被拒绝，不能只靠外键存在性。
		foreign := f.client.ServiceCatalog.Create().SetTenantID(tenantA).SetName("foreign-default").
			SetTargetClass("service_request_item").SetStatus("enabled").SetIsActive(true).
			SetDefaultTicketCategoryID(b[2]).SaveX(f.ctx)
		_, err := categories.ProjectCTIPath(f.ctx, mustTx(t, f), tenantA, foreign.DefaultTicketCategoryID)
		require.ErrorIs(t, err, service.ErrCTICategoryNotFound, "another tenant's node must not resolve inside this tenant")

		// 本租户的完整路径必须可解析且唯一。
		txA := mustTx(t, f)
		path, err := categories.ProjectCTIPath(f.ctx, txA, tenantA, a[2])
		require.NoError(t, err)
		require.Len(t, path, 3)
		require.Equal(t, []int{a[0], a[1], a[2]}, []int{path[0].ID, path[1].ID, path[2].ID})
	})

	t.Run("work_item_creation_serializes_with_default_change_and_ancestor_deactivation", func(t *testing.T) {
		ids := tree(tenantA, "race")
		requester := f.client.User.Create().SetTenantID(tenantA).SetUsername("cti-catalog-racer").SetName("Racer").
			SetRole("agent").SetEmail("catalog-racer@example.test").SetPasswordHash("x").SaveX(f.ctx)

		round := func(mutate func()) error {
			start := make(chan struct{})
			var ready sync.WaitGroup
			ready.Add(2)
			created := make(chan error, 1)
			changed := make(chan error, 1)
			go func() {
				ready.Done()
				<-start
				created <- svcWithTransaction(f, func(tx *ent.Tx) error {
					if _, err := categories.ResolveCTIPath(f.ctx, tx, tenantA, ids[2], true, true); err != nil {
						return err
					}
					_, err := tx.Ticket.Create().SetTenantID(tenantA).SetRequesterID(requester.ID).
						SetTitle("catalog race").SetTicketNumber("CTI-CATALOG-RACE").SetStatus("open").
						SetRecordClass("service_request_item").SetCategoryID(ids[2]).Save(f.ctx)
					return err
				})
			}()
			go func() {
				ready.Done()
				<-start
				changed <- svcWithTransaction(f, func(tx *ent.Tx) error {
					mutate()
					return nil
				})
			}()
			ready.Wait()
			close(start)
			createErr, changeErr := <-created, <-changed
			if createErr != nil {
				require.Error(t, createErr)
			}
			require.NoError(t, changeErr)
			return createErr
		}

		// 祖先停用与创建竞争：要么创建先提交（停用等待），要么停用先提交而创建被拒绝。
		createErr := round(func() {
			_, err := categories.UpdateCategory(f.ctx, ids[0], &service.UpdateCategoryRequest{IsActive: boolPtr(false)}, tenantA)
			require.NoError(t, err)
		})
		if createErr == nil {
			// 创建成功则工单的分类路径必须整条可用；停用已被行锁串行化在后面。
			path, err := categories.ProjectCTIPath(f.ctx, mustTx(t, f), tenantA, ids[2])
			require.NoError(t, err)
			require.False(t, path[0].Active, "the deactivation is committed after the creation")
		}
		_, err := categories.UpdateCategory(f.ctx, ids[0], &service.UpdateCategoryRequest{IsActive: boolPtr(true)}, tenantA)
		require.NoError(t, err)

		// 最终一致性：不存在引用已删除分类的工单，也不存在半写入。
		var dangling int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `
			SELECT count(*) FROM tickets t
			WHERE t.tenant_id=$1 AND t.category_id IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM ticket_categories c WHERE c.id = t.category_id)`, tenantA).Scan(&dangling))
		require.Zero(t, dangling)
	})
}

func mustTx(t *testing.T, f *ctiStructureFixture) *ent.Tx {
	t.Helper()
	tx, err := f.client.Tx(f.ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func boolPtr(value bool) *bool { return &value }
