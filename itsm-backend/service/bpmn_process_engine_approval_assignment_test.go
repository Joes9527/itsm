package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processauditlog"
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

	// authorizeTaskViewer (called internally via GetTaskByID) now denies by
	// default when the context carries no authenticated caller — match
	// production's getBPMNTenantContext behavior by declaring the claiming
	// user's identity on the context, not just as the ClaimTaskByID param.
	claimCtx := context.WithValue(fx.ctx, bpmn.BPMNUserIDContextKey, backupApprover.ID)
	claimCtx = context.WithValue(claimCtx, bpmn.BPMNTenantIDContextKey, fx.tenant.ID)

	err := fx.engine.TaskService().ClaimTaskByID(claimCtx, task.ID, backupApprover.ID)
	require.NoError(t, err, "真正在候选组里的人应该能成功认领")

	claimed := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(backupApprover.ID), claimed.Assignee)
}

func TestClaimTaskByID_RequesterCannotClaimViaCandidateGroupMembership(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	dept := fx.createDepartment(t, "Orphan Dept 8", 0, 0)
	requester := fx.createUser(t, "requester16", dept.ID)
	backupApprover := fx.createUser(t, "backupApprover8", 0)
	// 跟 TestClaimTaskByID_RequesterCannotClaimOwnCandidateGroupFallbackTask 不同：
	// 这次申请人真的是 ticket-approvers 组成员——createUserTask 展开候选组时会把申请人从
	// candidate_users 里过滤掉，但 candidate_groups 字段本身保留原始组名不做过滤，所以
	// 申请人仍然「属于」这个组。isTaskCandidate 必须额外靠 isProcessInstanceRequester
	// 排除申请人本人，否则申请人能靠组成员身份认领自己提交工单的审批任务。
	fx.createGroup(t, "ticket-approvers", requester.ID, backupApprover.ID)

	instance := fx.createInstance(t, "claim-requester-in-group", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})
	require.NoError(t, fx.engine.createUserTask(fx.ctx, instance, approvalTask("Activity_Approval", "工单审批")))
	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	require.NotContains(t, task.CandidateUsers, "requester16", "createUserTask 展开候选组时应该已经把申请人过滤掉")
	require.Contains(t, task.CandidateGroups, "ticket-approvers", "candidate_groups 字段应该保留原始组名，不做过滤")

	err := fx.engine.TaskService().ClaimTaskByID(fx.ctx, task.ID, requester.ID)
	assert.Error(t, err, "申请人虽然是候选组成员，但不应该能认领自己提交工单的审批任务")
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

// ==================== 新增 fixture 辅助方法：Team / Project / 指定角色的用户 ====================

func (f *approvalAssignmentFixture) createTeam(t *testing.T, name string, managerID int) *ent.Team {
	t.Helper()
	team, err := f.client.Team.Create().
		SetName(name).
		SetCode(name).
		SetTenantID(f.tenant.ID).
		SetManagerID(managerID).
		Save(f.ctx)
	require.NoError(t, err)
	return team
}

func (f *approvalAssignmentFixture) createProject(t *testing.T, name string, managerID int) *ent.Project {
	t.Helper()
	proj, err := f.client.Project.Create().
		SetName(name).
		SetCode(name).
		SetTenantID(f.tenant.ID).
		SetManagerID(managerID).
		SetStatus("active").
		Save(f.ctx)
	require.NoError(t, err)
	return proj
}

// createUserWithRole 建一个指定 ent/user.Role 枚举值的用户；deptID <= 0 表示不设置部门。
func (f *approvalAssignmentFixture) createUserWithRole(t *testing.T, username string, role string, deptID int) *ent.User {
	t.Helper()
	q := f.client.User.Create().
		SetUsername(username).
		SetEmail(username + "@aa.example.com").
		SetName(username).
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(f.tenant.ID).
		SetRole(role)
	if deptID > 0 {
		q = q.SetDepartmentID(deptID)
	}
	u, err := q.Save(f.ctx)
	require.NoError(t, err)
	return u
}

// approvalTaskWithRole 是 approvalTask 的变体，额外设置 AssigneeRole。固定范围的四个属性
// （AssigneeDeptId 等）用不上单独的构造变体——测试里直接在 approvalTask() 返回值上赋值一个
// 字段就够了，不需要为每个属性各写一个变体函数。

