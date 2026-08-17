package middleware

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"itsm-backend/common"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/role"
	"itsm-backend/ent/rolepermission"
	"itsm-backend/ent/user"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Permission 权限结构
type Permission struct {
	Resource string `json:"resource"` // 资源名称，如 "ticket", "user", "dashboard"
	Action   string `json:"action"`   // 操作类型，如 "read", "write", "delete", "admin"
}

// cachedPermission 带过期时间的缓存条目
type cachedPermission struct {
	permissions []Permission
	expiresAt   time.Time
}

// DefaultPermissionCacheTTL 默认权限缓存TTL（5分钟）
const DefaultPermissionCacheTTL = 5 * time.Minute

// PermissionCache 权限缓存（带TTL）
var (
	permissionCache     = make(map[string]*cachedPermission)
	permissionCacheLock sync.RWMutex
	permissionCacheTTL  = DefaultPermissionCacheTTL
)

// SetPermissionCacheTTL 设置权限缓存TTL
func SetPermissionCacheTTL(ttl time.Duration) {
	permissionCacheLock.Lock()
	permissionCacheTTL = ttl
	permissionCacheLock.Unlock()
}

// RolePermissions 角色权限映射
//
// 说明：这是「最小兜底集」，不再是第二套平行权威。运行时权限以数据库
// （seeder 初始化）为唯一权威；硬编码仅保留以下角色：
//   - super_admin：平台超管，跨租户运维，在 hasResourcePermission 中代码级放行。
//   - end_user：数据库未初始化时的防御性兜底（与 seeder rolePermissionMap 保持一致）。
//   - msp_*：MSP 服务提供商角色的 RBAC 权限（MSP 子系统活跃，尚未迁入数据库）。
//
// 注：sysadmin 是「租户系统管理员」，走数据库权限（seeder 的 allPermissionCodes），
// 不在此兜底；super_admin 与 sysadmin 语义不同，勿混用。
var RolePermissions = map[string][]Permission{
	"super_admin": {
		{Resource: "*", Action: "*"}, // 平台超管拥有所有权限
	},
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
	// MSP Roles - MSP服务提供商角色权限（MSP 子系统活跃，暂保留在硬编码）
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

// PermissionConfigMode 权限配置模式
type PermissionConfigMode int

const (
	// PermissionConfigModeDBOnly 仅使用数据库权限
	PermissionConfigModeDBOnly PermissionConfigMode = iota
	// PermissionConfigModeHardcodeOnly 仅使用硬编码权限
	PermissionConfigModeHardcodeOnly
	// PermissionConfigModeMerge 合并数据库和硬编码权限（并集）
	PermissionConfigModeMerge
	// PermissionConfigModeFallback 先数据库，失败则使用硬编码（默认）
	PermissionConfigModeFallback
)

// PermissionConfig 权限配置
var PermissionConfig = struct {
	// Mode 权限加载模式
	Mode PermissionConfigMode
	// EnableCache 是否启用缓存
	EnableCache bool
}{
	Mode:        PermissionConfigModeDBOnly, // 企业级交付：仅使用数据库权限，支持多租户差异化
	EnableCache: true,
}

// loadRolePermissionsFromDB 从数据库加载角色的权限
// 直接从 role_permissions 联表查询，支持多租户
func loadRolePermissionsFromDB(client *ent.Client, roleName string, tenantID int) []Permission {
	// 如果 client 为 nil，直接返回空权限
	if client == nil {
		return nil
	}

	cacheKey := roleName + "_" + strconv.Itoa(tenantID)

	// 先检查缓存（包括TTL检查）
	permissionCacheLock.RLock()
	if cached, exists := permissionCache[cacheKey]; exists {
		if time.Now().Before(cached.expiresAt) {
			permissionCacheLock.RUnlock()
			return cached.permissions
		}
	}
	permissionCacheLock.RUnlock()

	// 从数据库加载: 通过 role_permissions 联表查询
	var perms []Permission

	// 首先查找角色ID
	roleEntity, err := client.Role.Query().
		Where(
			role.Code(roleName),
			role.TenantID(tenantID),
		).
		Only(context.Background())

	if err == nil && roleEntity != nil {
		roleID := roleEntity.ID

		// 直接查询 role_permissions 联表获取该角色的权限
		rolePerms, err := client.RolePermission.Query().
			Where(rolepermission.RoleIDEQ(roleID), rolepermission.TenantID(tenantID)).
			All(context.Background())

		if err == nil && len(rolePerms) > 0 {
			// 提取permission_id列表
			permIDs := make([]int, len(rolePerms))
			for i, rp := range rolePerms {
				permIDs[i] = rp.PermissionID
			}

			// 查询 permissions 表获取权限详情（加 tenant 过滤）
			permsData, err := client.Permission.Query().
				Where(permission.IDIn(permIDs...), permission.TenantID(tenantID)).
				All(context.Background())

			if err == nil {
				for _, p := range permsData {
					perms = append(perms, Permission{
						Resource: p.Resource,
						Action:   p.Action,
					})
				}
			}
		}
	}

	// 存入缓存（带TTL）
	permissionCacheLock.Lock()
	permissionCache[cacheKey] = &cachedPermission{
		permissions: perms,
		expiresAt:   time.Now().Add(permissionCacheTTL),
	}
	permissionCacheLock.Unlock()

	return perms
}

// invalidatePermissionCache 使指定角色的缓存失效
func invalidatePermissionCache(roleName string, tenantID int) {
	cacheKey := roleName + "_" + strconv.Itoa(tenantID)
	permissionCacheLock.Lock()
	delete(permissionCache, cacheKey)
	permissionCacheLock.Unlock()
}

// InvalidateAllPermissionCaches 使所有权限缓存失效
func InvalidateAllPermissionCaches() {
	permissionCacheLock.Lock()
	clear(permissionCache)
	permissionCacheLock.Unlock()
}

// GetRolePermissions 从数据库加载指定角色的权限（导出，供 service 层复用）。
// 与 hasResourcePermission 的数据库加载逻辑一致，返回 Resource/Action 权限列表。
func GetRolePermissions(client *ent.Client, roleName string, tenantID int) []Permission {
	return loadPermissionsFromDB(client, roleName, tenantID)
}

// loadPermissionsFromDB 从新的permission_definition和role_permission表加载权限
// 如果新表没有数据，则fallback到旧的Permission表
func loadPermissionsFromDB(client *ent.Client, roleName string, tenantID int) []Permission {
	// 如果 client 为 nil，直接返回空权限（将使用默认权限）
	if client == nil {
		return nil
	}

	cacheKey := roleName + "_" + strconv.Itoa(tenantID)

	// 先检查缓存（包括TTL检查）
	permissionCacheLock.RLock()
	if cached, exists := permissionCache[cacheKey]; exists {
		if time.Now().Before(cached.expiresAt) {
			permissionCacheLock.RUnlock()
			return cached.permissions
		}
	}
	permissionCacheLock.RUnlock()

	// 从新的permission_definition + role_permission表加载
	var perms []Permission

	// 首先查找角色ID
	roleEntity, err := client.Role.Query().
		Where(
			role.Code(roleName),
			role.TenantID(tenantID),
		).
		Only(context.Background())

	if err == nil && roleEntity != nil {
		roleID := roleEntity.ID

		// 查询role_permission表获取该角色的权限定义ID
		rolePerms, err := client.RolePermission.Query().
			Where(rolepermission.RoleIDEQ(roleID), rolepermission.TenantID(tenantID)).
			All(context.Background())

		if err == nil && len(rolePerms) > 0 {
			// 提取permission_id列表
			permIDs := make([]int, len(rolePerms))
			for i, rp := range rolePerms {
				permIDs[i] = rp.PermissionID
			}

			// 查询 permissions 表获取权限详情（加 tenant 过滤）
			permsData, err := client.Permission.Query().
				Where(permission.IDIn(permIDs...), permission.TenantID(tenantID)).
				All(context.Background())

			if err == nil {
				for _, p := range permsData {
					perms = append(perms, Permission{
						Resource: p.Resource,
						Action:   p.Action,
					})
				}
			}
		}
	}

	// 如果仍然没有权限，fallback到旧的方式
	if len(perms) == 0 {
		perms = loadRolePermissionsFromDB(client, roleName, tenantID)
	}

	// 存入缓存（带TTL）
	permissionCacheLock.Lock()
	permissionCache[cacheKey] = &cachedPermission{
		permissions: perms,
		expiresAt:   time.Now().Add(permissionCacheTTL),
	}
	permissionCacheLock.Unlock()

	return perms
}

var ResourceActionMap = map[string]map[string]Permission{
	"GET": {
		"/api/v1/tickets":               {Resource: "ticket", Action: "read"},
		"/api/v1/tickets/*":             {Resource: "ticket", Action: "read"},
		"/api/v1/notifications":              {Resource: "notification", Action: "read"},
		"/api/v1/notifications/*":            {Resource: "notification", Action: "read"},
		"/api/v1/notification-preferences":   {Resource: "notification", Action: "read"},
		"/api/v1/notification-preferences/*": {Resource: "notification", Action: "read"},
		"/api/v1/ticket-categories":     {Resource: "ticket_category", Action: "read"},
		"/api/v1/ticket-categories/*":   {Resource: "ticket_category", Action: "read"},
		"/api/v1/tickets/templates":      {Resource: "ticket_template", Action: "read"},
		"/api/v1/tickets/templates/*":    {Resource: "ticket_template", Action: "read"},
		"/api/v1/ticket-tags":           {Resource: "ticket_tag", Action: "read"},
		"/api/v1/ticket-tags/*":         {Resource: "ticket_tag", Action: "read"},
		"/api/v1/users":                 {Resource: "user", Action: "read"},
		"/api/v1/users/*":               {Resource: "user", Action: "read"},
		"/api/v1/dashboard":             {Resource: "dashboard", Action: "read"},
		"/api/v1/dashboard/*":           {Resource: "dashboard", Action: "read"},
		"/api/v1/knowledge":             {Resource: "knowledge", Action: "read"},
		"/api/v1/knowledge/search":      {Resource: "knowledge", Action: "read"},
		"/api/v1/knowledge/*":           {Resource: "knowledge", Action: "read"},
		"/api/v1/knowledge-articles":    {Resource: "knowledge", Action: "read"},
		"/api/v1/knowledge-articles/*":  {Resource: "knowledge", Action: "read"},
		"/api/v1/cmdb":                  {Resource: "cmdb", Action: "read"},
		"/api/v1/cmdb/*":                {Resource: "cmdb", Action: "read"},
		"/api/v1/configuration-items":   {Resource: "cmdb", Action: "read"},
		"/api/v1/configuration-items/*": {Resource: "cmdb", Action: "read"},
		"/api/v1/incidents":             {Resource: "incident", Action: "read"},
		"/api/v1/incidents/*":           {Resource: "incident", Action: "read"},
		"/api/v1/audit-logs":            {Resource: "audit_log", Action: "read"},
		"/api/v1/audit-logs/*":          {Resource: "audit_log", Action: "read"},
		"/api/v1/system/*":              {Resource: "system_config", Action: "read"},
		"/api/v1/ai/*":                  {Resource: "ai", Action: "read"},
		"/api/v1/agent/*":               {Resource: "ai", Action: "read"},
		"/api/v1/service-catalogs":      {Resource: "service_catalog", Action: "read"},
		"/api/v1/service-catalogs/*":    {Resource: "service_catalog", Action: "read"},
		"/api/v1/service-requests":      {Resource: "service_request", Action: "read"},
		"/api/v1/service-requests/*":    {Resource: "service_request", Action: "read"},
		"/api/v1/provisioning-tasks/*":  {Resource: "service_request", Action: "read"},
		"/api/v1/knowledge/articles":    {Resource: "knowledge", Action: "read"},
		"/api/v1/knowledge/articles/*":  {Resource: "knowledge", Action: "read"},
		"/api/v1/problems":              {Resource: "problem", Action: "read"},
		"/api/v1/problems/*":            {Resource: "problem", Action: "read"},
		"/api/v1/changes":               {Resource: "change", Action: "read"},
		"/api/v1/changes/*":             {Resource: "change", Action: "read"},
		"/api/v1/releases":              {Resource: "release", Action: "read"},
		"/api/v1/releases/stats":        {Resource: "release", Action: "read"},
		"/api/v1/releases/*":            {Resource: "release", Action: "read"},
		"/api/v1/roles":                 {Resource: "role", Action: "read"},
		"/api/v1/roles/*":               {Resource: "role", Action: "read"},
		"/api/v1/permissions":           {Resource: "permission", Action: "read"},
		"/api/v1/sla/*":                 {Resource: "sla", Action: "read"},
		"/api/v1/org/*":                 {Resource: "org", Action: "read"},
		"/api/v1/auth/me":               {Resource: "user", Action: "read"},
		"/api/v1/auth/profile":          {Resource: "user", Action: "read"},
		"/api/v1/auth/menus":            {Resource: "user", Action: "read"},
		// BPMN Workflow permissions
		"/api/v1/bpmn/*":             {Resource: "bpmn", Action: "read"},
		"/api/v1/bpmn/tasks":         {Resource: "task", Action: "read"},
		"/api/v1/bpmn/tasks/*":       {Resource: "task", Action: "read"},
		"/api/v1/bpmn/instances":     {Resource: "process_instance", Action: "read"},
		"/api/v1/bpmn/instances/*":   {Resource: "process_instance", Action: "read"},
		"/api/v1/process-trigger/*":  {Resource: "bpmn", Action: "read"},
		"/api/v1/process-bindings":   {Resource: "bpmn", Action: "read"},
		"/api/v1/process-bindings/*": {Resource: "bpmn", Action: "read"},
		// 审批工作流/审批链
		"/api/v1/approval-workflows":     {Resource: "approval_workflow", Action: "read"},
		"/api/v1/approval-workflows/*":   {Resource: "approval_workflow", Action: "read"},
		"/api/v1/approval-chains":        {Resource: "approval_workflow", Action: "read"},
		"/api/v1/approval-chains/*":      {Resource: "approval_workflow", Action: "read"},
		"/api/v1/my-approvals":           {Resource: "task", Action: "read"},
		"/api/v1/my-approvals/*":         {Resource: "task", Action: "read"},
		// 通知偏好/系统配置/菜单/租户/权限
		"/api/v1/system-configs":               {Resource: "system_config", Action: "read"},
		"/api/v1/system-configs/*":             {Resource: "system_config", Action: "read"},
		"/api/v1/menus":                        {Resource: "menu", Action: "read"},
		"/api/v1/menus/*":                      {Resource: "menu", Action: "read"},
		"/api/v1/tenants":                      {Resource: "tenant", Action: "read"},
		"/api/v1/tenants/*":                    {Resource: "tenant", Action: "read"},
		// 云管理/调查/视图/部件/标签/自动化
		"/api/v1/cloud-accounts":          {Resource: "cloud_account", Action: "read"},
		"/api/v1/cloud-accounts/*":        {Resource: "cloud_account", Action: "read"},
		"/api/v1/cloud-resources":         {Resource: "cloud_resource", Action: "read"},
		"/api/v1/cloud-resources/*":       {Resource: "cloud_resource", Action: "read"},
		"/api/v1/cloud-services":          {Resource: "cloud_service", Action: "read"},
		"/api/v1/cloud-services/*":        {Resource: "cloud_service", Action: "read"},
		"/api/v1/investigations":          {Resource: "investigation", Action: "read"},
		"/api/v1/investigations/*":        {Resource: "investigation", Action: "read"},
		"/api/v1/assignment-rules":        {Resource: "assignment_rule", Action: "read"},
		"/api/v1/assignment-rules/*":      {Resource: "assignment_rule", Action: "read"},
		"/api/v1/automation-rules":        {Resource: "automation_rule", Action: "read"},
		"/api/v1/automation-rules/*":      {Resource: "automation_rule", Action: "read"},
		// MSP Permissions
		"/api/v1/msp/status":              {Resource: "msp", Action: "read"},
		"/api/v1/msp/context":             {Resource: "msp", Action: "read"},
		"/api/v1/msp/allocations":         {Resource: "msp_allocation", Action: "read"},
		"/api/v1/msp/customers":           {Resource: "msp_customer", Action: "read"},
		"/api/v1/msp/customers/*":         {Resource: "msp_customer", Action: "read"},
		"/api/v1/msp/customers/*/tickets": {Resource: "msp_ticket", Action: "read"},
		"/api/v1/msp/reports/*":           {Resource: "msp_report", Action: "read"},
	},
	"POST": {
		"/api/v1/tickets":               {Resource: "ticket", Action: "write"},
		"/api/v1/tickets/*/assign":      {Resource: "ticket", Action: "assign"},
		"/api/v1/tickets/*":             {Resource: "ticket", Action: "write"},
		"/api/v1/ticket-categories":     {Resource: "ticket_category", Action: "write"},
		"/api/v1/ticket-categories/*":   {Resource: "ticket_category", Action: "write"},
		"/api/v1/tickets/templates":      {Resource: "ticket_template", Action: "write"},
		"/api/v1/tickets/templates/*":    {Resource: "ticket_template", Action: "write"},
		"/api/v1/ticket-tags":           {Resource: "ticket_tag", Action: "write"},
		"/api/v1/ticket-tags/*":         {Resource: "ticket_tag", Action: "write"},
		"/api/v1/users":                 {Resource: "user", Action: "write"},
		"/api/v1/knowledge":             {Resource: "knowledge", Action: "write"},
		"/api/v1/knowledge/search":      {Resource: "knowledge", Action: "read"},
		"/api/v1/knowledge-articles":    {Resource: "knowledge", Action: "write"},
		"/api/v1/knowledge-articles/*":  {Resource: "knowledge", Action: "write"},
		"/api/v1/cmdb":                  {Resource: "cmdb", Action: "write"},
		"/api/v1/cmdb/*":                {Resource: "cmdb", Action: "write"},
		"/api/v1/configuration-items":   {Resource: "cmdb", Action: "write"},
		"/api/v1/configuration-items/*": {Resource: "cmdb", Action: "write"},
		"/api/v1/incidents":             {Resource: "incident", Action: "write"},
		"/api/v1/incidents/*":           {Resource: "incident", Action: "write"},
		"/api/v1/ai/*":                  {Resource: "ai", Action: "write"},
		"/api/v1/agent/*":               {Resource: "ai", Action: "write"},
		"/api/v1/service-catalogs":      {Resource: "service_catalog", Action: "write"},
		"/api/v1/service-catalogs/*":    {Resource: "service_catalog", Action: "write"},
		"/api/v1/service-requests":      {Resource: "service_request", Action: "write"},
		"/api/v1/service-requests/*":    {Resource: "service_request", Action: "write"},
		"/api/v1/provisioning-tasks/*":  {Resource: "service_request", Action: "write"},
		"/api/v1/knowledge/articles":    {Resource: "knowledge", Action: "write"},
		"/api/v1/knowledge/articles/*":  {Resource: "knowledge", Action: "write"},
		"/api/v1/problems":              {Resource: "problem", Action: "write"},
		"/api/v1/problems/*":            {Resource: "problem", Action: "write"},
		"/api/v1/changes":               {Resource: "change", Action: "write"},
		"/api/v1/changes/*":             {Resource: "change", Action: "write"},
		"/api/v1/releases":              {Resource: "release", Action: "write"},
		"/api/v1/releases/*":            {Resource: "release", Action: "write"},
		"/api/v1/roles":                 {Resource: "role", Action: "write"},
		"/api/v1/roles/*":               {Resource: "role", Action: "write"},
		"/api/v1/system/*":              {Resource: "system_config", Action: "write"},
		"/api/v1/org/*":                 {Resource: "org", Action: "write"},
		"/api/v1/sla/*":                 {Resource: "sla", Action: "write"},
		// BPMN Workflow permissions
		"/api/v1/bpmn/*":             {Resource: "bpmn", Action: "write"},
		"/api/v1/process-trigger":    {Resource: "bpmn", Action: "write"},
		"/api/v1/process-trigger/*":  {Resource: "bpmn", Action: "write"},
		"/api/v1/process-bindings":   {Resource: "bpmn", Action: "write"},
		"/api/v1/process-bindings/*": {Resource: "bpmn", Action: "write"},
		// MSP Permissions
		"/api/v1/msp/allocations":            {Resource: "msp_allocation", Action: "write"},
		"/api/v1/msp/allocations/deallocate": {Resource: "msp_allocation", Action: "write"},
		"/api/v1/msp/tickets/*/assign":       {Resource: "msp_ticket", Action: "write"},
	},
	"PUT": {
		"/api/v1/tickets/*":            {Resource: "ticket", Action: "write"},
		"/api/v1/notifications/*":            {Resource: "notification", Action: "write"},
		"/api/v1/notification-preferences":   {Resource: "notification", Action: "write"},
		"/api/v1/notification-preferences/*": {Resource: "notification", Action: "write"},
		"/api/v1/ticket-categories/*":  {Resource: "ticket_category", Action: "write"},
		"/api/v1/tickets/templates/*":   {Resource: "ticket_template", Action: "write"},
		"/api/v1/ticket-tags/*":        {Resource: "ticket_tag", Action: "write"},
		"/api/v1/users/*":              {Resource: "user", Action: "write"},
		"/api/v1/knowledge/*":          {Resource: "knowledge", Action: "write"},
		"/api/v1/cmdb/*":               {Resource: "cmdb", Action: "write"},
		"/api/v1/incidents/*":          {Resource: "incident", Action: "write"},
		"/api/v1/service-catalogs/*":   {Resource: "service_catalog", Action: "write"},
		"/api/v1/service-requests/*":   {Resource: "service_request", Action: "write"},
		"/api/v1/knowledge-articles/*": {Resource: "knowledge", Action: "write"},
		"/api/v1/problems/*":           {Resource: "problem", Action: "write"},
		"/api/v1/changes/*":            {Resource: "change", Action: "write"},
		"/api/v1/releases/*":           {Resource: "release", Action: "write"},
		// BPMN Workflow permissions
		"/api/v1/bpmn/*": {Resource: "bpmn", Action: "write"},
	},
	"DELETE": {
		"/api/v1/tickets/*":            {Resource: "ticket", Action: "delete"},
		"/api/v1/ticket-categories/*":  {Resource: "ticket_category", Action: "delete"},
		"/api/v1/tickets/templates/*":   {Resource: "ticket_template", Action: "delete"},
		"/api/v1/ticket-tags/*":        {Resource: "ticket_tag", Action: "delete"},
		"/api/v1/users/*":              {Resource: "user", Action: "delete"},
		"/api/v1/knowledge/*":          {Resource: "knowledge", Action: "delete"},
		"/api/v1/cmdb/*":               {Resource: "cmdb", Action: "delete"},
		"/api/v1/incidents/*":          {Resource: "incident", Action: "delete"},
		"/api/v1/service-catalogs/*":   {Resource: "service_catalog", Action: "delete"},
		"/api/v1/knowledge-articles/*": {Resource: "knowledge", Action: "delete"},
		"/api/v1/problems/*":           {Resource: "problem", Action: "delete"},
		"/api/v1/changes/*":            {Resource: "change", Action: "delete"},
		"/api/v1/releases/*":           {Resource: "release", Action: "delete"},
		"/api/v1/roles/*":              {Resource: "role", Action: "delete"},
		// BPMN Workflow permissions
		"/api/v1/bpmn/*": {Resource: "bpmn", Action: "delete"},
	},
}

// RBACMiddleware RBAC权限控制中间件
func RBACMiddleware(client *ent.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 调试日志
		zap.S().Infow(
			"RBACMiddleware: received request",
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
		)

		// 将 Ent 客户端放入上下文，供资源级别检查使用（例如工单所有权校验）
		if client != nil {
			c.Set("client", client)
		}
		// 获取用户信息
		userIDInterface, exists := c.Get("user_id")
		if !exists {
			zap.S().Warnw(
				"RBACMiddleware: user_id not found in context",
				"path", c.Request.URL.Path,
			)
			common.Fail(c, common.AuthFailedCode, "用户未认证")
			c.Abort()
			return
		}

		userID, ok := userIDInterface.(int)
		if !ok {
			zap.S().Warnw(
				"RBACMiddleware: user_id format error",
				"path", c.Request.URL.Path,
				"user_id_interface", userIDInterface,
			)
			common.Fail(c, common.AuthFailedCode, "用户ID格式错误")
			c.Abort()
			return
		}

		// 获取租户ID
		// 特殊路径：认证相关端点不需要租户ID检查
		authPaths := map[string]bool{
			"/api/v1/auth/me":      true,
			"/api/v1/auth/tenants": true,
			"/api/v1/auth/menus":   true,
			"/api/v1/auth/profile": true,
		}
		isAuthPath := authPaths[c.Request.URL.Path]

		tenantIDInterface, exists := c.Get("tenant_id")
		if !exists {
			// 对于认证端点，尝试从JWT claim获取租户ID
			if isAuthPath {
				// 仅从JWT的tenant_id claim获取，不信任请求头
				if jwtTenantID, ok := c.Get("tenant_id"); ok {
					if tid, ok := jwtTenantID.(int); ok && tid > 0 {
						c.Set("tenant_id", tid)
						tenantIDInterface = tid
					}
				}
				// 如果仍然没有租户ID，拒绝请求而非默认分配
				if tenantIDInterface == nil {
					zap.S().Warnw(
						"RBACMiddleware: tenant_id not found in context for auth path",
						"path", c.Request.URL.Path,
						"user_id", userID,
					)
					common.Fail(c, common.AuthFailedCode, "租户信息缺失")
					c.Abort()
					return
				}
			} else {
				zap.S().Warnw(
					"RBACMiddleware: tenant_id not found in context",
					"path", c.Request.URL.Path,
					"user_id", userID,
				)
				common.Fail(c, common.AuthFailedCode, "租户信息缺失")
				c.Abort()
				return
			}
		}
		tenantID, ok := tenantIDInterface.(int)
		if !ok {
			zap.S().Warnw(
				"RBACMiddleware: tenant_id format error",
				"path", c.Request.URL.Path,
				"tenant_id_interface", tenantIDInterface,
			)
			common.Fail(c, common.AuthFailedCode, "租户ID格式错误")
			c.Abort()
			return
		}

		// 从数据库获取用户最新角色信息
		userEntity, err := client.User.Query().
			Where(user.ID(userID)).
			Only(context.Background())
		if err != nil {
			zap.S().Warnw(
				"RBACMiddleware: user not found in DB",
				"path", c.Request.URL.Path,
				"user_id", userID,
				"error", err.Error(),
			)
			common.Fail(c, common.AuthFailedCode, "用户不存在")
			c.Abort()
			return
		}

		// 检查用户是否被禁用
		if !userEntity.Active {
			zap.S().Warnw(
				"RBACMiddleware: user is disabled",
				"path", c.Request.URL.Path,
				"user_id", userID,
			)
			common.Fail(c, common.ForbiddenCode, "用户已被禁用")
			c.Abort()
			return
		}

		// 从JWT中获取角色信息
		roleInterface, exists := c.Get("role")
		if !exists {
			zap.S().Warnw(
				"RBACMiddleware: role not found in context",
				"path", c.Request.URL.Path,
				"user_id", userID,
			)
			common.Fail(c, common.AuthFailedCode, "角色信息缺失")
			c.Abort()
			return
		}

		role, ok := roleInterface.(string)
		if !ok {
			zap.S().Warnw(
				"RBACMiddleware: role format error",
				"path", c.Request.URL.Path,
				"role_interface", roleInterface,
			)
			common.Fail(c, common.AuthFailedCode, "角色格式错误")
			c.Abort()
			return
		}

		// 获取请求路径和方法
		path := c.Request.URL.Path
		method := c.Request.Method

		// 检查权限（从数据库加载权限）
		if !hasPermission(client, role, method, path, userID, tenantID, c) {
			zap.S().Warnw(
				"RBACMiddleware: permission denied",
				"path", c.Request.URL.Path,
				"method", c.Request.Method,
				"user_id", userID,
				"role", role,
			)
			common.Fail(c, common.ForbiddenCode, "权限不足")
			c.Abort()
			return
		}

		// 调试日志：RBAC检查通过
		zap.S().Infow(
			"RBACMiddleware: access granted",
			"path", c.Request.URL.Path,
			"user_id", userID,
			"role", role,
			"tenant_id", tenantID,
		)

		// 将用户实体信息存储到上下文中
		c.Set("user_entity", userEntity)

		c.Next()
	}
}

