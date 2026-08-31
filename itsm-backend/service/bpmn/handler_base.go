package bpmn

import (
	"context"
	"fmt"
	"strings"

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

type bpmnCallbackExecutionKeyContextKey struct{}

// WithBPMNCallbackExecutionKey attaches the durable callback idempotency label.
// It is deliberately separate from authorization and tenant context.
func WithBPMNCallbackExecutionKey(ctx context.Context, key string) context.Context {
	key = strings.TrimSpace(key)
	if key == "" {
		panic("bpmn callback execution key is required")
	}
	return context.WithValue(ctx, bpmnCallbackExecutionKeyContextKey{}, key)
}

// BPMNCallbackExecutionKey returns the durable idempotency label when a caller
// entered through the callback outbox processor.
func BPMNCallbackExecutionKey(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	key, ok := ctx.Value(bpmnCallbackExecutionKeyContextKey{}).(string)
	key = strings.TrimSpace(key)
	return key, ok && key != ""
}

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

// CallbackPayloadPolicy declares the only participant/process fields a handler
// permits in durable callback storage. Handlers without this interface receive
// an empty durable payload.
type CallbackPayloadPolicy interface {
	CallbackPayloadFields(action string) []string
}

// CallbackPayloadNormalizer lets a handler derive its durable callback payload
// from dynamic process values without persisting those dynamic source fields.
type CallbackPayloadNormalizer interface {
	NormalizeCallbackPayload(action string, variables map[string]interface{}) (map[string]interface{}, error)
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
		switch val := v.(type) {
		case []int:
			return append([]int(nil), val...)
		case []interface{}:
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

// GetTenantIDFromVars returns only the execution context tenant. Callback
// variables are untrusted and can never select a tenant.
func GetTenantIDFromVars(ctx context.Context, variables map[string]interface{}) int {
	if ctx != nil {
		if tenantID, ok := ctx.Value(BPMNTenantIDContextKey).(int); ok && tenantID > 0 {
			return tenantID
		}
	}
	return 0
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
