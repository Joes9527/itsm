# 审批收敛·组件① — BPMN 引擎按角色/固定范围解析审批人 — 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 `createUserTask` 的 approval 分支新增两类声明式候选人/assignee 解析方式——按角色查候选人（`assigneeRole`）、固定范围组织路由（`assigneeDeptId`/`assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId`）——为后续"审批收敛到 BPMN"的组件②③打地基。

**Architecture:** 在 `service/bpmn_types.go` 的 `BPMNUserTask` 加 5 个新的自定义 XML 属性字段；`service/bpmn_process_engine.go` 的 `createUserTask` 把现有的两级判断（candidateGroups/candidateUsers 存在则跳过 → 否则解析申请人自己部门负责人）扩成一个 5 级优先级 `switch`，新增 2 个辅助函数（`resolveRoleCandidates`、`resolveFixedScopeAssignee`），复用已有的 `service/approver/*.go` 四个 resolver（`DeptManagerResolver`/`TeamLeaderResolver`/`ProjectMgrResolver`/`TempTeamResolver`），不重新实现解析逻辑。

**Tech Stack:** Go 1.x / Ent ORM / `stretchr/testify` / sqlite3（enttest）——跟这次会话之前的自审批修复用同一套。

## Global Constraints

- 设计文档：`docs/superpowers/specs/2026-08-08-approval-bpmn-convergence-design.md`（组件①部分，已经用户复审通过）。
- 新增 5 个 BPMN 自定义属性，跟已有的 `taskPurpose`/`approvalMode` 一样是这个引擎自己的扩展，不是标准 BPMN：`assigneeRole`（string）、`assigneeDeptId`/`assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId`（int）。
- `createUserTask` 里 `task.TaskPurpose == "approval"` 分支的完整优先级链（`assignee == ""` 时）：
  1. BPMN 声明了 `candidateGroups` 或 `candidateUsers` → 维持现状，不做任何自动解析（已有逻辑）
  2. 否则 BPMN 声明了 `assigneeRole` → 查该租户下所有 active 且该角色的用户，**全部作为候选人**（不是单选一个），排除申请人自己
  3. 否则 BPMN 声明了 `assigneeDeptId`/`assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId` 中任意一个（按这个顺序取第一个非零的）→ 对应 resolver 解析出**单个** assignee，解析失败或解析出的人是申请人自己 → 退到第 4 级（不是直接跳到候选组兜底）
  4. 否则 → 解析申请人自己所在部门的负责人（已有逻辑，`resolveApprovalAssignee`）
  5. 以上都没有产出结果（assignee 为空且候选人列表为空）→ 兜底 `ticket-approvers` 候选组（已有逻辑）
- 第 2 级（按角色）返回候选人**列表**，第 3 级（固定范围）返回**单个** assignee——因为同一个角色可能有多个用户，但部门/团队/项目/临时团队架构上至多只有一个负责人（跟"申请人自己部门"那条已有路径同一个形状）。不要把这两级做成同一种返回类型。
- 固定范围解析（第 3 级）绝对不能用 `ApproverInfo.UserName` 作为候选人字符串——这个字段实际填的是 `User.Name`（显示名，比如"张三"），不是 `authorizeTaskActor`/`excludeUserFromCandidates` 用来比对的 `User.Username`（登录名，比如"zhangsan"）。必须用 `strconv.Itoa(approvers[0].UserID)`，跟已有的 `resolveApprovalAssignee` 保持完全一致的返回形态。
- 两级新解析都要排除申请人自己：角色候选人列表复用已有的 `excludeUserFromCandidates`；固定范围单一 assignee 复用已有的 `resolveApprovalAssignee` 那种"解析出来的人 ID 等于申请人 ID 就返回空"的内联比较方式，不要为了"复用"而把单人场景硬套进候选人列表的排除函数。
- 四个 resolver（`service/approver/dept_manager_resolver.go`、`team_leader_resolver.go`、`project_mgr_resolver.go`、`temp_team_resolver.go`）已经存在、已经测试过、已经被 legacy 审批链引用——只新增 BPMN 声明式入口去调用它们，不修改这四个文件本身。`TempTeamResolver` 内部直接复用 `TeamLeaderResolver`（同一个 `Team` 实体、同一个 `TeamID` 字段，只是把返回结果的 `Role` 改成 `"temp_team_leader"`），所以 `assigneeTempTeamId` 属性对应的 `ApproverContext` 也要填 `TeamID`，不是一个不存在的独立字段。
- 角色查询的 `role` 值必须是 `ent/user.Role` 枚举的合法值（`super_admin`/`admin`/`manager`/`agent`/`technician`/`security`/`end_user`）——传入不在这个集合里的字符串不是错误，只是查询不到任何用户，自然落到候选组兜底。**这次会话在设计阶段核实过，4 个种子默认模板里的 `"ops_manager"`/`"change_manager"`/`"security_admin"` 都不是合法枚举值**——这些模板的重新 authoring（组件②的工作，不在这次计划范围内）需要映射到真实存在的枚举值（比如 `"security_admin"` 应该映射成 `"security"`），这里只是提前记录，不在这次任务里处理。
- 所有新增代码保持在 `package service`（`bpmn_process_engine.go`/`bpmn_types.go` 所在包），不新建子包。
- 测试用 `enttest.Open(t, "sqlite3", "file:<unique-name>?mode=memory&cache=shared&_fk=1")`，复用 `service/bpmn_process_engine_approval_assignment_test.go` 里已经存在的 `approvalAssignmentFixture`（同一个 sqlite DSN `"file:approval_assignment_test?..."`），新增的 fixture 辅助方法（建 Team/Project）挂在这个 struct 上，不要新建一个平行的测试文件/fixture。

