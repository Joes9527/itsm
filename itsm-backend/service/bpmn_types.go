package service

import (
	"encoding/xml"
	"strings"
)

// BPMN metaData 的 key 常量。写进 ProcessTask.TaskVariables 时沿用同名 key，
// CompleteTask 再按同名 key 读回来做回调分发。
const (
	bpmnMetaDataServiceTaskType = "service_task_type"
	bpmnMetaDataAction          = "action"
)

// BPMNElement BPMN元素的基础接口
type BPMNElement interface {
	GetID() string
	GetName() string
	GetType() string
}

// BPMNProcess BPMN流程定义
type BPMNProcess struct {
	ID                 string                   `xml:"id,attr"`
	Name               string                   `xml:"name,attr"`
	ProcessType        string                   `xml:"processType,attr"`
	IsExecutable       bool                     `xml:"isExecutable,attr"`
	IsClosed           bool                     `xml:"isClosed,attr"`
	StartEvents        []*BPMNStartEvent        `xml:"startEvent"`
	EndEvents          []*BPMNEndEvent          `xml:"endEvent"`
	UserTasks          []*BPMNUserTask          `xml:"userTask"`
	ServiceTasks       []*BPMNServiceTask       `xml:"serviceTask"`
	ScriptTasks        []*BPMNScriptTask        `xml:"scriptTask"`
	BusinessRuleTasks  []*BPMNBusinessRuleTask  `xml:"businessRuleTask"`
	ManualTasks        []*BPMNManualTask        `xml:"manualTask"`
	CallActivities     []*BPMNCallActivity      `xml:"callActivity"`
	ExclusiveGateways  []*BPMNExclusiveGateway  `xml:"exclusiveGateway"`
	ParallelGateways   []*BPMNParallelGateway   `xml:"parallelGateway"`
	InclusiveGateways  []*BPMNInclusiveGateway  `xml:"inclusiveGateway"`
	SequenceFlows      []*BPMNSequenceFlow      `xml:"sequenceFlow"`
	SubProcesses       []*BPMNSubProcess        `xml:"subProcess"`
	BoundaryEvents     []*BPMNBoundaryEvent     `xml:"boundaryEvent"`
	IntermediateEvents []*BPMNIntermediateEvent `xml:"intermediateCatchEvent"`
	DataObjects        []*BPMNDataObject        `xml:"dataObject"`
	DataStores         []*BPMNDataStore         `xml:"dataStore"`
}

// BPMNStartEvent 开始事件
type BPMNStartEvent struct {
	ID         string `xml:"id,attr"`
	Name       string `xml:"name,attr"`
	EventType  string `xml:"eventType,attr"`
	MessageRef string `xml:"messageRef,attr"`
	TimerRef   string `xml:"timerRef,attr"`
	SignalRef  string `xml:"signalRef,attr"`
}

// GetID 获取ID
func (e *BPMNStartEvent) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNStartEvent) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNStartEvent) GetType() string { return "StartEvent" }

// BPMNEndEvent 结束事件
type BPMNEndEvent struct {
	ID         string `xml:"id,attr"`
	Name       string `xml:"name,attr"`
	EventType  string `xml:"eventType,attr"`
	MessageRef string `xml:"messageRef,attr"`
	SignalRef  string `xml:"signalRef,attr"`
	ErrorRef   string `xml:"errorRef,attr"`
}

// GetID 获取ID
func (e *BPMNEndEvent) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNEndEvent) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNEndEvent) GetType() string { return "EndEvent" }

// BPMNMetaData 是 <bpmn:extensionElements> 下的一条 <bpmn:metaData name="...">值</bpmn:metaData>。
// 模板用它承载引擎需要的声明式配置，目前用到的是 service_task_type / action
// （见 change_normal_flow.bpmn 等模板），用来把节点完成后的业务副作用路由到
// service/bpmn 下已注册的 ServiceTaskHandler。
type BPMNMetaData struct {
	Name  string `xml:"name,attr"`
	Value string `xml:",chardata"`
}

