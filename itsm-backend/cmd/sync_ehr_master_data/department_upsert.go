package main

import (
	"context"

	"itsm-backend/ent"
	"itsm-backend/ent/department"
)

// departmentUpsertInput 是一次导入所需的部门字段。
type departmentUpsertInput struct {
	Name        string
	Code        string
	Description string
	AreaName    string
	OrgType     string
	ManagerID   int
	ParentID    int
	TenantID    int
}

// upsertDepartment 按 (tenant_id, code) 幂等写入部门。
//
// 组织编码来自旧 ITIL / eHR 的对齐结果，是节点的稳定业务键；导入脚本重跑
// 不得新增第二行，否则组织树会立刻出现同名重复节点，且与唯一索引冲突。
func upsertDepartment(ctx context.Context, client *ent.Client, in departmentUpsertInput) (*ent.Department, error) {
	existing, err := client.Department.Query().
		Where(department.TenantIDEQ(in.TenantID), department.CodeEQ(in.Code)).
		Only(ctx)
	if err == nil {
		update := client.Department.UpdateOneID(existing.ID).
			SetName(in.Name).
			SetDescription(in.Description).
			SetAreaName(in.AreaName).
			SetOrgType(in.OrgType)
		if in.ParentID > 0 {
			update = update.SetParentID(in.ParentID)
		}
		if in.ManagerID > 0 {
			update = update.SetManagerID(in.ManagerID)
		}
		return update.Save(ctx)
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}

	create := client.Department.Create().
		SetName(in.Name).
		SetCode(in.Code).
		SetDescription(in.Description).
		SetAreaName(in.AreaName).
		SetOrgType(in.OrgType).
		SetTenantID(in.TenantID)
	if in.ParentID > 0 {
		create = create.SetParentID(in.ParentID)
	}
	if in.ManagerID > 0 {
		create = create.SetManagerID(in.ManagerID)
	}
	return create.Save(ctx)
}
