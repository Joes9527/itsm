package dto

import (
	"strconv"
	"strings"
	"time"

	"itsm-backend/common/workitemidentity"
	"itsm-backend/ent"
)

// BPMNTaskResponse 「我的待办」任务视图：任务字段 + 所属流程实例的业务上下文（camelCase）
type BPMNTaskUIActions struct {
	CompletionNoteRequired bool   `json:"completionNoteRequired,omitempty"`
	Claim                  bool   `json:"claim"`
	Complete               bool   `json:"complete"`
	Reason                 string `json:"reason,omitempty"`
}

type BPMNTaskCallbackBlock struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

type BPMNTaskResponse struct {
	CallbackBlock        *BPMNTaskCallbackBlock `json:"callbackBlock,omitempty"`
	AssigneeSource       string                 `json:"assigneeSource"`
	AssignmentState      string                 `json:"assignmentState"`
	ResponsibleUserID    int                    `json:"responsibleUserId"`
	ActorID              int                    `json:"actorId"`
	UIActions            BPMNTaskUIActions      `json:"uiActions"`
	ID                   int                    `json:"id"`
	TaskID               string                 `json:"taskId"`
	TaskDefinitionKey    string                 `json:"taskDefinitionKey"`
	TaskName             string                 `json:"taskName"`
	TaskType             string                 `json:"taskType"`
	Status               string                 `json:"status"`
	Priority             string                 `json:"priority"`
	Assignee             string                 `json:"assignee"`
	CandidateUsers       string                 `json:"candidateUsers"`
	CandidateGroups      string                 `json:"candidateGroups"`
	ProcessInstanceID    int                    `json:"processInstanceId"`
	ProcessInstanceKey   string                 `json:"processInstanceKey"`
	ProcessDefinitionKey string                 `json:"processDefinitionKey"`
	BusinessKey          string                 `json:"businessKey"`
	BusinessType         string                 `json:"businessType"`
	BusinessID           int                    `json:"businessId"`
	WorkItemNumber       string                 `json:"workItemNumber,omitempty"`
	TaskPurpose          string                 `json:"taskPurpose"`
	FormKey              string                 `json:"formKey,omitempty"`
	TaskVariables        map[string]interface{} `json:"taskVariables,omitempty"`
	DueDate              *time.Time             `json:"dueDate,omitempty"`
	CreatedTime          time.Time              `json:"createdTime"`
}

// parseBusinessKey 解析规范业务键 "{recordClass}:{workItemId}"。
//
// 失败关闭：Wave-1 旧词表（ticket/change/service_request）与畸形键一律不解释，返回空身份，
// 使新运行时不会把旧实例身份当成自己的。Release 保留其显式遗留值。
func parseBusinessKey(businessKey string) (businessType string, businessID int) {
	class, rawID, found := strings.Cut(businessKey, ":")
	if !found || rawID == "" {
		return "", 0
	}
	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 {
		return "", 0
	}
	if !workitemidentity.IsKnownProcessIdentity(class) {
		return "", 0
	}
	return class, id
}

// ToBPMNTaskResponse 转换任务实体；instance 允许为 nil（历史数据缺实例时业务上下文留空）
func ToBPMNTaskResponse(task *ent.ProcessTask, instance *ent.ProcessInstance) *BPMNTaskResponse {
	resp := &BPMNTaskResponse{
		AssigneeSource:       task.AssigneeSource,
		ID:                   task.ID,
		TaskID:               task.TaskID,
		TaskDefinitionKey:    task.TaskDefinitionKey,
		TaskName:             task.TaskName,
		TaskType:             task.TaskType,
		Status:               task.Status,
		Priority:             task.Priority,
		Assignee:             task.Assignee,
		CandidateUsers:       task.CandidateUsers,
		CandidateGroups:      task.CandidateGroups,
		ProcessInstanceID:    task.ProcessInstanceID,
		ProcessDefinitionKey: task.ProcessDefinitionKey,
		FormKey:              task.FormKey,
		TaskVariables:        task.TaskVariables,
		CreatedTime:          task.CreatedTime,
	}
	if !task.DueDate.IsZero() {
		due := task.DueDate
		resp.DueDate = &due
	}
	if purpose, ok := task.TaskVariables["taskPurpose"].(string); ok {
		resp.TaskPurpose = purpose
	}
	if instance != nil {
		resp.ProcessInstanceKey = instance.ProcessInstanceID
		resp.BusinessKey = instance.BusinessKey
		resp.WorkItemNumber, _ = instance.Variables["ticket_number"].(string)
		if task.AssigneeSource != "" {
			resp.BusinessType, resp.BusinessID = instance.BusinessType, instance.BusinessID
		} else {
			resp.BusinessType, resp.BusinessID = parseBusinessKey(instance.BusinessKey)
		}
	}
	return resp
}

// ToBPMNTaskResponseList 批量转换，instances 以实例数据库 ID 为键
func ToBPMNTaskResponseList(tasks []*ent.ProcessTask, instances map[int]*ent.ProcessInstance) []*BPMNTaskResponse {
	result := make([]*BPMNTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, ToBPMNTaskResponse(task, instances[task.ProcessInstanceID]))
	}
	return result
}
