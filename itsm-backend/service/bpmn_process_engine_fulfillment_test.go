package service

import (
	"strconv"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fulfillmentTask(id, name, teamCode string) *BPMNUserTask {
	return &BPMNUserTask{ID: id, Name: name, TaskPurpose: "fulfillment", FulfillmentTeamCode: teamCode}
}

func (f *approvalAssignmentFixture) createOpenTicket(t *testing.T, requesterID int) *ent.Ticket {
	t.Helper()
	tk, err := f.client.Ticket.Create().
		SetTitle("待执行工单").SetDescription("x").SetPriority("medium").SetStatus("open").
		SetTicketNumber("FF-" + strconv.Itoa(requesterID)).
		SetRequesterID(requesterID).SetTenantID(f.tenant.ID).
		Save(f.ctx)
	require.NoError(t, err)
	return tk
}

func TestCreateUserTask_Fulfillment_AssignsLeastBusyTeamMemberAndSyncsTicket(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	team, err := fx.client.Team.Create().SetName("服务台-L1").SetCode("服务台-l1").SetTenantID(fx.tenant.ID).Save(fx.ctx)
	require.NoError(t, err)
	idle, err := fx.client.User.Create().
		SetUsername("l1_idle").SetEmail("l1_idle@example.com").SetName("l1_idle").
		SetPasswordHash("hash").SetActive(true).SetTenantID(fx.tenant.ID).
		Save(fx.ctx)
	require.NoError(t, err)
	_, err = fx.client.Team.UpdateOneID(team.ID).AddUserIDs(idle.ID).Save(fx.ctx)
	require.NoError(t, err)

	requester := fx.createUser(t, "ff_requester", 0)
	tk := fx.createOpenTicket(t, requester.ID)

	instance := fx.createInstance(t, "fulfillment-path", map[string]interface{}{
		"requester_id": float64(requester.ID),
		"business_id":  float64(tk.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, fulfillmentTask("Activity_Execute", "执行服务", "服务台-l1"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Execute")
	assert.Equal(t, strconv.Itoa(idle.ID), task.Assignee)

	updated, err := fx.client.Ticket.Get(fx.ctx, tk.ID)
	require.NoError(t, err)
	assert.Equal(t, idle.ID, updated.AssigneeID, "fulfillment 解析成功后必须同步回写 ticket.assignee_id")
	assert.Equal(t, common.TicketStatusAssigned, updated.Status)
}

func TestCreateUserTask_Fulfillment_EmptyTeam_LeavesTicketUnassigned(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	_, err := fx.client.Team.Create().SetName("服务台-L1").SetCode("服务台-l1").SetTenantID(fx.tenant.ID).Save(fx.ctx)
	require.NoError(t, err)

	requester := fx.createUser(t, "ff_requester2", 0)
	tk := fx.createOpenTicket(t, requester.ID)

	instance := fx.createInstance(t, "fulfillment-empty", map[string]interface{}{
		"requester_id": float64(requester.ID),
		"business_id":  float64(tk.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, fulfillmentTask("Activity_Execute", "执行服务", "服务台-l1"))
	require.NoError(t, err, "候选池为空不应该让节点创建本身失败")

	task := fx.getCreatedTask(t, instance.ID, "Activity_Execute")
	assert.Equal(t, "", task.Assignee)

	updated, err := fx.client.Ticket.Get(fx.ctx, tk.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, updated.AssigneeID, "候选池为空时不应该误写 ticket.assignee_id")
}

func TestCreateUserTask_Fulfillment_NoTeamCodeDeclared_UsesDefault(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	team, err := fx.client.Team.Create().
		SetName("服务台-L1").SetCode(defaultFulfillmentTeamCode).SetTenantID(fx.tenant.ID).Save(fx.ctx)
	require.NoError(t, err)
	member, err := fx.client.User.Create().
		SetUsername("default_team_member").SetEmail("dtm@example.com").SetName("dtm").
		SetPasswordHash("hash").SetActive(true).SetTenantID(fx.tenant.ID).
		Save(fx.ctx)
	require.NoError(t, err)
	_, err = fx.client.Team.UpdateOneID(team.ID).AddUserIDs(member.ID).Save(fx.ctx)
	require.NoError(t, err)

	requester := fx.createUser(t, "ff_requester3", 0)
	tk := fx.createOpenTicket(t, requester.ID)

	instance := fx.createInstance(t, "fulfillment-default", map[string]interface{}{
		"requester_id": float64(requester.ID),
		"business_id":  float64(tk.ID),
	})

	// FulfillmentTeamCode 留空，验证 fallback 到 defaultFulfillmentTeamCode 常量。
	err = fx.engine.createUserTask(fx.ctx, instance, fulfillmentTask("Activity_Execute", "执行服务", ""))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Execute")
	assert.Equal(t, strconv.Itoa(member.ID), task.Assignee)
}
