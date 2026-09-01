package middleware

import (
	"context"
	"fmt"
	"strconv"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// resourceForRecordClass 把 tickets.record_class 映射到 RBAC 资源名，供
// RequireWorkItemRecordClassPermission 使用。除 incident/problem/change_request 三个专业域外，
// service_request_item/catalog_task 走 service_request 专业域权限；未知类型 fail closed。
func resourceForRecordClass(recordClass string) string {
	switch recordClass {
	case "incident":
		return "incident"
	case "problem":
		return "problem"
	case "change_request":
		return "change"
	case "service_request_item", "catalog_task":
		return "service_request"
	case "generic":
		return "ticket"
	default:
		return ""
	}
}

// resolveWorkItemPermission 把 (tickets.record_class, 路由声明的 action) 解析成
// RBAC 实际要检查的 (resource, resolvedAction) 二元组。incident/problem/change 三个
// 资源的权限词表只有 read/write/delete（见 pkg/seeder/seeder.go 的权限定义），没有独立的
// create/update——历史上 Incident 自己的评论路由用的就是 incident:write（router.go 里
// inc.POST("/:id/comments", ..., RequirePermission("incident", "write"), ...)），所以这里把
// create/update 归一化成 write，不引入第二套动作词表。ticket 资源保留原有的细分动作
// （ticket:create/ticket:update 都是真实存在的权限码），不做任何归一化。
func resolveWorkItemPermission(recordClass, action string) (resource string, resolvedAction string, err error) {
	resource = resourceForRecordClass(recordClass)
	if resource == "" {
		return "", "", fmt.Errorf("unsupported WorkItem record class %q", recordClass)
	}
	if resource == "ticket" || resource == "service_request" {
		return resource, action, nil
	}
	switch action {
	case "create", "update":
		return resource, "write", nil
	default:
		return resource, action, nil
	}
}

// AuthorizeWorkItemRecordClassPermission is the shared WorkItem object-level
// policy used by HTTP middleware and application services. It first resolves a
// live tenant-scoped WorkItem, then checks the immutable recordClass against
// the owning professional domain's RBAC resource.
func AuthorizeWorkItemRecordClassPermission(ctx context.Context, client *ent.Client, workItemID, tenantID int, role, action string) (*ent.Ticket, error) {
	if client == nil {
		return nil, common.NewInternalError("WorkItem authorization client is unavailable", nil)
	}
	workItem, err := client.Ticket.Query().
		Where(ticket.ID(workItemID), ticket.TenantID(tenantID), ticket.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, common.NewNotFoundError("work item")
		}
		return nil, common.NewInternalError("query WorkItem for authorization", err)
	}
	resource, resolvedAction, err := resolveWorkItemPermission(workItem.RecordClass, action)
	if err != nil {
		return nil, common.NewForbiddenError("unsupported WorkItem record class")
	}
	if !hasResourcePermission(client, role, resource, resolvedAction, tenantID) {
		return nil, common.NewForbiddenError("insufficient WorkItem permission")
	}
	return workItem, nil
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

		_, err = AuthorizeWorkItemRecordClassPermission(c.Request.Context(), client, id, tenantID, role.(string), action)
		if err != nil {
			appErr, ok := err.(*common.AppError)
			if !ok || appErr.Code == common.ErrCodeInternal {
				// 真正的 DB 故障（连接断开、超时……）不能和"工单不存在"混为一谈——否则一次
				// 数据库中断会被误读成一堆正常的 404，掩盖真实的可观测性信号。
				zap.S().Warnw(
					"RequireWorkItemRecordClassPermission: ticket lookup failed",
					"ticket_id", id,
					"tenant_id", tenantID,
					"error", err.Error(),
				)
				common.Fail(c, common.InternalErrorCode, "查询工单失败")
				c.Abort()
				return
			}
			// 查不到该 ticket（不存在、跨租户、或已软删除）统一返回 404，不是 403——避免让响应
			// 差异变成一个可以探测其它租户 ID 是否存在的信号；DeletedAtIsNil() 与
			// repository/ticket/repository_impl.go 的 EntRepository.GetByID 保持一致，
			// 避免软删除的工单还能通过这条共享路由的 RBAC 网关被访问。
			if appErr.Code == common.ErrCodeNotFound {
				common.Fail(c, common.NotFoundCode, "工单不存在")
				c.Abort()
				return
			}
			common.Fail(c, common.ForbiddenCode, "权限不足")
			c.Abort()
			return
		}

		c.Next()
	}
}