func approvalTaskWithRole(id, name, role string) *BPMNUserTask {
	task := approvalTask(id, name)
	task.AssigneeRole = role
	return task
}

// ==================== 第 2 级：按角色查候选人 ====================

func TestCreateUserTask_Approval_AssigneeRole_ResolvesAllUsersWithRole(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	requester := fx.createUser(t, "requester20", 0)
	_ = fx.createUserWithRole(t, "opsA", "manager", 0)
	_ = fx.createUserWithRole(t, "opsB", "manager", 0)
	// 无关角色的用户不应该混进候选人
	fx.createUserWithRole(t, "agentC", "agent", 0)

	instance := fx.createInstance(t, "assignee-role-multi", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTaskWithRole("Activity_Approval", "工单审批", string("manager")))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", task.Assignee, "按角色解析应该产出候选人列表，不是单一 assignee")
	assert.Contains(t, task.CandidateUsers, "opsA")
	assert.Contains(t, task.CandidateUsers, "opsB")
	assert.NotContains(t, task.CandidateUsers, "agentC")
}

func TestCreateUserTask_Approval_AssigneeRole_ExcludesRequester(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	// 申请人自己就是这个角色下的一员——现实中常见场景（比如运维经理自己也会提工单）
	requester := fx.createUserWithRole(t, "requester21", "manager", 0)
	_ = fx.createUserWithRole(t, "backupManager", "manager", 0)

	instance := fx.createInstance(t, "assignee-role-exclude", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTaskWithRole("Activity_Approval", "工单审批", string("manager")))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotContains(t, task.CandidateUsers, "requester21", "候选人列表不能包含申请人自己")
	assert.Contains(t, task.CandidateUsers, "backupManager")
}

func TestCreateUserTask_Approval_AssigneeRole_NoMatchingUsers_FallsBackToCandidateGroup(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	requester := fx.createUser(t, "requester22", 0)
	backup := fx.createUser(t, "backupApprover7", 0)
	fx.createGroup(t, "ticket-approvers", backup.ID)

	instance := fx.createInstance(t, "assignee-role-no-match", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	// technician 角色这个租户里一个用户都没有
	err := fx.engine.createUserTask(fx.ctx, instance, approvalTaskWithRole("Activity_Approval", "工单审批", string("technician")))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", task.Assignee)
	assert.Contains(t, task.CandidateUsers, "backupApprover7", "角色查不到人时应该转候选组兜底，不是留空孤儿任务")
}

// ==================== 第 3 级：固定范围组织路由 ====================

func TestCreateUserTask_Approval_AssigneeDeptId_ResolvesFixedDepartmentManager(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	manager := fx.createUser(t, "fixedDeptManager", 0)
	fixedDept := fx.createDepartment(t, "Fixed Dept", manager.ID, 0)
	// requester 自己在另一个部门——固定范围路由不应该受 requester 自己部门的影响
	requesterDept := fx.createDepartment(t, "Requester Dept", 0, 0)
	requester := fx.createUser(t, "requester23", requesterDept.ID)

	instance := fx.createInstance(t, "assignee-dept-id", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.AssigneeDeptId = fixedDept.ID
	err := fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(manager.ID), created.Assignee, "应该解析到固定部门的负责人，不是申请人自己部门的负责人（这个部门没配 manager，会是空）")
}

func TestCreateUserTask_Approval_AssigneeTeamId_ResolvesTeamLeader(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	leader := fx.createUser(t, "teamLeader1", 0)
	team := fx.createTeam(t, "Fixed Team", leader.ID)
	requester := fx.createUser(t, "requester24", 0)

	instance := fx.createInstance(t, "assignee-team-id", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.AssigneeTeamId = team.ID
	err := fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(leader.ID), created.Assignee)
}

func TestCreateUserTask_Approval_AssigneeProjectId_ResolvesProjectManager(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	manager := fx.createUser(t, "projectManager1", 0)
	proj := fx.createProject(t, "Fixed Project", manager.ID)
	requester := fx.createUser(t, "requester25", 0)

	instance := fx.createInstance(t, "assignee-project-id", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.AssigneeProjectId = proj.ID
	err := fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(manager.ID), created.Assignee)
}

func TestCreateUserTask_Approval_AssigneeTempTeamId_ResolvesTeamLeader(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	leader := fx.createUser(t, "tempTeamLeader1", 0)
	team := fx.createTeam(t, "Temp Team", leader.ID)
	requester := fx.createUser(t, "requester26", 0)

	instance := fx.createInstance(t, "assignee-temp-team-id", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.AssigneeTempTeamId = team.ID
	err := fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(leader.ID), created.Assignee, "assigneeTempTeamId 复用的是 Team 实体的 manager_id，跟 assigneeTeamId 同一个数据源")
}

func TestCreateUserTask_Approval_AssigneeGmChain_ResolvesSubmitterOwnChain(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	gm := fx.createUser(t, "gmChainGM1", 0)
	err := fx.client.User.UpdateOneID(gm.ID).SetJobTitle("综合物流总经理").Exec(fx.ctx)
	require.NoError(t, err)

	teamLead := fx.createUser(t, "gmChainLead1", 0)
	err = fx.client.User.UpdateOneID(teamLead.ID).SetJobTitle("操作主管").SetManagerID(gm.ID).Exec(fx.ctx)
	require.NoError(t, err)

	requester := fx.createUser(t, "gmChainRequester1", 0)
	err = fx.client.User.UpdateOneID(requester.ID).SetManagerID(teamLead.ID).Exec(fx.ctx)
	require.NoError(t, err)

	instance := fx.createInstance(t, "assignee-gm-chain", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "总经理审批")
	task.AssigneeGmChain = true
	err = fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(gm.ID), created.Assignee, "应该顺着提交人自己的汇报链爬过没有总经理头衔的直属上级，解析到链路上第一个总经理")
}