## File Structure

- Modify: `itsm-backend/service/bpmn_types.go` — `BPMNUserTask` 加 5 个新字段。
- Modify: `itsm-backend/service/bpmn_process_engine.go` — `createUserTask` 优先级链重构 + 2 个新辅助函数（`resolveRoleCandidates`、`resolveFixedScopeAssignee`）。
- Modify: `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go` — 追加 fixture 辅助方法（建 Team/Project、建指定角色的用户）+ 新测试函数。

---

### Task 1: BPMN 引擎新增按角色/固定范围解析审批人

**Files:**
- Modify: `itsm-backend/service/bpmn_types.go:77-94`（`BPMNUserTask` struct）
- Modify: `itsm-backend/service/bpmn_process_engine.go:623-`（`createUserTask` 函数体）+ 新增两个辅助函数
- Modify: `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go`（追加内容，不改已有测试）

**Interfaces:**
- Consumes: `approver.NewTeamLeaderResolver()`/`NewProjectMgrResolver()`/`NewTempTeamResolver()`/`NewDeptManagerResolver()`（均已存在，`service/approver/*.go`，都实现 `approver.ApproverResolver` 接口：`Resolve(ctx, client, appCtx *ApproverContext) ([]*ApproverInfo, error)`，`ApproverContext{TenantID, DepartmentID, TeamID, ProjectID}`，`ApproverInfo{UserID, UserName, UserEmail, Role, Source}`，均已存在于 `service/approver/resolver.go`）；`ent/user.RoleEQ(user.Role) predicate.User`、`user.TenantIDEQ`、`user.Active`（已存在，ent 生成代码）；`excludeUserFromCandidates(usernames []string, u *ent.User) []string`（已存在，`bpmn_process_engine.go`，这次会话早前自审批修复引入）；`e.groupResolver.MergeCandidateUsers(bpmnCandidateUsers string, groupUsers []string) string`（已存在，`service/bpmn/bpmn_group_resolver.go`）。
- Produces：本任务新增 2 个未导出方法，供 `createUserTask` 内部使用：
  - `func (e *CustomProcessEngine) resolveRoleCandidates(ctx context.Context, tenantID int, role string) ([]string, error)`
  - `func (e *CustomProcessEngine) resolveFixedScopeAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User, task *BPMNUserTask) string`

- [ ] **Step 1: 写测试文件（先写，此时会失败——现有代码还没有这两级解析）**

在 `itsm-backend/service/bpmn_process_engine_approval_assignment_test.go` 文件末尾（`TestExcludeUserFromCandidates` 函数之后）追加以下内容：