// RequirePermission 要求特定权限的中间件
func RequirePermission(resource, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			common.Fail(c, common.AuthFailedCode, "用户角色信息缺失")
			c.Abort()
			return
		}

		// 获取租户ID
		tenantIDInterface, exists := c.Get("tenant_id")
		if !exists {
			common.Fail(c, common.AuthFailedCode, "租户信息缺失")
			c.Abort()
			return
		}
		tenantID := tenantIDInterface.(int)

		// 获取客户端
		clientInterface, exists := c.Get("client")
		if !exists {
			common.Fail(c, common.InternalErrorCode, "客户端缺失")
			c.Abort()
			return
		}
		client := clientInterface.(*ent.Client)

		if !hasResourcePermission(client, role.(string), resource, action, tenantID) {
			common.Fail(c, common.ForbiddenCode, "权限不足")
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireRole enforces that the authenticated user role is one of the allowed roles
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	normalized := make([]string, 0, len(allowedRoles))
	for _, r := range allowedRoles {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(r)))
	}
	return func(c *gin.Context) {
		roleAny, exists := c.Get("role")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{"code": 2003, "message": "缺少角色信息"})
			c.Abort()
			return
		}
		role, _ := roleAny.(string)
		role = strings.ToLower(strings.TrimSpace(role))
		for _, ar := range normalized {
			if role == ar {
				c.Next()
				return
			}
		}
		c.JSON(http.StatusForbidden, gin.H{"code": 2003, "message": "无权限执行该操作"})
		c.Abort()
	}
}

