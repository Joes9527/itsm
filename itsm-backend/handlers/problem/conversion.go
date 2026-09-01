package problem

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/workitemrelation"
)

const (
	incidentConversionAuditPath = "/api/v1/incidents/:id/convert-to-problem"
)

// ConversionService is the narrow Incident-controller boundary for creating a
// Problem from an Incident without exposing Problem repository internals.
type ConversionService interface {
	CreateFromIncident(ctx context.Context, tenantID, incidentID, actorUserID int, req dto.ConvertIncidentToProblemRequest) (*Problem, error)
}

func (s *Service) CreateFromIncident(
	ctx context.Context,
	tenantID, incidentID, actorUserID int,
	req dto.ConvertIncidentToProblemRequest,
) (*Problem, error) {
	return s.repo.CreateFromIncident(ctx, tenantID, incidentID, actorUserID, req)
}

func (r *EntRepository) CreateFromIncident(
	ctx context.Context,
	tenantID, incidentID, actorUserID int,
	req dto.ConvertIncidentToProblemRequest,
) (*Problem, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start incident conversion transaction: %w", err)
	}
	fail := func(cause error) (*Problem, error) {
		return nil, rollbackProblemTx(tx, cause)
	}

	source, err := tx.Incident.Query().Where(
		incident.IDEQ(incidentID),
		incident.HasWorkItemWith(ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()),
	).WithWorkItem(withProblemWorkItemProjection).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fail(fmt.Errorf("incident not found"))
		}
		return fail(fmt.Errorf("load incident for conversion: %w", err))
	}
	if common.IsIncidentFinalStatus(source.Edges.WorkItem.Status) {
		return fail(fmt.Errorf("%s incident cannot be converted to a problem", source.Edges.WorkItem.Status))
	}
	if source.WorkItemID <= 0 {
		return fail(fmt.Errorf("incident source work item is missing"))
	}

	_, err = tx.Ticket.Query().Where(
		ticket.IDEQ(source.WorkItemID),
		ticket.TenantIDEQ(tenantID),
		ticket.RecordClassEQ("incident"),
		ticket.DeletedAtIsNil(),
	).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fail(fmt.Errorf("incident source work item not found"))
		}
		return fail(fmt.Errorf("validate incident source work item: %w", err))
	}

	alreadyConverted, err := tx.WorkItemRelation.Query().Where(
		workitemrelation.TenantID(tenantID),
		workitemrelation.SourceWorkItemID(source.WorkItemID),
		workitemrelation.RelationType(common.WorkItemRelationInvestigatedBy),
		workitemrelation.DeletedAtIsNil(),
	).Exist(ctx)
	if err != nil {
		return fail(fmt.Errorf("check existing incident problem relation: %w", err))
	}
	if alreadyConverted {
		return fail(fmt.Errorf("incident is already investigated by a problem"))
	}

	title := "问题-" + source.Edges.WorkItem.Title
	description := source.Edges.WorkItem.Description
	if customTitle := strings.TrimSpace(req.Title); customTitle != "" {
		title = customTitle
	}
	if customDescription := strings.TrimSpace(req.Description); customDescription != "" {
		description = customDescription
	}

	categoryName := ""
	if source.Edges.WorkItem.Edges.Category != nil {
		categoryName = source.Edges.WorkItem.Edges.Category.Name
	}
	created, err := r.createInTx(ctx, tx, &Problem{
		Title:       title,
		Description: description,
		Status:      "open",
		Priority:    source.Edges.WorkItem.Priority,
		Category:    categoryName,
		RootCause:   strings.TrimSpace(req.RootCause),
		Impact:      source.Impact,
		CreatedBy:   actorUserID,
		TenantID:    tenantID,
	})
	if err != nil {
		return fail(err)
	}
	if created.WorkItemID == nil {
		return fail(fmt.Errorf("created problem has no work item"))
	}

	_, err = tx.WorkItemRelation.Create().
		SetTenantID(tenantID).
		SetSourceWorkItemID(source.WorkItemID).
		SetTargetWorkItemID(*created.WorkItemID).
		SetRelationType(common.WorkItemRelationInvestigatedBy).
		SetCreatedByID(actorUserID).
		Save(ctx)
	if err != nil {
		return fail(fmt.Errorf("create incident problem relation: %w", err))
	}

	_, err = tx.IncidentEvent.Create().
		SetIncidentID(incidentID).
		SetTenantID(tenantID).
		SetUserID(actorUserID).
		SetSource("incident_conversion").
		SetEventType("conversion").
		SetEventName("convert_to_problem").
		SetData(map[string]any{
			"problem_id":           created.ID,
			"problem_work_item_id": *created.WorkItemID,
		}).
		Save(ctx)
	if err != nil {
		return fail(fmt.Errorf("create incident conversion event: %w", err))
	}

	_, err = tx.AuditLog.Create().
		SetTenantID(tenantID).
		SetUserID(actorUserID).
		SetResource("incident").
		SetAction("convert_to_problem").
		SetPath(incidentConversionAuditPath).
		SetMethod("POST").
		SetRequestBody(redactedConversionAuditJSON(
			incidentID,
			source.WorkItemID,
			created.ID,
			*created.WorkItemID,
			req,
		)).
		Save(ctx)
	if err != nil {
		return fail(fmt.Errorf("create incident conversion audit log: %w", err))
	}

	if err := tx.Commit(); err != nil {
		return nil, rollbackProblemTx(tx, fmt.Errorf("commit incident conversion transaction: %w", err))
	}
	return created, nil
}

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
