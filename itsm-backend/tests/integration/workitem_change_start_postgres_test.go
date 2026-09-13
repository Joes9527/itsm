//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent/change"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processbinding"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	catalog "itsm-backend/handlers/service_catalog"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"sync"
	"testing"
	"time"
)

func newSubmittedChangeIntake(t *testing.T, kind string) (*changeLifecycleFixture, *intake.Service, creation.Identity, creation.CreateWorkItemCommand, *creation.CreateWorkItemResult) {
	noProcess := kind == "no_process"
	if noProcess {
		kind = "normal"
	}
	f := newChangeLifecycleFixture(t, kind)
	var role string
	tx, err := f.runtime.Tx(f.ctx)
	require.NoError(t, err)
	rows, err := tx.QueryContext(f.ctx, "SELECT current_user")
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&role))
	require.NoError(t, rows.Close())
	require.NoError(t, tx.Rollback())
	for _, table := range []string{"intake_resolution_snapshots", "process_bindings", "field_definitions", "field_values", "work_item_number_sequences", "sla_definitions"} {
		_, err = f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT,INSERT,UPDATE ON %s TO %s", table, role))
		require.NoError(t, err)
		var seq *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&seq))
		if seq != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*seq+" TO "+role)
			require.NoError(t, err)
		}
	}
	f.client.ProcessBinding.Create().SetTenantID(f.tenant.ID).SetBusinessType("change_request").SetIsDefault(true).SetProcessDefinitionKey("change_normal_flow").SaveX(f.ctx)
	if noProcess {
		f.client.ProcessBinding.Update().SetConditions(map[string]interface{}{"no_process": true}).ExecX(f.ctx)
	}
	f.client.ProcessBinding.Create().SetTenantID(f.tenant.ID).SetBusinessType("change_request").SetBusinessSubType("emergency").SetIsDefault(false).SetProcessDefinitionKey("change_emergency_flow").SaveX(f.ctx)
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(f.owner))
	logger := zap.NewNop().Sugar()
	resolver := intake.NewResolver(catalog.NewService(nil, f.runtime, logger, nil), service.NewProcessBindingService(f.runtime), service.NewConfigurationItemService(f.runtime, logger, nil, nil), service.NewTicketCategoryService(f.runtime))
	app := intake.NewService(f.runtime, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), f.clients.IntakeDirectorySnapshot(), executionfixture.Standard())
	actor := creation.Identity{TenantID: f.tenant.ID, ActorTenantID: f.tenant.ID, ActorID: f.actor.ID, RequesterID: f.actor.ID, Channel: "itsm_web", Role: "super_admin"}
	command := creation.CreateWorkItemCommand{RecordClass: "change_request", IntakeKind: "change_request", Confirmation: "confirmed", IdempotencyKey: "frozen-change", Title: "Frozen change", Change: &creation.ChangeInput{Type: kind, Justification: "controlled update", ImplementationPlan: "deploy", RollbackPlan: "restore", RiskLevel: "low", ImpactScope: "low"}}
	created, err := app.Create(f.ctx, actor, command)
	require.NoError(t, err)
	f.c = f.client.Change.Query().Where(change.WorkItemID(created.WorkItemID)).OnlyX(f.ctx)
	return f, app, actor, command, created
}

func TestChangeSubmitOwnsFrozenWorkflowStart(t *testing.T) {
	for _, order := range []string{"worker_first", "submit_first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			f, app, actor, create, created := newSubmittedChangeIntake(t, "normal")
			require.Equal(t, "awaiting_submit", created.WorkflowStartStatus)
			replay, err := app.Create(f.ctx, actor, create)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, "awaiting_submit", replay.WorkflowStartStatus)
			snapshot := f.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemID(f.c.WorkItemID)).OnlyX(f.ctx)
			// Later routing changes must not select a different definition at submit.
			f.client.ProcessBinding.Update().Where(processbinding.BusinessType("change_request")).SetProcessDefinitionKey("change_emergency_flow").ExecX(f.ctx)
			worker := func() error {
				due, err := service.NewOutboxEventRepository(f.runtime, executionfixture.Standard()).ClaimDueByEventType(f.ctx, time.Now().Add(time.Second), 100, "workflow.start.requested")
				if err != nil {
					return err
				}
				for _, event := range due {
					if err := service.NewWorkflowStartOutboxHandler(f.runtime, f.engine, f.clients.System).Deliver(f.ctx, event); err != nil {
						return err
					}
				}
				return nil
			}
			cmd := f.command("submit", "submit")
			switch order {
			case "worker_first":
				require.NoError(t, worker())
				require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
				f.apply(t, cmd)
			case "submit_first":
				f.apply(t, cmd)
				require.NoError(t, worker())
			case "concurrent":
				var wg sync.WaitGroup
				var workerErr, submitErr error
				start := make(chan struct{})
				wg.Add(2)
				go func() { defer wg.Done(); <-start; workerErr = worker() }()
				go func() { defer wg.Done(); <-start; _, submitErr = f.owner.ApplyCommand(f.ctx, cmd) }()
				close(start)
				wg.Wait()
				require.NoError(t, workerErr)
				require.NoError(t, submitErr)
			}
			instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
			require.Equal(t, *snapshot.WorkflowDefinitionID, instance.ProcessDefinitionID)
			require.Equal(t, snapshot.WorkflowDefinitionKey, instance.ProcessDefinitionKey)
			require.Equal(t, fmt.Sprintf("change_request:%d", f.c.WorkItemID), instance.BusinessKey)
			require.Equal(t, true, instance.Variables["approval_required"])
			require.NotEmpty(t, instance.StartRequestDigest)
			f.apply(t, cmd)
			require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(f.ctx))
			replay, err = app.Create(f.ctx, actor, create)
			require.NoError(t, err)
			require.Equal(t, "active", replay.WorkflowStartStatus)
			require.Zero(t, f.client.OutboxEvent.Query().Where(outboxevent.EventType("workflow.start.requested"), outboxevent.AggregateID(fmt.Sprint(created.WorkItemID))).CountX(f.ctx))
		})
	}
}