// hasPermission 检查用户是否有权限访问指定资源
// Uses Smart Permission Checker (4-layer fallback architecture)
func hasPermission(client *ent.Client, role, method, path string, userID, tenantID int, c *gin.Context) bool {
	// 超级管理员拥有所有权限
	if role == "super_admin" {
		return true
	}

	// 特殊路径：允许所有认证用户访问自己的菜单
	// 菜单服务会根据用户权限过滤菜单，这里不需要额外权限检查
	if method == "GET" && (path == "/api/v1/auth/menus" || strings.HasPrefix(path, "/api/v1/auth/menus?")) {
		return true
	}

	// 使用智能权限检查器（4层兜底架构）
	// 获取底层数据库连接进行 ACL 查询
	db := database.GetRawDB()
	return SmartCheckPermission(c, db, client, role, method, path, tenantID)
}

// HasResourcePermission 检查角色是否有指定资源的操作权限（导出供 AI 工具 RBAC 校验复用）
// P2-6: AI 工具执行前的 Gate 2 校验入口
func HasResourcePermission(client *ent.Client, role, resource, action string, tenantID int) bool {
	return hasResourcePermission(client, role, resource, action, tenantID)
}

// hasResourcePermission 检查角色是否有指定资源的操作权限（支持多种配置模式）
func hasResourcePermission(client *ent.Client, role, resource, action string, tenantID int) bool {
	// 超级管理员拥有所有权限
	if role == "super_admin" {
		return true
	}

	// 根据配置模式加载权限
	permissions := loadPermissionsByMode(client, role, tenantID)

	// 检查权限
	return checkPermissionMatch(permissions, resource, action)
}

