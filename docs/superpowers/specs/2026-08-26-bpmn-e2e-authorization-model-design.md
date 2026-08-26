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

**范围**:除了最初列出的 `authorizeTaskViewer`/`authorizeTaskMutation`/`authorizeCounterSignViewer`,`authorizeTaskMutation` 的同类函数 `authorizeTaskActor`(`service/bpmn_process_engine.go:542-567`,把守 `CompleteTask`/`CompleteTaskByID`/`SubmitTaskDecision`/`Vote` 四个接口)有完全相同的 `if userID <= 0 { return nil }` 隐式放行模式,一并纳入本组件的改造范围——否则会在同一批 `/bpmn/tasks/*` 接口里留下"一部分显式声明、一部分隐式推导"的新碎片化。`isTaskCandidate`(把守 `ClaimTask`/`ClaimTaskByID`)没有这个模式(它直接按传入的 `userID` 解析身份,`userID<=0` 时 `ResolveCallerIdentity` 会因为查不到用户而报错,天然 fail-closed),不需要改动。

**审计结论(设计阶段已核实,非待办)**:全代码库搜索确认,`GetTask`/`GetTaskByID`/`AssignTask`/`CancelTask`/`SetTaskVariables`/`CreateCounterSignTasks`/`GetCounterSignStatus`/`CompleteTask`/`CompleteTaskByID`/`Vote` 这些公开方法,除了 HTTP controller(`controller/bpmn_workflow_controller.go`)和测试代码,**没有任何其它调用方**——也就是说"系统调用隐式放行"这条分支,今天在生产代码里从未被真正触发过。因此 `BPMNSystemCallerContextKey` 机制在实施阶段的落地是纯粹的"收紧默认行为 + 为未来预留显式声明入口",不需要额外寻找、迁移现存的系统调用方——但仍然要把这四个函数原有的"没传 userID 就放行"测试用例,改成显式测试"没有系统调用声明 + 没有用户身份 = 拒绝"。

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
    {Method: "GET", Path: "/bpmn/tasks/:id", Primitive: AuthPrimitiveTaskViewer, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/assign", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/claim", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/complete", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
    {Method: "POST", Path: "/bpmn/tasks/:id/decisions", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/cancel", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/variables", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
    {Method: "POST", Path: "/bpmn/tasks/:id/counter-sign", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
    {Method: "GET", Path: "/bpmn/tasks/:id/counter-sign-status", Primitive: AuthPrimitiveCounterSignViewer, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: false},
    {Method: "PUT", Path: "/bpmn/tasks/:id/vote", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
    {Method: "GET", Path: "/bpmn/process-instances", Primitive: AuthPrimitiveParticipantScoped, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: false},
    {Method: "GET", Path: "/bpmn/process-instances/:id", Primitive: AuthPrimitiveTaskViewer, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: false},
}
```

上表 `AllowSystemCaller` 全部为 `false`——这不是一个待定的保守默认值,而是设计阶段已经做完的审计结论(见组件 2):这些接口今天在代码库里没有任何真实的非 HTTP 内部调用方,`BPMNSystemCallerContextKey` 是为未来预留的显式声明入口,不是要迁移某个已知的现存调用方。如果实施阶段(或未来任何一次改动)确实需要让某个接口支持系统调用,必须显式把对应行的 `AllowSystemCaller` 改成 `true` 并写明原因——不允许为了让某个新用例跑通就静默改动而不留痕迹。

配套守卫测试(`controller/bpmn_workflow_controller_authz_registry_test.go`):遍历 `BPMNWorkflowController.RegisterRoutes` 实际注册的路由(通过一个临时 `gin.Engine` + 路由自省,或直接复用现有测试里构造路由树的方式),对每一条 `/bpmn/tasks/*`、`/bpmn/process-instances/*` 路由,断言它出现在 `BPMNTaskInstanceAuthRegistry` 里且方法匹配;反向断言登记表里的每一条也确实被注册了(防止登记表本身腐化成"越修越对不上"的死文档)。新增接口如果没有登记,这个测试直接失败。

### 组件 4:`/my-approvals` 修复

已被组件 1 覆盖(路由准入改为仅需登录 + 菜单 `PermissionCode` 改为 `bpmn:read`),不需要额外的独立改动。

## 权限种子迁移

新建迁移文件 `migrations/20260826_bpmn_permission_model_e2e_fix.sql`(命名对齐既有惯例,如 `20260812_fill_missing_permissions.sql`):

1. 从 `dept_manager`、`end_user`、`service_catalog_admin` 的 `role_permissions` 中删除 `task:read`/`process_instance:read`/`task:update`(存在才删,`DELETE ... WHERE EXISTS` 风格,幂等)。
2. 更新 `menus` 表中"我的待办"菜单项的 `permission_code` 字段,从 `task:read` 改为 `bpmn:read`。
3. 同步更新 `pkg/seeder/seeder.go` 里对应的角色权限清单和菜单种子定义,保证新建租户和迁移后的存量租户权限数据一致(这是 AGENTS.md 强调的"幂等 seed/migration"要求)。

迁移文件的注释里必须写清楚"改动前"的完整状态(被删除的每一条 `role_permissions` 行、菜单项改动前的 `permission_code` 原值),不是只写"改了什么",而是写"从什么改成什么"——这是为了让未来万一需要撤销这次改动时,能像 `migrations/20260814_revert_end_user_overgrant.sql` 撤销 `20260814_end_user_missing_permissions.sql`/`20260814_missing_permission_definitions.sql` 那样,直接照着注释写一条新的正向撤销迁移。**这个仓库里 `migrations/*.sql` 目前是单向 forward-only 迁移(已核实:没有找到这些 `.sql` 文件被接入 `migration/migrator.go` 里 `RollbackSQL`/`RollbackMigration` 机制的代码路径——那套机制服务于另一批 Go 结构体定义的迁移,不是这个目录),撤销的方式是"再写一条新迁移",不是执行某种自动回滚,所以不在这里承诺一个不存在的"down 迁移自动化测试"。**

## 风险与应对

| 风险 | 描述 | 应对 |
|---|---|---|
| 残留隐式放行 | 组件 2 覆盖 `authorizeTaskViewer`/`authorizeTaskMutation`/`authorizeCounterSignViewer`/`authorizeTaskActor` 四个函数,均已在设计阶段核实无任何真实非 HTTP 调用方(见组件 2 审计结论)。风险在于实施阶段落地这几个函数的改动到 PR 合入之间,可能有新代码引入了新的隐式依赖。 | 实施阶段落地这四个函数的改动之前,重新执行一次 `grep -rn "BPMNUserIDContextKey\|userID <= 0" service/ controller/` 确认审计结论仍然成立,而不是直接采信设计阶段的结论。 |
| 权限码残留引用未同步 | "我的待办"菜单 `PermissionCode` 从 `task:read` 改成 `bpmn:read` 后,如果前端或其它模块还有硬编码依赖 `task:read` 的地方没跟着改,会产生新的不一致。 | 已在设计阶段核实:`itsm-frontend/src` 全文搜索 `task:read` 零命中;后端除 `pkg/seeder/seeder.go`(本设计要改的目标)和 `service/bpmn_process_engine.go`/`service/bpmn/handler_base.go`(`hasElevatedBPMNAccess` 的 `"task","read"` 字符串参数,属于保留不变的提权检查代码,不是要清理的对象)外无其它引用。实施阶段仍需在改动落地后重新跑一次同样的全局搜索确认没有新代码在此期间引入新的硬编码引用。 |
| 登记表手工维护漂移 | `BPMNTaskInstanceAuthRegistry` 是手写的 Go 切片,理论上有"改了路由忘了改登记表"的风险。 | 已通过组件 3 的守卫测试正面解决(登记表和实际注册路由双向比对,不一致直接测试失败)——这就是防漂移机制本身,不需要额外引入 `go generate`/YAML 配置等更重的代码生成工具链;当前接口数量(13 条)规模下,额外的生成工具链只会增加维护成本,不会带来生成失败时测试无法捕获的场景生成工具本身也解决不了的问题。 |
| 迁移导致权限意外缺失 | 收紧 `dept_manager`/`end_user`/`service_catalog_admin` 的权限码,如果范围判断有误,可能意外导致这些角色的其它依赖被波及。 | 见上文迁移文件注释要求(记录改动前状态,便于后续撤销);验证计划里的"迁移脚本正向验证"项(见下)覆盖迁移后这三个角色仍能正常访问自己的任务/流程实例;`tests/rbac` 目录下现有的角色权限回归测试需要在迁移后重新跑一遍,确认没有意外波及这三个角色的其它权限。 |

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
