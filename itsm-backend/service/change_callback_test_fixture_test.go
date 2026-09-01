package service

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/ent"
	entchange "itsm-backend/ent/change"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/workflowcallback"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/require"
)

// engineChangeCallbackTestService is a test seam for service-package engine
// tests, which cannot import handlers/change because that package imports
// service. Callback CAS/domain behavior is covered against the real owning
// service in service/bpmn; this seam keeps engine routing tests focused on
// durable callback and gateway behavior.
type engineChangeCallbackTestService struct {
	client *ent.Client
}

func (s *engineChangeCallbackTestService) CreateChangeForWorkflow(context.Context, int, int, string, string, string, string) (int, error) {
	return 0, fmt.Errorf("create_change is outside the engine callback test seam")
}

func (s *engineChangeCallbackTestService) ApplyChangeWorkflowCallback(ctx context.Context, cmd workflowcallback.ChangeCommand) (workflowcallback.Result, error) {
	changeEntity, err := s.client.Change.Query().Where(
		entchange.ID(cmd.ChangeID),
		entchange.HasWorkItemWith(ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil()),
	).WithWorkItem().Only(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	workItem := changeEntity.Edges.WorkItem
	result := workflowcallback.Result{Status: workflowcallback.StatusApplied, Message: "engine callback test effect applied"}
	switch cmd.Action {
	case "update_change":
		update := s.client.Ticket.UpdateOneID(workItem.ID).Where(ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil())
		changed := false
		if cmd.Title != nil && workItem.Title != *cmd.Title {
			update.SetTitle(*cmd.Title)
			changed = true
		}
		if cmd.Description != nil && workItem.Description != *cmd.Description {
			update.SetDescription(*cmd.Description)
			changed = true
		}
		if !changed {
			result.Status = workflowcallback.StatusIdempotent
			return result, nil
		}
		_, err = update.SetUpdatedAt(time.Now()).Save(ctx)
	case "reject_change":
		if workItem.Status == "rejected" {
			result.Status = workflowcallback.StatusIdempotent
			return result, nil
		}
		_, err = workItem.Update().SetStatus("rejected").Save(ctx)
	case "schedule_change":
		if workItem.Status == "scheduled" {
			result.Status = workflowcallback.StatusIdempotent
			return result, nil
		}
		_, err = workItem.Update().SetStatus("scheduled").Save(ctx)
	case "implement_change":
		if workItem.Status == "in_progress" {
			result.Status = workflowcallback.StatusIdempotent
			return result, nil
		}
		_, err = workItem.Update().SetStatus("in_progress").Save(ctx)
	case "verify_change", "close_change":
		target := "completed"
		if cmd.VerificationResult == "failed" {
			target = "failed"
		}
		if workItem.Status == target {
			result.Status = workflowcallback.StatusIdempotent
			return result, nil
		}
		_, err = workItem.Update().SetStatus(target).Save(ctx)
	case "assess_risk":
		result.Output = map[string]interface{}{"risk_level": changeEntity.RiskLevel, "impact_scope": changeEntity.ImpactScope}
		result.Status = workflowcallback.StatusIdempotent
	default:
		result.Status = workflowcallback.StatusBlocked
		result.BlockCode = "handler_contract"
	}
	return result, err
}

func injectEngineChangeCallbackTestService(t require.TestingT, engine ProcessEngine, client *ent.Client) {
	customEngine, ok := engine.(*CustomProcessEngine)
	require.True(t, ok, "expected ProcessEngine to be *CustomProcessEngine")
	handler, ok := customEngine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler)
	require.True(t, ok, "expected registered ChangeServiceTaskHandler")
	handler.SetChangeService(&engineChangeCallbackTestService{client: client})
}
