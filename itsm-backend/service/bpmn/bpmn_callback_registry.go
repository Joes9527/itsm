package bpmn

import (
	"sync"

	"itsm-backend/ent"

	"go.uber.org/zap"
)

// CallbackRegistry 流程回调注册中心
// 负责管理所有服务任务处理器的注册和处理流程回调
type CallbackRegistry struct {
	client     *ent.Client
	logger     *zap.SugaredLogger
	handlers   map[string]ServiceTaskHandlerInterface
	handlersMu sync.RWMutex
}

// NewCallbackRegistry 创建新的回调注册中心
func NewCallbackRegistry(client *ent.Client, logger *zap.SugaredLogger) *CallbackRegistry {
	registry := &CallbackRegistry{
		client:   client,
		logger:   logger,
		handlers: make(map[string]ServiceTaskHandlerInterface),
	}

	// 注册默认处理器
	registry.registerDefaultHandlers()

	return registry
}

// RegisterHandler 注册服务任务处理器
func (r *CallbackRegistry) RegisterHandler(handler ServiceTaskHandlerInterface) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()
	r.handlers[handler.GetHandlerID()] = handler
}

// UnregisterHandler 注销处理器
func (r *CallbackRegistry) UnregisterHandler(handlerID string) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()
	delete(r.handlers, handlerID)
}

// GetHandler 获取处理器
func (r *CallbackRegistry) GetHandler(handlerID string) ServiceTaskHandlerInterface {
	r.handlersMu.RLock()
	defer r.handlersMu.RUnlock()
	return r.handlers[handlerID]
}

// registerDefaultHandlers 注册默认处理器
// 注意：通知服务的设置需要在外部通过 SetNotificationService 完成
func (r *CallbackRegistry) registerDefaultHandlers() {
	// 注册 Ticket 服务任务处理器
	r.RegisterHandler(NewTicketServiceTaskHandler(r.client, r.logger))

	// 注册 Change 服务任务处理器
	r.RegisterHandler(NewChangeServiceTaskHandler(r.client, r.logger))
	// 注册 Incident 服务任务处理器
	r.RegisterHandler(NewIncidentServiceTaskHandler(r.client, r.logger))
	// 注册通用服务任务处理器
	r.RegisterHandler(NewGenericServiceTaskHandler(r.client, r.logger))
	// 注册服务请求处理器
	r.RegisterHandler(NewServiceRequestServiceTaskHandler(r.client, r.logger))
	// 注册通知处理器
	r.RegisterHandler(NewNotificationHandler(r.client, r.logger))
	// 注册Webhook处理器
	// 注册抄送处理器
	r.RegisterHandler(NewCCTaskHandler(r.client, r.logger))
	r.RegisterHandler(NewWebhookHandler(r.client, r.logger))
	// 注册发布服务任务处理器
	r.RegisterHandler(NewReleaseServiceTaskHandler(r.client, r.logger))
	// 注册 KAF 委派处理器（异步，见 KafDelegateServiceTaskHandler 注释）
	r.RegisterHandler(NewKafDelegateServiceTaskHandler(r.client, r.logger))
}

// RegisterTicketHandlerWithNotification 注册带通知服务的工单处理器
func (r *CallbackRegistry) RegisterTicketHandlerWithNotification(handler *TicketServiceTaskHandler) {
	r.RegisterHandler(handler)
}

// ListHandlers 列出所有已注册的处理器
func (r *CallbackRegistry) ListHandlers() []ServiceTaskHandlerInterface {
	r.handlersMu.RLock()
	defer r.handlersMu.RUnlock()

	handlers := make([]ServiceTaskHandlerInterface, 0, len(r.handlers))
	for _, h := range r.handlers {
		handlers = append(handlers, h)
	}
	return handlers
}
