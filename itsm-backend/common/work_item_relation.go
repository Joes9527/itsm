package common

const WorkItemRelationInvestigatedBy = "investigated_by"

func IsIncidentFinalStatus(status string) bool {
	return status == IncidentStatusClosed || status == IncidentStatusCancelled
}