```go

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
func (f *approvalAssignmentFixture) createUserWithRole(t *testing.T, username string, role user.Role, deptID int) *ent.User {
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
	opsA := fx.createUserWithRole(t, "opsA", user.RoleManager, 0)
	opsB := fx.createUserWithRole(t, "opsB", user.RoleManager, 0)
	// 无关角色的用户不应该混进候选人
	fx.createUserWithRole(t, "agentC", user.RoleAgent, 0)

	instance := fx.createInstance(t, "assignee-role-multi", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTaskWithRole("Activity_Approval", "工单审批", string(user.RoleManager)))
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
	requester := fx.createUserWithRole(t, "requester21", user.RoleManager, 0)
	backup := fx.createUserWithRole(t, "backupManager", user.RoleManager, 0)

	instance := fx.createInstance(t, "assignee-role-exclude", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err := fx.engine.createUserTask(fx.ctx, instance, approvalTaskWithRole("Activity_Approval", "工单审批", string(user.RoleManager)))
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
	err := fx.engine.createUserTask(fx.ctx, instance, approvalTaskWithRole("Activity_Approval", "工单审批", string(user.RoleTechnician)))
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
		SetRole(user.RoleManager).
		Save(fx.ctx)
	require.NoError(t, err)

	requester := fx.createUser(t, "requester28", 0)
	backup := fx.createUser(t, "backupApprover8", 0)
	fx.createGroup(t, "ticket-approvers", backup.ID)

	instance := fx.createInstance(t, "assignee-role-tenant-isolation", map[string]interface{}{
		"requester_id": float64(requester.ID),
	})

	err = fx.engine.createUserTask(fx.ctx, instance, approvalTaskWithRole("Activity_Approval", "工单审批", string(user.RoleManager)))
	require.NoError(t, err)

	task := fx.getCreatedTask(t, instance.ID, "Activity_Approval")
	assert.NotContains(t, task.CandidateUsers, "otherTenantManager", "不能跨租户解析到别的租户同角色的用户")
	assert.Contains(t, task.CandidateUsers, "backupApprover8", "本租户查不到该角色用户时应该转候选组兜底")
}
```

在文件顶部 `import` 块里补上 `"strconv"`（如果还没有——检查一下，`bpmn_process_engine_approval_assignment_test.go` 目前的断言大量用到 `strconv.Itoa`，本来就应该已经导入；如果没有就加）和 `"itsm-backend/ent/user"`（用来引用 `user.Role`/`user.RoleManager`/`user.RoleAgent`/`user.RoleTechnician` 这些 ent 生成的类型和常量）。

- [ ] **Step 2: 运行测试，确认按预期失败**

Run: `cd itsm-backend && go test ./service/... -run 'TestCreateUserTask_Approval_AssigneeRole|TestCreateUserTask_Approval_AssigneeDeptId|TestCreateUserTask_Approval_AssigneeTeamId|TestCreateUserTask_Approval_AssigneeProjectId|TestCreateUserTask_Approval_AssigneeTempTeamId' -v`

Expected：编译失败——`task.AssigneeRole`/`task.AssigneeDeptId`/`task.AssigneeTeamId`/`task.AssigneeProjectId`/`task.AssigneeTempTeamId` 这几个字段还不存在于 `BPMNUserTask` struct。这一步是确认测试写对了会先编译失败（字段不存在），不是断言失败——Step 3 加完字段后应该能编译，但断言仍然失败（因为 `createUserTask` 还不认这几个字段），Step 4 实现完才会真正全部通过。

- [ ] **Step 3: 给 `BPMNUserTask` 加新字段**

在 `itsm-backend/service/bpmn_types.go` 的 `BPMNUserTask` struct（第 77-94 行）里，`CommentRequiredOnReject` 那一行下面加：

```go
	AssigneeRole            string `xml:"assigneeRole,attr"`
	AssigneeDeptId          int    `xml:"assigneeDeptId,attr"`
	AssigneeTeamId          int    `xml:"assigneeTeamId,attr"`
	AssigneeProjectId       int    `xml:"assigneeProjectId,attr"`
	AssigneeTempTeamId      int    `xml:"assigneeTempTeamId,attr"`
```

- [ ] **Step 4: 运行测试，确认编译通过但断言失败**

Run: `cd itsm-backend && go build ./... && go test ./service/... -run 'TestCreateUserTask_Approval_AssigneeRole|TestCreateUserTask_Approval_AssigneeDeptId|TestCreateUserTask_Approval_AssigneeTeamId|TestCreateUserTask_Approval_AssigneeProjectId|TestCreateUserTask_Approval_AssigneeTempTeamId' -v`

