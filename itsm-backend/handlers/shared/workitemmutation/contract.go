// Package workitemmutation defines trusted command metadata and immutable results.
package workitemmutation

// Meta is constructed by the authenticated application boundary, never bound from JSON.
// The owning domain persists one audit receipt keyed by tenant, actor and operation.
type Meta struct {
	TenantID        int
	ActorID         int
	ExpectedVersion int
	Source          string
	CorrelationID   string
	OperationID     string
}
type Result struct {
	WorkItemID int    `json:"workItemId"`
	Version    int    `json:"version"`
	Status     string `json:"status"`
	Replayed   bool   `json:"replayed"`
}