func TestCreateUserTask_Approval_AssigneeGmChain_SelfApproval_FallsBackToOwnDepartment(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	deptManager := fx.createUser(t, "gmChainDeptManager1", 0)
	dept := fx.createDepartment(t, "GM Chain Self Approval Dept", deptManager.ID, 0)

	// requester 自己是总经理，manager_id 指向自己——PersonalManagerResolver 会爬到自己，
	// 必须被 resolveGmChainAssignee 的自我审批排除，退到"申请人自己部门"这一级。
	requester := fx.createUser(t, "gmChainSelfApprover1", dept.ID)
	err := fx.client.User.UpdateOneID(requester.ID).SetJobTitle("综合物流总经理").SetManagerID(requester.ID).Exec(fx.ctx)
	require.NoError(t, err)

	instance := fx.createInstance(t, "assignee-gm-chain-self-approval", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "总经理审批")
	task.AssigneeGmChain = true
	err = fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(deptManager.ID), created.Assignee, "总经理审批解析出申请人自己时应该排除自我审批，退到申请人自己部门的负责人")
}

func TestCreateUserTask_Approval_AssigneeDeptId_RequesterIsTheManager_FallsBackToOwnDepartment(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	// requester 自己部门的负责人（不是 requester 自己）——固定范围解析失败退到这一级时
	// 应该解析到这个人，而不是直接跳过到候选组兜底
	requesterDeptManager := fx.createUser(t, "requesterDeptManager1", 0)
	requesterDept := fx.createDepartment(t, "Requester Own Dept", requesterDeptManager.ID, 0)
	requester := fx.createUser(t, "requester29", requesterDept.ID)

	// BPMN 声明的固定范围部门，负责人恰好就是 requester 自己
	fixedDept := fx.createDepartment(t, "Fixed Dept Managed By Requester", requester.ID, 0)

	instance := fx.createInstance(t, "assignee-dept-id-is-requester", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.AssigneeDeptId = fixedDept.ID
	err := fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(requester.ID), created.Assignee, "固定范围解析出的人是申请人自己时不能直接指派给他")
	assert.Equal(t, strconv.Itoa(requesterDeptManager.ID), created.Assignee, "应该退到申请人自己部门这一级，解析出申请人自己部门的负责人")
}

func TestCreateUserTask_Approval_AssigneeDeptId_ResolveFails_FallsBackToOwnDepartment(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	ownManager := fx.createUser(t, "ownDeptManager2", 0)
	requesterDept := fx.createDepartment(t, "Requester Dept For Fallback", ownManager.ID, 0)
	requester := fx.createUser(t, "requester27", requesterDept.ID)

	instance := fx.createInstance(t, "assignee-dept-id-not-found", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.AssigneeDeptId = 999999 // 不存在的部门 ID
	err := fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, strconv.Itoa(ownManager.ID), created.Assignee, "固定部门解析不到时应该退到申请人自己部门这一级，不是直接跳到候选组兜底")
}

