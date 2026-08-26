# BPMN 任务/流程实例权限模型端到端整改 Design

## 背景

`2026-08-25-bpmn-task-instance-authorization` 分支(9 个任务 + 最终评审修复轮)已经把 `ListProcessInstances`/`GetTask`/`GetTaskByID`/`ListUserTasks`/`AssignTask`/`CancelTask`/`SetTaskVariables`/`CreateCounterSignTasks`/`GetCounterSignStatus`/`Vote` 这些接口从"只有租户隔离,没有参与者级别授权"修到了"参与者/发起人/提权,三种身份分别可见,且跨任务全部复用同一套 `bpmn.CallerIdentity` 判定逻辑"。

但这轮工作本身是逐任务、逐接口修的——修完之后复核代码库其它部分,发现权限模型在更高层面仍然是碎片化的:同一个权限码 `task:read`/`process_instance:read` 同时承担了三个语义完全不同的职责(路由准入 / 提权判断 / 菜单可见性),而这三者是在不同时间(RBAC 双轨制收敛分支种下权限数据、本分支新建提权判断逻辑)由不同工作分别决定的,彼此没有对齐过。本设计的目标就是把这层"权限码复用导致的语义冲突"连根拔起,并建立一套机制防止以后再出现同类碎片化。

## 现状核实(均已对照当前代码库验证,非推测)

### 1. `task:read`/`process_instance:read` 三重职责冲突

| 职责 | 机制 | 具体位置 |
|---|---|---|
| ① 路由访问门槛 | `middleware.RequirePermission("task"/"process_instance","read")` | `router/router.go:601`(`/api/v1/tenant/my-approvals`)、`:1398`(`/api/v1/workflow/instances`)、`:1405`(`/api/v1/workflow/tasks`) |
| ② 提权信号("是否可见租户内全部数据") | `hasElevatedBPMNAccess(ctx, "task"/"process_instance", "read")`(`controller/bpmn_workflow_controller.go`,Task 4-8 引入) | `ListProcessInstances`/`GetTask`/`ListUserTasks`/`AssignTask`/`CancelTask`/`SetTaskVariables`/`CreateCounterSignTasks`/`GetCounterSignStatus`/`Vote` |
| ③ 菜单可见性 | `PermissionCode: "task:read"` | `pkg/seeder/seeder.go:1499`("我的待办"菜单项,全文件唯一一处) |

默认种子数据(`pkg/seeder/seeder.go`)里,`task:read` 分发给:`change_manager`(:1782)、`service_catalog_admin`(:1800)、`dept_manager`(:1871)、`end_user`(:1911),外加 `it_director`/`ops_director`(通过 `allExcept()`——"全部权限减去一个黑名单"派生,`task:read` 不在黑名单内)、`sysadmin`(`migrations/20260812_fill_missing_permissions.sql:114`)、`super_admin`(`middleware/rbac.go:579` 硬编码短路返回 `true`,不查表)。

`middleware.RequireLegacyBPMNRoles()`(`middleware/rbac.go:567`)= `RequireRole("super_admin","change_manager","dept_manager","end_user","it_director","ops_director","sysadmin")`——这 7 个角色恰好就是上面 `task:read` 的持有者全集。`/api/v1/bpmn/process-instances` 等 `managed` 分组接口用这个角色白名单把关(`controller/bpmn_workflow_controller.go:88-91`);`/api/v1/bpmn/tasks/*`(含 `GetTask`/`ListUserTasks`/4 个任务操作/`GetCounterSignStatus`/`Vote`)则完全不设路由级门槛(`controller/bpmn_workflow_controller.go:150-163`,原因见该文件内注释:候选组可以是任意角色名,不受限于 7 角色集,已由 SSL-VPN E2E 测试反证过粗粒度门槛会误拒合法候选人)。