Expected：编译通过；测试运行但断言失败（比如期望 `task.Assignee` 是某个具体用户 ID，实际拿到的是空字符串，因为 `createUserTask` 还没认识这几个新字段，它们会被当成"什么都没声明"，落到已有的"申请人自己部门"这一级）。

- [ ] **Step 5: 加两个新辅助函数**

在 `itsm-backend/service/bpmn_process_engine.go` 里，`resolveApprovalAssignee` 函数（现有）后面、`excludeUserFromCandidates` 函数（现有）前面，插入：

```go
// resolveRoleCandidates 查询该租户下所有 active 且 role = role 的用户，返回候选人展开
// 形态的字符串列表（跟 GroupResolver.ExpandGroupsToUsers 的 usernames 返回值同样的
// username→email→ID 兜底规则），供 excludeUserFromCandidates/MergeCandidateUsers 直接复用。
// role 必须是 ent/user.Role 枚举的合法值——不是这几个值的字符串查询会直接返回空列表，
// 不是错误，调用方应该按"没查到候选人"处理，转候选组兜底。
func (e *CustomProcessEngine) resolveRoleCandidates(ctx context.Context, tenantID int, role string) ([]string, error) {
	users, err := e.client.User.Query().
		Where(user.RoleEQ(user.Role(role)), user.TenantIDEQ(tenantID), user.Active(true)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询角色候选审批人失败: %w", err)
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		display := strings.TrimSpace(u.Username)
		if display == "" {
			display = strings.TrimSpace(u.Email)
		}
		if display == "" {
			display = strconv.Itoa(u.ID)
		}
		names = append(names, display)
	}
	return names, nil
}

// resolveFixedScopeAssignee 处理固定范围组织路由（BPMN 声明 assigneeDeptId/assigneeTeamId/
// assigneeProjectId/assigneeTempTeamId 中的一个，按这个顺序取第一个非零的）。四个 resolver
// （service/approver/*.go，已有、已测试）都是"至多解析出一个人"的形状，返回值/自我审批
// 排除规则完全比照 resolveApprovalAssignee（申请人自己部门）——用 approvers[0].UserID 而
// 不是 approvers[0].UserName，因为 ApproverInfo.UserName 实际填的是 User.Name（显示名），
// 不是 authorizeTaskActor 用来比对的 User.Username（登录名），用 UserName 会导致候选人
// 字符串永远匹配不上真实登录用户。
func (e *CustomProcessEngine) resolveFixedScopeAssignee(ctx context.Context, instance *ent.ProcessInstance, requester *ent.User, task *BPMNUserTask) string {
	var resolver approver.ApproverResolver
	appCtx := &approver.ApproverContext{TenantID: instance.TenantID}
	switch {
	case task.AssigneeDeptId != 0:
		resolver = approver.NewDeptManagerResolver()
		appCtx.DepartmentID = task.AssigneeDeptId
	case task.AssigneeTeamId != 0:
		resolver = approver.NewTeamLeaderResolver()
		appCtx.TeamID = task.AssigneeTeamId
	case task.AssigneeProjectId != 0:
		resolver = approver.NewProjectMgrResolver()
		appCtx.ProjectID = task.AssigneeProjectId
	case task.AssigneeTempTeamId != 0:
		resolver = approver.NewTempTeamResolver()
		appCtx.TeamID = task.AssigneeTempTeamId
	default:
		return ""
	}
	approvers, err := resolver.Resolve(ctx, e.client, appCtx)
	if err != nil || len(approvers) == 0 {
		e.logger.Infow(
			"固定范围审批人解析失败，转候选组兜底",
			"resolverType", resolver.GetType(), "error", err,
		)
		return ""
	}
	if requester != nil && approvers[0].UserID == requester.ID {
		e.logger.Infow(
			"固定范围解析出的审批人是申请人本人，转候选组兜底，避免自己审批自己",
			"resolverType", resolver.GetType(), "requesterID", requester.ID,
		)
		return ""
	}
	return strconv.Itoa(approvers[0].UserID)
}
```

- [ ] **Step 6: 重构 `createUserTask` 的优先级链**

