//go:build integration_postgres

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"itsm-backend/ent/intakeresolutionsnapshot"
	"itsm-backend/ent/processdefinition"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/migration"
	"itsm-backend/service"
)

type changeLifecycleFixture struct {
	clients *database.RuntimeClients
	*incidentEffectsFixture
	pirOwner *service.ChangePIRService
	engine   *service.CustomProcessEngine
	runtime  *ent.Client
	owner    *changedomain.Service
	c        *ent.Change
}

func newChangeLifecycleFixture(t *testing.T, kind string) *changeLifecycleFixture {
	t.Helper()
	f := newIncidentEffectsFixture(t)
	migrator := migration.NewMigrator(f.db, zap.NewNop().Sugar())
	require.NoError(t, migrator.EnsureMigrationsTable(f.ctx))
	_, err := migrator.RunMigrations(f.ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
	f.actor.Update().SetRole("super_admin").ExecX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	_, err = f.db.ExecContext(f.ctx, "GRANT SELECT ON user_roles,work_item_relations TO "+cfg.User)
	require.NoError(t, err)
	for _, table := range []string{"changes", "change_pi_rs", "change_risk_assessments", "intake_resolution_snapshots", "standard_changes", "process_definitions", "process_deployments", "process_instances", "process_tasks", "process_audit_logs", "process_callback_outboxes", "process_execution_histories", "process_approval_decisions", "groups", "departments"} {
		_, err = f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
		var sequence *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&sequence))
		if sequence != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*sequence+" TO "+cfg.User)
			require.NoError(t, err)
		}
	}
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	owner := changedomain.NewService(changedomain.NewEntRepository(clients.Tenant, nil), clients.Tenant, zap.NewNop().Sugar(), executionfixture.Standard())
	owner.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
	engine := service.NewCustomProcessEngine(clients.Tenant, zap.NewNop().Sugar(), executionfixture.Standard()).(*service.CustomProcessEngine)
	engine.SetCallbackCandidateClient(clients.System)
	engine.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
	owner.SetProcessEngine(engine)
	// A4b deliberately stops at an actual pending assessment task. A4c owns the
	// complete default definition and callback cutover, not a fake consumer here.
	xml := []byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="change" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:userTask id="assessment" name="Assessment"/><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="assessment"/><bpmn:sequenceFlow id="b" sourceRef="assessment" targetRef="end"/></bpmn:process></bpmn:definitions>`)
	deployment := f.client.ProcessDeployment.Create().SetTenantID(f.tenant.ID).SetDeploymentID("change-core").SetDeploymentName("Change core").SaveX(f.ctx)
	for _, key := range []string{"change_normal_flow", "change_emergency_flow"} {
		f.client.ProcessDefinition.Create().SetTenantID(f.tenant.ID).SetDeploymentID(deployment.ID).SetKey(key).SetName(key).SetBpmnXML(xml).SetIsActive(true).SaveX(f.ctx)
	}
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("Change core").SetTicketNumber("CHG-CORE").SetRecordClass("change_request").SetStatus("draft").SetPriority("high").SaveX(f.ctx)
	record := f.client.Change.Create().SetWorkItemID(item.ID).SetType(kind).SetImplementationPlan("deploy package").SetRollbackPlan("restore previous package").SaveX(f.ctx)
	f.ctx = ctx
	pirOwner := service.NewChangePIRService(clients.Tenant, zap.NewNop().Sugar(), executionfixture.Standard())
	pirOwner.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
	return &changeLifecycleFixture{clients, f, pirOwner, engine, clients.Tenant, owner, record}
}

func (f *changeLifecycleFixture) command(action, key string) changedomain.Command {
	// Legacy lifecycle fixtures create their graph directly. Freeze the fixture
	// definition before its first submit command; production Intake always does this at creation.
	if action == "submit" && !f.client.IntakeResolutionSnapshot.Query().Where(intakeresolutionsnapshot.WorkItemID(f.c.WorkItemID)).ExistX(f.ctx) {
		definitionKey := "change_normal_flow"
		if f.c.Type == "emergency" {
			definitionKey = "change_emergency_flow"
		}
		definition := f.client.ProcessDefinition.Query().Where(processdefinition.Key(definitionKey), processdefinition.TenantID(f.tenant.ID), processdefinition.IsActive(true)).FirstX(f.ctx)
		frozen := service.FreezeProcessDefinition(definition)
		item := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
		receipt := f.client.IntakeRequest.Create().SetTenantID(f.tenant.ID).SetActorTenantID(f.tenant.ID).SetActorID(item.OpenedByID).SetRequesterID(item.RequesterID).SetChannel("itsm_web").SetOperation("create_work_item").SetIdempotencyKey(fmt.Sprint("fixture:", item.ID)).SetRequestDigest("fixture-digest").SetDigestVersion("intake-v3").SetStatus("completed").SetWorkItemID(item.ID).SaveX(f.ctx)
		variables, _ := json.Marshal(map[string]interface{}{"work_item_id": item.ID, "record_class": "change_request", "tenant_id": item.TenantID, "requester_id": item.RequesterID, "triggered_by": fmt.Sprint(item.OpenedByID), "change_id": f.c.ID, "change_type": f.c.Type})
		f.client.IntakeResolutionSnapshot.Create().SetTenantID(item.TenantID).SetIntakeRequestID(receipt.ID).SetWorkItemID(item.ID).SetChannel("itsm_web").SetSourceProvider("itsm_web").SetRecordClass(item.RecordClass).SetWorkflowDefinitionID(frozen.ID).SetWorkflowDefinitionKey(frozen.Key).SetWorkflowDefinitionVersion(frozen.Version).SetWorkflowDefinitionDigest(frozen.Digest).SetWorkflowVariables(variables).SetResolverVersion("fixture").SetRequestDigest("fixture-digest").SaveX(f.ctx)
	}

	return changedomain.Command{Meta: workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version, Source: "http", OperationID: key}, ChangeID: f.c.ID, Action: action, Evidence: "observed evidence " + key}
}
func (f *changeLifecycleFixture) apply(t *testing.T, cmd changedomain.Command) workitemmutation.Result {
	t.Helper()
	r, err := f.owner.ApplyCommand(f.ctx, cmd)
	require.NoError(t, err)
	return r
}

func (f *changeLifecycleFixture) authorize(t *testing.T) {
	t.Helper()
	f.apply(t, f.command("submit", "submit"))
	f.apply(t, f.command("assess", "assess"))
	approver := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("approver").SetName("approver").SetEmail("approver@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
	f.actor = approver
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	decision := f.client.ProcessApprovalDecision.Create().SetTenantID(f.tenant.ID).SetProcessInstanceID(instance.ID).SetProcessTaskID(100).SetProcessInstanceKey(instance.ProcessInstanceID).SetTaskID("cab").SetProcessDefinitionKey(instance.ProcessDefinitionKey).SetNodeKey("cab").SetBusinessType("change_request").SetBusinessID(fmt.Sprint(f.c.WorkItemID)).SetActorID(approver.ID).SetAction("approve").SetDecision("approved").SaveX(f.ctx)
	cmd := f.command("authorize", "authorize")
	cmd.ApprovalDecisionID = decision.ID
	f.apply(t, cmd)
	if f.c.Type != "emergency" {
		start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
		schedule := f.command("schedule", "schedule")
		schedule.PlannedStart = &start
		schedule.PlannedEnd = &end
		f.apply(t, schedule)
	}
	f.apply(t, f.command("implement", "implement"))
}

func TestWorkItemChangeLifecycleEvidenceAndTypes(t *testing.T) {
	for _, kind := range []string{"normal", "standard", "emergency"} {
		for _, outcome := range []string{"successful", "failed", "rolled_back"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				f := newChangeLifecycleFixture(t, kind)
				_, err := f.owner.ApplyCommand(f.ctx, f.command("implement", "unauthorized"))
				require.Error(t, err)
				f.authorize(t)
				_, err = f.owner.ApplyCommand(f.ctx, f.command("implement", "duplicate-start"))
				require.Error(t, err)
				pir := f.client.ChangePIR.Create().SetChangeID(f.c.ID).SetTenantID(f.tenant.ID).SetReviewerID(f.actor.ID).SaveX(f.ctx)
				close := f.command("close", "premature")
				close.PIRID = pir.ID
				_, err = f.owner.ApplyCommand(f.ctx, close)
				require.Error(t, err, "default successful PIR is not evidence")
				finish := f.command("record_outcome", "result")
				finish.Outcome = outcome
				end := time.Now()
				finish.ActualEnd = &end
				result := f.apply(t, finish)
				require.Equal(t, "in_progress", result.Status)
				pir.Update().SetOverallResult(outcome).SetSuccessSummary("validated result").SetReviewDate(time.Now()).ExecX(f.ctx)
				review := f.command("review", "review")
				review.PIRID = pir.ID
				f.apply(t, review)
				duplicate := f.command("review", "duplicate-review")
				duplicate.PIRID = pir.ID
				duplicate.Evidence = review.Evidence
				_, err = f.owner.ApplyCommand(f.ctx, duplicate)
				require.Error(t, err, "fresh key cannot repeat unchanged review")
				pir.Update().SetLessonsLearned("changed after review").ExecX(f.ctx)
				close = f.command("close", "stale-review")
				close.PIRID = pir.ID
				_, err = f.owner.ApplyCommand(f.ctx, close)
				require.Error(t, err)
				review = f.command("review", "updated-review")
				review.PIRID = pir.ID
				f.apply(t, review)
				changedResult := f.command("record_outcome", "edited-result")
				changedResult.Outcome = outcome
				editedEnd := time.Now()
				changedResult.ActualEnd = &editedEnd
				f.apply(t, changedResult)
				close = f.command("close", "edited-result-invalidates-review")
				close.PIRID = pir.ID
				_, err = f.owner.ApplyCommand(f.ctx, close)
				require.Error(t, err)
				pir.Update().SetReviewDate(time.Now()).ExecX(f.ctx)
				review = f.command("review", "result-rereview")
				review.PIRID = pir.ID
				f.apply(t, review)
				close = f.command("close", "close")
				close.PIRID = pir.ID
				closed := f.apply(t, close)
				require.Equal(t, "completed", closed.Status)
				require.Equal(t, outcome, f.client.Change.GetX(f.ctx, f.c.ID).Outcome)
				projection, err := f.owner.GetChange(f.ctx, f.c.ID, f.command("read", "projection").Meta)
				require.NoError(t, err)
				require.Equal(t, closed.Version, projection.Version)
				require.Equal(t, outcome, projection.Outcome)
				replay := f.apply(t, close)
				require.True(t, replay.Replayed)
				require.Equal(t, closed.Version, replay.Version)
				close.Evidence = "conflicting request"
				_, err = f.owner.ApplyCommand(f.ctx, close)
				require.Error(t, err)
				_, err = f.owner.ApplyCommand(f.ctx, f.command("reopen", "unsupported"))
				require.Error(t, err)
			})
		}
	}
}

func TestWorkItemChangeLifecycleAtomicSubmit(t *testing.T) {
	for _, fault := range []string{"audit", "process", "commit"} {
		t.Run(fault, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			var enabled = true
			switch fault {
			case "audit":
				f.runtime.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if enabled {
							return nil, errors.New("injected audit failure")
						}
						return next.Mutate(ctx, m)
					})
				})
			case "process":
				f.runtime.ProcessTask.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if enabled {
							return nil, errors.New("injected task failure")
						}
						return next.Mutate(ctx, m)
					})
				})
			case "commit":
				_, err := f.db.ExecContext(f.ctx, "ALTER TABLE process_instances ADD CONSTRAINT change_core_commit_check FOREIGN KEY (business_id) REFERENCES incidents(id) DEFERRABLE INITIALLY DEFERRED")
				require.NoError(t, err)
			}
			cmd := f.command("submit", "atomic-submit")
			_, err := f.owner.ApplyCommand(f.ctx, cmd)
			require.Error(t, err)
			item := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			require.Equal(t, "draft", item.Status)
			require.Equal(t, 1, item.Version)
			require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
			require.Zero(t, f.client.ProcessTask.Query().CountX(f.ctx))
			require.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.ctx))
			require.Zero(t, f.client.AuditLog.Query().Where(auditlog.OperationIDNotNil()).CountX(f.ctx))
			enabled = false
			if fault == "commit" {
				_, err = f.db.ExecContext(f.ctx, "ALTER TABLE process_instances DROP CONSTRAINT change_core_commit_check")
				require.NoError(t, err)
			}
			f.apply(t, cmd)
			require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.ProcessTask.Query().CountX(f.ctx))
		})
	}
}

func TestWorkItemChangeLifecycleConcurrentAndCurrentAuthority(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	f.apply(t, f.command("submit", "submit"))
	cmd := f.command("assess", "concurrent")
	var wg sync.WaitGroup
	results := make(chan workitemmutation.Result, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := f.owner.ApplyCommand(f.ctx, cmd); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	replayed := 0
	for r := range results {
		if r.Replayed {
			replayed++
		}
		require.Equal(t, 3, r.Version)
	}
	require.Equal(t, 1, replayed)
	stale := cmd
	stale.Meta.OperationID = "stale"
	_, err := f.owner.ApplyCommand(f.ctx, stale)
	require.Error(t, err)
	f.actor.Update().SetActive(false).ExecX(f.ctx)
	_, err = f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err, "revocation must precede replay")
	f.actor.Update().SetActive(true).ExecX(f.ctx)
	cmd.Meta.TenantID++
	_, err = f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err)
}

func TestWorkItemChangeLifecycleOutcomeAuditRollback(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	f.authorize(t)
	version := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version
	before := f.client.AuditLog.Query().CountX(f.ctx)
	f.runtime.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, err := next.Mutate(ctx, m); err != nil {
				return nil, err
			}
			return nil, errors.New("audit failure after insert")
		})
	})
	cmd := f.command("record_outcome", "rollback-outcome")
	cmd.Outcome = "failed"
	end := time.Now()
	cmd.ActualEnd = &end
	_, err := f.owner.ApplyCommand(f.ctx, cmd)
	require.ErrorContains(t, err, "audit failure after insert")
	require.Equal(t, version, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
	current := f.client.Change.GetX(f.ctx, f.c.ID)
	require.Empty(t, current.Outcome)
	require.Empty(t, current.OutcomeEvidence)
	require.True(t, current.ActualEndDate.IsZero())
	require.Equal(t, before, f.client.AuditLog.Query().CountX(f.ctx))
}

func TestWorkItemChangeLifecycleAssessmentFreshness(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	f.apply(t, f.command("submit", "submit"))
	assessment := f.command("assess", "assessment")
	failAssessment := true
	f.runtime.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if failAssessment {
				return nil, errors.New("assessment receipt failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err := f.owner.ApplyCommand(f.ctx, assessment)
	require.ErrorContains(t, err, "assessment receipt failure")
	require.True(t, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).FirstResponseAt.IsZero())
	failAssessment = false
	first := f.apply(t, assessment)
	firstResponse := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).FirstResponseAt
	require.False(t, firstResponse.IsZero())
	f.client.Change.UpdateOneID(f.c.ID).SetImplementationPlan("changed deployment plan").ExecX(f.ctx)
	_, err = f.owner.ApplyCommand(f.ctx, f.command("authorize", "stale-assessment"))
	require.ErrorContains(t, err, "current assessment required")
	replay := f.apply(t, assessment)
	require.True(t, replay.Replayed)
	require.Equal(t, first.Version, replay.Version)
	reassess := f.command("assess", "reassessment")
	reassess.Evidence = assessment.Evidence
	f.apply(t, reassess)
	require.Equal(t, firstResponse, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).FirstResponseAt, "reassessment must not move first response")
	approver := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("fresh-approver").SetName("Approver").SetEmail("fresh@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
	f.actor = approver
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	decision := f.client.ProcessApprovalDecision.Create().SetTenantID(f.tenant.ID).SetProcessInstanceID(instance.ID).SetProcessTaskID(101).SetProcessInstanceKey(instance.ProcessInstanceID).SetTaskID("fresh-cab").SetProcessDefinitionKey(instance.ProcessDefinitionKey).SetNodeKey("cab").SetBusinessType("change_request").SetBusinessID(fmt.Sprint(f.c.WorkItemID)).SetActorID(approver.ID).SetAction("approve").SetDecision("approved").SaveX(f.ctx)
	cmd := f.command("authorize", "fresh-authorization")
	cmd.ApprovalDecisionID = decision.ID
	f.apply(t, cmd)
}

func TestWorkItemChangeLifecycleCloseSerializesPIREdit(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	f.authorize(t)
	outcome := f.command("record_outcome", "result")
	outcome.Outcome = "successful"
	end := time.Now()
	outcome.ActualEnd = &end
	f.apply(t, outcome)
	pir := f.client.ChangePIR.Create().SetChangeID(f.c.ID).SetTenantID(f.tenant.ID).SetReviewerID(f.actor.ID).SetOverallResult("successful").SetReviewDate(time.Now()).SetSuccessSummary("validated").SaveX(f.ctx)
	review := f.command("review", "review")
	review.PIRID = pir.ID
	f.apply(t, review)
	validated, release := make(chan struct{}), make(chan struct{})
	f.runtime.Ticket.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			close(validated)
			<-release
			return next.Mutate(ctx, m)
		})
	})
	command := f.command("close", "close")
	command.PIRID = pir.ID
	done := make(chan error, 1)
	go func() { _, err := f.owner.ApplyCommand(f.ctx, command); done <- err }()
	<-validated
	editTx, err := f.db.BeginTx(f.ctx, nil)
	require.NoError(t, err)
	_, err = editTx.ExecContext(f.ctx, "SET LOCAL lock_timeout='100ms'")
	require.NoError(t, err)
	_, editErr := editTx.ExecContext(f.ctx, "UPDATE change_pi_rs SET lessons_learned='racing edit' WHERE id=$1", pir.ID)
	require.NoError(t, editTx.Rollback())
	close(release)
	require.NoError(t, <-done)
	require.Error(t, editErr, "PIR edits must wait until the reviewed closure commits")
}

func TestWorkItemChangeLifecycleStandardPolicyCreation(t *testing.T) {
	f := newChangeLifecycleFixture(t, "standard")
	template := f.client.StandardChange.Create().SetTenantID(f.tenant.ID).SetCreatedBy(f.actor.ID).SetTitle("standard policy").SetJustification("routine").SetImplementationPlan("deploy package").SetRollbackPlan("restore previous package").SetIsActive(true).SetApprovalRequired(false).SaveX(f.ctx)
	tx, err := f.runtime.Tx(f.ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	plan, err := f.owner.Prepare(f.ctx, tx, creation.ResolvedIntake{Identity: creation.Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, Role: "super_admin", Channel: "http"}, Command: creation.CreateWorkItemCommand{Title: "standard", Change: &creation.ChangeInput{StandardTemplateID: &template.ID}}})
	require.NoError(t, err)
	item := tx.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("standard policy change").SetTicketNumber("CHG-POLICY").SetRecordClass("change_request").SetStatus("draft").SetPriority("low").SaveX(f.ctx)
	ref, err := f.owner.CreateExtension(f.ctx, tx, item, plan)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	f.c = f.client.Change.GetX(f.ctx, ref.ID)
	require.Equal(t, template.ID, f.c.StandardTemplateID)
	require.Equal(t, false, f.c.StandardPolicy["approvalRequired"])
	assertDraftScheduleRejected(t, f)
	template.Update().SetApprovalRequired(true).ExecX(f.ctx)
	f.actor.Update().SetRole("agent").ExecX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("agent").SetName("Change operator").SetIsActive(true).SaveX(f.ctx)
	for _, resource := range []string{"change", "ticket"} {
		permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + ":write").SetName(resource + " write").SetResource(resource).SetAction("write").SaveX(f.ctx)
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	}
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	f.apply(t, f.command("submit", "policy-submit"))
	f.apply(t, f.command("assess", "policy-assess"))
	command := f.command("authorize", "policy-authorize")
	f.apply(t, command)
	assertChangeAuthorizationAudit(t, f, "standard_policy", 0)
	start, end := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	schedule := f.command("schedule", "policy-schedule")
	schedule.PlannedStart = &start
	schedule.PlannedEnd = &end
	require.Equal(t, "scheduled", f.apply(t, schedule).Status)
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx), "configured policy is not a fabricated approval")
	role.Update().SetIsActive(false).ExecX(f.ctx)
	_, err = f.owner.ApplyCommand(f.ctx, command)
	require.Error(t, err, "current write permission before policy replay")
	role.Update().SetIsActive(true).ExecX(f.ctx)
	// The other fixture record has no frozen policy, so write permission alone
	// cannot turn either a standard or a normal change into an authorized change.
	other := f.client.Change.Query().Where().AllX(f.ctx)
	for _, record := range other {
		if record.ID != f.c.ID {
			f.c = record
			break
		}
	}
	f.client.Ticket.UpdateOneID(f.c.WorkItemID).SetStatus("submitted").ExecX(f.ctx)
	for _, kind := range []string{"standard", "normal"} {
		f.client.Change.UpdateOneID(f.c.ID).SetType(kind).ExecX(f.ctx)
		_, err = f.owner.ApplyCommand(f.ctx, f.command("authorize", "write-only-"+kind))
		require.Error(t, err)
	}
}

func assertDraftScheduleRejected(t *testing.T, f *changeLifecycleFixture) {
	t.Helper()
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	receipts := f.client.AuditLog.Query().CountX(f.ctx)
	start, end := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	cmd := f.command("schedule", "draft-schedule")
	cmd.PlannedStart = &start
	cmd.PlannedEnd = &end
	_, err := f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err)
	after := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Version, after.Version)
	require.Equal(t, receipts, f.client.AuditLog.Query().CountX(f.ctx))
	require.True(t, f.client.Change.GetX(f.ctx, f.c.ID).PlannedStartDate.IsZero())
}

func TestWorkItemChangeLifecycleScheduleGovernance(t *testing.T) {
	f := newChangeLifecycleFixture(t, "standard")
	assertDraftScheduleRejected(t, f)
	f.authorize(t) // Includes governed standard schedule and implementation.
	f.client.Ticket.UpdateOneID(f.c.WorkItemID).SetStatus("failed").ExecX(f.ctx)
	start, end := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	cmd := f.command("schedule", "governed-retry")
	cmd.PlannedStart = &start
	cmd.PlannedEnd = &end
	require.Equal(t, "scheduled", f.apply(t, cmd).Status)
	f.client.Ticket.UpdateOneID(f.c.WorkItemID).SetStatus("failed").ExecX(f.ctx)
	f.client.Change.UpdateOneID(f.c.ID).SetImplementationPlan("changed after assessment").ExecX(f.ctx)
	cmd = f.command("schedule", "stale-retry")
	cmd.PlannedStart = &start
	cmd.PlannedEnd = &end
	_, err := f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err)
	t.Run("assessment-without-authorization", func(t *testing.T) {
		f := newChangeLifecycleFixture(t, "standard")
		f.apply(t, f.command("submit", "submit"))
		f.apply(t, f.command("assess", "assess"))
		f.client.Ticket.UpdateOneID(f.c.WorkItemID).SetStatus("failed").ExecX(f.ctx)
		cmd := f.command("schedule", "missing-authority")
		cmd.PlannedStart = &start
		cmd.PlannedEnd = &end
		_, err := f.owner.ApplyCommand(f.ctx, cmd)
		require.ErrorContains(t, err, "governed authorization")
	})
	t.Run("emergency-has-no-scheduled-implementation-edge", func(t *testing.T) {
		f := newChangeLifecycleFixture(t, "emergency")
		f.authorize(t)
		f.client.Ticket.UpdateOneID(f.c.WorkItemID).SetStatus("failed").ExecX(f.ctx)
		cmd := f.command("schedule", "emergency-retry")
		cmd.PlannedStart = &start
		cmd.PlannedEnd = &end
		_, err := f.owner.ApplyCommand(f.ctx, cmd)
		require.ErrorContains(t, err, "legal scheduled implementation")
	})
}

func TestWorkItemChangeLifecycleAllocatedMSP(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("change-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("provider").SetName("provider").SetEmail("provider@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP technician").SetIsActive(true).SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:write").SetName("Change write").SetResource("change").SetAction("write").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	f.apply(t, f.command("submit", "native-submit"))
	f.actor = actor
	_, err := f.runtime.User.Get(f.ctx, actor.ID)
	require.True(t, ent.IsNotFound(err))
	cmd := f.command("assess", "msp-assess")
	f.apply(t, cmd)
	require.Equal(t, actor.ID, f.client.Change.GetX(f.ctx, f.c.ID).AssessedBy)
	allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
	_, err = f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err)
	allocation.Update().ClearDeassignedAt().ExecX(f.ctx)
	role.Update().SetIsActive(false).ExecX(f.ctx)
	_, err = f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err, "current permission before replay")
	role.Update().SetIsActive(true).ExecX(f.ctx)
	actor.Update().ClearMspRole().ExecX(f.ctx)
	_, err = f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err, "foreign native actor")
	cmd.Meta.ActorID = 999999
	_, err = f.owner.ApplyCommand(f.ctx, cmd)
	require.Error(t, err, "forged actor")
}

func TestWorkItemChangeLifecycleTerminalClocks(t *testing.T) {
	for _, action := range []string{"close", "cancel"} {
		for _, bound := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bound=%v", action, bound), func(t *testing.T) {
				f := newChangeLifecycleFixture(t, "normal")
				if action == "close" {
					f.authorize(t)
				}
				if bound {
					policy := f.client.SLADefinition.Create().SetTenantID(f.tenant.ID).SetName("Change clock").SetResponseTime(30).SetResolutionTime(60).SaveX(f.ctx)
					f.client.SLAAlertRule.Create().SetTenantID(f.tenant.ID).SetSLADefinitionID(policy.ID).SetName("Clock warning").SetThresholdPercentage(100).SetNotificationChannels([]string{}).SaveX(f.ctx)
					tx, err := f.client.Tx(f.ctx)
					require.NoError(t, err)
					require.NoError(t, service.NewTicketSLAService(f.client, zap.NewNop().Sugar()).ApplyCreationSLA(f.ctx, tx, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID), &policy.ID))
					require.NoError(t, tx.Commit())
					f.client.Ticket.UpdateOneID(f.c.WorkItemID).SetSLACycleStartedAt(time.Now().Add(-2 * time.Hour)).SetSLAResponseDeadline(time.Now().Add(-90 * time.Minute)).SetSLAResolutionDeadline(time.Now().Add(-time.Hour)).ExecX(f.ctx)
				}
				cmd := f.command(action, "terminal")
				if action == "close" {
					outcome := f.command("record_outcome", "result")
					outcome.Outcome = "failed"
					end := time.Now()
					outcome.ActualEnd = &end
					f.apply(t, outcome)
					pir := f.client.ChangePIR.Create().SetChangeID(f.c.ID).SetTenantID(f.tenant.ID).SetReviewerID(f.actor.ID).SetOverallResult("failed").SetReviewDate(time.Now()).SetIssuesEncountered("implementation failed").SaveX(f.ctx)
					review := f.command("review", "review")
					review.PIRID = pir.ID
					f.apply(t, review)
					cmd = f.command(action, "terminal")
					cmd.PIRID = pir.ID
				}
				before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
				failTerminal := true
				f.runtime.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if failTerminal {
							return nil, errors.New("terminal receipt failure")
						}
						return next.Mutate(ctx, m)
					})
				})
				_, err := f.owner.ApplyCommand(f.ctx, cmd)
				require.ErrorContains(t, err, "terminal receipt failure")
				rolledBack := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
				require.Equal(t, before.Version, rolledBack.Version)
				require.Equal(t, before.ClosedAt, rolledBack.ClosedAt)
				require.Equal(t, before.ResolvedAt, rolledBack.ResolvedAt)
				require.Equal(t, before.FirstResponseAt, rolledBack.FirstResponseAt)
				failTerminal = false
				f.apply(t, cmd)
				item := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
				require.NotNil(t, item.ClosedAt)
				if action == "cancel" {
					require.True(t, item.ResolvedAt.IsZero())
					require.True(t, item.FirstResponseAt.IsZero())
				} else {
					require.False(t, item.ResolvedAt.IsZero())
					require.False(t, item.FirstResponseAt.IsZero())
					require.Equal(t, *item.ClosedAt, item.ResolvedAt)
				}
				sla := service.NewTicketSLAService(f.runtime, zap.NewNop().Sugar())
				overdue, err := sla.GetOverdueTickets(f.ctx, f.tenant.ID)
				require.NoError(t, err)
				for _, candidate := range overdue {
					require.NotEqual(t, item.ID, candidate.ID, "closed item is not active overdue")
				}
				// Actual active monitor/alert entry points must not manufacture a new
				// violation after closure, even though old breach facts remain true.
				monitor := service.NewSLAMonitorService(f.client, zap.NewNop().Sugar(), executionfixture.Standard())
				stats, err := monitor.CheckSLAViolations(f.ctx, f.tenant.ID)
				require.NoError(t, err)
				require.Zero(t, stats.NewViolations)
				// Exercise future-deadline warning rules as well as overdue scans.
				if bound {
					f.client.Ticket.UpdateOneID(item.ID).SetSLAResponseDeadline(time.Now().Add(time.Minute)).SetSLAResolutionDeadline(time.Now().Add(time.Minute)).ExecX(f.ctx)
				}
				alerts := service.NewSLAAlertService(f.client, zap.NewNop().Sugar())
				alerted, err := alerts.CheckAndTriggerAlerts(f.ctx, item.ID, f.tenant.ID)
				require.NoError(t, err)
				require.False(t, alerted)
				warned, err := alerts.TriggerSLAWarning(f.ctx, item.ID, "resolution_time", f.tenant.ID)
				require.NoError(t, err)
				require.False(t, warned)
			})
		}
	}
}
