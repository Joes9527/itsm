package service

import "itsm-backend/common"

// RejectGenericFulfillmentReservedInputs validates public input only after the
// owning entry establishes that its immutable definition enables the contract.
// Trusted creation flags and approval projections must use their owning paths.
func RejectGenericFulfillmentReservedInputs(variables map[string]interface{}) error {
	for _, key := range []string{"approval_required", "need_escalate", "approvalResult"} {
		if _, present := variables[key]; present {
			return common.NewValidationError(key+" is reserved by the workflow definition or approval decision", nil)
		}
	}
	return nil
}
