package dto

import (
	"time"

	relationmeta "itsm-backend/common/workitemrelation"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// CreateProblemRequest 创建问题请求
type CreateProblemRequest struct {
	RequesterID *int               `json:"requesterId,omitempty" binding:"omitempty,gt=0"` // 可选目标租户申请人
	Title       string             `json:"title" binding:"required,min=2,max=200"`
	Description string             `json:"description" binding:"required,min=10,max=5000"`
	Priority    string             `json:"priority" binding:"required"`
	CTI         *creation.CTIInput `json:"cti,omitempty"`
	RootCause   string             `json:"rootCause"`
	Impact      string             `json:"impact"`
	ImpactScope string             `json:"impactScope"` // 影响范围
}

// UpdateProblemRequest 更新问题请求
type UpdateProblemRequest struct {
	OperationID      string  `json:"operationId" binding:"required,max=200"`
	AssigneeID       *int    `json:"assigneeId,omitempty" binding:"omitempty,gt=0"`
	AssignmentReason string  `json:"assignmentReason"`
	// ClassificationReason 是分类纠正原因：分类确实变化时必填（B1 治理契约）。
	ClassificationReason string `json:"classificationReason"`
	Workaround       *string `json:"workaround"`
	Resolution       *string `json:"resolution"`
	Version          int     `json:"version" binding:"required,gt=0"`
	Title            *string `json:"title" binding:"omitempty,min=2,max=200"`
	Description      *string `json:"description" binding:"omitempty,min=10,max=5000"`
	Priority         *string `json:"priority" binding:"omitempty"`
	Status           *string `json:"status" binding:"omitempty"`
	CategoryID       *int    `json:"categoryId,omitempty" binding:"omitempty,gte=0"`
	RootCause        *string `json:"rootCause" binding:"omitempty"`
	Impact           *string `json:"impact" binding:"omitempty"`
}

// UpdateProblemRootCauseRequest 记录问题根因。
type UpdateProblemRootCauseRequest struct {
	OperationID string `json:"operationId" binding:"required,max=200"`
	Version     int    `json:"version" binding:"required,gt=0"`
	RootCause   string `json:"rootCause" binding:"required"`
}

// UpdateProblemResolutionRequest 记录问题的临时或最终解决方案。
type UpdateProblemResolutionRequest struct {
	OperationID string  `json:"operationId" binding:"required,max=200"`
	Version     int     `json:"version" binding:"required,gt=0"`
	Solution    *string `json:"solution"`
	Workaround  *string `json:"workaround"`
	Resolution  *string `json:"resolution"`
}

// CloseProblemRequest 关闭问题时可同时记录最终解决方案。
type CloseProblemRequest struct {
	Resolution string `json:"resolution"`
}

// ListProblemsRequest 获取问题列表请求
type ListProblemsRequest struct {
	Page      int        `json:"page" form:"page"`
	PageSize  int        `json:"pageSize" form:"pageSize"`
	Status    string     `json:"status" form:"status"`
	Priority  string     `json:"priority" form:"priority"`
	Category  string     `json:"category" form:"category"`
	Keyword   string     `json:"keyword" form:"keyword"`
	DateFrom  *time.Time `json:"dateFrom" form:"dateFrom"`
	DateTo    *time.Time `json:"dateTo" form:"dateTo"`
	SortBy    string     `json:"sortBy" form:"sortBy"`
	SortOrder string     `json:"sortOrder" form:"sortOrder"`
}

// ProblemResponse 问题响应
type ProblemResponse struct {
	Number           string                      `json:"number"`
	Version          int                         `json:"version"`
	VerifiedVersion  int                         `json:"verifiedVersion"`
	VerificationNote string                      `json:"verificationNote"`
	CategoryID       int                         `json:"categoryId"`
	ID               int                         `json:"id"`
	Title            string                      `json:"title"`
	Description      string                      `json:"description"`
	Status           string                      `json:"status"`
	Priority         string                      `json:"priority"`
	Category         string                      `json:"category"`
	RootCause        string                      `json:"rootCause"`
	Workaround       string                      `json:"workaround"`
	Resolution       string                      `json:"resolution"`
	Impact           string                      `json:"impact"`
	AssigneeID       *int                        `json:"assigneeId,omitempty"`
	CreatedBy        int                         `json:"createdBy"`
	TenantID         int                         `json:"tenantId"`
	CreatedAt        time.Time                   `json:"createdAt"`
	UpdatedAt        time.Time                   `json:"updatedAt"`
	Actions          map[string]ActionPermission `json:"actions,omitempty"`
	// WorkItemID 关联的 WorkItem（tickets.id）。Problem 创建事务保证该值存在；nil 表示
	// 开发数据违反 WorkItem 创建不变量。与 dto.IncidentResponse.WorkItemID 同一模式。
	WorkItemID *int `json:"workItemId,omitempty"`
	// Relations is omitted in metadata mutation responses without a current-authorized query; reads return an array, including empty.
	Relations *[]relationmeta.View `json:"relations,omitempty"`
}

// ListProblemsResponse 问题列表响应
type ListProblemsResponse struct {
	Problems   []*ProblemResponse `json:"problems"`
	Total      int                `json:"total"`
	Page       int                `json:"page"`
	PageSize   int                `json:"pageSize"`
	TotalPages int                `json:"totalPages"`
}

// ProblemStatsResponse 问题统计响应
type ProblemStatsResponse struct {
	Total        int `json:"total"`
	Open         int `json:"open"`
	InProgress   int `json:"inProgress"`
	Resolved     int `json:"resolved"`
	Closed       int `json:"closed"`
	HighPriority int `json:"highPriority"`
}

// ProblemDetailResponse 问题详情响应
type ProblemDetailResponse struct {
	Problem ProblemResponse `json:"problem"`
}

// CreateKnownErrorFromProblemRequest 从问题创建已知错误请求
type CreateKnownErrorFromProblemRequest struct {
	Title            *string  `json:"title"`
	Description      *string  `json:"description"`
	Symptoms         *string  `json:"symptoms"`
	RootCause        *string  `json:"rootCause"`
	Workaround       *string  `json:"workaround"`
	Resolution       *string  `json:"resolution"`
	Category         *string  `json:"category"`
	Severity         *string  `json:"severity" binding:"omitempty,oneof=critical high medium low"`
	AffectedProducts []string `json:"affectedProducts"`
	AffectedCIs      []string `json:"affectedCis"`
	Keywords         []string `json:"keywords"`
}