// BPMNExtensionElements 对应 <bpmn:extensionElements>。
// 只解析 metaData，其余厂商扩展元素按 encoding/xml 的默认行为忽略。
type BPMNExtensionElements struct {
	MetaData []BPMNMetaData `xml:"metaData"`
}

// GetMetaData 按 name 返回 metaData 的值（去掉首尾空白），不存在时返回空串。
// 接收者可为 nil，调用方无需先判空。
func (e *BPMNExtensionElements) GetMetaData(name string) string {
	if e == nil {
		return ""
	}
	for _, md := range e.MetaData {
		if md.Name == name {
			return strings.TrimSpace(md.Value)
		}
	}
	return ""
}

// BPMNUserTask 用户任务
type BPMNUserTask struct {
	ID                      string `xml:"id,attr"`
	Name                    string `xml:"name,attr"`
	Assignee                string `xml:"assignee,attr"`
	CandidateUsers          string `xml:"candidateUsers,attr"`
	CandidateGroups         string `xml:"candidateGroups,attr"`
	Priority                int    `xml:"priority,attr"`
	FormKey                 string `xml:"formKey,attr"`
	DueDate                 string `xml:"dueDate,attr"`
	TaskPurpose             string `xml:"taskPurpose,attr"`
	ApprovalMode            string `xml:"approvalMode,attr"`
	ApprovalThreshold       int    `xml:"approvalThreshold,attr"`
	RejectStrategy          string `xml:"rejectStrategy,attr"`
	TimeoutAction           string `xml:"timeoutAction,attr"`
	AllowDelegate           bool   `xml:"allowDelegate,attr"`
	AllowAddApprover        bool   `xml:"allowAddApprover,attr"`
	CommentRequiredOnReject bool   `xml:"commentRequiredOnReject,attr"`
	AssigneeRole            string `xml:"assigneeRole,attr"`
	AssigneeDeptId          int    `xml:"assigneeDeptId,attr"`
	AssigneeTeamId          int    `xml:"assigneeTeamId,attr"`
	AssigneeProjectId       int    `xml:"assigneeProjectId,attr"`
	AssigneeTempTeamId      int    `xml:"assigneeTempTeamId,attr"`
	AssigneeGmChain         bool   `xml:"assigneeGmChain,attr"`
	FulfillmentTeamCode     string `xml:"fulfillmentTeamCode,attr"`

	// ExtensionElements 承载 <bpmn:metaData>，其中 service_task_type/action 决定
	// 该用户任务完成后要不要走 ServiceTaskHandler 回调（见 CompleteTask）。
	ExtensionElements *BPMNExtensionElements `xml:"extensionElements"`
}

// ServiceTaskType 返回该用户任务声明的 service_task_type metaData，未声明时返回空串。
func (e *BPMNUserTask) ServiceTaskType() string {
	return e.ExtensionElements.GetMetaData(bpmnMetaDataServiceTaskType)
}

// ServiceTaskAction 返回该用户任务声明的 action metaData，未声明时返回空串。
func (e *BPMNUserTask) ServiceTaskAction() string {
	return e.ExtensionElements.GetMetaData(bpmnMetaDataAction)
}

// GetID 获取ID
func (e *BPMNUserTask) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNUserTask) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNUserTask) GetType() string { return "UserTask" }

