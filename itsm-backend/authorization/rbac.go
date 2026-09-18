package authorization

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
)

// Permission is the application-level RBAC capability used by HTTP middleware,
// domain handlers and application services.
type Permission struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

type cachedPermission struct {
	permissions []Permission
	expiresAt   time.Time
}

const DefaultPermissionCacheTTL = 5 * time.Minute

var (
	permissionCache     = make(map[string]*cachedPermission)
	permissionCacheLock sync.RWMutex
	permissionCacheTTL  = DefaultPermissionCacheTTL
)

func SetPermissionCacheTTL(ttl time.Duration) {
	permissionCacheLock.Lock()
	permissionCacheTTL = ttl
	permissionCacheLock.Unlock()
}

// RolePermissions is a defensive bootstrap set, not a parallel production
// authority. DBOnly remains the production mode.
var RolePermissions = map[string][]Permission{
	"super_admin": {{Resource: "*", Action: "*"}},
	"end_user": {
		{Resource: "ticket", Action: "read"},
		{Resource: "ticket", Action: "write"},
		{Resource: "knowledge", Action: "read"},
		{Resource: "service_catalog", Action: "read"},
		{Resource: "ticket_category", Action: "read"},
		{Resource: "ticket_template", Action: "read"},
		{Resource: "notification", Action: "read"},
		{Resource: "tag", Action: "read"},
	},
	"msp_viewer": {
		{Resource: "msp", Action: "read"},
		{Resource: "msp_customer", Action: "read"},
		{Resource: "msp_ticket", Action: "read"},
		{Resource: "msp_allocation", Action: "read"},
		{Resource: "msp_report", Action: "read"},
	},
	"msp_tech": {
		{Resource: "msp", Action: "read"},
		{Resource: "msp_customer", Action: "read"},
		{Resource: "msp_ticket", Action: "read"},
		{Resource: "msp_ticket", Action: "write"},
		{Resource: "msp_allocation", Action: "read"},
		{Resource: "msp_report", Action: "read"},
	},
	"msp_specialist": {
		{Resource: "msp", Action: "read"},
		{Resource: "msp_customer", Action: "read"},
		{Resource: "msp_customer", Action: "write"},
		{Resource: "msp_ticket", Action: "read"},
		{Resource: "msp_ticket", Action: "write"},
		{Resource: "msp_allocation", Action: "read"},
		{Resource: "msp_report", Action: "read"},
	},
	"msp_manager": {
		{Resource: "msp", Action: "read"},
		{Resource: "msp", Action: "write"},
		{Resource: "msp_customer", Action: "read"},
		{Resource: "msp_customer", Action: "write"},
		{Resource: "msp_ticket", Action: "read"},
		{Resource: "msp_ticket", Action: "write"},
		{Resource: "msp_allocation", Action: "read"},
		{Resource: "msp_allocation", Action: "write"},
		{Resource: "msp_report", Action: "read"},
		{Resource: "msp_report", Action: "write"},
	},
	"msp_admin": {
		{Resource: "msp", Action: "*"},
		{Resource: "msp_customer", Action: "*"},
		{Resource: "msp_ticket", Action: "*"},
		{Resource: "msp_allocation", Action: "*"},
		{Resource: "msp_report", Action: "*"},
	},
}

type PermissionConfigMode int

const (
	PermissionConfigModeDBOnly PermissionConfigMode = iota
	PermissionConfigModeHardcodeOnly
	PermissionConfigModeMerge
	PermissionConfigModeFallback
)

var PermissionConfig = struct {
	Mode        PermissionConfigMode
	EnableCache bool
}{Mode: PermissionConfigModeDBOnly, EnableCache: true}

func permissionCacheKey(roleName string, tenantID int) string {
	return roleName + "_" + strconv.Itoa(tenantID)
}

func loadPermissionsFromDB(client *ent.Client, roleName string, tenantID int) []Permission {
	permissions, _ := loadPermissionsFromDBChecked(context.Background(), client, roleName, tenantID)
	return permissions
}

