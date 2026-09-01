package authorization

import (
	"context"
	"strconv"
	"sync"
	"time"

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
	if client == nil {
		return nil
	}
	cacheKey := permissionCacheKey(roleName, tenantID)
	if PermissionConfig.EnableCache {
		permissionCacheLock.RLock()
		cached, exists := permissionCache[cacheKey]
		permissionCacheLock.RUnlock()
		if exists && time.Now().Before(cached.expiresAt) {
			return cached.permissions
		}
	}

	permissions := make([]Permission, 0)
	roleEntity, err := client.Role.Query().
		Where(role.Code(roleName), role.TenantID(tenantID)).
		Only(context.Background())
	if err == nil {
		rolePermissions, queryErr := client.RolePermission.Query().
			Where(rolepermission.RoleIDEQ(roleEntity.ID), rolepermission.TenantID(tenantID)).
			All(context.Background())
		if queryErr == nil && len(rolePermissions) > 0 {
			permissionIDs := make([]int, len(rolePermissions))
			for index, rolePermission := range rolePermissions {
				permissionIDs[index] = rolePermission.PermissionID
			}
			permissionEntities, permissionErr := client.Permission.Query().
				Where(permission.IDIn(permissionIDs...), permission.TenantID(tenantID)).
				All(context.Background())
			if permissionErr == nil {
				for _, permissionEntity := range permissionEntities {
					permissions = append(permissions, Permission{Resource: permissionEntity.Resource, Action: permissionEntity.Action})
				}
			}
		}
	}

	if PermissionConfig.EnableCache {
		permissionCacheLock.Lock()
		permissionCache[cacheKey] = &cachedPermission{permissions: permissions, expiresAt: time.Now().Add(permissionCacheTTL)}
		permissionCacheLock.Unlock()
	}
	return permissions
}

func GetRolePermissions(client *ent.Client, roleName string, tenantID int) []Permission {
	return loadPermissionsFromDB(client, roleName, tenantID)
}

func LoadPermissionsByMode(client *ent.Client, roleName string, tenantID int) []Permission {
	switch PermissionConfig.Mode {
	case PermissionConfigModeDBOnly:
		return loadPermissionsFromDB(client, roleName, tenantID)
	case PermissionConfigModeHardcodeOnly:
		return RolePermissions[roleName]
	case PermissionConfigModeMerge:
		databasePermissions := loadPermissionsFromDB(client, roleName, tenantID)
		merged := make(map[string]Permission, len(databasePermissions)+len(RolePermissions[roleName]))
		for _, candidate := range append(databasePermissions, RolePermissions[roleName]...) {
			merged[candidate.Resource+":"+candidate.Action] = candidate
		}
		result := make([]Permission, 0, len(merged))
		for _, candidate := range merged {
			result = append(result, candidate)
		}
		return result
	case PermissionConfigModeFallback:
		fallthrough
	default:
		databasePermissions := loadPermissionsFromDB(client, roleName, tenantID)
		if len(databasePermissions) > 0 {
			return databasePermissions
		}
		return RolePermissions[roleName]
	}
}

func CheckPermissionMatch(permissions []Permission, resource, action string) bool {
	for _, candidate := range permissions {
		if candidate.Resource == "*" && (candidate.Action == "*" || candidate.Action == action) {
			return true
		}
		if candidate.Resource == resource && (candidate.Action == "*" || candidate.Action == "admin" || candidate.Action == action) {
			return true
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