// BPMNServiceTask 服务任务
type BPMNServiceTask struct {
	ID                 string `xml:"id,attr"`
	Name               string `xml:"name,attr"`
	Type               string `xml:"type,attr"`
	OperationRef       string `xml:"operationRef,attr"`
	Implementation     string `xml:"implementation,attr"`
	Class              string `xml:"class,attr"`
	Expression         string `xml:"expression,attr"`
	DelegateExpression string `xml:"delegateExpression,attr"`
	CCType             string `xml:"ccType,attr"`
	CCUserIDs          string `xml:"ccUserIds,attr"`
	CCGroupIDs         string `xml:"ccGroupIds,attr"`
	CCRoleIDs          string `xml:"ccRoleIds,attr"`
	CCVariable         string `xml:"ccVariable,attr"`
	CCNotify           string `xml:"ccNotify,attr"`
	NotifyChannels     string `xml:"notifyChannels,attr"`

	// ExtensionElements 承载 <bpmn:metaData>，用法跟 BPMNUserTask 完全一样——
	// 之前这里没有这个字段，encoding/xml 会静默丢弃 <bpmn:extensionElements> 子元素，
	// 导致内置模板里所有 serviceTask 声明的 service_task_type/action 完全读不到，
	// handleElement 只能退回按 implementation/class 等属性猜 handler ID，而内置模板
	// 这些属性要么是占位符 "##WebService" 要么整个不写，猜测必然落空。
	ExtensionElements *BPMNExtensionElements `xml:"extensionElements"`
}

// ServiceTaskType 返回该服务任务声明的 service_task_type metaData，未声明时返回空串。
func (e *BPMNServiceTask) ServiceTaskType() string {
	return e.ExtensionElements.GetMetaData(bpmnMetaDataServiceTaskType)
}

// ServiceTaskAction 返回该服务任务声明的 action metaData，未声明时返回空串。
func (e *BPMNServiceTask) ServiceTaskAction() string {
	return e.ExtensionElements.GetMetaData(bpmnMetaDataAction)
}

// GetID 获取ID
func (e *BPMNServiceTask) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNServiceTask) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNServiceTask) GetType() string { return "ServiceTask" }

// BPMNScriptTask 脚本任务
type BPMNScriptTask struct {
	ID           string `xml:"id,attr"`
	Name         string `xml:"name,attr"`
	ScriptFormat string `xml:"scriptFormat,attr"`
	Script       string `xml:"script"`
}

// GetID 获取ID
func (e *BPMNScriptTask) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNScriptTask) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNScriptTask) GetType() string { return "ScriptTask" }

// BPMNExclusiveGateway 排他网关
type BPMNExclusiveGateway struct {
	ID          string `xml:"id,attr"`
	Name        string `xml:"name,attr"`
	DefaultFlow string `xml:"default,attr"`
}

// GetID 获取ID
func (e *BPMNExclusiveGateway) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNExclusiveGateway) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNExclusiveGateway) GetType() string { return "ExclusiveGateway" }

// BPMNParallelGateway 并行网关
type BPMNParallelGateway struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

// GetID 获取ID
func (e *BPMNParallelGateway) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNParallelGateway) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNParallelGateway) GetType() string { return "ParallelGateway" }

// BPMNInclusiveGateway 包容网关
type BPMNInclusiveGateway struct {
	ID          string `xml:"id,attr"`
	Name        string `xml:"name,attr"`
	DefaultFlow string `xml:"default,attr"`
}

// GetID 获取ID
func (e *BPMNInclusiveGateway) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNInclusiveGateway) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNInclusiveGateway) GetType() string { return "InclusiveGateway" }

// BPMNSequenceFlow 顺序流
type BPMNSequenceFlow struct {
	ID                  string                   `xml:"id,attr"`
	Name                string                   `xml:"name,attr"`
	SourceRef           string                   `xml:"sourceRef,attr"`
	TargetRef           string                   `xml:"targetRef,attr"`
	ConditionExpression *BPMNConditionExpression `xml:"conditionExpression"`
}

// GetID 获取ID
func (e *BPMNSequenceFlow) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNSequenceFlow) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNSequenceFlow) GetType() string { return "SequenceFlow" }

// BPMNConditionExpression 条件表达式
type BPMNConditionExpression struct {
	Type       string `xml:"type,attr"`
	Expression string `xml:",chardata"`
}

// BPMNSubProcess 子流程
type BPMNSubProcess struct {
	ID               string `xml:"id,attr"`
	Name             string `xml:"name,attr"`
	TriggeredByEvent bool   `xml:"triggeredByEvent,attr"`
}