func loadPermissionsFromDBChecked(ctx context.Context, client *ent.Client, roleName string, tenantID int) ([]Permission, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("RBAC client unavailable")
	}
	cacheKey := permissionCacheKey(roleName, tenantID)
	if PermissionConfig.EnableCache {
		permissionCacheLock.RLock()
		cached, exists := permissionCache[cacheKey]
		permissionCacheLock.RUnlock()
		if exists && time.Now().Before(cached.expiresAt) {
			return cached.permissions, nil
		}
	}
	permissions := make([]Permission, 0)
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	roleEntity, err := client.Role.Query().Where(role.Code(roleName), role.TenantID(tenantID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		grants, err := client.RolePermission.Query().Where(rolepermission.RoleIDEQ(roleEntity.ID), rolepermission.TenantID(tenantID)).All(ctx)
		if err != nil {
			return nil, err
		}
		if len(grants) > 0 {
			ids := make([]int, len(grants))
			for i, grant := range grants {
				ids[i] = grant.PermissionID
			}
			rows, err := client.Permission.Query().Where(permission.IDIn(ids...), permission.TenantID(tenantID)).All(ctx)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				permissions = append(permissions, Permission{Resource: row.Resource, Action: row.Action})
			}
		}
	}
	if PermissionConfig.EnableCache {
		permissionCacheLock.Lock()
		permissionCache[cacheKey] = &cachedPermission{permissions: permissions, expiresAt: time.Now().Add(permissionCacheTTL)}
		permissionCacheLock.Unlock()
	}
	return permissions, nil
}

func GetRolePermissions(client *ent.Client, roleName string, tenantID int) []Permission {
	return loadPermissionsFromDB(client, roleName, tenantID)
}

func LoadPermissionsByMode(client *ent.Client, roleName string, tenantID int) []Permission {
	permissions, _ := LoadPermissionsByModeChecked(context.Background(), client, roleName, tenantID)
	return permissions
}

// LoadPermissionsByModeChecked retains the shared RBAC policy and distinguishes
// storage failure from genuine empty grants. Failures never populate the cache.
func LoadPermissionsByModeChecked(ctx context.Context, client *ent.Client, roleName string, tenantID int) ([]Permission, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if PermissionConfig.Mode == PermissionConfigModeHardcodeOnly {
		return RolePermissions[roleName], nil
	}
	databasePermissions, err := loadPermissionsFromDBChecked(ctx, client, roleName, tenantID)
	if err != nil {
		return nil, err
	}
	switch PermissionConfig.Mode {
	case PermissionConfigModeDBOnly:
		return databasePermissions, nil
	case PermissionConfigModeMerge:
		merged := make(map[string]Permission)
		for _, candidate := range append(databasePermissions, RolePermissions[roleName]...) {
			merged[candidate.Resource+":"+candidate.Action] = candidate
		}
		result := make([]Permission, 0, len(merged))
		for _, candidate := range merged {
			result = append(result, candidate)
		}
		return result, nil
	default:
		if len(databasePermissions) > 0 {
			return databasePermissions, nil
		}
		return RolePermissions[roleName], nil
	}
}

func CheckPermissionMatch(permissions []Permission, resource, action string) bool {
	for _, candidate := range permissions {
		if candidate.Resource == "*" && (candidate.Action == "*" || candidate.Action == action) {
			return true
		}
		if candidate.Resource == resource {
			if candidate.Action == "*" || candidate.Action == "admin" || candidate.Action == action {
				return true
			}
			// "write" covers create and update actions
			if candidate.Action == "write" && (action == "create" || action == "update") {
				return true
			}
			if action == "write" && (candidate.Action == "create" || candidate.Action == "update") {
				return true
			}
		}
	}
	return false
}

func HasResourcePermission(client *ent.Client, roleName, resource, action string, tenantID int) bool {
	if roleName == "super_admin" {
		return true
	}
	return CheckPermissionMatch(LoadPermissionsByMode(client, roleName, tenantID), resource, action)
}

func InvalidateRolePermissionCache(roleName string, tenantID int) {
	permissionCacheLock.Lock()
	delete(permissionCache, permissionCacheKey(roleName, tenantID))
	permissionCacheLock.Unlock()
}

func InvalidateAllPermissionCaches() {
	permissionCacheLock.Lock()
	clear(permissionCache)
	permissionCacheLock.Unlock()
}

func InvalidateAllPermissionCachesEx() {
	if PermissionConfig.EnableCache {
		InvalidateAllPermissionCaches()
	}
}