// ==================== 跨租户隔离 ====================

func TestCreateUserTask_Approval_AssigneeRole_TenantIsolation(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	otherTenant, err := fx.client.Tenant.Create().
		SetName("Role Isolation Tenant").
		SetCode("role-isolation-tenant").
		SetDomain("role-isolation.example.com").
		SetStatus("active").
		Save(fx.ctx)
	require.NoError(t, err)

	// 另一个租户里同样角色的用户不应该被本租户的查询看到
	_, err = fx.client.User.Create().
		SetUsername("otherTenantManager").
		SetEmail("otherTenantManager@aa.example.com").
		SetName("otherTenantManager").
		SetPasswordHash("hash").
		SetActive(true).
		SetTenantID(otherTenant.ID).
		SetRole("manager").
		Save(fx.ctx)
	require.NoError(t, err)

	requester := fx.createUser(t, "requester28", 0)
	backup := fx.createUser(t, "backupApprover8", 0)
	fx.createGroup(t, "ticket-approvers", backup.ID)

	instance := fx.createInstance(t, "assignee-role-tenant-isolation", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, approvalTaskWithRole("Activity_Approval", "工单审批", string("manager")))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotContains(t, task.CandidateUsers, "otherTenantManager", "不能跨租户解析到别的租户同角色的用户")
	assert.Contains(t, task.CandidateUsers, "backupApprover8", "本租户查不到该角色用户时应该转候选组兜底")
}

// ==================== Finding 1（最终审查）：角色唯一匹配是申请人自己时，必须转候选组兜底，
// 不能变成 assignee/candidate_users/candidate_groups 三者皆空的孤儿任务 ====================

func TestCreateUserTask_Approval_AssigneeRole_SoleMatchIsRequester_FallsBackToCandidateGroup(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	// 这个角色在本租户下唯一匹配的用户就是申请人自己——排除申请人后候选人列表应该是空，
	// 必须触发 candidateGroups 兜底，而不是留下一个无人能领取、也没有兜底组的孤儿任务。
	requester := fx.createUserWithRole(t, "requester30", "manager", 0)
	backup := fx.createUser(t, "backupApprover9", 0)
	fx.createGroup(t, "ticket-approvers", backup.ID)

	instance := fx.createInstance(t, "assignee-role-sole-match-is-requester", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTaskWithRole("Activity_Approval", "工单审批", string("manager")))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.Equal(t, "", task.Assignee)
	assert.NotContains(t, task.CandidateUsers, "requester30", "候选人列表不能包含申请人自己")
	assert.Contains(t, task.CandidateUsers, "backupApprover9", "角色唯一匹配是申请人自己时应该转候选组兜底，不能留下无人可领的孤儿任务")
}

// ==================== Finding 3（最终审查）：固定范围组织路由的另外三个属性也需要跨租户隔离测试
// （assigneeTempTeamId 复用 assigneeTeamId 同一个 Team 实体/resolver，跳过，避免和团队测试重复）====================

func TestCreateUserTask_Approval_AssigneeDeptId_TenantIsolation(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	otherTenant, err := fx.client.Tenant.Create().
		SetName("Dept Isolation Tenant").
		SetCode("dept-isolation-tenant").
		SetDomain("dept-isolation.example.com").
		SetStatus("active").
		Save(fx.ctx)
	require.NoError(t, err)

	otherManager := fx.createUserInTenant(t, otherTenant.ID, "otherTenantDeptManager", 0)
	otherDept := fx.createDepartmentInTenant(t, otherTenant.ID, "Other Tenant Dept", otherManager.ID, 0)

	ownManager := fx.createUser(t, "ownDeptManager3", 0)
	requesterDept := fx.createDepartment(t, "Requester Dept For Dept Isolation", ownManager.ID, 0)
	requester := fx.createUser(t, "requester31", requesterDept.ID)

	instance := fx.createInstance(t, "assignee-dept-id-tenant-isolation", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.AssigneeDeptId = otherDept.ID // 本租户里这个 ID 属于别的租户，应该查不到
	err = fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(otherManager.ID), created.Assignee, "不能跨租户解析到另一个租户的固定部门负责人")
	assert.Equal(t, strconv.Itoa(ownManager.ID), created.Assignee, "固定部门跨租户查不到时应该退到申请人自己部门这一级")
}