func TestChangeSubmitFrozenWorkflowFailures(t *testing.T) {
	for _, mode := range []string{"missing_snapshot", "no_process", "definition_changed", "missing_variables"} {
		t.Run(mode, func(t *testing.T) {
			kind := "normal"
			if mode == "no_process" {
				kind = mode
			}
			f, _, _, _, created := newSubmittedChangeIntake(t, kind)
			if mode == "no_process" {
				require.Equal(t, "not_required", created.WorkflowStartStatus)
			}
			cmd := f.command("submit", "submit")
			snapshot := f.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemID(f.c.WorkItemID)).OnlyX(f.ctx)
			switch mode {
			case "missing_snapshot":
				f.client.IntakeResolutionSnapshot.DeleteOneID(snapshot.ID).ExecX(f.ctx)
			case "no_process":
				require.True(t, snapshot.NoProcess)
			case "definition_changed":
				f.client.ProcessDefinition.UpdateOneID(*snapshot.WorkflowDefinitionID).SetBpmnXML([]byte("changed")).ExecX(f.ctx)
			case "missing_variables":
				_, err := f.db.ExecContext(f.ctx, "UPDATE intake_resolution_snapshots SET workflow_variables=NULL WHERE id=$1", snapshot.ID)
				require.NoError(t, err)
			}
			_, err := f.owner.ApplyCommand(f.ctx, cmd)
			require.Error(t, err)
			require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
			item := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			require.Equal(t, "draft", item.Status)
			require.Equal(t, cmd.Meta.ExpectedVersion, item.Version)
		})
	}
}
func TestChangeSubmitEmergencyFrozenBinding(t *testing.T) {
	f, _, _, _, _ := newSubmittedChangeIntake(t, "emergency")
	snapshot := f.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemID(f.c.WorkItemID)).OnlyX(f.ctx)
	require.Equal(t, "change_emergency_flow", snapshot.WorkflowDefinitionKey)
	f.apply(t, f.command("submit", "submit"))
	require.Equal(t, *snapshot.WorkflowDefinitionID, f.client.ProcessInstance.Query().OnlyX(f.ctx).ProcessDefinitionID)
}
func TestChangeSubmitRejectsGenericEngineStart(t *testing.T) {
	for _, entry := range []string{"direct", "transaction", "trigger"} {
		t.Run(entry, func(t *testing.T) {
			f, _, _, _, _ := newSubmittedChangeIntake(t, "normal")
			ctx := service.WithTrustedBPMNTenantContext(f.ctx, f.tenant.ID)
			ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.actor.ID)
			key, _ := dto.WorkItemBusinessKey("change_request", f.c.WorkItemID)
			var err error
			vars := map[string]interface{}{"requester_id": float64(f.actor.ID), "triggered_by": fmt.Sprint(f.actor.ID), "work_item_id": f.c.WorkItemID, "record_class": "change_request", "change_id": f.c.ID}
			if entry == "direct" {
				_, err = f.engine.StartProcess(ctx, "change_normal_flow", key, "change_request", f.c.WorkItemID, vars)
			} else if entry == "trigger" {
				_, err = service.NewProcessTriggerService(f.runtime, f.engine).TriggerProcess(ctx, &dto.ProcessTriggerRequest{TenantID: f.tenant.ID, BusinessType: dto.BusinessTypeChangeRequest, BusinessID: f.c.WorkItemID, ProcessDefinitionKey: "change_normal_flow", TriggeredBy: fmt.Sprint(f.actor.ID)})
			} else {
				tx, txErr := f.runtime.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				require.NoError(t, txErr)
				_, err = f.engine.StartProcessTx(ctx, tx, "change_normal_flow", key, "change_request", f.c.WorkItemID, vars)
				require.NoError(t, tx.Rollback())
			}
			require.ErrorContains(t, err, "professional submit")
			require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
		})
	}
}

