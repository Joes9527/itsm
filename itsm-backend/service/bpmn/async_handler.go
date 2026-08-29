package bpmn

// AsyncServiceTaskHandler 是 ServiceTaskHandlerInterface 的可选扩展接口。
// 实现此接口且 IsAsync() 返回 true 的 handler，其对应的 serviceTask 节点
// 在流程到达时不会同步执行 Execute，而是创建 ProcessTask 并暂停，直到外部
// 通过 CompleteTask 显式完成——见 CustomProcessEngine.createDelegatedTask。
//
// 这是一个能力接口而不是对 ServiceTaskHandlerInterface 的必需扩展：现有的
// 9 个同步 handler（Ticket/Change/Incident/Generic/ServiceRequest/Notification/
// CC/Webhook/Release）不需要实现它，类型断言落空时自动走原有同步路径。
type AsyncServiceTaskHandler interface {
	IsAsync() bool
}

// KafDelegateTaskType 是 kaf_delegate 委派节点在 BPMN metaData 里声明的
// service_task_type 值，也是对应 ProcessTask.TaskType 的值。定义在这里
// （而不是 KafDelegateServiceTaskHandler 所在文件）是因为 CustomProcessEngine
// 的 authorizeTaskActor 也需要引用它，属于跨文件共享的常量。
const KafDelegateTaskType = "kaf_delegate"
