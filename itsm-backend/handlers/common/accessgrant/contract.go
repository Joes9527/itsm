// Package accessgrant defines the shared Catalog/SR/delegation boundary.
// It contains no execution dispatch or professional lifecycle rules.
package accessgrant

import "time"

type Provider string

const Graph Provider = "graph"
const Capability = "external_group_grant"

type DurationOption struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Seconds int64  `json:"seconds"`
}
type Policy struct {
	ID              int              `json:"id"`
	Version         int              `json:"version"`
	Provider        Provider         `json:"provider"`
	ExternalSystem  string           `json:"externalSystem"`
	GroupID         string           `json:"groupId"`
	DurationField   string           `json:"durationField"`
	DurationOptions []DurationOption `json:"durationOptions"`
}

// ApprovalSnapshot freezes the requested terms at creation. It grants no
// approval itself; the owning BPMN task/approval decisions authorize execution.
type ApprovalSnapshot struct {
	PolicyID        int      `json:"policyId"`
	PolicyVersion   int      `json:"policyVersion"`
	Provider        Provider `json:"provider"`
	ExternalSystem  string   `json:"externalSystem"`
	SubjectID       string   `json:"subjectId"`
	GroupID         string   `json:"groupId"`
	DurationKey     string   `json:"durationKey"`
	DurationSeconds int64    `json:"durationSeconds"`
}
type Result struct {
	Outcome     string    `json:"outcome"`
	Provider    Provider  `json:"provider"`
	SubjectID   string    `json:"subjectId"`
	GroupID     string    `json:"groupId"`
	Baseline    string    `json:"baseline"`
	VerifiedAt  time.Time `json:"verifiedAt"`
	EvidenceRef string    `json:"evidenceRef"`
}

// View deliberately has no grant start: verifiedAt is first confirmation time.
// An already-present membership has no expiry managed by this request.
type View struct {
	Outcome    string     `json:"outcome"`
	VerifiedAt time.Time  `json:"verifiedAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	Managed    bool       `json:"managed"`
}
type Fulfillment struct {
	State        string
	AccessResult *View
}

type ApprovalEvidence struct {
	DecisionID int    `json:"decisionId"`
	TaskID     string `json:"taskId"`
	ActorID    int    `json:"actorId"`
	Decision   string `json:"decision"`
}
type ApprovedContext struct {
	ApprovalSnapshot ApprovalSnapshot   `json:"snapshot"`
	Approvals        []ApprovalEvidence `json:"approvals"`
}
