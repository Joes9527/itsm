package workitemcreation

import (
	"context"
	"itsm-backend/ent"
)

type Application interface {
	Create(context.Context, Identity, CreateWorkItemCommand) (*CreateWorkItemResult, error)
}
type ProfessionalCreator interface {
	RecordClass() string
	Prepare(ctx context.Context, tx *ent.Tx, in ResolvedIntake) (*CreationPlan, error)
	CreateExtension(ctx context.Context, tx *ent.Tx, workItem *ent.Ticket, plan *CreationPlan) (*ProfessionalReference, error)
}

// IsSupportedRecordClass describes the bounded creation contract, not lifecycle dispatch.
func IsSupportedRecordClass(class string) bool {
	switch class {
	case RecordClassGeneric, RecordClassProblem, RecordClassIncident, RecordClassChangeRequest, RecordClassServiceRequestItem:
		return true
	}
	return false
}