**结果**:凡是能碰到这些 BPMN 接口的角色,默认情况下(除极少数自定义角色外)几乎全部同时持有 `task:read`,因此也全部会被 ② 判定为"已提权",直接看到租户内所有人的任务——本分支 9 个任务建立的参与者范围限定,在默认权限种子下对多数真实用户不产生实际限制效果。`process_instance:read` 的分发范围略窄(`dept_manager`/`end_user` 确认没有),但 `change_manager`/`it_director`/`ops_director`/`sysadmin` 仍会被误判为提权。

### 2. 系统/内部调用的隐式放行

`authorizeTaskViewer`/`authorizeTaskMutation`/`authorizeCounterSignViewer`(`service/bpmn_process_engine.go`)在 `ctx.Value(bpmn.BPMNUserIDContextKey)` 取不到正整数、且未提权时,直接放行(`if userID <= 0 { return nil }`)——设计意图是允许系统/内部调用(如工单创建自动触发流程),但这个身份是靠"context 里没塞用户 ID"这个副作用隐式推导的,不是显式声明。

### 3. `GetCounterSignStatus` 之前完全没有授权检查——本轮已修,但暴露了"接口清单"缺失的问题

最终评审发现并已修复:`GetCounterSignStatus` 此前既没有租户过滤也没有任何授权检查,是这次分支最严重的一个遗漏(见该分支 ledger)。它之所以被漏掉,是因为这批接口从未有过一张"每个接口对应哪种授权原语"的清单——全靠逐个任务手动排查。本设计要把这张清单变成结构化数据,并配自动化守卫,防止下一个 `GetCounterSignStatus` 式的遗漏。

## 目标与不变式

1. **参与者范围判断与提权判断,必须是两个独立的权限维度**,不能复用同一个"能否使用基础功能"的权限码。
2. **系统/内部调用必须显式声明**,不允许靠"没有用户身份"这一隐式信号放行。
3. **每一个 `/bpmn/tasks/*`、`/bpmn/process-instances/*` 接口,必须在一张集中登记表里有对应条目**,登记表驱动路由注册和自动化测试,清单之外的接口 = 测试失败,不允许"漏登记"却仍能正常提供服务。
4. 不改变本分支已经建好的参与者范围限定本身(`authorizeTaskViewer`/`authorizeTaskMutation`/`authorizeCounterSignViewer`/`ListUserTasks`/`ListProcessInstances` 的核心判定逻辑),只修正它们依赖的权限码语义和路由准入方式。

## 设计

### 组件 1:权限码职责拆分

- **路由准入**:`/api/v1/tenant/my-approvals`、`/api/v1/workflow/tasks`、`/api/v1/workflow/instances` 三条路由去掉 `middleware.RequirePermission("task"/"process_instance","read")`,只保留上层 `auth`/`tenant` 分组已有的 `AuthMiddleware`+`TenantMiddleware`(即:登录即可访问,可见范围由 handler 内部的参与者范围逻辑控制,该逻辑本分支已经建好)。
- **提权信号**:`task:read`/`process_instance:read`/`task:update` 保留现有语义和检查代码(`hasElevatedBPMNAccess` 不变),但收紧种子分发范围——只保留给真正的管理/运维角色:`sysadmin`、`it_director`、`ops_director`、`super_admin`(硬编码,天然提权)、`change_manager`(需要跨人查看变更审批进度,确认算提权角色)。**从 `dept_manager`、`end_user`、`service_catalog_admin` 的默认种子权限中移除** `task:read`/`process_instance:read`/`task:update`(`service_catalog_admin` 目前只有 `task:read`/`process_instance:read`,没有 `task:update`,一并收紧)。
- **菜单可见性**:"我的待办"菜单项(`pkg/seeder/seeder.go:1499`)的 `PermissionCode` 从 `task:read` 改为 `bpmn:read`——`bpmn:read` 已广泛分发给 `change_manager`/`dept_manager`/`end_user`(加上 `it_director`/`ops_director`),且代码库里其它 BPMN 菜单项已经用 `bpmn:read` 做可见性门槛(而非 API 访问门槛),这是复用既有惯例,不是新发明一套规则。

### 组件 2:显式系统调用声明

