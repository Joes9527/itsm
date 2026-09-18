package service

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// validateUserManager 校验"某人的直属上级"是否可以设置。
//
// 走的是**汇报线**（users.manager_id，个人级），与 departments.manager_id
// 那条"组织负责人（岗位级）"的轴完全无关，两者不得互相回填。
//
// managerID == 0 表示"上级暂缺"，是合法状态：审批会沿**组织的轴**向上找，
// 最终落到兜底组，不因上级为空而阻塞提单。
func validateUserManager(ctx context.Context, client *ent.Client, tenantID, userID, managerID int) error {
	if managerID == 0 {
		return nil
	}
	if managerID == userID {
		return fmt.Errorf("a user cannot be their own manager")
	}

	manager, err := client.User.Query().
		Where(user.IDEQ(managerID), user.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("manager %d is not a user in tenant %d", managerID, tenantID)
		}
		return err
	}
	if !manager.Active {
		return fmt.Errorf("manager %d is not active", managerID)
	}

	// 沿上级链上溯，命中 userID 即成环。
	// visited 以 userID 起头并保护上溯：历史数据里可能已有脏环，
	// 撞到它就停下——脏环由数据步骤清理，不因为别人的脏数据挡住一次合法写入。
	visited := map[int]bool{userID: true}
	cursor := manager
	for cursor != nil && cursor.ManagerID != 0 {
		next := cursor.ManagerID
		if next == userID {
			return fmt.Errorf("setting manager %d would create a reporting cycle", managerID)
		}
		if visited[next] {
			return nil
		}
		visited[next] = true

		following, err := client.User.Query().
			Where(user.IDEQ(next), user.TenantIDEQ(tenantID)).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				// 链断在一个不存在的用户上：无法构成环，允许写入。
				return nil
			}
			return err
		}
		cursor = following
	}
	return nil
}
