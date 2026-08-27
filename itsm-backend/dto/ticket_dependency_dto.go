package dto

// 依赖关系影响分析相关DTO
type RelationImpactAnalysisRequest struct {
	TicketID  int     `json:"ticketId" binding:"required" example:"1001"`
	Action    string  `json:"action" binding:"required,oneof=close delete change_status" example:"close"`
	NewStatus *string `json:"newStatus,omitempty" example:"closed"`
}

type RelationImpactAnalysis struct {
	TicketID        int                  `json:"ticketId" example:"1001"`
	TicketNumber    string               `json:"ticketNumber" example:"T-2024-001"`
	TicketTitle     string               `json:"ticketTitle" example:"系统响应缓慢"`
	Action          string               `json:"action" example:"close"`
	AffectedCount   int                  `json:"affectedCount" example:"3"`
	AffectedTickets []AffectedTicketInfo `json:"affectedTickets"`
	Warnings        []string             `json:"warnings"`
	Recommendations []string             `json:"recommendations"`
	RiskLevel       string               `json:"riskLevel" example:"medium"`
}

type AffectedTicketInfo struct {
	ID          int    `json:"id" example:"1002"`
	Number      string `json:"number" example:"T-2024-002"`
	Title       string `json:"title" example:"数据库优化"`
	Status      string `json:"status" example:"in_progress"`
	ImpactType  string `json:"impactType" example:"blocked"`
	Description string `json:"description" example:"父工单关闭可能导致此工单无法继续"`
}

// TicketRelationStats 工单关联统计 DTO (camelCase 契约)
type TicketRelationStats struct {
	TotalRelations  int            `json:"totalRelations"`
	RelationsByType map[string]int `json:"relationsByType"`
	InboundCount    int            `json:"inboundCount"`
	OutboundCount   int            `json:"outboundCount"`
	ParentCount     int            `json:"parentCount"`
	ChildrenCount   int            `json:"childrenCount"`
	BlockedByCount  int            `json:"blockedByCount"`
	BlockingCount   int            `json:"blockingCount"`
	RelatedCount    int            `json:"relatedCount"`
	DuplicateCount  int            `json:"duplicateCount"`
}

// TicketRelationTicketRef 关联工单的最小展示信息（camelCase 契约）
type TicketRelationTicketRef struct {
	ID           int    `json:"id"`
	TicketNumber string `json:"ticketNumber"`
	Title        string `json:"title"`
	Status       string `json:"status"`
}

// TicketRelation 一条工单关联记录（camelCase 契约）。
//
// 当前唯一的关联来源是 tickets.parent_ticket_id（父子关系），没有独立的关联表，
// 所以没有真正的"创建人/创建时间"审计字段——CreatedAt 用子工单自身的创建时间
// 近似（父子关系目前只能在建单时设置），CreatedBy/CreatedByName 留空，不编造
// 不存在的数据。等真正的多类型关联（阻塞/依赖/重复等）落地到独立的关联表时，
// 这些字段才有真实来源。
type TicketRelation struct {
	ID                 string                   `json:"id"`
	SourceTicketID     int                      `json:"sourceTicketId"`
	SourceTicketNumber string                   `json:"sourceTicketNumber"`
	TargetTicketID     int                      `json:"targetTicketId"`
	TargetTicketNumber string                   `json:"targetTicketNumber"`
	RelationType       string                   `json:"relationType"`
	Direction          string                   `json:"direction"`
	Description        string                   `json:"description,omitempty"`
	CreatedBy          int                      `json:"createdBy"`
	CreatedByName      string                   `json:"createdByName"`
	CreatedAt          string                   `json:"createdAt"`
	SourceTicket       *TicketRelationTicketRef `json:"sourceTicket,omitempty"`
	TargetTicket       *TicketRelationTicketRef `json:"targetTicket,omitempty"`
}