func TestCreateUserTask_Approval_AssigneeTeamId_TenantIsolation(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	otherTenant, err := fx.client.Tenant.Create().
		SetName("Team Isolation Tenant").
		SetCode("team-isolation-tenant").
		SetDomain("team-isolation.example.com").
		SetStatus("active").
		Save(fx.ctx)
	require.NoError(t, err)

	otherLeader := fx.createUserInTenant(t, otherTenant.ID, "otherTenantTeamLeader", 0)
	otherTeam, err := fx.client.Team.Create().
		SetName("Other Tenant Team").
		SetCode("Other Tenant Team").
		SetTenantID(otherTenant.ID).
		SetManagerID(otherLeader.ID).
		Save(fx.ctx)
	require.NoError(t, err)

	ownManager := fx.createUser(t, "ownDeptManager4", 0)
	requesterDept := fx.createDepartment(t, "Requester Dept For Team Isolation", ownManager.ID, 0)
	requester := fx.createUser(t, "requester32", requesterDept.ID)

	instance := fx.createInstance(t, "assignee-team-id-tenant-isolation", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.AssigneeTeamId = otherTeam.ID // 本租户里这个 ID 属于别的租户，应该查不到
	err = fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(otherLeader.ID), created.Assignee, "不能跨租户解析到另一个租户的固定团队负责人")
	assert.Equal(t, strconv.Itoa(ownManager.ID), created.Assignee, "固定团队跨租户查不到时应该退到申请人自己部门这一级")
}

// ==================== 端到端：真实部署 change_normal_flow，
// CAB 审批候选人应包含 role=change_manager 的用户 ====================

func TestCABApprovalAssignsChangeManagerRole(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)
	ctx := context.WithValue(fx.ctx, bpmn.BPMNTenantIDContextKey, fx.tenant.ID)
	// Elevated: this test drives ListUserTasks as an internal lookup helper
	// to find the just-created task, not to simulate a specific end user's
	// authorized view — matches ListUserTasks's fail-closed-for-non-elevated
	// guard (final whole-branch review Finding 4), which otherwise denies a
	// non-elevated caller with no BPMNUserIDContextKey set.
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)
	// System caller: this test also drives CompleteTask directly (on the
	// assessment task, before a specific actor is involved) to advance the
	// gateway — authorizeTaskActor now denies by default without either a
	// user ID or this explicit declaration.
	ctx = context.WithValue(ctx, bpmn.BPMNSystemCallerContextKey, true)

	deploySvc := NewBPMNTemplateService(fx.client)
	_, err := deploySvc.LoadAndDeployTemplates(ctx, fx.tenant.ID)
	require.NoError(t, err)

	// 建一个 role=change_manager 的用户
	cmUser := fx.createUserWithRole(t, "cm_user", "change_manager", 0)

	// 申请人自己不是 change_manager，避免被排除逻辑误判
	requester := fx.createUser(t, "requester_cab", 0)

	instance, err := fx.engine.StartProcess(ctx, "change_normal_flow", "test-cab-approval", map[string]interface{}{
		"approval_required": true,
		"requester_id":      float64(requester.ID),
	})
	require.NoError(t, err)

	// 完成"变更评估"任务，让网关推进到 CAB 审批节点
	tasks, _, err := fx.engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
		ProcessInstanceID: instance.ID,
		PageSize:          10,
	})
	require.NoError(t, err)
	require.Len(t, tasks, 1, "启动流程后应该恰好有一个待办任务（变更评估）")

	err = fx.engine.CompleteTask(ctx, tasks[0].TaskID, map[string]interface{}{})
	require.NoError(t, err)

	updated, err := fx.client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, "Activity_CABApproval", updated.CurrentActivityID,
		"approval_required=true 应该推进到 CAB 审批节点")

	task := fx.getCreatedTask(t, instance.ID, "Activity_CABApproval")
	require.Contains(t, task.CandidateUsers, cmUser.Username,
		"CAB 审批候选人必须包含 role=change_manager 的用户")
}

