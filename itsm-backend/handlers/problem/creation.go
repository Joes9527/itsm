package problem

import (
	"context"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/workitemrelation"
	creation "itsm-backend/handlers/common/workitemcreation"
)

type problemCreationDraft struct {
	Input            creation.ProblemInput
	SourceWorkItemID int
}

func (*Service) RecordClass() string { return creation.RecordClassProblem }
func (s *Service) Prepare(ctx context.Context, tx *ent.Tx, in creation.ResolvedIntake) (*creation.CreationPlan, error) {
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
	draft := problemCreationDraft{}
	if input.SourceIncidentID != nil {
		source, err := prepareIncidentConversion(ctx, tx, in.Identity, *input.SourceIncidentID)
		if err != nil {
			return nil, err
		}
		draft.SourceWorkItemID = source.WorkItemID
		if plan.WorkItem.Title == "" {
			plan.WorkItem.Title = "问题-" + source.Edges.WorkItem.Title
		}
		if plan.WorkItem.Description == "" {
			plan.WorkItem.Description = source.Edges.WorkItem.Description
		}
		if in.Command.Priority == "" {
			plan.WorkItem.Priority = source.Edges.WorkItem.Priority
		}
		if plan.WorkItem.CategoryID == nil && source.Edges.WorkItem.CategoryID > 0 {
			id := source.Edges.WorkItem.CategoryID
			plan.WorkItem.CategoryID = &id
		}
		if input.Impact == "" {
			input.Impact = source.Impact
		}
		plan.WorkflowVariables["title"], plan.WorkflowVariables["description"], plan.WorkflowVariables["priority"] = plan.WorkItem.Title, plan.WorkItem.Description, plan.WorkItem.Priority
	}
	draft.Input = input
	plan.ProfessionalInput = draft
	plan.WorkflowVariables["root_cause"] = input.RootCause
	plan.WorkflowVariables["impact"] = input.Impact
	return plan, nil
}
func (*Service) CreateExtension(ctx context.Context, tx *ent.Tx, item *ent.Ticket, plan *creation.CreationPlan) (*creation.ProfessionalReference, error) {
	draft, ok := plan.ProfessionalInput.(problemCreationDraft)
	if !ok {
		return nil, creation.NewInternalFailure("problem creation plan is invalid", nil)
	}
	input := draft.Input
	record, err := tx.Problem.Create().SetWorkItemID(item.ID).SetRootCause(input.RootCause).SetImpact(input.Impact).Save(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not create problem extension", err)
	}
	if input.SourceIncidentID != nil {
		if err := writeIncidentConversion(ctx, tx, plan, *input.SourceIncidentID, draft.SourceWorkItemID, record.ID, item.ID); err != nil {
			return nil, err
		}
	}
	plan.WorkflowVariables["problem_id"] = record.ID
	return &creation.ProfessionalReference{Type: "problem", ID: record.ID}, nil
}

var _ creation.ProfessionalCreator = (*Service)(nil)

func prepareIncidentConversion(ctx context.Context, tx *ent.Tx, identity creation.Identity, incidentID int) (*ent.Incident, error) {
	for _, action := range []string{"read", "write"} {
		if err := authorization.RequireCurrentPermission(ctx, tx, identity, "incident", action); err != nil {
			return nil, err
		}
	}
	source, err := tx.Incident.Query().Where(incident.IDEQ(incidentID), incident.HasWorkItemWith(ticket.TenantIDEQ(identity.TenantID), ticket.RecordClassEQ("incident"), ticket.DeletedAtIsNil())).WithWorkItem().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, creation.NewReferenceNotFound("source incident is unavailable", err)
	}
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not load source incident", err)
	}
	if common.IsIncidentFinalStatus(source.Edges.WorkItem.Status) {
		return nil, creation.NewDomainValidationFailed("final incident cannot be converted to a problem", nil)
	}
	exists, err := tx.WorkItemRelation.Query().Where(workitemrelation.TenantIDEQ(identity.TenantID), workitemrelation.SourceWorkItemIDEQ(source.WorkItemID), workitemrelation.RelationTypeEQ(common.WorkItemRelationInvestigatedBy), workitemrelation.DeletedAtIsNil()).Exist(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not check incident relation", err)
	}
	if exists {
		return nil, creation.NewDomainValidationFailed("incident is already investigated by a problem", nil)
	}
	return source, nil
}

func writeIncidentConversion(ctx context.Context, tx *ent.Tx, plan *creation.CreationPlan, incidentID, sourceWorkItemID, problemID, workItemID int) error {
	identity := plan.Resolved.Identity
	_, err := tx.WorkItemRelation.Create().SetTenantID(identity.TenantID).SetSourceWorkItemID(sourceWorkItemID).SetTargetWorkItemID(workItemID).SetRelationType(common.WorkItemRelationInvestigatedBy).SetCreatedByID(identity.ActorID).Save(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not create incident problem relation", err)
	}
	_, err = tx.IncidentEvent.Create().SetIncidentID(incidentID).SetTenantID(identity.TenantID).SetUserID(identity.ActorID).SetSource("incident_conversion").SetEventType("conversion").SetEventName("convert_to_problem").SetData(map[string]any{"problem_id": problemID, "problem_work_item_id": workItemID}).Save(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not create incident conversion timeline", err)
	}
	command := plan.Resolved.Command
	request := dto.ConvertIncidentToProblemRequest{Title: command.Title, Description: command.Description, RootCause: command.Problem.RootCause}
	_, err = tx.AuditLog.Create().SetTenantID(identity.TenantID).SetUserID(identity.ActorID).SetResource("incident").SetAction("convert_to_problem").SetPath(incidentConversionAuditPath).SetMethod("POST").SetRequestBody(redactedConversionAuditJSON(incidentID, sourceWorkItemID, problemID, workItemID, request)).Save(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not create incident conversion audit", err)
	}
	return nil
}
