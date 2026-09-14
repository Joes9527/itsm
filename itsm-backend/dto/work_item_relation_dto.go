package dto

import relationmeta "itsm-backend/common/workitemrelation"

// WorkItemRelationRequest uses WorkItem identities in both directions. The
// source owns expectedVersion, including when it is the incoming endpoint.
type WorkItemRelationRequest struct {
	SourceWorkItemID int                   `json:"sourceWorkItemId" binding:"required,gt=0"`
	TargetWorkItemID int                   `json:"targetWorkItemId" binding:"required,gt=0"`
	RelationType     string                `json:"relationType" binding:"required"`
	ExpectedVersion  int                   `json:"expectedVersion" binding:"required,gt=0"`
	OperationID      string                `json:"operationId" binding:"required"`
	Metadata         relationmeta.Metadata `json:"metadata"`
}
