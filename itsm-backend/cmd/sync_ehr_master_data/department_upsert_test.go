package main

import (
	"context"
	"testing"

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
