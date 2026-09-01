package middleware

import (
	"strconv"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/ent"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

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

		_, _, err = authorization.AuthorizeWorkItem(c.Request.Context(), client, id, tenantID, role.(string), action)
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
