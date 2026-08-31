package bpmn

import (
	"context"

	"itsm-backend/dto"
	"itsm-backend/ent"

	"go.uber.org/zap"
)

// KafDelegateServiceTaskHandler 是 kaf_delegate 委派节点的服务任务处理器。
//
// 它是异步 handler（IsAsync()==true）：流程到达声明了 service_task_type="kaf_delegate"
// 的节点时，引擎的 handleElement 不会调用它的 Execute，而是创建 ProcessTask 并暂停
// （见 CustomProcessEngine.createDelegatedTask）。Execute 只在任务完成后的异步回调
// 阶段触发一次，用于记录/审计，不产生任何业务副作用——真正的
// WorkItem 动作（resolve/close 等）走上游委派设计 §4.3 的 typed action API，
// 不经过这个 Execute。
type KafDelegateServiceTaskHandler struct {
	logger *zap.SugaredLogger
}

// NewKafDelegateServiceTaskHandler 创建 KAF 委派任务处理器
func NewKafDelegateServiceTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *KafDelegateServiceTaskHandler {
	return &KafDelegateServiceTaskHandler{logger: logger}
}

// GetTaskType 返回任务类型
func (h *KafDelegateServiceTaskHandler) GetTaskType() string {
	return KafDelegateTaskType
}

// GetHandlerID 返回处理器标识
func (h *KafDelegateServiceTaskHandler) GetHandlerID() string {
	return "kaf_delegate_handler"
}

// IsAsync 声明该 handler 对应的 serviceTask 节点是暂停型的，不同步执行。
func (h *KafDelegateServiceTaskHandler) IsAsync() bool {
	return true
}

// Validate 验证配置
func (h *KafDelegateServiceTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// Execute 只在委派任务完成后的异步回调阶段被调用一次，用于记录完成事件，
// 不产生业务副作用。
func (h *KafDelegateServiceTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	taskID := ""
	tenantID := 0
	if task != nil {
		taskID = task.TaskID
		tenantID = task.TenantID
	}
	if scope, ok := KafActionScopeFromContext(ctx); ok {
		h.logger.Infow("KAF 委派任务已完成", "taskID", taskID, "tenantID", tenantID, "ledgerID", scope.LedgerID(), "runID", scope.RunID(), "stepID", scope.StepID())
	} else {
		h.logger.Infow("KAF 委派任务已完成", "taskID", taskID, "tenantID", tenantID)
	}
	return &dto.ServiceTaskResult{Success: true, Message: "kaf_delegate 任务已完成"}, nil
}

// 确保 KafDelegateServiceTaskHandler 实现了 ServiceTaskHandlerInterface 和 AsyncServiceTaskHandler
var _ ServiceTaskHandlerInterface = (*KafDelegateServiceTaskHandler)(nil)
var _ AsyncServiceTaskHandler = (*KafDelegateServiceTaskHandler)(nil)
