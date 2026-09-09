package dto

import "itsm-backend/handlers/shared/workitemmutation"

// IncidentCommand is domain input shared with orchestration without importing
// the legacy service package upward into BPMN.
type IncidentCommand struct {
	Meta       workitemmutation.Meta
	IncidentID int
	Action     string
	Reason     string
	Resolution string
}

type IncidentCommandRequest struct {
	Version     int    `json:"version" binding:"required,gt=0"`
	OperationID string `json:"operationId" binding:"required,max=200"`
	Reason      string `json:"reason"`
	Resolution  string `json:"resolution"`
}