把 `itsm-backend/service/bpmn_process_engine.go` 里 `createUserTask` 函数中，从"如果BPMN没有定义分配人..."这段 `if assignee == "" { ... }` 开始，到"展开 candidateGroups 为具体用户..."那个 `if e.groupResolver != nil { ... }` 块结束（包括中间的候选组兜底判断），整体替换成：

```go
	// 角色查询解析出的候选人列表——跟 candidateGroups 展开不是同一条路径（角色不等于组），
	// 但最终都要合并进 candidate_users、排除申请人自己，所以先收集，稍后统一处理。
	var roleCandidates []string

	// 如果BPMN没有定义分配人，从流程变量中获取
	if assignee == "" {
		if task.TaskPurpose == "approval" {
			switch {
			case strings.TrimSpace(task.CandidateGroups) != "" || strings.TrimSpace(task.CandidateUsers) != "":
				// BPMN 已经显式声明了 candidateGroups/candidateUsers（比如 legacy 审批链迁移出来的
				// 按角色/组路由节点，见 legacy_approval_migration_service.go，或者流程设计器直接
				// 指定了候选人），说明这个节点的路由方式是配置驱动的，不触发下面任何自动解析——
				// 避免用一个跟节点配置无关的语义覆盖它。
			case strings.TrimSpace(task.AssigneeRole) != "":
				// 按角色查这个租户里所有 active 且该角色的用户，全部作为候选人（不是挑一个人
				// 直接指派）——同一个角色可能有多人，候选人列表让谁先领谁审批，不会因为引擎
				// 武断选中的那个人离职/请假就卡死。查询语义复用 approval_service.go
				// resolveApprover "role" 分支的过滤条件（RoleEQ + TenantID + Active）。
				candidates, err := e.resolveRoleCandidates(ctx, instance.TenantID, task.AssigneeRole)
				if err != nil {
					e.logger.Warnw("按角色解析候选审批人失败，转候选组兜底", "role", task.AssigneeRole, "error", err)
				} else {
					roleCandidates = candidates
				}
			case task.AssigneeDeptId != 0 || task.AssigneeTeamId != 0 || task.AssigneeProjectId != 0 || task.AssigneeTempTeamId != 0:
				// BPMN 声明了固定范围的组织路由（部门/团队/项目/临时团队负责人，范围钉死在配置的
				// 具体 ID 上，不取申请人的）——四个 resolver 都是"至多解析出一个人"的形状，
				// 跟下面 default 分支的"申请人自己部门"解析方式一样，只是 appCtx 的范围来源不同。
				assignee = e.resolveFixedScopeAssignee(ctx, instance, approvalRequester, task)
				if assignee == "" {
					// 固定范围没配置/解析失败/解析出来是申请人自己——退到"申请人自己部门"这一级，
					// 而不是直接跳到候选组兜底，保持跟原有优先级链兼容。
					assignee = e.resolveApprovalAssignee(ctx, instance, approvalRequester)
				}
			default:
				// 都没声明：解析申请人自己所在部门的负责人（这次会话早前已经做的部分）
				assignee = e.resolveApprovalAssignee(ctx, instance, approvalRequester)
			}
		} else {
			// 优先使用 requester_id（工单申请人）
			assignee = getUserID("requester_id")
			// 其次使用 triggered_by（触发者）
			if assignee == "" {
				assignee = getUserID("triggered_by")
			}
			// 再其次使用 assignee_id
			if assignee == "" {
				assignee = getUserID("assignee_id")
			}
			// 如果还是没有，根据任务名称自动分配
			if assignee == "" {
				assignee = e.getDefaultAssigntee(ctx, instance, task)
			}
		}
	}

	// 审批任务如果自动解析都没有产出结果（部门负责人解析失败/角色查询没找到候选人/BPMN
	// 也没有声明 candidateGroups），兜底用固定候选组，保证任务始终有机会被领取。
	candidateGroupsToExpand := task.CandidateGroups
	if task.TaskPurpose == "approval" && assignee == "" && len(roleCandidates) == 0 && strings.TrimSpace(candidateGroupsToExpand) == "" {
		candidateGroupsToExpand = approvalFallbackCandidateGroup
	}

	// 展开 candidateGroups 为具体用户，合并到 candidate_users。
	// 这样「我的待办」接口才有可能查到分配给我的任务。
	expandedCandidateUsers := task.CandidateUsers
	if e.groupResolver != nil && strings.TrimSpace(candidateGroupsToExpand) != "" {
		_, groupUsernames, err := e.groupResolver.ExpandGroupsToUsers(ctx, instance.TenantID, candidateGroupsToExpand)
		if err != nil {
			// 解析失败：记录警告但不阻塞流程，以免审批组配置漂移导致整个流程中断
			e.logger.Warnw(
				"审批组展开失败，继续仅使用 BPMN candidateUsers",
				"taskID", task.ID,
				"candidateGroups", candidateGroupsToExpand,
				"error", err,
			)
		} else {
			if task.TaskPurpose == "approval" && approvalRequester != nil {
				groupUsernames = excludeUserFromCandidates(groupUsernames, approvalRequester)
			}
			expandedCandidateUsers = e.groupResolver.MergeCandidateUsers(task.CandidateUsers, groupUsernames)
			e.logger.Infow(
				"审批组已展开",
				"taskID", task.ID,
				"candidateGroups", candidateGroupsToExpand,
				"expandedUsers", groupUsernames,
			)
		}
	}
	if task.TaskPurpose == "approval" && len(roleCandidates) > 0 {
		// 按角色查出来的候选人，排除申请人自己，合并进 candidate_users——跟 candidateGroups
		// 展开是互斥的两条路径（见上面的 switch），这里不会重复合并同一批人。
		filtered := roleCandidates
		if approvalRequester != nil {
			filtered = excludeUserFromCandidates(filtered, approvalRequester)
		}
		expandedCandidateUsers = e.groupResolver.MergeCandidateUsers(expandedCandidateUsers, filtered)
		e.logger.Infow(
			"按角色的候选审批人已展开",
			"taskID", task.ID,
			"role", task.AssigneeRole,
			"expandedUsers", filtered,
		)
	}
	if task.TaskPurpose == "approval" && assignee == "" && strings.TrimSpace(expandedCandidateUsers) == "" {
		e.logger.Warnw(
			"审批任务没有解析到任何审批人（自动分配全部失败，候选组/候选角色展开后也为空），任务将无人可领",
			"taskID", task.ID,
			"taskName", task.Name,
			"candidateGroups", candidateGroupsToExpand,
		)
	}
```

