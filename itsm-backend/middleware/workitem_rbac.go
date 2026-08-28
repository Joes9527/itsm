package middleware

import (
	"strconv"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"

	"github.com/gin-gonic/gin"
)

// resourceForRecordClass 把 tickets.record_class 映射到 RBAC 资源名，供
// RequireWorkItemRecordClassPermission 使用。除 incident/problem/change_request 三个专业域外，
// 其余 record_class（generic/service_request_item/catalog_task，以及未来任何新值）统一映射到
// "ticket"——这三个是本设计新引入的专业资源名，其余都是 Ticket 自己的记录类型。
func resourceForRecordClass(recordClass string) string {
	switch recordClass {
	case "incident":
		return "incident"
	case "problem":
		return "problem"
	case "change_request":
		return "change"
	default:
		return "ticket"
	}
}

// RequireWorkItemRecordClassPermission 按路径参数 :id 对应 tickets 行的实际 record_class
// 动态解析资源名，再复用现有的 hasResourcePermission。用于 WorkItem 级共享接口
// （comments/attachments/history/relations/sla），因为同一条路由现在可能服务 Ticket、
// Incident、Problem 或 Change 四种专业域，静态 RequirePermission("ticket", action) 会
// 错误地要求非 Ticket 域的查看者也必须有 ticket 权限。
//
// 认证上下文提取（role/tenant_id/client）与 RequirePermission 完全一致，是刻意复制而不是
// 提取公共函数——两者都要在各自的错误分支里返回不同的错误信息，抽出来反而增加间接层。
func RequireWorkItemRecordClassPermission(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			common.Fail(c, common.AuthFailedCode, "用户角色信息缺失")
			c.Abort()
			return
		}

		tenantIDInterface, exists := c.Get("tenant_id")
		if !exists {
			common.Fail(c, common.AuthFailedCode, "租户信息缺失")
			c.Abort()
			return
		}
		tenantID := tenantIDInterface.(int)

		clientInterface, exists := c.Get("client")
		if !exists {
			common.Fail(c, common.InternalErrorCode, "客户端缺失")
			c.Abort()
			return
		}
		client := clientInterface.(*ent.Client)

		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			common.Fail(c, common.ParamErrorCode, "无效的工单ID")
			c.Abort()
			return
		}

		t, err := client.Ticket.Query().
			Where(ticket.ID(id), ticket.TenantID(tenantID), ticket.DeletedAtIsNil()).
			Only(c.Request.Context())
		if err != nil {
			// 查不到该 ticket（不存在、跨租户、或已软删除）统一返回 404，不是 403——避免让响应
			// 差异变成一个可以探测其它租户 ID 是否存在的信号；DeletedAtIsNil() 与
			// repository/ticket/repository_impl.go 的 EntRepository.GetByID 保持一致，
			// 避免软删除的工单还能通过这条共享路由的 RBAC 网关被访问。
			common.Fail(c, common.NotFoundCode, "工单不存在")
			c.Abort()
			return
		}

		resource := resourceForRecordClass(t.RecordClass)
		if !hasResourcePermission(client, role.(string), resource, action, tenantID) {
			common.Fail(c, common.ForbiddenCode, "权限不足")
			c.Abort()
			return
		}

		c.Next()
	}
}
