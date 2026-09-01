package change

import (
	"time"
)

// Change domain entity
type Change struct {
	ID            int
	Title         string
	Description   string
	Justification string
	Type          string
	Status        string
	Priority      string
	ImpactScope   string
	RiskLevel     string
	AssigneeID    *int
	Assignee      *User
	CreatedBy     int
	CreatedByUser *User
	// WorkItemID 关联的 WorkItem（tickets.id）。统一 WorkItem 领域模型宪章 §3.2 要求
	// 每条 Change 都在同一事务内建好对应的 tickets 行并回填这个字段；nil 表示开发数据
	// 违反 WorkItem 创建不变量。与 dto.IncidentResponse.WorkItemID /
	// dto.ProblemResponse.WorkItemID 同一模式。这也是 Wave 2
	// 起 BPMN businessKey/businessId 的权威身份来源，不再是 Change.ID 自己（见
	// Service.resolveWorkItemID）。
	WorkItemID         *int
	TenantID           int
	PlannedStartDate   *time.Time
	PlannedEndDate     *time.Time
	ActualStartDate    *time.Time
	ActualEndDate      *time.Time
	ImplementationPlan string
	RollbackPlan       string
	AffectedCIs        []string
	RelatedTickets     []string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// User is the minimal user projection needed by the Change domain response.
// Keeping this projection local avoids exposing the persistence model while
// still allowing repositories to hydrate user display information.
type User struct {
	ID   int
	Name string
}

// ApprovalRecord represents an individual approval action
type ApprovalRecord struct {
	ID           int
	ChangeID     int
	TenantID     int
	ApproverID   int
	ApproverName string
	Status       string
	Comment      *string
	ApprovedAt   *time.Time
	CreatedAt    time.Time
}

// RiskAssessment represents the risk evaluation of a change
type RiskAssessment struct {
	ID                 int
	ChangeID           int
	TenantID           int
	RiskLevel          string
	RiskDescription    string
	ImpactAnalysis     string
	MitigationMeasures string
	ContingencyPlan    string
	RiskOwner          string
	RiskReviewDate     *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Stats represents change statistics.
// Field names and JSON tags mirror dto.ChangeStatsResponse so callers can either
// consume the domain struct directly or map it through the DTO. Status values
// follow the canonical set defined in dto.ChangeStatus (draft, pending, approved,
// scheduled, in_progress, completed, failed, rolled_back, rejected, cancelled).
type Stats struct {
	Total      int `json:"total"`
	Draft      int `json:"draft"`
	Pending    int `json:"pending"`
	Approved   int `json:"approved"`
	Scheduled  int `json:"scheduled"`
	InProgress int `json:"inProgress"`
	Completed  int `json:"completed"`
	Failed     int `json:"failed"`
	RolledBack int `json:"rolledBack"`
	Rejected   int `json:"rejected"`
	Cancelled  int `json:"cancelled"`
}
