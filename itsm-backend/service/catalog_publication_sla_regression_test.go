package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent/enttest"
	"testing"
	"time"
)

func TestA5FixSLAStoredCalendar(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	row := client.SLADefinition.Create().SetTenantID(1).SetName("Working hours").SetBusinessHours(map[string]interface{}{"work_days": []interface{}{1, 2, 3, 4, 5}, "start_time": "09:00", "end_time": "18:00"}).SaveX(ctx)
	stored := client.SLADefinition.GetX(ctx, row.ID)
	require.IsType(t, json.Number("1"), stored.BusinessHours["work_days"].([]interface{})[0])
	start := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	deadline, err := NewTicketSLAService(client, zap.NewNop().Sugar()).calculateDeadlineWithBusinessHours(start, 60, stored.BusinessHours)
	require.NoError(t, err)
	require.Equal(t, start.Add(time.Hour), deadline)
}
func TestA5FixSLAStoredEscalation(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	row := client.SLADefinition.Create().SetTenantID(1).SetName("Escalation").SetEscalationRules(map[string]interface{}{"high": []interface{}{map[string]interface{}{"level": 1, "afterMinutes": 30, "notifyRoles": []interface{}{"manager"}}}}).SaveX(ctx)
	matrix := NewEscalationMatrixService(zap.NewNop().Sugar())
	matrix.SetClient(client)
	level, err := matrix.FindNextEscalationLevel(ctx, 1, "high", 30, 0, row.ID)
	require.NoError(t, err)
	require.NotNil(t, level)
	require.Equal(t, 1, level.Level)
	require.Equal(t, 30, level.AfterMinutes)
}

func TestA5FixSLARejectsInvalidPersistedConfiguration(t *testing.T) {
	for _, days := range []interface{}{[]interface{}{}, []interface{}{json.Number("0")}, []interface{}{json.Number("8")}, []interface{}{json.Number("1.25")}, []interface{}{json.Number("9223372036854775808")}, []interface{}{"1"}} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			ctx := context.Background()
			client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
			defer client.Close()
			client.Tenant.Create().SetName("SLA").SetCode("sla").SaveX(ctx)
			client.User.Create().SetTenantID(1).SetUsername("requester").SetName("Requester").SetEmail("requester@example.test").SetPasswordHash("unused").SaveX(ctx)
			sla := client.SLADefinition.Create().SetTenantID(1).SetName("Invalid").SetServiceType("incident").SetPriority("high").SetResponseTime(60).SetResolutionTime(120).SetBusinessHours(map[string]interface{}{"work_days": days}).SaveX(ctx)
			owner := NewTicketSLAService(client, zap.NewNop().Sugar())
			_, err := owner.CalculateSLADeadline(ctx, 1, "incident", "high", 0)
			require.Error(t, err)
			_, err = owner.CalculateSLADeadlineFromRequest(ctx, 1, "incident", "high", 0)
			require.Error(t, err)
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			item := tx.Ticket.Create().SetTenantID(1).SetRequesterID(1).SetTitle("Invalid calendar").SetTicketNumber("SLA-invalid").SaveX(ctx)
			require.Error(t, owner.ApplyCreationSLA(ctx, tx, item, &sla.ID))
			require.NoError(t, tx.Rollback())
			require.Zero(t, client.Ticket.Query().CountX(ctx))
		})
	}
	for _, raw := range []interface{}{json.Number("1.5"), json.Number("-1"), json.Number("9223372036854775808")} {
		for _, key := range []string{"level", "afterMinutes"} {
			values := map[string]interface{}{"level": json.Number("1"), "afterMinutes": json.Number("30")}
			values[key] = raw
			_, err := parseSLAEscalationMatrix(map[string]interface{}{"high": []interface{}{values}})
			require.Error(t, err)
		}
	}
	for _, calendar := range []map[string]interface{}{{"start_time": "25:00"}, {"start_time": "18:00", "end_time": "09:00"}} {
		_, err := parseBusinessHoursConfig(calendar)
		require.Error(t, err)
	}
	_, err := (businessHoursConfig{}).nextWorkDayStart(time.Now())
	require.Error(t, err)
}

func TestA5FixSLAEscalationJobRejectsInvalidStoredLevel(t *testing.T) {
	client, owner, ctx := setupEscalationTest(t)
	defer client.Close()
	tenant, err := createEscalationTestTenant(ctx, client, "invalid-level")
	require.NoError(t, err)
	user, err := createEscalationTestUser(ctx, client, tenant.ID, "invalid-level")
	require.NoError(t, err)
	sla := client.SLADefinition.Create().SetTenantID(tenant.ID).SetName("Invalid escalation").SetEscalationRules(map[string]interface{}{"high": []interface{}{map[string]interface{}{"level": json.Number("1.5"), "afterMinutes": 30}}}).SaveX(ctx)
	item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(user.ID).SetTitle("Escalation").SetTicketNumber("ESC-INVALID").SetPriority("high").SetSLADefinitionID(sla.ID).SaveX(ctx)
	rule := client.SLAAlertRule.Create().SetTenantID(tenant.ID).SetName("Rule").SetSLADefinitionID(sla.ID).SetEscalationEnabled(true).SaveX(ctx)
	alert := client.SLAAlertHistory.Create().SetTenantID(tenant.ID).SetTicketID(item.ID).SetTicketNumber(item.TicketNumber).SetTicketTitle(item.Title).SetAlertRuleID(rule.ID).SetAlertRuleName(rule.Name).SetCreatedAt(time.Now().Add(-40 * time.Minute)).SaveX(ctx)
	require.Error(t, owner.ProcessEscalations(ctx, tenant.ID))
	require.Zero(t, client.SLAAlertHistory.GetX(ctx, alert.ID).EscalationLevel)
	sla.Update().SetEscalationRules(map[string]interface{}{"high": []interface{}{map[string]interface{}{"level": json.Number("1"), "afterMinutes": json.Number("30")}}}).ExecX(ctx)
	require.NoError(t, owner.ProcessEscalations(ctx, tenant.ID))
	require.Equal(t, 1, client.SLAAlertHistory.GetX(ctx, alert.ID).EscalationLevel, "configured 30 minutes must be consumed instead of the default high threshold of 60")
}