（`ProcessTask.Create()...` 及之后的代码不变，保留原样。）

- [ ] **Step 7: 运行测试，确认全部通过**

Run: `cd itsm-backend && go test ./service/... -run 'TestCreateUserTask_Approval|TestCreateUserTask_NonApproval|TestAuthorizeTaskActor|TestClaimTask|TestExcludeUserFromCandidates' -v`
Expected：全部 PASS，包括这次新增的和之前已经存在的（回归确认没破坏自审批修复那批测试）。

- [ ] **Step 8: 跑整个 service 包和全量测试，确认没有回归**

Run: `cd itsm-backend && go build ./... && go test ./service/... -v 2>&1 | tail -150`
Expected：编译通过；重点确认 `bpmn_process_engine_test.go`、`bpmn_process_engine_ext_test.go`、`bpmn_approval_bridge_service_test.go`、`bpmn_template_service_test.go`、`legacy_approval_migration_service_unit_test.go` 全部继续 PASS。

Run: `cd itsm-backend && go test ./... 2>&1 | grep -v "^ok"`
Expected：没有 FAIL 输出。

- [ ] **Step 9: Commit**

```bash
cd itsm-backend
git add service/bpmn_types.go service/bpmn_process_engine.go service/bpmn_process_engine_approval_assignment_test.go
git commit -m "feat(bpmn): resolve approval assignees by role and fixed org scope

Adds two new declarative resolution levels to createUserTask's
approval branch, reusing the existing service/approver resolvers:
assigneeRole (queries all active users with a given ent/user.Role,
populates candidate_users -- multiple people can share a role) and
assigneeDeptId/assigneeTeamId/assigneeProjectId/assigneeTempTeamId
(fixed-scope org routing to a single resolved manager, mirroring the
existing requester's-own-department resolution). Both exclude the
requester from the result, consistent with the self-approval fix.
Lays the groundwork for the upcoming approval-to-BPMN convergence
work (see docs/superpowers/specs/2026-08-08-approval-bpmn-convergence-design.md)."
```
