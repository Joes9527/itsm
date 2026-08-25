# 组织架构与角色建模：总经理 / 分公司负责人 / 一人多角色

日期：2026-08-19
状态：待实施

## 背景

上一轮工作里，为了让"Copilot采购申请"这个测试场景走通"用户提交 → 部门负责人 → 总经理 → IT-director"四级审批，给系统新增了一个 `company_gm`（总经理）角色，并在 BPMN 设计器里加了"按角色指派 (assigneeRole)"下拉框，让"总经理"这一审批环节走"按角色查全租户候选人"的路由方式。

用户随后从设计视角提出三个问题：

1. "总经理"从设计上应不应该是一个 role？
2. 一个 user 是否应该支持多个角色？
3. ITSM 的角色/用户层面应该如何支持集团-分公司的组织架构？

经过对代码库现状的调研和逐段讨论，结论是：**"总经理"不应该建模为角色，而应该建模为组织架构（部门树）根节点的负责人**；一人多角色的真实需求范围很窄（只影响 BPMN 候选资格，不影响 RBAC 权限判定）；集团-分公司架构可以直接复用现有的、支持任意深度的部门树，不需要新的租户层级概念。这份设计文档记录最终方案，并列出对上一轮 `company_gm` 实现的回滚清单。

## 调研结论（现状，作为设计依据）

- `ent/schema/user.go`：`User.role` 是纯字符串列（对应 `roles.code`），同时声明了 `edge.To("roles", Role.Type)` 多对多边（对应 `user_roles` 中间表），但**全仓库没有任何业务代码读写这条边**——是声明了但从未启用的死代码路径。
- `middleware/rbac.go`：权限判定唯一路径是 `user.role`（单字符串）→ 查 `roles.code` → 查 `role_permissions`。没有第二条不一致的权限路径。
- `ent/schema/tenant.go`：`parent_tenant_id` 明确是 **MSP 服务商-客户** 语义（"MSP客户指向MSP提供商"），不是集团-子公司语义，不能挪用。
- `ent/schema/department.go`：`parent_id` + `manager_id`，**无深度限制**，单一 `tenant_id` 下可以建任意深度的树。
- `service/approver/dept_manager_resolver.go`：`Resolve()` 查询指定 `DepartmentID` 的 `manager_id`；若为 0 则递归取 `parent_id` 向上找，直到找到有 manager 的祖先部门，或到达树顶（`parent_id=0` 且无 manager）报错。**当前实现无环路保护**，`parent_id` 成环会导致无限递归。
- `service/bpmn_process_engine.go` `resolveFixedScopeAssignee`（L1114-1139）：BPMN UserTask 若声明了 `assigneeDeptId`（非 0），走 `DeptManagerResolver` 解析**这个固定部门**（不是申请人自己的部门）的负责人，找不到就沿部门树向上找。此路径与"部门经理审批"（申请人自己部门）复用同一个 resolver，只是范围来源不同。
- `resolveRoleCandidates`（L1051-1070）：`WHERE user.role = ? AND tenant_id = ? AND active = true`，直接查单字符串列，不查 `user_roles` 边。
- 设计器（`WorkflowNodeInspector.tsx`）此前只暴露 `assignee`/`candidateUsers`/`candidateGroups`/`taskPurpose`；上一轮新增了 `assigneeRole`；`assigneeDeptId`/`assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId` 这几个引擎已支持的固定范围路由，**UI 和 moddle descriptor 都没有暴露**。
- `itsm-frontend/src/app/(main)/admin/departments/page.tsx`：部门管理表单已支持 `parentId`，可以建任意深度的部门树，**这块无需新增 UI**。
- `itsm-frontend/src/lib/services/department-service.ts`：`departmentService` 已提供 `Department{id,name,parentId,...}` 的 CRUD/list，可直接复用作为设计器里"固定部门审批人"下拉框的数据源。

## 范围

**本次设计覆盖**：
1. 撤销上一轮的 `company_gm` 角色（数据 + 代码 + 白名单）。
2. 设计器暴露 `assigneeDeptId`（固定部门审批人），用于总经理/分公司负责人这类"跟组织架构位置绑定、不看申请人自己部门"的审批环节。
3. `dept_manager_resolver.go` 增加环路检测。
4. 启用沉睡的 `user_roles` 多对多边，仅用于 BPMN 按角色查候选人时的资格判定（`resolveRoleCandidates`），不影响 RBAC 权限判定。

**明确不在本次范围内**（保留、不动）：
- `service_catalogs.process_definition_key`（服务目录条目专属流程）与设计器已有的 `assigneeRole`（按角色指派）下拉框——这两个跟总经理建模无关，是独立成立的能力（`assigneeRole` 对 `it_director` 这类"确实是权限包、不跟组织架构绑定"的角色仍然适用）。
- 真正的多租户集团-分公司隔离（分公司作为独立租户）——本次讨论已确认分公司是"共享系统的大部门"，不需要租户级隔离，因此不涉及 `tenant.go` 改动。
- RBAC 权限判定的多角色叠加——已确认真实需求只是 BPMN 候选资格，不动 `middleware/rbac.go`。