// GetID 获取ID
func (e *BPMNSubProcess) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNSubProcess) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNSubProcess) GetType() string { return "SubProcess" }

// BPMNBusinessRuleTask 业务规则任务
type BPMNBusinessRuleTask struct {
	ID                 string `xml:"id,attr"`
	Name               string `xml:"name,attr"`
	Implementation     string `xml:"implementation,attr"`
	Class              string `xml:"class,attr"`
	Expression         string `xml:"expression,attr"`
	DelegateExpression string `xml:"delegateExpression,attr"`
	DecisionTaskRef    string `xml:"decisionTaskRef,attr"`
}

// GetID 获取ID
func (e *BPMNBusinessRuleTask) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNBusinessRuleTask) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNBusinessRuleTask) GetType() string { return "BusinessRuleTask" }

// BPMNManualTask 手工任务
type BPMNManualTask struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

// GetID 获取ID
func (e *BPMNManualTask) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNManualTask) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNManualTask) GetType() string { return "ManualTask" }

// BPMNCallActivity 调用活动
type BPMNCallActivity struct {
	ID                string `xml:"id,attr"`
	Name              string `xml:"name,attr"`
	CalledElement     string `xml:"calledElement,attr"`
	CalledElementType string `xml:"calledElementType,attr"`
	InheritVariables  bool   `xml:"inheritVariables,attr"`
}

// GetID 获取ID
func (e *BPMNCallActivity) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNCallActivity) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNCallActivity) GetType() string { return "CallActivity" }

// BPMNDataObject 数据对象
type BPMNDataObject struct {
	ID           string `xml:"id,attr"`
	Name         string `xml:"name,attr"`
	DataState    string `xml:"dataState,attr"`
	IsCollection bool   `xml:"isCollection,attr"`
}

// GetID 获取ID
func (e *BPMNDataObject) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNDataObject) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNDataObject) GetType() string { return "DataObject" }

// BPMNDataStore 数据存储
type BPMNDataStore struct {
	ID          string `xml:"id,attr"`
	Name        string `xml:"name,attr"`
	DataState   string `xml:"dataState,attr"`
	Capacity    string `xml:"capacity,attr"`
	IsUnlimited bool   `xml:"isUnlimited,attr"`
}

// GetID 获取ID
func (e *BPMNDataStore) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNDataStore) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNDataStore) GetType() string { return "DataStore" }

// BPMNBoundaryEvent 边界事件
type BPMNBoundaryEvent struct {
	ID             string `xml:"id,attr"`
	Name           string `xml:"name,attr"`
	AttachedToRef  string `xml:"attachedToRef,attr"`
	CancelActivity bool   `xml:"cancelActivity,attr"`
}

// GetID 获取ID
func (e *BPMNBoundaryEvent) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNBoundaryEvent) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNBoundaryEvent) GetType() string { return "BoundaryEvent" }

// BPMNIntermediateEvent 中间事件
type BPMNIntermediateEvent struct {
	ID         string `xml:"id,attr"`
	Name       string `xml:"name,attr"`
	EventType  string `xml:"eventType,attr"`
	MessageRef string `xml:"messageRef,attr"`
	TimerRef   string `xml:"timerRef,attr"`
	SignalRef  string `xml:"signalRef,attr"`
}

// GetID 获取ID
func (e *BPMNIntermediateEvent) GetID() string { return e.ID }

// GetName 获取名称
func (e *BPMNIntermediateEvent) GetName() string { return e.Name }

// GetType 获取类型
func (e *BPMNIntermediateEvent) GetType() string { return "IntermediateEvent" }

// BPMNDefinitions BPMN定义根元素
type BPMNDefinitions struct {
	XMLName         xml.Name       `xml:"definitions"`
	ID              string         `xml:"id,attr"`
	Name            string         `xml:"name,attr"`
	TargetNamespace string         `xml:"targetNamespace,attr"`
	Processes       []*BPMNProcess `xml:"process"`
}
