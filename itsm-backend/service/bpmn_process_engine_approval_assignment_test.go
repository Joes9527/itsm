package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// ==================== 测试夹具 ====================

type approvalAssignmentFixture struct {
	ctx    context.Context
	engine *CustomProcessEngine
	client *ent.Client
	tenant *ent.Tenant
	def    *ent.ProcessDefinition
}

func newApprovalAssignmentFixture(t *testing.T) *approvalAssignmentFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:approval_assignment_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	logger := zaptest.NewLogger(t).Sugar()
	engineIface := NewCustomProcessEngine(client, logger)
	engine, ok := engineIface.(*CustomProcessEngine)
	require.True(t, ok, "expected ProcessEngine to be *CustomProcessEngine")

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Approval Assignment Tenant").
		SetCode("aa-tenant").
		SetDomain("aa.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("DEP-aa").
		SetDeploymentName("Approval Assignment Deployment").
		SetDeploymentTime(time.Now()).
		SetDeployedBy("test").
		SetIsActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	def, err := client.ProcessDefinition.Create().
		SetKey("approval_assignment_test").
		SetName("Approval Assignment Test").
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(time.Now()).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	return &approvalAssignmentFixture{ctx: ctx, engine: engine, client: client, tenant: tenant, def: def}
}

// createUserInTenant 在给定租户下建一个用户；deptID <= 0 表示不设置部门。
func (f *approvalAssignmentFixture) createUserInTenant(t *testing.T, tenantID int, username string, deptID int) *ent.User {
	t.Helper()
	q := f.client.User.Create().
		SetUsername(username).
		SetEmail(username + "@aa.example.com").
		SetName(username).
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(tenantID)
	if deptID > 0 {
		q = q.SetDepartmentID(deptID)
	}
	u, err := q.Save(f.ctx)
	require.NoError(t, err)
	return u
}

func (f *approvalAssignmentFixture) createUser(t *testing.T, username string, deptID int) *ent.User {
	return f.createUserInTenant(t, f.tenant.ID, username, deptID)
}

// createDepartmentInTenant 在给定租户下建一个部门；managerID/parentID <= 0 表示不设置。
func (f *approvalAssignmentFixture) createDepartmentInTenant(t *testing.T, tenantID int, name string, managerID, parentID int) *ent.Department {
	t.Helper()
	q := f.client.Department.Create().
		SetName(name).
		SetCode(name).
		SetTenantID(tenantID)
	if managerID > 0 {
		q = q.SetManagerID(managerID)
	}
	if parentID > 0 {
		q = q.SetParentID(parentID)
	}
	d, err := q.Save(f.ctx)
	require.NoError(t, err)
	return d
}

func (f *approvalAssignmentFixture) createDepartment(t *testing.T, name string, managerID, parentID int) *ent.Department {
	return f.createDepartmentInTenant(t, f.tenant.ID, name, managerID, parentID)
}

func (f *approvalAssignmentFixture) createGroup(t *testing.T, name string, memberIDs ...int) *ent.Group {
	t.Helper()
	g, err := f.client.Group.Create().
		SetName(name).
		SetTenantID(f.tenant.ID).
		AddMemberIDs(memberIDs...).
		Save(f.ctx)
	require.NoError(t, err)
	return g
}

func (f *approvalAssignmentFixture) createInstance(t *testing.T, keySuffix string, variables map[string]interface{}) *ent.ProcessInstance {
	t.Helper()
	inst, err := f.client.ProcessInstance.Create().
		SetProcessInstanceID("PI-aa-" + keySuffix).
		SetProcessDefinitionKey(f.def.Key).
		SetProcessDefinitionID(f.def.ID).
		SetStatus("running").
		SetVariables(variables).
		SetTenantID(f.tenant.ID).
		Save(f.ctx)
	require.NoError(t, err)
	return inst
}

func approvalTask(id, name string) *BPMNUserTask {
	return &BPMNUserTask{ID: id, Name: name, TaskPurpose: "approval"}
}

// getCreatedTask 按 taskDefinitionKey 取回刚创建的 ProcessTask，方便断言 Assignee/CandidateUsers。
func (f *approvalAssignmentFixture) getCreatedTask(t *testing.T, instanceID int, taskDefinitionKey string) *ent.ProcessTask {
	t.Helper()
	task, err := f.client.ProcessTask.Query().
		Where(processtask.ProcessInstanceIDEQ(instanceID), processtask.TaskDefinitionKeyEQ(taskDefinitionKey)).
		Only(f.ctx)
	require.NoError(t, err)
	return task
}