## 设计一：组织架构直接复用现有部门树

不引入新 schema。集团/分公司/部门/科室都是 `Department` 树上不同深度的节点：

- "公司"（集团或独立公司）= 部门树的根节点（`parent_id = 0`）。
- "分公司" = 根节点的直接子节点。
- 普通部门/科室 = 更深层节点。

管理员通过现有的 `/admin/departments` 页面搭建这棵树，指定每一级的 `manager_id`。不需要新增前端页面或后端 API。

## 设计二：总经理 / 分公司负责人 = 固定部门审批人（`assigneeDeptId`）

### 为什么不用角色

`resolveRoleCandidates` 是"按角色查全租户候选人"，跟组织架构位置无关；如果集团下有多个分公司、每个分公司都有自己的负责人，"按角色查"无法区分"这是哪个分公司的负责人"。而 `resolveFixedScopeAssignee` + `DeptManagerResolver` 天然按**具体部门 ID**解析，把这个 ID 设成公司根节点就是总经理，设成某个分公司节点就是那个分公司的负责人——精确匹配组织架构的语义，且是引擎里已经实现、已经在"部门经理审批"场景验证过的路径，只是从来没在 BPMN 设计器里把 `assigneeDeptId` 这个入口暴露给用户配置过（跟上一轮 `assigneeRole` 是完全一样的"引擎支持、UI 没接"缺口）。

### 改动

**后端**：
- `itsm-moddle-descriptor.ts` 补 `{ name: 'assigneeDeptId', isAttr: true, type: 'Integer' }`（不需要后端改动，`resolveFixedScopeAssignee` 已经支持）。

**前端**：
- `WorkflowNodeInspector.tsx`：在"受理人"/"按角色指派"旁边加一个"固定部门审批人 (assigneeDeptId)"选择器，数据源用现成的 `departmentService.getDepartments()`（下拉框按树形缩进展示，或至少显示 `部门名 (父部门名)` 避免同名部门混淆）。跟 `assignee`/`assigneeRole` 三选一互斥——设置其一清空另外两个（沿用上一轮 `assignee`/`assigneeRole` 互斥的模式，扩成三者互斥）。
- `apply({ assigneeDeptId: value || undefined, assignee: '', assigneeRole: '' })`，清空时其余两个不受影响（跟上一轮逻辑一致，只在选中新值时才清空另外两个）。

### 回滚 `company_gm`

具体改动清单（与上一轮新增的改动一一对应）：

| 文件 | 上一轮改动 | 本次处理 |
|---|---|---|
| `config/seed/default.json` | 在 `roles` 数组里加了"总经理"/`company_gm` | 删除该条目 |
| `pkg/seeder/seeder.go` | `getProductDefaultConfig()` 的 `Roles` 字面量加了同一条；`rolePermissionMap` 加了 `"company_gm": allExcept(...)` | 两处都删除 |
| `dto/user_dto.go` | `CreateUserRequest.Role` / `UpdateUserRequest.Role` 的 `oneof` 白名单加了 `company_gm` | 从两个白名单里删除 |
| 数据库 | `roles` 表已插入一行（id=37，`tenant_id=1`，无用户绑定） | 直接 `DELETE`；同时清理 `role_permissions` 里指向这个 role_id 的 160 行 |
| 前端 `WorkflowNodeInspector.tsx` / `itsm-moddle-descriptor.ts` | 加了 `assigneeRole` 支持 | **保留**（`assigneeRole` 本身是独立能力，服务于 `it_director` 这类权限包角色，不是本次回滚对象） |

回滚顺序：先确认 DB 里 `company_gm` 角色确实没有被任何用户实际使用（上一轮加完之后没有分配给任何测试账号），再按 数据库 → seeder.go → default.json → user_dto.go 的顺序改，最后重新种子验证角色数量恢复到 18。

## 设计三：一人多角色（仅 BPMN 候选资格）

### 数据面

复用 `ent/schema/user.go` 已声明的 `edge.To("roles", Role.Type)`（`user_roles` 中间表），不改 schema。

### 写入面

在用户编辑页新增一个"附加角色"多选字段，与主角色字段（决定 RBAC 权限的那个 `user.role` 单字符串）分开展示、分开提交，避免使用者误以为这是权限叠加：

- 复用现有 `PUT /api/v1/users/:id`（`UpdateUser`），不新增接口：`dto.UpdateUserRequest` 加一个可选字段 `AdditionalRoleIds []int \`json:"additionalRoleIds,omitempty"\``；`service/user_service.go` 的 `UpdateUser` 在字段非 nil 时调用 `client.User.UpdateOneID(id).ClearRoles().AddRoleIDs(req.AdditionalRoleIds...).Save(ctx)`（先清空再整体重设，语义等同于"提交的列表就是完整的附加角色集合"，避免增量 add/remove 的状态漂移）。
- 前端 `/admin/users` 编辑弹窗加"附加角色"`Select mode="multiple"`，选项复用已有的 `RoleAPI.getRoles()`（跟上一轮"角色"单选下拉框用同一个数据源，UI 上分成两个字段：单选"角色"（主角色，决定权限）+ 多选"附加角色（仅影响审批候选资格）"，并在附加角色字段旁加简短说明文字，避免用户误解成权限叠加）。