// loadPermissionsByMode 根据配置模式加载权限
func loadPermissionsByMode(client *ent.Client, role string, tenantID int) []Permission {
	switch PermissionConfig.Mode {
	case PermissionConfigModeDBOnly:
		// Production is fail-closed: an empty, revoked, or unavailable database
		// permission set must never regain privileges from compiled defaults.
		return loadPermissionsFromDB(client, role, tenantID)
	case PermissionConfigModeHardcodeOnly:
		if perms, ok := RolePermissions[role]; ok {
			return perms
		}
		return nil
	case PermissionConfigModeMerge:
		// 合并数据库和硬编码权限（并集）
		dbPerms := loadPermissionsFromDB(client, role, tenantID)
		hardcodePerms, hasHardcode := RolePermissions[role]
		if !hasHardcode {
			return dbPerms
		}
		if len(dbPerms) == 0 {
			return hardcodePerms
		}
		// 合并去重
		permMap := make(map[string]Permission)
		for _, p := range dbPerms {
			permMap[p.Resource+":"+p.Action] = p
		}
		for _, p := range hardcodePerms {
			key := p.Resource + ":" + p.Action
			if _, exists := permMap[key]; !exists {
				permMap[key] = p
			}
		}
		result := make([]Permission, 0, len(permMap))
		for _, p := range permMap {
			result = append(result, p)
		}
		return result
	case PermissionConfigModeFallback:
		fallthrough
	default:
		// 默认：先数据库（新的permission_definition+role_permission表，fallback到旧的Permission表），失败则使用硬编码
		dbPerms := loadPermissionsFromDB(client, role, tenantID)
		if len(dbPerms) > 0 {
			return dbPerms
		}
		if defaultPerms, exists := RolePermissions[role]; exists {
			return defaultPerms
		}
		return nil
	}
}

