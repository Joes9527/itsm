package problem

import (
	relationmeta "itsm-backend/common/workitemrelation"
	"time"
)

// Problem domain entity
type Problem struct {
	Number           string
	Version          int
	VerifiedVersion  int
	VerificationNote string
	ID               int
	Title            string
	Description      string
	Status           string
	Priority         string
	Category         string
	CategoryID       *int
	RootCause        string
	Workaround       string
	Resolution       string
	Impact           string
	AssigneeID       *int
	CreatedBy        int
	TenantID         int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ResolvedAt       *time.Time
	ClosedAt         *time.Time
	// WorkItemID 关联的 WorkItem（tickets.id）。统一 WorkItem 领域模型宪章 §3.2：
	// Problem 创建时必须在同一事务内建好对应的 tickets 行并回填这个字段；nil 表示
	// 开发数据违反 WorkItem 创建不变量，调用方必须 fail closed。
	WorkItemID *int
	Relations  []relationmeta.View
}

// ProblemStats domain entity
type ProblemStats struct {
	Total        int
	Open         int
	InProgress   int
	Resolved     int
	Closed       int
	HighPriority int
}
