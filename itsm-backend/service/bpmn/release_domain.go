package bpmn

import "context"

// ReleaseWorkflowAction is the bounded set of professional Release mutations
// that a release BPMN callback may request.
type ReleaseWorkflowAction string

const (
	ReleaseWorkflowActionTechReview ReleaseWorkflowAction = "tech_review"
	ReleaseWorkflowActionReject     ReleaseWorkflowAction = "reject"
	ReleaseWorkflowActionStatus     ReleaseWorkflowAction = "status"
)

// ReleaseWorkflowCommand is the typed boundary from process orchestration to
// the Release application service. BPMN handlers parse process variables; the
// Release service remains the only owner of professional persistence rules.
type ReleaseWorkflowCommand struct {
	ReleaseID    int
	TenantID     int
	Action       ReleaseWorkflowAction
	Comment      string
	TargetStatus string
}

// ReleaseWorkflowMutation reports whether the authoritative Release write was
// applied or was already durably present.
type ReleaseWorkflowMutation struct {
	Changed bool
	Message string
}

// ReleaseDomainService is the downward dependency used by the BPMN callback
// handler. Missing injection is an explicit callback failure.
type ReleaseDomainService interface {
	ApplyReleaseWorkflowCallback(context.Context, ReleaseWorkflowCommand) (*ReleaseWorkflowMutation, error)
}