// checkPermissionMatch 检查权限是否匹配
func checkPermissionMatch(permissions []Permission, resource, action string) bool {
	if len(permissions) == 0 {
		return false
	}

	for _, perm := range permissions {
		// 检查通配符权限
		if perm.Resource == "*" && perm.Action == "*" {
			return true
		}
		if perm.Resource == "*" && perm.Action == action {
			return true
		}
		if perm.Resource == resource && perm.Action == "*" {
			return true
		}
		// 资源管理员权限包含该资源下的具体业务动作。
		if perm.Resource == resource && perm.Action == "admin" {
			return true
		}
		if perm.Resource == resource && perm.Action == action {
			return true
		}
	}

	return false
}

// InvalidateRolePermissionCache 使指定角色-租户的权限缓存失效（导出供外部调用）
func InvalidateRolePermissionCache(roleName string, tenantID int) {
	if PermissionConfig.EnableCache {
		invalidatePermissionCache(roleName, tenantID)
	}
}

// InvalidateAllPermissionCachesEx 使所有权限缓存失效（导出供外部调用）
func InvalidateAllPermissionCachesEx() {
	if PermissionConfig.EnableCache {
		InvalidateAllPermissionCaches()
	}
}

// getPermissionFromPath 从路径获取权限信息
func getPermissionFromPath(method, path string) *Permission {
	methodMap, exists := ResourceActionMap[method]
	if !exists {
		return nil
	}

	// 精确匹配
	if perm, exists := methodMap[path]; exists {
		return &perm
	}

	// 通配符匹配。多个规则命中时选择最具体的规则，避免
	// /tickets/* 抢先覆盖 /tickets/*/assign 这类动作权限。
	var matched *Permission
	bestSpecificity := -1
	for pattern, perm := range methodMap {
		if matchPath(pattern, path) {
			specificity := len(strings.ReplaceAll(pattern, "*", ""))
			if specificity > bestSpecificity {
				permission := perm
				matched = &permission
				bestSpecificity = specificity
			}
		}
	}

	return matched
}

// matchPath 匹配路径（支持通配符）
func matchPath(pattern, path string) bool {
	if pattern == path {
		return true
	}

	// 末尾 * 保持历史语义：匹配剩余任意层级。
	if strings.Count(pattern, "*") == 1 && strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(path, prefix)
	}

	// 中间 * 匹配单个路径段，例如 /tickets/*/assign。
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i := range patternParts {
		if patternParts[i] != "*" && patternParts[i] != pathParts[i] {
			return false
		}
	}
	return true
}