// ==================== Task 7.5: Gateway_ApprovalResult 必须按 approvalAction 决策路由，
// 不能无条件路由到驳回。回归背景：Gateway_ApprovalResult 自己声明的 <outgoing> 里的
// Flow_Schedule 这个 ID 其实被 Gateway_Approval 的出边占用了（两个不同网关共享同一个
// sequenceFlow ID），导致 Gateway_ApprovalResult 真正生效的出边只有无条件的 Flow_Reject——
// CAB 审批不管 approve 还是 reject 都会被路由到 Activity_Reject。====================

func TestCABApprovalGatewayRoutesToScheduleOnApprove(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)
	ctx := context.WithValue(fx.ctx, bpmn.BPMNTenantIDContextKey, fx.tenant.ID)
	// Elevated: this test drives ListUserTasks as an internal lookup helper
	// to find the just-created task, not to simulate a specific end user's
	// authorized view — matches ListUserTasks's fail-closed-for-non-elevated
	// guard (final whole-branch review Finding 4), which otherwise denies a
	// non-elevated caller with no BPMNUserIDContextKey set.
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	deploySvc := NewBPMNTemplateService(fx.client)
	_, err := deploySvc.LoadAndDeployTemplates(ctx, fx.tenant.ID)
	require.NoError(t, err)

	cmUser := fx.createUserWithRole(t, "cm_user_approve", "change_manager", 0)
	requester := fx.createUser(t, "requester_cab_approve", 0)

	instance, err := fx.engine.StartProcess(ctx, "change_normal_flow", "test-cab-decision-approve", map[string]interface{}{
		"approval_required": true,
		"requester_id":      float64(requester.ID),
	})
	require.NoError(t, err)

	// 完成"变更评估"，推进到 CAB 审批节点
	tasks, _, err := fx.engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
		ProcessInstanceID: instance.ID,
		PageSize:          10,
	})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	// System caller: completing the assessment task here isn't simulating a
	// specific end user acting on it — kept off the shared `ctx` (rather than
	// merged into it above) so it doesn't leak into actorCtx below and
	// silently bypass the actor check the CAB decision is meant to exercise.
	systemCtx := context.WithValue(ctx, bpmn.BPMNSystemCallerContextKey, true)
	require.NoError(t, fx.engine.CompleteTask(systemCtx, tasks[0].TaskID, map[string]interface{}{}))

	cabTask := fx.getCreatedTask(t, instance.ID, "Activity_CABApproval")

	// change_manager 身份完成 CAB 审批，决策为通过
	actorCtx := context.WithValue(ctx, bpmn.BPMNUserIDContextKey, cmUser.ID)
	err = fx.engine.CompleteTask(actorCtx, cabTask.TaskID, map[string]interface{}{
		"approvalAction":  "approve",
		"approvalResult":  "approved",
		"approvalComment": "looks good",
	})
	require.NoError(t, err)

	updated, err := fx.client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Schedule", updated.CurrentActivityID,
		"CAB 审批通过应该路由到变更排期节点，不是变更驳回")
}

