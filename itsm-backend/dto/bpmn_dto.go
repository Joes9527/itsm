package dto

import (
	"time"

	"itsm-backend/ent"
)

// ProcessDefinitionResponse 流程定义响应（camelCase）
type ProcessDefinitionResponse struct {
	ID                int                    `json:"id"`
	Key               string                 `json:"key"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description,omitempty"`
	Version           string                 `json:"version"`
	Category          string                 `json:"category"`
	BpmnXml           string                 `json:"bpmnXml,omitempty"`
	ProcessVariables  map[string]interface{} `json:"processVariables,omitempty"`
	IsActive          bool                   `json:"isActive"`
	IsLatest          bool                   `json:"isLatest"`
	DeploymentID      int                    `json:"deploymentId"`
	DeploymentName    string                 `json:"deploymentName,omitempty"`
	DeployedAt        *time.Time             `json:"deployedAt,omitempty"`
	TenantID          int                    `json:"tenantId"`
	CreatedAt         time.Time              `json:"createdAt"`
	UpdatedAt         time.Time              `json:"updatedAt"`
}

// ProcessInstanceResponse 流程实例响应（camelCase）
type ProcessInstanceResponse struct {
	ID                   int                    `json:"id"`
	ProcessInstanceID    string                 `json:"processInstanceId"`
	ProcessDefinitionKey string                 `json:"processDefinitionKey"`
	ProcessDefinitionID  int                    `json:"processDefinitionId"`
	Status               string                 `json:"status"`
	CurrentActivityID    string                 `json:"currentActivityId,omitempty"`
	CurrentActivityName  string                 `json:"currentActivityName,omitempty"`
	Variables            map[string]interface{} `json:"variables,omitempty"`
	BusinessKey          string                 `json:"businessKey,omitempty"`
	Initiator            string                 `json:"initiator,omitempty"`
	StartTime            *time.Time             `json:"startTime,omitempty"`
	EndTime              *time.Time             `json:"endTime,omitempty"`
	TenantID             int                    `json:"tenantId"`
	CreatedAt            time.Time              `json:"createdAt"`
	UpdatedAt            time.Time              `json:"updatedAt"`
}

