package main

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent/department"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestUpsertDepartmentIsIdempotentByTenantAndCode(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptupsert?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	in := departmentUpsertInput{
		Name: "信息技术部", Code: "IT", Description: "EHR UniqueID: u1",
		AreaName: "中国", OrgType: "department", TenantID: 1,
	}

	first, err := upsertDepartment(ctx, client, in)
	require.NoError(t, err)

	in.Name = "信息技术部（改名）"
	second, err := upsertDepartment(ctx, client, in)
	require.NoError(t, err)

	require.Equal(t, first.ID, second.ID, "re-running the import must not create a second row")
	require.Equal(t, "信息技术部（改名）", second.Name)

	count, err := client.Department.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestUpsertDepartmentKeepsSameCodeInAnotherTenantSeparate(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptupsert2?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	base := departmentUpsertInput{Name: "信息技术部", Code: "IT", OrgType: "department"}
	base.TenantID = 1
	_, err := upsertDepartment(ctx, client, base)
	require.NoError(t, err)

	base.TenantID = 2
	_, err = upsertDepartment(ctx, client, base)
	require.NoError(t, err)

	count, err := client.Department.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

// 唯一索引是"编码唯一"这句话的实际承载：没有它，任何绕过 upsert 的写入路径
// （手工 SQL、其它导入脚本）都能造出重复编码。
func TestDepartmentCodeIsUniquePerTenantAtSchemaLevel(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptunique?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	_, err := client.Department.Create().SetName("A").SetCode("IT").SetTenantID(1).Save(ctx)
	require.NoError(t, err)

	_, err = client.Department.Create().SetName("B").SetCode("IT").SetTenantID(1).Save(ctx)
	require.Error(t, err, "duplicate (tenant_id, code) must be rejected")

	_, err = client.Department.Create().SetName("C").SetCode("IT").SetTenantID(2).Save(ctx)
	require.NoError(t, err, "the same code in another tenant must stay allowed")
}

// 软删除过的同编码部门不得破坏重跑导入：必须留下一个**活动**部门。
//
// 这是一个真实踩到过的缺陷：upsert 的查询过去不过滤 deleted_at（本仓库没有
// softdelete mixin，Ent 不会自动加），于是软删同编码部门后重跑导入会**更新那条
// 看不见的已删除行、不建活动部门、也不报错**——组织树悄悄少掉一个 HR 说存在的部门。
// 049 是部分唯一索引，所以"已删除 + 活动"同编码并存是被允许的。
func TestUpsertDepartmentAfterSoftDeleteLeavesAnActiveRow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptsoftdel?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	in := departmentUpsertInput{
		Name: "旧名", Code: "SD1", AreaName: "中国",
		OrgType: "department", TenantID: 1,
	}
	first, err := upsertDepartment(ctx, client, in)
	require.NoError(t, err)

	// 组织撤销 / 隔离种子部门：软删除该节点
	_, err = client.Department.UpdateOneID(first.ID).SetDeletedAt(time.Now()).Save(ctx)
	require.NoError(t, err)

	// HR 侧该编码仍然存在，重跑导入
	in.Name = "新名"
	second, err := upsertDepartment(ctx, client, in)
	require.NoError(t, err, "重跑导入不得因为存在已软删除的同编码行而失败")

	require.Nil(t, second.DeletedAt, "导入出来的必须是**活动**部门，不能是那条已软删除的行")
	require.NotEqual(t, first.ID, second.ID, "应新建一条活动行，而不是复活/更新已删除行")

	active, err := client.Department.Query().
		Where(department.CodeEQ("SD1"), department.DeletedAtIsNil()).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, active, "软删除后重跑导入，必须恰好留下一个活动部门")
}