func TestCABApprovalGatewayRoutesToRejectOnReject(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)
	ctx := context.WithValue(fx.ctx, bpmn.BPMNTenantIDContextKey, fx.tenant.ID)
	// Elevated: this test drives ListUserTasks as an internal lookup helper
	// to find the just-created task, not to simulate a specific end user's
	// authorized view — matches ListUserTasks's fail-closed-for-non-elevated
	// guard (final whole-branch review Finding 4), which otherwise denies a
	// non-elevated caller with no BPMNUserIDContextKey set.
	ctx = context.WithValue(ctx, bpmn.BPMNElevatedContextKey, true)

	deploySvc := NewBPMNTemplateService(fx.client)
	_, err := deploySvc.LoadAndDeployTemplates(ctx, fx.tenant.ID)
	require.NoError(t, err)

	cmUser := fx.createUserWithRole(t, "cm_user_reject", "change_manager", 0)
	requester := fx.createUser(t, "requester_cab_reject", 0)

	instance, err := fx.engine.StartProcess(ctx, "change_normal_flow", "test-cab-decision-reject", map[string]interface{}{
		"approval_required": true,
		"requester_id":      float64(requester.ID),
	})
	require.NoError(t, err)

	tasks, _, err := fx.engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{
		ProcessInstanceID: instance.ID,
		PageSize:          10,
	})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	// System caller: completing the assessment task here isn't simulating a
	// specific end user acting on it — kept off the shared `ctx` (rather than
	// merged into it above) so it doesn't leak into actorCtx below and
	// silently bypass the actor check the CAB decision is meant to exercise.
	systemCtx := context.WithValue(ctx, bpmn.BPMNSystemCallerContextKey, true)
	require.NoError(t, fx.engine.CompleteTask(systemCtx, tasks[0].TaskID, map[string]interface{}{}))

	cabTask := fx.getCreatedTask(t, instance.ID, "Activity_CABApproval")

	// change_manager 身份完成 CAB 审批，决策为驳回
	actorCtx := context.WithValue(ctx, bpmn.BPMNUserIDContextKey, cmUser.ID)
	err = fx.engine.CompleteTask(actorCtx, cabTask.TaskID, map[string]interface{}{
		"approvalAction":  "reject",
		"approvalResult":  "rejected",
		"approvalComment": "insufficient risk assessment",
	})
	require.NoError(t, err)

	updated, err := fx.client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Reject", updated.CurrentActivityID,
		"CAB 审批驳回应该路由到变更驳回节点")
}