// ProcessTaskResponse 流程任务响应（camelCase）
type ProcessTaskResponse struct {
	ID                  int        `json:"id"`
	TaskID              string     `json:"taskId"`
	TaskName            string     `json:"taskName"`
	TaskDefinitionKey   string     `json:"taskDefinitionKey"`
	TaskType            string     `json:"taskType,omitempty"`
	ProcessInstanceID   int        `json:"processInstanceId"`
	ProcessDefinitionKey string    `json:"processDefinitionKey"`
	Assignee            string     `json:"assignee,omitempty"`
	CandidateUsers      string     `json:"candidateUsers,omitempty"`
	Status              string     `json:"status"`
	DueDate             *time.Time `json:"dueDate,omitempty"`
	TenantID            int        `json:"tenantId"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// ToProcessDefinitionResponse 转换流程定义 Ent → DTO
func ToProcessDefinitionResponse(def *ent.ProcessDefinition) *ProcessDefinitionResponse {
	if def == nil {
		return nil
	}
	dto := &ProcessDefinitionResponse{
		ID:             def.ID,
		Key:            def.Key,
		Name:           def.Name,
		Description:    def.Description,
		Version:        def.Version,
		Category:       def.Category,
		IsActive:       def.IsActive,
		IsLatest:       def.IsLatest,
		DeploymentID:   def.DeploymentID,
		DeploymentName: def.DeploymentName,
		TenantID:       def.TenantID,
		CreatedAt:      def.CreatedAt,
		UpdatedAt:      def.UpdatedAt,
	}
	if len(def.BpmnXML) > 0 {
		dto.BpmnXml = string(def.BpmnXML)
	}
	if def.ProcessVariables != nil {
		dto.ProcessVariables = def.ProcessVariables
	}
	if !def.DeployedAt.IsZero() {
		dto.DeployedAt = &def.DeployedAt
	}
	return dto
}

// ToProcessDefinitionResponseList 批量转换
func ToProcessDefinitionResponseList(defs []*ent.ProcessDefinition) []*ProcessDefinitionResponse {
	if defs == nil {
		return nil
	}
	out := make([]*ProcessDefinitionResponse, 0, len(defs))
	for _, d := range defs {
		if d != nil {
			out = append(out, ToProcessDefinitionResponse(d))
		}
	}
	return out
}

// ToProcessInstanceResponse 转换流程实例 Ent → DTO
func ToProcessInstanceResponse(inst *ent.ProcessInstance) *ProcessInstanceResponse {
	if inst == nil {
		return nil
	}
	dto := &ProcessInstanceResponse{
		ID:                   inst.ID,
		ProcessInstanceID:    inst.ProcessInstanceID,
		ProcessDefinitionKey: inst.ProcessDefinitionKey,
		ProcessDefinitionID:  inst.ProcessDefinitionID,
		Status:               inst.Status,
		CurrentActivityID:    inst.CurrentActivityID,
		CurrentActivityName:  inst.CurrentActivityName,
		BusinessKey:          inst.BusinessKey,
		Initiator:            inst.Initiator,
		TenantID:             inst.TenantID,
		CreatedAt:            inst.CreatedAt,
		UpdatedAt:            inst.UpdatedAt,
	}
	if inst.Variables != nil {
		dto.Variables = inst.Variables
	}
	if !inst.StartTime.IsZero() {
		dto.StartTime = &inst.StartTime
	}
	if !inst.EndTime.IsZero() {
		dto.EndTime = &inst.EndTime
	}
	return dto
}

// ToProcessTaskResponse 转换流程任务 Ent → DTO
func ToProcessTaskResponse(task *ent.ProcessTask) *ProcessTaskResponse {
	if task == nil {
		return nil
	}
	dto := &ProcessTaskResponse{
		ID:                  task.ID,
		TaskID:              task.TaskID,
		TaskName:            task.TaskName,
		TaskDefinitionKey:   task.TaskDefinitionKey,
		TaskType:            task.TaskType,
		ProcessInstanceID:   task.ProcessInstanceID,
		ProcessDefinitionKey: task.ProcessDefinitionKey,
		Status:              task.Status,
		TenantID:            task.TenantID,
		CreatedAt:           task.CreatedAt,
		UpdatedAt:           task.UpdatedAt,
	}
	if task.Assignee != "" {
		dto.Assignee = task.Assignee
	}
	if task.CandidateUsers != "" {
		dto.CandidateUsers = task.CandidateUsers
	}
	if !task.DueDate.IsZero() {
		dto.DueDate = &task.DueDate
	}
	return dto
}

// ToProcessTaskResponseList 批量转换
func ToProcessTaskResponseList(tasks []*ent.ProcessTask) []*ProcessTaskResponse {
	if tasks == nil {
		return nil
	}
	out := make([]*ProcessTaskResponse, 0, len(tasks))
	for _, t := range tasks {
		if t != nil {
			out = append(out, ToProcessTaskResponse(t))
		}
	}
	return out
}

// ToBPMNProcessInstanceResponse 兼容旧命名 → ToProcessInstanceResponse
func ToBPMNProcessInstanceResponse(inst *ent.ProcessInstance) *ProcessInstanceResponse {
	return ToProcessInstanceResponse(inst)
}

// ToBPMNProcessInstanceListResponse 兼容旧命名 → 列表转换
func ToBPMNProcessInstanceListResponse(insts []*ent.ProcessInstance) []*ProcessInstanceResponse {
	if insts == nil {
		return nil
	}
	out := make([]*ProcessInstanceResponse, 0, len(insts))
	for _, i := range insts {
		if i != nil {
			out = append(out, ToProcessInstanceResponse(i))
		}
	}
	return out
}
