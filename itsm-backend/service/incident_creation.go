package service

import (
	"bytes"
	"context"
	"encoding/json"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"
	"time"
)

type incidentCreation struct {
	Input      creation.IncidentInput
	DetectedAt time.Time
}

func (*IncidentService) RecordClass() string { return creation.RecordClassIncident }
func (s *IncidentService) Prepare(_ context.Context, _ *ent.Tx, in creation.ResolvedIntake) (*creation.CreationPlan, error) {
	source, err := s.ValidateIncidentCreationInput(in.Identity, in.Command.SourceReference, in.Command.Incident)
	if err != nil {
		return nil, err
	}
	input := creation.IncidentInput{}
	if in.Command.Incident != nil {
		input = *in.Command.Incident
	}
	if input.Type == "" {
		input.Type = "incident"
	}
	if input.Impact == "" {
		input.Impact = "medium"
	}
	if input.Urgency == "" {
		input.Urgency = "medium"
	}
	if input.Severity == "" {
		input.Severity = "medium"
	}
	for field, value := range map[string]string{"impact": input.Impact, "urgency": input.Urgency, "severity": input.Severity} {
		switch value {
		case "low", "medium", "high", "critical":
		default:
			return nil, creation.NewDomainValidationFailed("invalid incident classification", nil, creation.FieldError{Field: "incident." + field, Message: "unsupported value"})
		}
	}
	priority := in.Command.Priority
	if priority == "" {
		if s.priorityMatrixService == nil {
			return nil, creation.NewInternalFailure("incident priority matrix is required", nil)
		}
		var err error
		priority, err = s.priorityMatrixService.CalculatePriority(in.Identity.TenantID, input.Impact, input.Urgency)
		if err != nil {
			return nil, creation.NewDomainValidationFailed("incident priority matrix cannot resolve classification", err)
		}
	}
	switch priority {
	case "low", "medium", "high", "critical":
	default:
		return nil, creation.NewDomainValidationFailed("invalid incident priority", nil)
	}
	detected, err := creation.ParseOptionalTime(input.DetectedAt, "incident.detectedAt")
	if err != nil {
		return nil, err
	}
	if detected == nil {
		now := time.Now().UTC()
		detected = &now
	}
	plan := creation.NewPlan(in, "new", priority, source)
	plan.BusinessSubtype = input.Type
	for key, value := range map[string]any{"type": input.Type, "severity": input.Severity, "impact": input.Impact, "urgency": input.Urgency, "category": in.CTI.CategoryName, "subcategory": input.Subcategory, "detected_at": *detected, "impact_analysis": input.ImpactAnalysis, "metadata": input.Metadata, "reporter_id": in.Identity.RequesterID} {
		plan.WorkflowVariables[key] = value
	}
	plan.RoutingValues = map[string]any{"severity": input.Severity, "impact": input.Impact, "urgency": input.Urgency}
	plan.ProfessionalInput = incidentCreation{Input: input, DetectedAt: *detected}
	return plan, nil
}
func (*IncidentService) CreateExtension(ctx context.Context, tx *ent.Tx, item *ent.Ticket, plan *creation.CreationPlan) (*creation.ProfessionalReference, error) {
	prepared, ok := plan.ProfessionalInput.(incidentCreation)
	if !ok {
		return nil, creation.NewInternalFailure("incident creation plan is invalid", nil)
	}
	input := prepared.Input
	// Preserve exact JSON numbers while projecting the typed domain object.
	var analysis map[string]any
	if input.ImpactAnalysis != nil {
		raw, err := json.Marshal(input.ImpactAnalysis)
		if err != nil {
			return nil, creation.NewDomainValidationFailed("invalid incident impact analysis", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err = decoder.Decode(&analysis); err != nil {
			return nil, creation.NewDomainValidationFailed("invalid incident impact analysis", err)
		}
	}
	record, err := tx.Incident.Create().SetWorkItemID(item.ID).SetIncidentNumber(item.TicketNumber).
		SetType(input.Type).SetSeverity(input.Severity).SetImpact(input.Impact).SetUrgency(input.Urgency).
		SetDetectedAt(prepared.DetectedAt).SetImpactAnalysis(analysis).SetMetadata(input.Metadata).
		AddConfigurationItemIDs(plan.Resolved.CIIDs...).Save(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not create incident extension", err)
	}
	_, err = tx.IncidentEvent.Create().SetIncidentID(record.ID).SetEventType("creation").SetEventName("事件创建").
		SetDescription("事件 " + item.TicketNumber + " 已创建").SetStatus("active").SetSeverity("info").SetSource(item.Source).
		SetUserID(plan.Resolved.Identity.ActorID).SetTenantID(item.TenantID).SetOccurredAt(item.CreatedAt).Save(ctx)
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not create incident timeline", err)
	}
	payload, err := json.Marshal(map[string]any{"tenantId": item.TenantID, "incidentId": record.ID, "workItemId": item.ID, "actorId": plan.Resolved.Identity.ActorID, "channel": plan.Resolved.Identity.Channel})
	if err != nil {
		return nil, creation.NewInternalFailure("could not encode incident creation event", err)
	}
	_, err = NewOutboxEventRepository(tx.Client()).Enqueue(ctx, tx, NewOutboxEvent{
		EventID: "incident-created:" + strconv.Itoa(item.ID), EventType: "incident.created", TenantID: item.TenantID, AggregateType: "work_item", AggregateID: strconv.Itoa(item.ID), Payload: payload,
	})
	if err != nil {
		return nil, creation.NewInfrastructureUnavailable("could not enqueue incident automation", err)
	}
	plan.WorkflowVariables["incident_id"] = record.ID
	plan.WorkflowVariables["incident_number"] = item.TicketNumber
	return &creation.ProfessionalReference{Type: "incident", ID: record.ID}, nil
}

var _ creation.IncidentCreationInputOwner = (*IncidentService)(nil)
