package dto

import "time"

// TicketWorkflowAction 工单流转操作类型
type TicketWorkflowAction string

const (
	WorkflowActionAccept   TicketWorkflowAction = "accept"
	WorkflowActionWithdraw TicketWorkflowAction = "withdraw"
	WorkflowActionForward  TicketWorkflowAction = "forward"
	WorkflowActionCC       TicketWorkflowAction = "cc"
	WorkflowActionEscalate TicketWorkflowAction = "escalate"
	WorkflowActionResolve  TicketWorkflowAction = "resolve"
	WorkflowActionClose    TicketWorkflowAction = "close"
	WorkflowActionReopen   TicketWorkflowAction = "reopen"
)

// WorkflowUserInfo 用户信息
type WorkflowUserInfo struct {
	ID         int    `json:"id"`
	Username   string `json:"username"`
	FullName   string `json:"fullName"`
	Email      string `json:"email"`
	Avatar     string `json:"avatar,omitempty"`
	Department string `json:"department,omitempty"`
	Role       string `json:"role"`
}

// AttachmentInfo 附件信息
type AttachmentInfo struct {
	ID       int    `json:"id"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// TicketWorkflowRecord 工单流转记录
type TicketWorkflowRecord struct {
	ID          int                    `json:"id"`
	TicketID    int                    `json:"ticketId"`
	Action      TicketWorkflowAction   `json:"action"`
	FromStatus  *string                `json:"fromStatus,omitempty"`
	ToStatus    *string                `json:"toStatus,omitempty"`
	Operator    WorkflowUserInfo       `json:"operator"`
	FromUser    *WorkflowUserInfo      `json:"fromUser,omitempty"`
	ToUser      *WorkflowUserInfo      `json:"toUser,omitempty"`
	Comment     string                 `json:"comment,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
	Attachments []AttachmentInfo       `json:"attachments,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
}

// TicketWorkflowState 工单流转状态
type TicketWorkflowState struct {
	TicketID         int                    `json:"ticketId"`
	CurrentStatus    string                 `json:"currentStatus"`
	CurrentAssignee  *WorkflowUserInfo      `json:"currentAssignee,omitempty"`
	CanAccept        bool                   `json:"canAccept"`
	CanWithdraw      bool                   `json:"canWithdraw"`
	CanForward       bool                   `json:"canForward"`
	CanCC            bool                   `json:"canCc"`
	CanResolve       bool                   `json:"canResolve"`
	CanClose         bool                   `json:"canClose"`
	AvailableActions []TicketWorkflowAction `json:"availableActions"`
}

// AcceptTicketRequest 接单请求
type AcceptTicketRequest struct {
	TicketID int    `json:"ticketId"`
	Comment  string `json:"comment"`
}

// WithdrawTicketRequest 撤回请求
type WithdrawTicketRequest struct {
	TicketID int    `json:"ticketId"`
	Reason   string `json:"reason" binding:"required"`
}

// ForwardTicketRequest 转发请求
type ForwardTicketRequest struct {
	TicketID          int    `json:"ticketId"`
	ToUserID          int    `json:"toUserId" binding:"required"`
	Comment           string `json:"comment" binding:"required"`
	TransferOwnership bool   `json:"transferOwnership"`
}

// CCTicketRequest 抄送请求
type CCTicketRequest struct {
	TicketID       int      `json:"ticketId"`
	CCUsers        []int    `json:"ccUsers" binding:"required,min=1"`
	Comment        string   `json:"comment"`
	NotifyChannels []string `json:"notifyChannels"`
}

// ReopenTicketRequest 重开工单请求
type ReopenTicketRequest struct {
	TicketID int    `json:"ticketId"`
	Reason   string `json:"reason" binding:"required"`
}

// TicketCC 抄送人
type TicketCC struct {
	ID       int              `json:"id"`
	TicketID int              `json:"ticketId"`
	User     WorkflowUserInfo `json:"user"`
	AddedBy  WorkflowUserInfo `json:"addedBy"`
	AddedAt  time.Time        `json:"addedAt"`
	IsActive bool             `json:"isActive"`
}

// TicketCCRecordResponse 抄送记录响应
type TicketCCRecordResponse struct {
	ID           int              `json:"id"`
	TicketID     int              `json:"ticketId"`
	TicketNumber string           `json:"ticketNumber"`
	Title        string           `json:"title"`
	Status       string           `json:"status"`
	Priority     string           `json:"priority"`
	User         WorkflowUserInfo `json:"user"`
	AddedBy      WorkflowUserInfo `json:"addedBy"`
	AddedAt      time.Time        `json:"addedAt"`
	IsActive     bool             `json:"isActive"`
}

// TicketCCListResponse 抄送记录列表响应
type TicketCCListResponse struct {
	Records []TicketCCRecordResponse `json:"records"`
	Total   int                      `json:"total"`
}