新增 `service/bpmn` 包下的 `BPMNSystemCallerContextKey`(与已有的 `BPMNUserIDContextKey`/`BPMNTenantIDContextKey`/`BPMNElevatedContextKey` 同级)。系统/内部触发路径(工单创建自动触发流程等)显式 `context.WithValue(ctx, bpmn.BPMNSystemCallerContextKey, true)`。

`authorizeTaskViewer`/`authorizeTaskMutation`/`authorizeCounterSignViewer` 的放行条件从"`userID<=0` 就放行"改为:

```go
if systemCaller, _ := ctx.Value(bpmn.BPMNSystemCallerContextKey).(bool); systemCaller {
    return nil // 显式声明的系统调用，放行
}
if elevated, _ := ctx.Value(bpmn.BPMNElevatedContextKey).(bool); elevated {
    // 已有提权逻辑不变
}
userID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
if userID <= 0 {
    return fmt.Errorf("未认证的调用") // 默认拒绝，不再隐式放行
}
```

需要排查现有代码里所有依赖"`userID<=0` 隐式放行"这条路径的真实系统调用点(预计集中在工单创建自动触发 BPMN 流程的路径),逐一改为显式声明,而不是假设当前没有真实依赖方。

### 组件 3:集中授权登记表 + 自动化守卫

新建 `service/bpmn/authorization_registry.go`:

```go
type AuthPrimitive string

const (
    AuthPrimitiveNone                AuthPrimitive = "none"                  // 无需授权（不应存在于 tasks/instances 清单里，仅供未来扩展占位）
    AuthPrimitiveTaskViewer           AuthPrimitive = "task_viewer"           // authorizeTaskViewer
    AuthPrimitiveTaskMutation         AuthPrimitive = "task_mutation"         // authorizeTaskMutation
    AuthPrimitiveCounterSignViewer    AuthPrimitive = "counter_sign_viewer"   // authorizeCounterSignViewer
    AuthPrimitiveTaskActor            AuthPrimitive = "task_actor"            // authorizeTaskActor / isTaskCandidate（claim/complete/decisions/vote 的候选人级校验）
    AuthPrimitiveParticipantScoped    AuthPrimitive = "participant_scoped"    // ListUserTasks / ListProcessInstances 的强制收敛到自身身份
)

type RouteAuthEntry struct {
    Method             string        // "GET" / "POST" / "PUT" / "DELETE"
    Path               string        // gin 路由模式，如 "/bpmn/tasks/:id"
    Primitive          AuthPrimitive
    ElevatedResource   string        // 空字符串表示该接口没有提权概念
    ElevatedAction     string
    AllowSystemCaller  bool          // 是否允许 BPMNSystemCallerContextKey 放行
}

var BPMNTaskInstanceAuthRegistry = []RouteAuthEntry{
    {Method: "GET", Path: "/bpmn/tasks", Primitive: AuthPrimitiveParticipantScoped, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: false},
    {Method: "GET", Path: "/bpmn/tasks/:id", Primitive: AuthPrimitiveTaskViewer, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: true},
    {Method: "PUT", Path: "/bpmn/tasks/:id/assign", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/claim", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/complete", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
    {Method: "POST", Path: "/bpmn/tasks/:id/decisions", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/cancel", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/variables", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
    {Method: "POST", Path: "/bpmn/tasks/:id/counter-sign", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
    {Method: "GET", Path: "/bpmn/tasks/:id/counter-sign-status", Primitive: AuthPrimitiveCounterSignViewer, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: true},
    {Method: "PUT", Path: "/bpmn/tasks/:id/vote", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
    {Method: "GET", Path: "/bpmn/process-instances", Primitive: AuthPrimitiveParticipantScoped, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: false},
    {Method: "GET", Path: "/bpmn/process-instances/:id", Primitive: AuthPrimitiveTaskViewer, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: true},
}
```