// ==================== 主路径：部门负责人 ====================

func TestCreateUserTask_Approval_ManagerPath_AssignsDeptManager(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	manager := fx.createUser(t, "manager1", 0)
	dept := fx.createDepartment(t, "Engineering", manager.ID, 0)
	requester := fx.createUser(t, "requester1", dept.ID)

	instance := fx.createInstance(t, "manager-path", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(manager.ID), task.Assignee)
}

func TestCreateUserTask_Approval_ManagerPath_ParentDepartmentFallback(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	manager := fx.createUser(t, "manager2", 0)
	parentDept := fx.createDepartment(t, "Parent", manager.ID, 0)
	childDept := fx.createDepartment(t, "Child", 0, parentDept.ID)
	requester := fx.createUser(t, "requester2", childDept.ID)

	instance := fx.createInstance(t, "parent-fallback", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(manager.ID), task.Assignee, "子部门没有 manager 时应该递归查父部门")
}

// ==================== 兜底路径：部门未配置 manager ====================

func TestCreateUserTask_Approval_FallbackPath_NoManager_UsesCandidateGroup(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept", 0, 0)
	requester := fx.createUser(t, "requester3", dept.ID)
	approverA := fx.createUser(t, "approverA", 0)
	approverB := fx.createUser(t, "approverB", 0)
	fx.createGroup(t, "ticket-approvers", approverA.ID, approverB.ID)

	instance := fx.createInstance(t, "no-manager", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", task.Assignee, "部门没配 manager 时不应该直接指派给任何人")
	assert.Contains(t, task.CandidateUsers, "approverA")
	assert.Contains(t, task.CandidateUsers, "approverB")
}

// ==================== 核心安全断言：两条路径都要排除申请人自己 ====================

func TestCreateUserTask_Approval_ManagerPath_SkipsWhenManagerIsRequester(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	// 申请人自己就是这个部门的 manager
	dept := fx.createDepartment(t, "Self Managed", 0, 0)
	requester := fx.createUser(t, "requester4", dept.ID)
	_, err := fx.client.Department.UpdateOne(dept).SetManagerID(requester.ID).Save(fx.ctx)
	require.NoError(t, err)

	backupApprover := fx.createUser(t, "backupApprover", 0)
	fx.createGroup(t, "ticket-approvers", requester.ID, backupApprover.ID)

	instance := fx.createInstance(t, "manager-is-requester", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(requester.ID), task.Assignee, "部门负责人是申请人自己时不能把 assignee 定成申请人")
	assert.Equal(t, "", task.Assignee, "应该转入候选组兜底，而不是留一个错误的 assignee")
}

func TestCreateUserTask_Approval_FallbackPath_ExcludesRequesterFromCandidateGroup(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 2", 0, 0)
	requester := fx.createUser(t, "requester5", dept.ID)
	backupApprover := fx.createUser(t, "backupApprover2", 0)
	// requester 自己也在 ticket-approvers 组里——现实中常见场景（普通审批人自己也会提工单）
	fx.createGroup(t, "ticket-approvers", requester.ID, backupApprover.ID)

	instance := fx.createInstance(t, "exclude-requester", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotContains(t, task.CandidateUsers, "requester5", "candidate_users 不能包含申请人的用户名")
	assert.NotContains(t, task.CandidateUsers, strconv.Itoa(requester.ID), "candidate_users 不能包含申请人的 ID")
	assert.Contains(t, task.CandidateUsers, "backupApprover2")
}

func TestCreateUserTask_Approval_FallbackPath_EmptyAfterExclusion_NoOrphanRequester(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 3", 0, 0)
	requester := fx.createUser(t, "requester6", dept.ID)
	// ticket-approvers 组里只有申请人自己——排除之后应该是空，而不是静默把申请人塞回去
	fx.createGroup(t, "ticket-approvers", requester.ID)

	instance := fx.createInstance(t, "empty-after-exclusion", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", task.Assignee)
	assert.Equal(t, "", task.CandidateUsers, "候选组排除申请人后为空时，不能兜底又把申请人塞回去")
}

// ==================== 跳过范围：assignee_id 变量对 approval 任务完全不生效 ====================

func TestCreateUserTask_Approval_IgnoresAssigneeIDVariable(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 4", 0, 0)
	requester := fx.createUser(t, "requester7", dept.ID)
	scriptedAssignee := fx.createUser(t, "scriptedAssignee", 0)

	instance := fx.createInstance(t, "ignore-assignee-id", map[string]interface{}{
		"requester_id": float64(requester.ID),
		"assignee_id":  float64(scriptedAssignee.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(scriptedAssignee.ID), task.Assignee, "approval 任务不应该读 assignee_id 流程变量")
}

// ==================== BPMN 显式声明 candidateGroups 时，不触发部门负责人解析 ====================
// 覆盖 legacy 审批链迁移出来的按角色/组路由节点（service/legacy_approval_migration_service.go
// 生成的 BPMN 也带 taskPurpose="approval"，但候选人来自迁移前配置的具体角色/组，不该被这次
// 修复新增的"部门负责人"语义覆盖）。

func TestCreateUserTask_Approval_ExplicitCandidateGroupsSkipsManagerPath(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	manager := fx.createUser(t, "manager3", 0)
	dept := fx.createDepartment(t, "Has Manager", manager.ID, 0)
	requester := fx.createUser(t, "requester8", dept.ID)
	legacyApprover := fx.createUser(t, "legacyApprover", 0)
	fx.createGroup(t, "legacy-role-approvers", legacyApprover.ID)

	instance := fx.createInstance(t, "explicit-candidate-groups", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.CandidateGroups = "legacy-role-approvers"

	err := fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", created.Assignee, "BPMN 显式声明 candidateGroups 时不应该触发部门负责人解析")
	assert.Contains(t, created.CandidateUsers, "legacyApprover")
	assert.NotContains(t, created.CandidateUsers, strconv.Itoa(manager.ID), "不应该混入跟这个节点配置无关的部门负责人")
}

// ==================== 跨租户隔离回归 ====================

func TestCreateUserTask_Approval_TenantIsolation_DepartmentNotVisibleAcrossTenants(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	otherTenant, err := fx.client.Tenant.Create().
		SetName("Other Tenant").
		SetCode("other-tenant").
		SetDomain("other.example.com").
		SetStatus("active").
		Save(fx.ctx)
	require.NoError(t, err)

	// 在另一个租户建一个有 manager 的部门
	otherManager := fx.createUserInTenant(t, otherTenant.ID, "otherManager", 0)
	otherDept := fx.createDepartmentInTenant(t, otherTenant.ID, "Other Dept", otherManager.ID, 0)

	// 本租户的申请人 department_id 误配置成了另一个租户的部门 ID（模拟数据错误场景）
	requester := fx.createUser(t, "requester9", otherDept.ID)
	backupApprover := fx.createUser(t, "backupApprover3", 0)
	fx.createGroup(t, "ticket-approvers", backupApprover.ID)

	instance := fx.createInstance(t, "tenant-isolation", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批"))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(otherManager.ID), task.Assignee, "不能跨租户解析到另一个租户的部门负责人")
	assert.Equal(t, "", task.Assignee, "部门在本租户查不到时应该转候选组兜底")
	assert.Contains(t, task.CandidateUsers, "backupApprover3")
}

// ==================== 回归：非 approval 任务的自动分配链不受影响 ====================

func TestCreateUserTask_NonApproval_StillUsesRequesterIDFallback(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	requester := fx.createUser(t, "requester10", 0)
	instance := fx.createInstance(t, "non-approval", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	// TaskPurpose 留空，模拟普通工作任务（比如"任务分配"节点）
	err := fx.engine.createUserTask(fx.ctx, instance, &BPMNUserTask{ID: "Activity_Handle", Name: "工单处理"})
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Handle")
	assert.Equal(t, strconv.Itoa(requester.ID), task.Assignee, "非 approval 任务应该继续走原来的 requester_id 兜底链")
}

// ==================== authorizeTaskActor 集成：申请人不能操作自己的审批任务 ====================

func TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask_ManagerPath(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Self Managed 2", 0, 0)
	requester := fx.createUser(t, "requester11", dept.ID)
	_, err := fx.client.Department.UpdateOne(dept).SetManagerID(requester.ID).Save(fx.ctx)
	require.NoError(t, err)
	backupApprover := fx.createUser(t, "backupApprover4", 0)
	fx.createGroup(t, "ticket-approvers", requester.ID, backupApprover.ID)

	instance := fx.createInstance(t, "authz-manager-path", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批")))
	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")

	actorCtx := context.WithValue(fx.ctx, bpmn.BPMNUserIDContextKey, requester.ID)
	err = fx.engine.authorizeTaskActor(actorCtx, task)
	assert.Error(t, err, "申请人不应该能操作自己提交工单的审批任务")
}

func TestAuthorizeTaskActor_RequesterCannotActOnOwnApprovalTask_CandidateGroupPath(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 5", 0, 0)
	requester := fx.createUser(t, "requester12", dept.ID)
	backupApprover := fx.createUser(t, "backupApprover5", 0)
	fx.createGroup(t, "ticket-approvers", requester.ID, backupApprover.ID)

	instance := fx.createInstance(t, "authz-candidate-path", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批")))
	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")

	actorCtx := context.WithValue(fx.ctx, bpmn.BPMNUserIDContextKey, requester.ID)
	err := fx.engine.authorizeTaskActor(actorCtx, task)
	assert.Error(t, err, "申请人不应该能通过候选组身份操作自己提交工单的审批任务")
}

// ==================== Finding 2：BPMN 显式声明 candidateUsers（无 candidateGroups）时，
// 也不应该触发部门负责人解析 ====================

func TestCreateUserTask_Approval_ExplicitCandidateUsersSkipsManagerPath(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	manager := fx.createUser(t, "manager4", 0)
	dept := fx.createDepartment(t, "Has Manager 2", manager.ID, 0)
	requester := fx.createUser(t, "requester13", dept.ID)
	designatedApprover := fx.createUser(t, "designatedApprover", 0)

	instance := fx.createInstance(t, "explicit-candidate-users", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	// 流程设计器画的审批节点：只设置了 candidateUsers，没有 candidateGroups。
	task.CandidateUsers = strconv.Itoa(designatedApprover.ID)

	err := fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", created.Assignee, "BPMN 显式声明 candidateUsers 时不应该触发部门负责人解析")
	assert.Contains(t, created.CandidateUsers, strconv.Itoa(designatedApprover.ID), "显式声明的 candidateUsers 应该原样保留")
	assert.NotContains(t, created.CandidateUsers, strconv.Itoa(manager.ID), "不应该混入跟这个节点配置无关的部门负责人")
}

// ==================== Finding 1：ClaimTask/ClaimTaskByID 必须校验候选人身份 ====================

func TestClaimTaskByID_RequesterCannotClaimOwnCandidateGroupFallbackTask(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 6", 0, 0)
	requester := fx.createUser(t, "requester14", dept.ID)
	backupApprover := fx.createUser(t, "backupApprover6", 0)
	// requester 不在 ticket-approvers 组里，走候选组兜底后应被排除在候选人之外。
	fx.createGroup(t, "ticket-approvers", backupApprover.ID)

	instance := fx.createInstance(t, "claim-requester-excluded", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批")))
	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	require.Equal(t, "", task.Assignee)
	require.NotContains(t, task.CandidateUsers, "requester14")

	err := fx.engine.TaskService().ClaimTaskByID(fx.ctx, task.ID, requester.ID)
	assert.Error(t, err, "申请人不是候选人，不应该能认领落到候选组兜底的审批任务")
}

func TestClaimTaskByID_RealCandidateCanClaim(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 7", 0, 0)
	requester := fx.createUser(t, "requester15", dept.ID)
	backupApprover := fx.createUser(t, "backupApprover7", 0)
	fx.createGroup(t, "ticket-approvers", backupApprover.ID)

	instance := fx.createInstance(t, "claim-real-candidate", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批")))
	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	require.Contains(t, task.CandidateUsers, "backupApprover7")

	err := fx.engine.TaskService().ClaimTaskByID(fx.ctx, task.ID, backupApprover.ID)
	require.NoError(t, err, "真正在候选组里的人应该能成功认领")

	claimed := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(backupApprover.ID), claimed.Assignee)
}

// ==================== 纯函数单元测试 ====================

func TestExcludeUserFromCandidates(t *testing.T) {
	u := &ent.User{ID: 42, Username: "alice", Email: "alice@example.com"}

	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"匹配用户名", []string{"alice", "bob"}, []string{"bob"}},
		{"匹配 ID 字符串", []string{"42", "bob"}, []string{"bob"}},
		{"匹配 Email", []string{"alice@example.com", "bob"}, []string{"bob"}},
		{"不匹配则原样保留", []string{"bob", "carol"}, []string{"bob", "carol"}},
		{"空输入", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := excludeUserFromCandidates(tt.input, u)
			assert.Equal(t, tt.want, got)
		})
	}

	assert.Equal(t, []string{"a"}, excludeUserFromCandidates([]string{"a"}, nil), "nil user 时原样返回")
}
