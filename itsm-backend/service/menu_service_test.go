package service

import (
	"testing"

	"itsm-backend/ent"
)

func TestShouldRestrictMenuForRole(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		roleCodes map[string]bool
		want      bool
	}{
		{
			name:      "end user cannot see workflow menu",
			path:      "/workflow/dashboard",
			roleCodes: map[string]bool{"end_user": true},
			want:      true,
		},
		{
			name:      "security cannot see admin menu",
			path:      "/admin/users",
			roleCodes: map[string]bool{"security": true},
			want:      true,
		},
		{
			name:      "admin can see workflow menu",
			path:      "/workflow/dashboard",
			roleCodes: map[string]bool{"admin": true},
			want:      false,
		},
		{
			// change_manager 的种子权限里真实授予了 workflow:read/write（见
			// pkg/seeder/seeder.go），需要自助查看跟自己业务相关的流程实例
			// （发布/变更审批链），不应该被这层挡住。
			name:      "change_manager can see workflow menu",
			path:      "/workflow/instances",
			roleCodes: map[string]bool{"change_manager": true},
			want:      false,
		},
		{
			// service_catalog_admin 在 seeder 里确实被授予了 workflow:read
			// （见 pkg/seeder/seeder.go），但没在 isWorkflowVisibleRole 白名单里——
			// 这条用例证明这一层是显式角色白名单，不是纯权限检查：一个角色
			// 拿到 workflow:read 不代表自动进这个菜单可见白名单，两者是分开维护的。
			// （这本身是已知的、待后续统一梳理的口径不一致，不是这次改动要解决的范围；
			// ops_manager/it_director/ops_director 也有同样的 seeder 已授权
			// 但白名单未覆盖的情况。）
			name:      "role outside allowlist still restricted even if granted workflow:read elsewhere",
			path:      "/workflow/dashboard",
			roleCodes: map[string]bool{"service_catalog_admin": true},
			want:      true,
		},
		{
			name:      "main menu stays visible for end user",
			path:      "/incidents",
			roleCodes: map[string]bool{"end_user": true},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRestrictMenuForRole(tt.path, tt.roleCodes)
			if got != tt.want {
				t.Fatalf("shouldRestrictMenuForRole(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFilterMenusByPermissionRestrictsLowPrivilegeAdminMenus(t *testing.T) {
	svc := &MenuService{}
	menus := []*ent.Menu{
		{Name: "事件管理", Path: "/incidents", PermissionCode: "incident:read"},
		{Name: "工作流", Path: "/admin/workflows", PermissionCode: "workflow:read"},
		{Name: "用户管理", Path: "/admin/users", PermissionCode: "user:read"},
	}

	filtered := svc.filterMenusByPermission(
		menus,
		map[string]bool{
			"incident:read": true,
			"workflow:read": true,
			"user:read":     true,
		},
		map[string]bool{"end_user": true},
	)

	if len(filtered) != 1 {
		t.Fatalf("expected 1 visible menu, got %d", len(filtered))
	}

	if filtered[0].Path != "/incidents" {
		t.Fatalf("expected incidents menu to remain visible, got %s", filtered[0].Path)
	}
}