### 读取面

`resolveRoleCandidates` 从：

```go
users, err := e.client.User.Query().
    Where(user.RoleEQ(role), user.TenantIDEQ(tenantID), user.Active(true)).
    All(ctx)
```

改成：

```go
users, err := e.client.User.Query().
    Where(
        user.TenantIDEQ(tenantID),
        user.Active(true),
        user.Or(
            user.RoleEQ(role),
            user.HasRolesWith(role_.CodeEQ(role), role_.TenantIDEQ(tenantID)),
        ),
    ).
    All(ctx)
```

（`role_` 是 `ent/role` 包的别名，避免跟函数参数 `role string` 撞名；`user.HasRolesWith` 是 ent 为 `edge.To("roles", ...)` 自动生成的关联查询谓词。）

去重：如果同一个用户既满足 `RoleEQ` 又通过附加角色边匹配（理论上不会同时命中同一个角色码，但如果附加角色恰好等于主角色，`Or` 查询本身不会产生重复行，`ent` 的 `Query().All()` 按用户去重，不需要额外处理）。

## 错误处理：`dept_manager_resolver.go` 环路检测

设计二让"固定部门→向上找负责人"这条路径的调用频率和影响都上升了（总经理/分公司负责人都要走它），需要顺手补上环路保护：

```go
func (r *DeptManagerResolver) Resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext) ([]*ApproverInfo, error) {
    return r.resolve(ctx, client, appCtx, make(map[int]bool))
}

func (r *DeptManagerResolver) resolve(ctx context.Context, client *ent.Client, appCtx *ApproverContext, visited map[int]bool) ([]*ApproverInfo, error) {
    if visited[appCtx.DepartmentID] {
        return nil, fmt.Errorf("department hierarchy cycle detected at department %d", appCtx.DepartmentID)
    }
    visited[appCtx.DepartmentID] = true
    // ... 原有逻辑，递归调用改成 r.resolve(ctx, client, &parentCtx, visited)
}
```

对外签名（`Resolve`）不变，不影响调用方（`bpmn_process_engine.go` 里的其他调用点）。

## 测试计划

**后端**：
- `service/approver/dept_manager_resolver_test.go`（新建，目前没有专门的单测文件）：覆盖"三级部门树、根节点才有 manager，中间层递归找到"、"环路数据触发报错而不是无限递归"。
- `service/bpmn_process_engine_test.go`：补"用户通过附加角色（非主角色）能被 `resolveRoleCandidates` 选中"的用例；补"`assigneeDeptId` 固定部门审批人"经 `resolveFixedScopeAssignee` 正确解析、且不受申请人自己部门影响的用例。
- `dto/user_dto.go` 相关：确认 `company_gm` 从 oneof 白名单移除后，旧数据/迁移不受影响（不需要新增测试，属于纯删除）。

**前端**：
- `itsm-moddle-descriptor.test.ts` 补 `assigneeDeptId` 声明的断言。
- `/admin/users` 编辑表单新增"附加角色"字段的 Jest 用例（选中多个角色 → 提交 payload 正确）。

**端到端**：
用 Playwright 重跑一遍"Copilot采购申请"场景，但总经理审批环节改成设计器里选"固定部门审批人"指向公司根部门节点，验证：
1. 该环节的候选人正确解析为根部门的 `manager_id`，不是全租户所有 `company_gm` 用户（因为这个角色已经不存在了）。
2. 走完全部审批后流程实例到达 `EndEvent`（复用上一轮已验证的完成态检查方式）。
3. 一人多角色：给测试账号加一个附加角色，验证该账号能在"我的待办"里看到按这个附加角色路由的任务。

## 风险与未决问题

- `resolveFixedScopeAssignee` 目前四个固定范围（部门/团队/项目/临时团队）共用一个 `switch` 分支，`assigneeDeptId` 和 `assigneeTeamId` 等如果被同时设置只会生效第一个匹配的 `case`——设计器 UI 需要保证互斥，不能让用户同时填多个固定范围字段（目前設計器連 `assigneeTeamId`/`assigneeProjectId`/`assigneeTempTeamId` 都还没暴露，本次只加 `assigneeDeptId`，互斥范围只需要覆盖 `assignee`/`assigneeRole`/`assigneeDeptId` 三者）。
- "附加角色"字段的授权边界：谁有权限给别人加附加角色？建议复用 `UpdateUser` 现有的"角色越权防护"逻辑（`roleRank` 比较），但 `roleRank` 目前只认粗粒度的 `super_admin/admin/manager/agent/end_user`，大部分细分角色（`it_director`、`dept_manager` 等）都落在 `default: return 0`——这是现有代码的已知粗糙点，本次不打算修，只是提醒：附加角色的分配目前跟主角色一样，实际上没有基于角色层级的强约束，仅受 RBAC 的 `user:write` 权限门槛控制。
