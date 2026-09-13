//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processtask"
	"itsm-backend/migration"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
)

func transactionBPMNFixture(t *testing.T, userTask bool) (*incidentEffectsFixture, *ent.Client, *service.CustomProcessEngine, context.Context, string) {
	t.Helper()
	f := newIncidentEffectsFixture(t)
	migrator := migration.NewMigrator(f.db, zap.NewNop().Sugar())
	require.NoError(t, migrator.EnsureMigrationsTable(f.ctx))
	_, err := migrator.RunMigrations(f.ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
	f.actor.Update().SetRole("super_admin").ExecX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	_, err = f.db.ExecContext(f.ctx, "GRANT SELECT ON user_roles TO "+cfg.User)
	require.NoError(t, err)
	for _, table := range []string{"process_definitions", "process_deployments", "process_instances", "process_tasks", "process_audit_logs", "process_callback_outboxes", "process_execution_histories", "process_approval_decisions", "groups", "departments"} {
		_, err := f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
		var sequence *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&sequence))
		if sequence != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*sequence+" TO "+cfg.User)
			require.NoError(t, err)
		}
	}
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	ctx = service.WithBPMNAccessScope(ctx, service.BPMNAccessScope{TenantID: f.tenant.ID, UserID: f.actor.ID})
	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.actor.ID)
	engine := service.NewCustomProcessEngine(clients.Tenant, zap.NewNop().Sugar(), executionfixture.Standard()).(*service.CustomProcessEngine)
	engine.SetCallbackCandidateClient(clients.System)
	domain := service.NewIncidentService(clients.Tenant, zap.NewNop().Sugar(), executionfixture.Standard())
	domain.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
	engine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler).SetIncidentService(domain)
	kind := "serviceTask"
	if userTask {
		kind = "userTask"
	}
	xml := []byte(fmt.Sprintf(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="transaction" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:%s id="ack" name="Acknowledge"><bpmn:extensionElements><bpmn:metaData name="service_task_type">incident_task</bpmn:metaData><bpmn:metaData name="action">acknowledge_incident</bpmn:metaData></bpmn:extensionElements></bpmn:%s><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="ack"/><bpmn:sequenceFlow id="b" sourceRef="ack" targetRef="end"/></bpmn:process></bpmn:definitions>`, kind, kind))
	deployment := f.client.ProcessDeployment.Create().SetTenantID(f.tenant.ID).SetDeploymentID("transaction").SetDeploymentName("Transaction").SaveX(f.ctx)
	definition := f.client.ProcessDefinition.Create().SetTenantID(f.tenant.ID).SetDeploymentID(deployment.ID).SetKey("transaction").SetName("Transaction").SetBpmnXML(xml).SetIsActive(true).SaveX(f.ctx)
	return f, clients.Tenant, engine, ctx, definition.Key
}

func TestPostgresBPMNStartProcessTxVisibility(t *testing.T) {
	for _, finish := range []string{"rollback", "commit_failure", "database_commit_failure", "commit", "cancel_after_commit"} {
		t.Run(finish, func(t *testing.T) {
			f, client, engine, ctx, key := transactionBPMNFixture(t, false)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			if finish == "database_commit_failure" {
				_, err := f.db.ExecContext(f.ctx, "ALTER TABLE process_instances ADD CONSTRAINT transaction_test_definition UNIQUE (process_definition_id) DEFERRABLE INITIALLY DEFERRED")
				require.NoError(t, err)
			}
			tx, err := client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			require.NoError(t, err)
			defer tx.Rollback()
			instance, err := engine.StartProcessTx(ctx, tx, key, "incident:tx", "incident", f.inc.WorkItemID, map[string]interface{}{"version": 1})
			require.NoError(t, err)
			require.Equal(t, 1, tx.ProcessCallbackOutbox.Query().CountX(ctx))
			require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx), "separate connection cannot see uncommitted process")
			require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
			require.Equal(t, "new", f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Status)
			if finish == "database_commit_failure" {
				_, err := engine.StartProcessTx(ctx, tx, key, "incident:tx-second", "incident", f.inc.WorkItemID, map[string]interface{}{"version": 1})
				require.NoError(t, err, "deferred constraint allows statements until commit")
				require.ErrorContains(t, tx.Commit(), "transaction_test_definition")
				require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
				require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
				require.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.ctx))
				require.Equal(t, "new", f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Status)
				return
			}
			if finish == "rollback" || finish == "commit_failure" {
				if finish == "commit_failure" {
					forced := errors.New("owner refuses commit")
					tx.OnCommit(func(ent.Committer) ent.Committer {
						return ent.CommitFunc(func(context.Context, *ent.Tx) error { return forced })
					})
					require.ErrorIs(t, tx.Commit(), forced)
				}
				require.NoError(t, tx.Rollback())
				require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
				require.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.ctx))
				require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
				return
			}
			if finish == "cancel_after_commit" {
				tx.OnCommit(func(next ent.Committer) ent.Committer {
					return ent.CommitFunc(func(c context.Context, tx *ent.Tx) error { err := next.Commit(c, tx); cancel(); return err })
				})
			}
			require.NoError(t, tx.Commit())
			if finish == "cancel_after_commit" {
				require.Equal(t, "new", f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Status)
				require.Equal(t, "pending", f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx).Status)
				scoped := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
				_, err := engine.ProcessPendingCallbacks(scoped, "tx-restart", 10)
				require.NoError(t, err)
			}
			require.Equal(t, "acknowledged", f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Status)
			require.Equal(t, "completed", f.client.ProcessInstance.GetX(f.ctx, instance.ID).Status)
			require.Equal(t, "completed", f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx).Status)
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.OperationIDNotNil()).CountX(f.ctx))
		})
	}
}

func TestPostgresBPMNCompleteTaskTxEvidenceAndRetry(t *testing.T) {
	f, client, engine, ctx, key := transactionBPMNFixture(t, true)
	instance, err := engine.StartProcess(ctx, key, "incident:task", "incident", f.inc.WorkItemID, map[string]interface{}{"version": 1, "requester_id": f.actor.ID})
	require.NoError(t, err)
	task := f.client.ProcessTask.Query().Where(processtask.ProcessInstanceID(instance.ID)).OnlyX(f.ctx)
	f.client.ProcessTask.UpdateOneID(task.ID).SetAssignee(fmt.Sprint(f.actor.ID)).SaveX(f.ctx)
	variables := map[string]interface{}{"approvalAction": "approve", "approvalResult": "approved", "approvalComment": "native actor decision", "version": 1}
	for _, finish := range []string{"rollback", "commit_failure", "commit"} {
		t.Run(finish, func(t *testing.T) {
			tx, err := client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			require.NoError(t, err)
			defer tx.Rollback()
			require.NoError(t, engine.CompleteTaskTx(ctx, tx, task.TaskID, variables))
			require.Equal(t, "completed", tx.ProcessTask.GetX(ctx, task.ID).Status)
			require.Equal(t, f.actor.ID, tx.ProcessApprovalDecision.Query().OnlyX(ctx).ActorID)
			row := tx.ProcessCallbackOutbox.Query().OnlyX(ctx)
			require.Equal(t, f.actor.ID, row.ActorID)
			require.Equal(t, "workflow", row.ActorSource)
			require.Equal(t, 1, bpmn.GetIntFromVars(row.Variables, "version"))
			require.Equal(t, task.Status, f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
			require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
			require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
			if finish != "commit" {
				if finish == "commit_failure" {
					forced := errors.New("completion commit refused")
					tx.OnCommit(func(ent.Committer) ent.Committer {
						return ent.CommitFunc(func(context.Context, *ent.Tx) error { return forced })
					})
					require.ErrorIs(t, tx.Commit(), forced)
				}
				require.NoError(t, tx.Rollback())
				require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
				require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
				require.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.Action(service.AuditActionTaskCompleted)).CountX(f.ctx))
				return
			}
			failReceipt := true
			client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(c context.Context, mutation ent.Mutation) (ent.Value, error) {
					if failReceipt {
						return nil, errors.New("temporary receipt storage failure")
					}
					return next.Mutate(c, mutation)
				})
			})
			require.NoError(t, tx.Commit(), "a deferred callback failure is not a commit failure")
			pending := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
			require.Equal(t, "pending", pending.Status)
			require.NotEmpty(t, pending.LastErrorClass)
			require.Equal(t, "new", f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Status)
			require.Equal(t, "completed", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
			failReceipt = false
			f.client.ProcessCallbackOutbox.UpdateOneID(pending.ID).SetNextAttemptAt(time.Now().Add(-time.Minute)).SaveX(f.ctx)
			_, err = engine.ProcessPendingCallbacks(ctx, "completion-restart", 10)
			require.NoError(t, err)
			require.Equal(t, "acknowledged", f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Status)
			require.Equal(t, "completed", f.client.ProcessCallbackOutbox.GetX(f.ctx, pending.ID).Status)
			require.Equal(t, f.actor.ID, f.client.ProcessApprovalDecision.Query().OnlyX(f.ctx).ActorID)
			require.Equal(t, f.actor.ID, f.client.AuditLog.Query().Where(auditlog.OperationIDNotNil()).OnlyX(f.ctx).UserID)
			duplicate, err := client.Tx(ctx)
			require.NoError(t, err)
			require.Error(t, engine.CompleteTaskTx(ctx, duplicate, task.TaskID, variables))
			require.NoError(t, duplicate.Rollback())
			require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ExecutionKey(pending.ExecutionKey)).CountX(f.ctx))
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.OperationIDNotNil()).CountX(f.ctx))
		})
	}
}