func TestCreateUserTask_Approval_AssigneeProjectId_TenantIsolation(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	otherTenant, err := fx.client.Tenant.Create().
		SetName("Project Isolation Tenant").
		SetCode("project-isolation-tenant").
		SetDomain("project-isolation.example.com").
		SetStatus("active").
		Save(fx.ctx)
	require.NoError(t, err)

	otherManager := fx.createUserInTenant(t, otherTenant.ID, "otherTenantProjectManager", 0)
	otherProject, err := fx.client.Project.Create().
		SetName("Other Tenant Project").
		SetCode("Other Tenant Project").
		SetTenantID(otherTenant.ID).
		SetManagerID(otherManager.ID).
		SetStatus("active").
		Save(fx.ctx)
	require.NoError(t, err)

	ownManager := fx.createUser(t, "ownDeptManager5", 0)
	requesterDept := fx.createDepartment(t, "Requester Dept For Project Isolation", ownManager.ID, 0)
	requester := fx.createUser(t, "requester33", requesterDept.ID)

	instance := fx.createInstance(t, "assignee-project-id-tenant-isolation", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	task := approvalTask("Activity_Approval", "工单审批")
	task.AssigneeProjectId = otherProject.ID // 本租户里这个 ID 属于别的租户，应该查不到
	err = fx.engine.createUserTask(fx.ctx, instance, task)
	require.NoError(t, err)

	created := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotEqual(t, strconv.Itoa(otherManager.ID), created.Assignee, "不能跨租户解析到另一个租户的固定项目负责人")
	assert.Equal(t, strconv.Itoa(ownManager.ID), created.Assignee, "固定项目跨租户查不到时应该退到申请人自己部门这一级")
}

// ==================== 会签自动创建的调用者授权（Finding A，Task 4 review 追加）====================

// TestCreateUserTask_CounterSignFanOut_NotAttributedToUpstreamActor 锁定
// createUserTask 内部自动创建会签子任务这条路径：多审批人节点
// （ApprovalMode != "single"）在 createUserTask 内部会调用
// taskService.CreateCounterSignTasks，后者经 authorizeTaskMutation 校验调用者
// 是不是"父任务"（刚刚创建出来的会签父任务）的 assignee/candidate。
//
// 问题：createUserTask 复用的是调用方 ctx——通常是完成上一个节点、推进网关到这个
// 多审批人节点的那个人的 ctx，而这个人几乎不可能是刚创建出来的会签父任务的候选人
// （候选人是这个节点自己配置的审批人列表）。Task 3 让 authorizeTaskMutation
// fail-closed 之后，这条内部自动创建路径会被这个跟它毫不相关的"上一个节点的操作人"
// 意外拒绝——引擎自己的内部记账动作，不应该被套用人类操作者的候选人校验。
//
// 用真实存在但跟本节点候选人无关的 upstreamActor 身份驱动 createUserTask，
// 断言不会被 authorizeTaskMutation 拒绝。
func TestCreateUserTask_CounterSignFanOut_NotAttributedToUpstreamActor(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	// upstreamActor：完成了上一个节点、推进流程到这个多审批人节点的人——跟这个节点
	// 自己的候选审批人列表（approver1/approver2）没有任何关系。
	upstreamActor := fx.createUser(t, "upstream-actor", 0)
	fx.createUser(t, "countersign-approver1", 0)
	fx.createUser(t, "countersign-approver2", 0)

	instance := fx.createInstance(t, "countersign-fanout", map[string]interface{}{})

	task := approvalTask("Activity_MultiApproval", "多人会签")
	task.CandidateUsers = "countersign-approver1,countersign-approver2"
	task.ApprovalMode = "any" // len(approvers)=2 > 1，触发 createUserTask 内部的自动会签创建

	ctx := context.WithValue(fx.ctx, bpmn.BPMNTenantIDContextKey, fx.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, upstreamActor.ID)

	err := fx.engine.createUserTask(ctx, instance, task)
	require.NoError(t, err, "会签子任务的内部自动创建不应该被跟本节点候选人无关的上一个操作人身份拒绝")

	parent := fx.getCreatedTask(t, instance.ID, "Activity_MultiApproval")
	children, err := fx.client.ProcessTask.Query().
		Where(processtask.RootTaskID(parent.TaskID)).
		All(fx.ctx)
	require.NoError(t, err)
	assert.Len(t, children, 2, "应该按候选人列表创建出对应数量的会签子任务")
}

// TestCreateUserTask_CounterSignFanOut_IsAudited guards the final
// whole-branch review's Important Finding 3: createUserTask's fan-out to
// CreateCounterSignTasks is wrapped with bpmn.BPMNSystemCallerContextKey
// (a correct, already-landed fix from Task 4), which makes
// authorizeTaskMutation return actorID=0. Before this fix,
// CreateCounterSignTasks's audit call was gated on "if actorID > 0",
// meaning it silently skipped the audit for EVERY multi-approver
// (ApprovalMode != "single") counter-sign auto-creation — a workflow
// automation action that AGENTS.md requires to be audited, and unlike the
// truly-unattributable system calls that gate AssignTask/CancelTask/
// SetTaskVariables's audits, this one has a knowable actor: the engine's
// own automatic fan-out.
//
// This test drives the exact same fan-out path as the sibling
// NotAttributedToUpstreamActor test above, then asserts a
// ProcessAuditLog row now exists for it, attributed to the "system"
// sentinel actor (UserID=0, UserName="system") rather than being skipped.
func TestCreateUserTask_CounterSignFanOut_IsAudited(t *testing.T) {
	fx := newApprovalAssignmentFixture(t)

	upstreamActor := fx.createUser(t, "audit-upstream-actor", 0)
	fx.createUser(t, "audit-countersign-approver1", 0)
	fx.createUser(t, "audit-countersign-approver2", 0)

	instance := fx.createInstance(t, "countersign-fanout-audit", map[string]interface{}{})

	task := approvalTask("Activity_MultiApprovalAudit", "多人会签-审计")
	task.CandidateUsers = "audit-countersign-approver1,audit-countersign-approver2"
	task.ApprovalMode = "any" // len(approvers)=2 > 1，触发 createUserTask 内部的自动会签创建

	ctx := context.WithValue(fx.ctx, bpmn.BPMNTenantIDContextKey, fx.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, upstreamActor.ID)

	err := fx.engine.createUserTask(ctx, instance, task)
	require.NoError(t, err)

	parent := fx.getCreatedTask(t, instance.ID, "Activity_MultiApprovalAudit")

	logs, err := fx.client.ProcessAuditLog.Query().
		Where(
			processauditlog.ProcessInstanceID(instance.ID),
			processauditlog.ActivityID(parent.TaskDefinitionKey),
			processauditlog.Action(AuditActionActivityStarted),
		).
		All(fx.ctx)
	require.NoError(t, err)
	require.Len(t, logs, 1, "counter-sign auto-creation must produce exactly one audit record instead of being silently skipped")
	assert.Equal(t, 0, logs[0].UserID, "system fan-out has no human actor, so UserID must stay the explicit 0 sentinel")
	assert.Equal(t, "system", logs[0].UserName, "system fan-out must be attributed to the 'system' sentinel actor, not silently unaudited")
}
