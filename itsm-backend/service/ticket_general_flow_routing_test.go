package service

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 通用工单流程的第一个用户任务「任务分配」过去没有声明任何处理人路由，落进引擎的
// requester_id 兜底分支，于是每张新工单的派单任务都被派给申请人自己，流程停在第一步
// 永远到不了审批网关（审批决策表因此一条记录都没有）。这个断言把"必须显式声明路由"
// 钉在定义上：删掉 assigneeTeamId 就会退回静默兜底。
func TestTicketGeneralFlowAssignTaskDeclaresFixedScopeRouting(t *testing.T) {
	task := parseEmbeddedUserTask(t, "ticket_general_flow.bpmn", "Activity_Assign")

	require.NotZero(t, task.AssigneeTeamId, "任务分配必须声明固定范围组织路由，否则引擎会静默回落到申请人")
	assert.Equal(t, "", task.AssigneeSource, "派单发生在工单被分配之前，不能用 work_item_assignee 自锁")
	assert.Equal(t, "", task.CandidateGroups, "固定范围路由与候选组是两种互斥的声明方式")

	// 声明的 teamId 必须能真正解析出人：publication 校验会因为负责人缺失而拒绝这个定义，
	// 运行期则是"任务无人可领"。这里断言声明确实被 fixedScopeApproverSources 消费，
	// 即该声明不是摆设。
	sources := fixedScopeApproverSources(task, 1)
	require.Len(t, sources, 1, "assigneeTeamId 必须映射到一个 resolver")
	assert.Equal(t, task.AssigneeTeamId, sources[0].context.TeamID)
}

func parseEmbeddedUserTask(t *testing.T, filename, taskID string) *BPMNUserTask {
	t.Helper()
	data, err := bpmnTemplates.ReadFile(filepath.Join("bpmn", filename))
	require.NoError(t, err)

	parsed, err := NewBPMNParser().ParseXML(data)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Processes)

	for i := range parsed.Processes[0].UserTasks {
		if parsed.Processes[0].UserTasks[i].ID == taskID {
			return parsed.Processes[0].UserTasks[i]
		}
	}
	require.Failf(t, "user task not found", "%s 中不存在 %s", filename, taskID)
	return nil
}

// 非审批任务声明固定范围组织路由时必须按声明解析——过去这段声明只被 taskPurpose="approval"
// 分支消费，非审批任务声明了也一样落进 requester 兜底，等于把任务静默派给申请人，
// 而 publication 校验却把同一个声明当作有效路由放行（fail-open 的错误派单）。
func TestFulfillmentTaskFixedScopeRoutingResolvesTeamLeader(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	leader := fx.createUser(t, "dispatchLeader", 0)
	team := fx.createTeam(t, "服务台-L1", leader.ID)
	requester := fx.createUser(t, "dispatchRequester", 0)

	instance := fx.createInstance(t, "fulfillment-fixed-scope", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	// 与 ticket_general_flow 的 Activity_Assign 同样形状：没有 taskPurpose、没有候选路由，
	// 只声明 assigneeTeamId。
	task := &BPMNUserTask{ID: "Activity_Assign", Name: "任务分配", AssigneeTeamId: team.ID}
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, task))

	created := fx.getCreatedTask(t, instance.ID, "Activity_Assign")
	assert.Equal(t, strconv.Itoa(leader.ID), created.Assignee, "必须按声明的团队负责人指派，而不是回落到申请人")
	assert.NotEqual(t, strconv.Itoa(requester.ID), created.Assignee)
}

// 声明的团队没有负责人时不能悄悄改派给申请人：任务保持无人可领，等待人工派单。
// 这正是 AGENTS.md 的 fail-closed 要求——不支持的组合必须留下可见的阻塞状态。
func TestFulfillmentTaskFixedScopeRoutingWithoutLeaderStaysUnassigned(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	team := fx.createTeam(t, "无负责人团队", 0)
	requester := fx.createUser(t, "unroutedRequester", 0)

	instance := fx.createInstance(t, "fulfillment-fixed-scope-no-leader", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := &BPMNUserTask{ID: "Activity_Assign", Name: "任务分配", AssigneeTeamId: team.ID}
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, task))

	created := fx.getCreatedTask(t, instance.ID, "Activity_Assign")
	assert.Equal(t, "", created.Assignee, "团队无负责人时不应把任务派给申请人")
}

// 完全没有声明路由的任务仍然沿用既有的 requester 兜底（本次不改变其顺序，避免影响其它流程），
// 但兜底必须可观测——引擎会打一条 warning，见 bpmn_process_engine.go 的
// "用户任务未声明处理人路由，按申请人兜底"。
func TestFulfillmentTaskWithoutRoutingKeepsRequesterFallback(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	requester := fx.createUser(t, "legacyRequester", 0)
	instance := fx.createInstance(t, "fulfillment-unrouted", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := &BPMNUserTask{ID: "Activity_Handle", Name: "工单处理"}
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, task))

	created := fx.getCreatedTask(t, instance.ID, "Activity_Handle")
	assert.Equal(t, strconv.Itoa(requester.ID), created.Assignee)
}
