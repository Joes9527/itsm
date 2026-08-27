package problem

import (
	"time"
)

// Problem domain entity
type Problem struct {
	ID          int
	Title       string
	Description string
	Status      string
	Priority    string
	Category    string
	RootCause   string
	Workaround  string
	Resolution  string
	Impact      string
	AssigneeID  *int
	CreatedBy   int
	TenantID    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ResolvedAt  *time.Time
	ClosedAt    *time.Time
	// WorkItemID 关联的 WorkItem（tickets.id）。统一 WorkItem 领域模型宪章 §3.2：
	// Problem 创建时必须在同一事务内建好对应的 tickets 行并回填这个字段。nil 只出现在
	// 迁移前创建、尚未跑 cmd/backfill_problem_work_item 回填的存量记录上。
	WorkItemID *int
	// 关联数据 (eager-loaded)
	Tickets   []*AssociatedItem
	Incidents []*AssociatedItem
	Changes   []*AssociatedItem
}

// AssociatedItem 关联项
type AssociatedItem struct {
	ID     int
	Title  string
	Status string
	Number string
	Type   string
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