上表 `AllowSystemCaller` 的取值遵循一条保守默认原则:**只读类原语(`task_viewer`/`counter_sign_viewer`/`participant_scoped`)默认允许系统调用,写类原语(`task_mutation`/`task_actor`)默认不允许**——因为写操作一旦被系统调用绕过参与者校验,后果比读操作被绕过更严重,而目前代码库里已知的系统触发场景(工单创建自动触发流程)只涉及"启动流程"而非"以系统身份操作某个具体任务"。实施计划的第一个任务必须审计代码库里所有真实的非 HTTP 内部调用点(即所有不经过 `RegisterRoutes` 注册的 handler、直接调用 `authorizeTaskViewer`/`authorizeTaskMutation`/`authorizeCounterSignViewer` 所在函数的调用方),用审计结果校正上表——审计发现的任何反例（比如某个写类调用确实需要被系统内部触发）都必须回填进这张表并写明原因,不能为了让审计通过而静默放宽默认原则。

配套守卫测试(`controller/bpmn_workflow_controller_authz_registry_test.go`):遍历 `BPMNWorkflowController.RegisterRoutes` 实际注册的路由(通过一个临时 `gin.Engine` + 路由自省,或直接复用现有测试里构造路由树的方式),对每一条 `/bpmn/tasks/*`、`/bpmn/process-instances/*` 路由,断言它出现在 `BPMNTaskInstanceAuthRegistry` 里且方法匹配;反向断言登记表里的每一条也确实被注册了(防止登记表本身腐化成"越修越对不上"的死文档)。新增接口如果没有登记,这个测试直接失败。

### 组件 4:`/my-approvals` 修复

已被组件 1 覆盖(路由准入改为仅需登录 + 菜单 `PermissionCode` 改为 `bpmn:read`),不需要额外的独立改动。

## 权限种子迁移

新建迁移文件 `migrations/20260826_bpmn_permission_model_e2e_fix.sql`(命名对齐既有惯例,如 `20260812_fill_missing_permissions.sql`):

1. 从 `dept_manager`、`end_user`、`service_catalog_admin` 的 `role_permissions` 中删除 `task:read`/`process_instance:read`/`task:update`(存在才删,`DELETE ... WHERE EXISTS` 风格,幂等)。
2. 更新 `menus` 表中"我的待办"菜单项的 `permission_code` 字段,从 `task:read` 改为 `bpmn:read`。
3. 同步更新 `pkg/seeder/seeder.go` 里对应的角色权限清单和菜单种子定义,保证新建租户和迁移后的存量租户权限数据一致(这是 AGENTS.md 强调的"幂等 seed/migration"要求)。

## 验证计划

- 每个受影响接口(`ListProcessInstances`/`GetTask`/`ListUserTasks`/4 个任务操作/`GetCounterSignStatus`/`Vote`)沿用本分支已建立的四态覆盖:参与者/非参与者/跨租户/提权。
- **重点回归**:收紧 `task:read`/`process_instance:read`/`task:update` 种子分发范围后,必须重新跑 `tests/e2e/sslvpn_scenario_test.go`——该测试曾经在 RBAC 收敛分支里实测证伪过一次"粗粒度角色门槛误拒合法候选人"的回归,这次改权限种子数据是同一类风险,不能只看单元测试通过就下结论。
- 新增：授权登记表守卫测试(组件 3),验证登记表与实际路由一致。
- 新增：迁移脚本的正向验证(迁移后 `dept_manager`/`end_user`/`service_catalog_admin` 三个角色确认不再持有被收紧的权限码,但仍能通过 `/my-approvals`/`/workflow/tasks`/`/workflow/instances` 看到自己参与的任务/实例)。
- 系统调用显式声明改造后,需要跑一遍工单创建自动触发 BPMN 流程的现有集成测试,确认没有真实系统调用点因为"隐式放行"被移除而意外 403。

## 非目标(明确排除)

- `Group.members` 单一外键、无法支持一人多组的数据模型限制——独立的更大改造,不在本设计范围内。
- MSP 角色(`msp_provider_admin` 等)的权限种子——这些角色目前在 `roles` 表里没有数据行,是独立于本设计的已知 backlog。
- ADR-0001 的 `endpoint_acls` DB 驱动权限模型——已知的更大架构方向,本设计不预先实现它,只是不破坏未来迁移到它的可能性。
