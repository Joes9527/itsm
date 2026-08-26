package bpmn

import (
	"context"
	"fmt"

	"itsm-backend/dto"
	"itsm-backend/ent"
)

// bpmnTenantIDKey is the unexported context key used to store the BPMN tenant ID.
type bpmnTenantIDKey struct{}

// BPMNTenantIDContextKey is the exported context key for the BPMN tenant ID.
// Using a typed key (instead of a plain string) prevents collisions with other
// packages that store values under the same context.
var BPMNTenantIDContextKey = bpmnTenantIDKey{}

type bpmnUserIDKey struct{}

// BPMNUserIDContextKey carries the authenticated actor into workflow services.
// It must only be populated from trusted authentication middleware.
var BPMNUserIDContextKey = bpmnUserIDKey{}

type bpmnElevatedKey struct{}

// BPMNElevatedContextKey carries whether the caller holds the elevated
// RBAC permission for the endpoint being served (e.g. process_instance:read,
// task:read, task:update — the specific resource:action pair is decided by
// the controller handler, not here). When true, participant-scoping is
// skipped and the caller sees/acts on data tenant-wide, matching ops-console
// use cases. Must only ever be set from a server-computed
// middleware.HasResourcePermission(...) result — never from client input.
var BPMNElevatedContextKey = bpmnElevatedKey{}

type bpmnSystemCallerKey struct{}

// BPMNSystemCallerContextKey carries an explicit declaration that this call
// originates from trusted internal/system code (e.g. a ticket creation flow
// auto-starting a BPMN process), not from an authenticated human caller.
// authorizeTaskViewer/authorizeTaskMutation/authorizeTaskActor/
// authorizeProcessInstanceViewer/authorizeProcessInstanceMutation check this
// key explicitly and fail closed when it's absent — replacing the previous
// implicit "no userID in context = permissive" convention. That convention
// was a latent fail-open trap, and real system-caller use cases DO exist:
// Task 4 found and fixed three production call sites in
// service/bpmn_approval_bridge_service.go (release/change stage-transition
// bridges calling CompleteTask with actorUserID=0 by design), plus two more
// internal engine call sites (createUserTask's counter-sign fan-out, Vote's
// parent-task auto-completion) that previously relied on this same implicit
// permissiveness. Do not assume a guarded method has zero non-HTTP callers —
// audit call sites explicitly each time one of these functions is hardened,
// and declare this key narrowly at the specific call site that needs it,
// not smeared over a broader ctx used for other purposes. Must only ever be
// set by code that is itself not reachable from an HTTP request — never
// derived from a client-suppliable field.
var BPMNSystemCallerContextKey = bpmnSystemCallerKey{}

// ServiceTaskHandlerInterface 服务任务处理器接口
// 定义所有服务任务处理器需要实现的方法
type ServiceTaskHandlerInterface interface {
	// GetTaskType 返回处理器支持的任务类型
	GetTaskType() string

	// Execute 执行服务任务
	Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error)

	// Validate 验证参数
	Validate(ctx context.Context, config map[string]interface{}) error

	// GetHandlerID 返回处理器标识
	GetHandlerID() string
}

// HandlerBase 处理器基类
// 提供通用的辅助方法
type HandlerBase struct {
	// 可以在这里添加通用的字段
}

// GetIntFromVars 从变量中提取整数
func GetIntFromVars(variables map[string]interface{}, key string) int {
	if v, ok := variables[key]; ok {
		switch val := v.(type) {
		case float64:
			return int(val)
		case int:
			return val
		case int64:
			return int(val)
		}
	}
	return 0
}

// GetIntSliceFromVars 从变量中提取整数切片
func GetIntSliceFromVars(variables map[string]interface{}, key string) []int {
	if v, ok := variables[key]; ok {
		if val, ok := v.([]interface{}); ok {
			res := make([]int, 0, len(val))
			for _, item := range val {
				switch i := item.(type) {
				case float64:
					res = append(res, int(i))
				case int:
					res = append(res, i)
				case int64:
					res = append(res, int(i))
				}
			}
			return res
		}
	}
	return []int{}
}

