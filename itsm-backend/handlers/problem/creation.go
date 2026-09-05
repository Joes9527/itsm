package problem

import (
	"context"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func (*Service) RecordClass() string { return creation.RecordClassProblem }
func (s *Service) Prepare(_ context.Context, _ *ent.Tx, in creation.ResolvedIntake) (*creation.CreationPlan, error) {
	priority := in.Command.Priority
	if priority == "" {
		priority = "medium"
	}
	if !isValidProblemPriority(priority) {
		return nil, creation.NewDomainValidationFailed("invalid problem priority", nil)
	}
	input := creation.ProblemInput{}
	if in.Command.Problem != nil {
		input = *in.Command.Problem
	}
	plan := creation.NewPlan(in, "open", priority, in.Identity.Channel)
	plan.ProfessionalInput = input
	return plan, nil
}
func (*Service) CreateExtension(ctx context.Context, tx *ent.Tx, item *ent.Ticket, plan *creation.CreationPlan) (*creation.ProfessionalReference, error) {
	input, ok := plan.ProfessionalInput.(creation.ProblemInput)
	if !ok {
		return nil, creation.NewInternalFailure("problem creation plan is invalid", nil)
	}
	record, err := tx.Problem.Create().SetWorkItemID(item.ID).SetRootCause(input.RootCause).SetImpact(input.Impact).Save(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not create problem extension", err)
	}
	return &creation.ProfessionalReference{Type: "problem", ID: record.ID}, nil
}

var _ creation.ProfessionalCreator = (*Service)(nil)
