package common

// IsValidChangeStatusTransition is the single authoritative Change lifecycle
// policy shared by HTTP application services and BPMN callback services.
func IsValidChangeStatusTransition(currentStatus, newStatus, changeType string) bool {
	if currentStatus == "pending" {
		currentStatus = ChangeStatusSubmitted
	}
	// Closing a recorded unsuccessful implementation preserves its professional
	// outcome. The Change command owner additionally requires current PIR evidence.
	if newStatus == ChangeStatusCompleted && (currentStatus == ChangeStatusFailed || currentStatus == "rolled_back") {
		return true
	}
	terminal := map[string]struct{}{
		ChangeStatusRejected: {}, ChangeStatusCompleted: {}, ChangeStatusCancelled: {}, "rolled_back": {},
	}
	if _, ok := terminal[currentStatus]; ok {
		return false
	}
	var transitions map[string][]string
	switch changeType {
	case "standard":
		transitions = map[string][]string{
			ChangeStatusDraft:      {ChangeStatusSubmitted, ChangeStatusApproved, ChangeStatusScheduled, ChangeStatusInProgress, ChangeStatusCancelled},
			ChangeStatusSubmitted:  {ChangeStatusApproved, ChangeStatusRejected, ChangeStatusCancelled},
			ChangeStatusApproved:   {ChangeStatusScheduled, ChangeStatusInProgress, ChangeStatusCancelled},
			ChangeStatusScheduled:  {ChangeStatusInProgress, ChangeStatusCancelled},
			ChangeStatusInProgress: {ChangeStatusCompleted, ChangeStatusFailed, "rolled_back", ChangeStatusCancelled},
			ChangeStatusFailed:     {ChangeStatusScheduled, "rolled_back", ChangeStatusCancelled},
		}
	case "emergency":
		transitions = map[string][]string{
			ChangeStatusDraft:      {ChangeStatusSubmitted, ChangeStatusApproved, ChangeStatusInProgress, ChangeStatusCancelled},
			ChangeStatusSubmitted:  {ChangeStatusApproved, ChangeStatusRejected, ChangeStatusCancelled},
			ChangeStatusApproved:   {ChangeStatusInProgress, ChangeStatusCancelled},
			ChangeStatusInProgress: {ChangeStatusCompleted, ChangeStatusFailed, "rolled_back", ChangeStatusCancelled},
			ChangeStatusFailed:     {ChangeStatusScheduled, "rolled_back", ChangeStatusCancelled},
		}
	default:
		transitions = map[string][]string{
			ChangeStatusDraft:      {ChangeStatusSubmitted, ChangeStatusCancelled},
			ChangeStatusSubmitted:  {ChangeStatusApproved, ChangeStatusRejected, ChangeStatusCancelled},
			ChangeStatusApproved:   {ChangeStatusScheduled, ChangeStatusCancelled},
			ChangeStatusScheduled:  {ChangeStatusInProgress, ChangeStatusCancelled},
			ChangeStatusInProgress: {ChangeStatusCompleted, ChangeStatusFailed, "rolled_back", ChangeStatusCancelled},
			ChangeStatusFailed:     {ChangeStatusScheduled, "rolled_back", ChangeStatusCancelled},
		}
	}
	for _, allowed := range transitions[currentStatus] {
		if allowed == newStatus {
			return true
		}
	}
	return false
}