func TestChangeSubmitUsesCurrentActorAndOriginalRequester(t *testing.T) {
	f, _, _, _, _ := newSubmittedChangeIntake(t, "normal")
	creator := f.actor
	submitter := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("submitter").SetName("submitter").SetEmail("submitter@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
	f.actor = submitter
	f.apply(t, f.command("submit", "submit-other-actor"))
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	require.Equal(t, fmt.Sprint(submitter.ID), instance.Initiator)
	require.Equal(t, fmt.Sprint(creator.ID), fmt.Sprint(instance.Variables["requester_id"]))
	require.Equal(t, fmt.Sprint(submitter.ID), instance.Variables["triggered_by"])
	snapshot := f.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemID(f.c.WorkItemID)).OnlyX(f.ctx)
	receipt := f.client.IntakeRequest.GetX(f.ctx, snapshot.IntakeRequestID)
	require.Equal(t, creator.ID, receipt.ActorID)
	require.Equal(t, creator.ID, receipt.RequesterID)
}

func TestChangeSubmitRejectsFrozenSubtypeDrift(t *testing.T) {
	for _, from := range []string{"normal", "emergency"} {
		t.Run(from, func(t *testing.T) {
			f, _, _, _, _ := newSubmittedChangeIntake(t, from)
			to := dto.ChangeType("emergency")
			if from == "emergency" {
				to = dto.ChangeType("normal")
			}
			meta := f.command("metadata", "change-type").Meta
			_, err := f.owner.ApplyMetadata(f.ctx, changedomain.MetadataCommand{Meta: meta, ChangeID: f.c.ID, Patch: dto.UpdateChangeRequest{Type: &to}})
			require.NoError(t, err)
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			_, err = f.owner.ApplyCommand(f.ctx, f.command("submit", "submit"))
			require.ErrorContains(t, err, "frozen change type")
			after := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, "draft", after.Status)
			require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
		})
	}
}

func TestChangeSubmitLegacyCreationEventIsBlocked(t *testing.T) {
	f, _, _, _, _ := newSubmittedChangeIntake(t, "normal")
	snapshot := f.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemID(f.c.WorkItemID)).OnlyX(f.ctx)
	eventID := fmt.Sprintf("workflow-start:%d:%d", f.c.WorkItemID, *snapshot.WorkflowDefinitionID)
	var variables map[string]interface{}
	require.NoError(t, json.Unmarshal(snapshot.WorkflowVariables, &variables))
	payload, err := json.Marshal(map[string]interface{}{"tenantId": f.tenant.ID, "workItemId": f.c.WorkItemID, "recordClass": "change_request", "workflowDefinitionId": *snapshot.WorkflowDefinitionID, "workflowDefinitionKey": snapshot.WorkflowDefinitionKey, "workflowDefinitionVersion": snapshot.WorkflowDefinitionVersion, "workflowDefinitionDigest": snapshot.WorkflowDefinitionDigest, "actorId": f.actor.ID, "channel": "itsm_web", "intakeRequestId": snapshot.IntakeRequestID, "dedupeKey": eventID, "variables": variables})
	require.NoError(t, err)
	repo := service.NewOutboxEventRepository(f.runtime, executionfixture.Standard())
	event, err := repo.Enqueue(f.ctx, nil, service.NewOutboxEvent{EventID: eventID, EventType: "workflow.start.requested", TenantID: f.tenant.ID, AggregateType: "work_item", AggregateID: fmt.Sprint(f.c.WorkItemID), Payload: payload, NextAttemptAt: time.Now().Add(-time.Second)})
	require.NoError(t, err)
	registry, err := service.NewOutboxEventTypeRegistry([]service.OutboxDeliveryHandler{service.NewWorkflowStartOutboxHandler(f.runtime, f.engine, f.clients.System)})
	require.NoError(t, err)
	worker, err := service.NewOutboxDeliveryWorker(service.NewOutboxEventRepository(f.clients.System, executionfixture.Standard()), service.OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zap.NewNop().Sugar(), registry)
	require.NoError(t, err)
	require.NoError(t, worker.DispatchOnce(f.ctx))
	recorded := f.client.OutboxEvent.GetX(f.ctx, event.ID)
	require.Equal(t, 1, recorded.AttemptCount, "worker must actually consume the event")
	require.Equal(t, "blocked", recorded.Status)
	require.Contains(t, recorded.LastError, "professional submit")
	require.True(t, recorded.PublishedAt.IsZero())
	require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
	f.apply(t, f.command("submit", "submit"))
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(f.ctx))
}
