//go:build integration_postgres

package integration

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/common/accessgrant"
	"itsm-backend/migration"
	"testing"
	"time"
)

func TestPostgresAccessPolicyResultContract(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	sql := migration.GetMigrationSQL("030_catalog_access_policy_result")
	_, err := f.db.ExecContext(f.ctx, sql)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, sql)
	require.NoError(t, err, "migration reentry")
	c, ctx := f.client, f.ctx
	catalog := c.ServiceCatalog.Create().SetTenantID(f.tenant.ID).SetName("Finite access").SetTargetClass("service_request_item").SaveX(ctx)
	policy := c.CatalogAccessPolicy.Create().SetCatalogID(catalog.ID).SetProvider("graph").SetExternalSystem("owned-directory").SetGroupID("owned-group").SetDurationField("duration").SetDurationOptions([]accessgrant.DurationOption{{Key: "month", Label: "一个月", Seconds: 2592000}}).SaveX(ctx)
	_, err = c.CatalogAccessPolicy.Create().SetCatalogID(catalog.ID).SetProvider("graph").SetExternalSystem("other").SetGroupID("other").SetDurationField("duration").SetDurationOptions([]accessgrant.DurationOption{{Key: "month", Label: "一个月", Seconds: 2592000}}).Save(ctx)
	require.Error(t, err, "unique Catalog policy")
	item := c.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetTitle("Finite access").SetDescription("test").SetTicketNumber("ACCESS-1").SetRecordClass("service_request_item").SaveX(ctx)
	c.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(catalog.ID).SaveX(ctx)
	c.ExternalIdentity.Create().SetTenantID(f.tenant.ID).SetUserID(f.actor.ID).SetProvider("graph").SetWorkspace("owned-directory").SetSubject("owned-subject").SaveX(ctx)
	snapshot := c.ServiceRequestAccessSnapshot.Create().SetWorkItemID(item.ID).SetPolicyID(policy.ID).SetPolicyVersion(1).SetProvider("graph").SetExternalSystem("owned-directory").SetSubjectID("owned-subject").SetGroupID("owned-group").SetDurationKey("month").SetDurationSeconds(2592000).SaveX(ctx)
	_, err = f.db.ExecContext(ctx, `INSERT INTO service_request_access_snapshots(policy_version,provider,external_system,subject_id,group_id,duration_key,duration_seconds,work_item_id,policy_id) SELECT policy_version,provider,external_system,subject_id,group_id,duration_key,duration_seconds,work_item_id,policy_id FROM service_request_access_snapshots WHERE id=$1`, snapshot.ID)
	require.Error(t, err, "one immutable snapshot per WorkItem")
	_, err = f.db.ExecContext(ctx, `UPDATE service_request_access_snapshots SET duration_seconds=3600 WHERE id=$1`, snapshot.ID)
	require.ErrorContains(t, err, "immutable")
	dep := c.ProcessDeployment.Create().SetTenantID(f.tenant.ID).SetDeploymentID("access-dep").SetDeploymentName("Access").SaveX(ctx)
	def := c.ProcessDefinition.Create().SetTenantID(f.tenant.ID).SetDeploymentID(dep.ID).SetKey("access").SetName("Access").SetBpmnXML([]byte(`<definitions/>`)).SaveX(ctx)
	inst := c.ProcessInstance.Create().SetTenantID(f.tenant.ID).SetProcessDefinitionID(def.ID).SetProcessDefinitionKey("access").SetProcessInstanceID("access-inst").SetBusinessType("service_request").SetBusinessID(item.ID).SaveX(ctx)
	task := c.ProcessTask.Create().SetTenantID(f.tenant.ID).SetProcessInstanceID(inst.ID).SetProcessDefinitionKey("access").SetTaskDefinitionKey("grant").SetTaskName("Grant").SetTaskID("access-task").SetTaskType("kaf_delegate").SetStatus("delegated").SetCallbackAction(accessgrant.Capability).SetCallbackConfigRef(fmt.Sprint(policy.ID)).SaveX(ctx)
	verified := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	makeResult := c.ServiceRequestAccessResult.Create().SetWorkItemID(item.ID).SetProcessTaskID(task.ID).SetOutcome("granted").SetProvider("graph").SetSubjectID("owned-subject").SetGroupID("owned-group").SetBaseline("not_member").SetVerifiedAt(verified).SetExpiresAt(verified.Add(time.Hour)).SetEvidenceRef("evidence")
	_, err = makeResult.Save(ctx)
	require.ErrorContains(t, err, "approved snapshot")
	result := c.ServiceRequestAccessResult.Create().SetWorkItemID(item.ID).SetProcessTaskID(task.ID).SetOutcome("granted").SetProvider("graph").SetSubjectID("owned-subject").SetGroupID("owned-group").SetBaseline("not_member").SetVerifiedAt(verified).SetExpiresAt(verified.Add(30 * 24 * time.Hour)).SetEvidenceRef("evidence").SaveX(ctx)
	_, err = f.db.ExecContext(ctx, `UPDATE service_request_access_results SET verified_at=now() WHERE id=$1`, result.ID)
	require.ErrorContains(t, err, "immutable")
	_, err = f.db.ExecContext(ctx, `INSERT INTO service_request_access_results(outcome,provider,subject_id,group_id,baseline,verified_at,expires_at,evidence_ref,work_item_id,process_task_id) SELECT outcome,provider,subject_id,group_id,baseline,verified_at,expires_at,evidence_ref,work_item_id,process_task_id FROM service_request_access_results WHERE id=$1`, result.ID)
	require.Error(t, err, "replay cannot insert another result")
	var expiry time.Time
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT expires_at FROM service_request_access_results WHERE id=$1`, result.ID).Scan(&expiry))
	require.Equal(t, verified.Add(30*24*time.Hour), expiry)
	_, err = f.db.ExecContext(ctx, `UPDATE catalog_access_policies SET duration_options='[{"key":"forever","label":"Forever","seconds":0}]' WHERE id=$1`, policy.ID)
	require.Error(t, err)
	// Runtime reads use a non-owner, NOSUPERUSER/NOBYPASSRLS role, not the fixture owner.
	role := fmt.Sprintf("access030_%d", time.Now().UnixNano())
	var schema string
	require.NoError(t, f.db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema))
	_, err = f.db.ExecContext(ctx, "CREATE ROLE "+role+" NOLOGIN NOSUPERUSER NOBYPASSRLS; GRANT USAGE ON SCHEMA "+schema+" TO "+role+"; GRANT SELECT,INSERT,UPDATE ON ALL TABLES IN SCHEMA "+schema+" TO "+role+"; GRANT USAGE ON ALL SEQUENCES IN SCHEMA "+schema+" TO "+role)
	require.NoError(t, err)
	defer func() {
		_, err := f.db.ExecContext(ctx, "DROP OWNED BY "+role+"; DROP ROLE "+role)
		require.NoError(t, err)
	}()
	for _, tenantID := range []int{f.tenant.ID, f.tenant.ID + 1, 0} {
		tx, err := f.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, "SET LOCAL ROLE "+role)
		require.NoError(t, err)
		guc := ""
		if tenantID > 0 {
			guc = fmt.Sprint(tenantID)
		}
		_, err = tx.ExecContext(ctx, "SELECT set_config('app.current_tenant',$1,true)", guc)
		require.NoError(t, err)
		for _, table := range []string{"catalog_access_policies", "service_request_access_snapshots", "service_request_access_results"} {
			var count int
			require.NoError(t, tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count))
			if tenantID == f.tenant.ID {
				require.Equal(t, 1, count)
			} else {
				require.Zero(t, count)
			}
		}
		if tenantID != f.tenant.ID {
			_, err = tx.ExecContext(ctx, `INSERT INTO service_request_access_results(outcome,provider,subject_id,group_id,baseline,verified_at,evidence_ref,work_item_id,process_task_id) VALUES('already_present','graph','owned-subject','owned-group','member',now(),'foreign',$1,$2)`, item.ID, task.ID)
			require.Error(t, err)
		}
		require.NoError(t, tx.Rollback())
	}
	// A failed professional snapshot creation rolls the WorkItem/extension back.
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	failedItem := tx.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetTitle("Rollback").SetDescription("test").SetTicketNumber("ACCESS-ROLLBACK").SetRecordClass("service_request_item").SaveX(ctx)
	tx.ServiceRequest.Create().SetTicketID(failedItem.ID).SetCatalogID(catalog.ID).SaveX(ctx)
	_, err = tx.ServiceRequestAccessSnapshot.Create().SetWorkItemID(failedItem.ID).SetPolicyID(policy.ID).SetPolicyVersion(1).SetProvider("graph").SetExternalSystem("owned-directory").SetSubjectID("untrusted-subject").SetGroupID("owned-group").SetDurationKey("month").SetDurationSeconds(2592000).Save(ctx)
	require.Error(t, err)
	require.NoError(t, tx.Rollback())
	require.False(t, c.Ticket.Query().Where(ticket.IDEQ(failedItem.ID)).ExistX(ctx))

}
