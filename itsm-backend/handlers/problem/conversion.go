package problem

import (
	"encoding/json"
	"strings"

	"itsm-backend/dto"
)

const (
	incidentConversionAuditPath = "/api/v1/incidents/:id/convert-to-problem"
)

func redactedConversionAuditJSON(
	incidentID, sourceWorkItemID, problemID, targetWorkItemID int,
	req dto.ConvertIncidentToProblemRequest,
) string {
	payload := struct {
		IncidentID       int `json:"incidentId"`
		SourceWorkItemID int `json:"sourceWorkItemId"`
		ProblemID        int `json:"problemId"`
		TargetWorkItemID int `json:"targetWorkItemId"`
		Request          struct {
			TitleProvided       bool `json:"titleProvided"`
			DescriptionProvided bool `json:"descriptionProvided"`
			RootCauseProvided   bool `json:"rootCauseProvided"`
		} `json:"request"`
	}{
		IncidentID:       incidentID,
		SourceWorkItemID: sourceWorkItemID,
		ProblemID:        problemID,
		TargetWorkItemID: targetWorkItemID,
	}
	payload.Request.TitleProvided = strings.TrimSpace(req.Title) != ""
	payload.Request.DescriptionProvided = strings.TrimSpace(req.Description) != ""
	payload.Request.RootCauseProvided = strings.TrimSpace(req.RootCause) != ""

	encoded, _ := json.Marshal(payload)
	return string(encoded)
}
