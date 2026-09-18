package common

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

// 运维/测试脚手架账号前缀：与 scripts/migration/seed_isolation_plan.py 同源口径。
// 这些账号没有真人值守，做部门负责人会让审批落到无人账号上。
var departmentManagerScaffoldPrefixes = []string{
	"qa_",
	"ui-runtime-",
	"ui-core-journey-",
	"ui-lifecycle-",
	"engineer-workspace-",
	"kaf_closeout_",
}

// validateDepartmentManager 校验一个部门负责人候选。
//
// managerID == 0 表示"负责人暂缺"，是合法状态（审批沿组织的轴往上找，最终兜底）。
// 除此之外必须满足：同租户、在职、且不是脚手架账号。
func validateDepartmentManager(ctx context.Context, client *ent.Client, tenantID, managerID int) error {
	if managerID == 0 {
		return nil
	}

	manager, err := client.User.Query().
		Where(user.IDEQ(managerID), user.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("department manager %d is not an active user in tenant %d", managerID, tenantID)
		}
		return err
	}
	if !manager.Active {
		return fmt.Errorf("department manager %d is not active", managerID)
	}
	if isScaffoldAccount(manager.Username) {
		return fmt.Errorf("user %q is a scaffold account and cannot own a department", manager.Username)
	}
	return nil
}

// isScaffoldAccount 判定开发期脚手架账号。真实员工用工号形态（如 D10001），不会被误判。
func isScaffoldAccount(username string) bool {
	if username == "admin" {
		return true
	}
	if strings.HasSuffix(username, "_test") {
		return true
	}
	for _, prefix := range departmentManagerScaffoldPrefixes {
		if strings.HasPrefix(username, prefix) {
			return true
		}
	}
	return false
}