// GetBoolFromVars 从变量中提取布尔值
func GetBoolFromVars(variables map[string]interface{}, key string, defaultValue bool) bool {
	if v, ok := variables[key]; ok {
		switch val := v.(type) {
		case bool:
			return val
		case int:
			return val != 0
		case float64:
			return val != 0
		case string:
			return val == "true" || val == "1" || val == "yes"
		}
	}
	return defaultValue
}

// GetStringFromVars 从变量中提取字符串
func GetStringFromVars(variables map[string]interface{}, key string) string {
	if v, ok := variables[key]; ok {
		if val, ok := v.(string); ok {
			return val
		}
	}
	return ""
}

// GetTenantIDFromVars 解析当前操作的租户ID，优先级与
// TicketServiceTaskHandler.getTenantID 完全一致：
//
//  1. ctx 里的 BPMNTenantIDContextKey —— 唯一可信来源。它由认证中间件解析出的 JWT 会话
//     经 controller 的 getBPMNTenantContext / BPMNApprovalBridge 注入，客户端伪造不了。
//  2. variables["tenant_id"] —— 仅作兜底。ServiceTask 分发路径上它来自
//     ProcessTriggerService 写入的实例变量（可信），但 UserTask 回调路径
//     （PUT /tasks/:id/complete → dispatchUserTaskCallback）会把请求体里的
//     req.Variables 原样透传进来，所以这一层整体上必须当作不可信输入。
//  3. 两者都没有 → 返回 0，fail closed。
//
// 绝不能像旧实现那样默认回落到租户 1：调用方可以带着别的租户的 business_id 完成自己的
// 合法任务，租户解析一旦默认到 1，越权写入就正好落在租户 1 的真实业务数据上。
// 返回 0 时，调用方必须自己拒绝执行租户范围内的读写（见各 handler 的 requireTenantID）。
func GetTenantIDFromVars(ctx context.Context, variables map[string]interface{}) int {
	if ctx != nil {
		if tenantID, ok := ctx.Value(BPMNTenantIDContextKey).(int); ok && tenantID > 0 {
			return tenantID
		}
	}
	return GetIntFromVars(variables, "tenant_id")
}

// RequireTenantID 在 GetTenantIDFromVars 之上加一道 fail-closed 断言：
// 涉及租户范围读写的 handler 动作宁可明确报错，也不要在"租户未知"的情况下发出
// 一条不带 tenant 过滤的全局查询/更新。
func RequireTenantID(ctx context.Context, variables map[string]interface{}) (int, error) {
	tenantID := GetTenantIDFromVars(ctx, variables)
	if tenantID <= 0 {
		return 0, fmt.Errorf("无法确定租户上下文，拒绝执行租户范围操作")
	}
	return tenantID, nil
}

// HandlerRegistry 处理器注册中心
// 负责管理所有服务任务处理器的注册和获取
type HandlerRegistry struct {
	handlers map[string]ServiceTaskHandlerInterface
}

// NewHandlerRegistry 创建新的处理器注册中心
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: make(map[string]ServiceTaskHandlerInterface),
	}
}

// Register 注册处理器
func (r *HandlerRegistry) Register(handler ServiceTaskHandlerInterface) {
	r.handlers[handler.GetHandlerID()] = handler
}

// Unregister 注销处理器
func (r *HandlerRegistry) Unregister(handlerID string) {
	delete(r.handlers, handlerID)
}

// GetHandler 获取处理器
func (r *HandlerRegistry) GetHandler(handlerID string) ServiceTaskHandlerInterface {
	return r.handlers[handlerID]
}

// GetHandlerByTaskType 根据任务类型获取处理器
func (r *HandlerRegistry) GetHandlerByTaskType(taskType string) ServiceTaskHandlerInterface {
	// 精确匹配
	if handler, ok := r.handlers[taskType]; ok {
		return handler
	}

	// 通配匹配
	for _, handler := range r.handlers {
		if handler.GetTaskType() == taskType {
			return handler
		}
	}

	return nil
}

// ListHandlers 列出所有已注册的处理器
func (r *HandlerRegistry) ListHandlers() []ServiceTaskHandlerInterface {
	handlers := make([]ServiceTaskHandlerInterface, 0, len(r.handlers))
	for _, h := range r.handlers {
		handlers = append(handlers, h)
	}
	return handlers
}
