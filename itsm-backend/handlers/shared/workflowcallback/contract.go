package workflowcallback

import (
	"context"
	"time"
)

type Status string

const (
	StatusApplied    Status = "applied"
	StatusIdempotent Status = "idempotent"
	StatusBlocked    Status = "blocked"
)

type Result struct {
	Status    Status
	BlockCode string
	Message   string
	Output    map[string]interface{}
}

type ServiceRequestCommand struct {
	Action             string
	RequestID          int
	TenantID           int
	FormData           map[string]interface{}
	CostCenter         *string
	DataClassification *string
	NeedsPublicIP      *bool
	SourceIPWhitelist  *[]string
	ExpireAt           *time.Time
	ComplianceAck      *bool
	AssigneeID         int
	ResourceType       string
	CompletionNote     string
}

type ChangeCommand struct {
	Action             string
	ChangeID           int
	TenantID           int
	Title              *string
	Description        *string
	PlannedStart       *time.Time
	PlannedEnd         *time.Time
	VerificationResult string
}

type ServiceRequestService interface {
	ApplyServiceRequestWorkflowCallback(ctx context.Context, command ServiceRequestCommand) (Result, error)
}

type ChangeService interface {
	ApplyChangeWorkflowCallback(ctx context.Context, command ChangeCommand) (Result, error)
}
