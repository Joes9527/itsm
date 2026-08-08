# 修复工单默认审批流程"自己批自己" + 补齐缺失的 ticket_urgent_flow - 设计文档

## 背景与问题

上一轮 ServiceRequest→Ticket 委托重构的最终评审发现：`service/bpmn/ticket_general_flow.bpmn`（所有工单在没有匹配到更具体 `ProcessBinding` 时的兜底流程）的"工单审批"节点没有配置任何 `candidateGroups`/`candidateUsers`，导致审批任务的 assignee 在自动分配兜底逻辑下会落到申请人自己身上——申请人可以批准自己提交的工单。旧的 ServiceRequest 专属审批代码里有明确的"申请人不能审批自己的请求"校验，这次委托给 ticket 之后这条校验没有对应物。

调查过程中确认了两个相关但独立的问题：

1. **`ticket_general_flow` 是所有工单类型的兜底流程**（`process_resolver.go`：没有匹配到 `ProcessBinding` 时，incident/problem/change/service_request/普通工单都会落到这条流程），所以这个缺口影响面是全体工单，不只是服务请求。
2. **`process_resolver.go` 的 `ResolveWithPriority` 会把高/紧急优先级的兜底工单指向流程 key `"ticket_urgent_flow"`，但这个文件根本不存在**（`service/bpmn/` 目录下没有，`BPMNTemplateService` 的部署清单也没有这个 case）——任何没有特定流程绑定的高优先级工单，创建时会因为找不到流程定义直接报错失败。这是完全独立于 SR 委托重构的既有 bug，本次一并修复。

## 现状核实（代码走查，2026-08-08；2026-08-08 复审后修正两处事实错误，见下方标注）

- **`authorizeTaskActor`（`service/bpmn_process_engine.go:417`）用 OR 逻辑**：`allowed(task.Assignee) || allowed(task.CandidateUsers)`，其中 `allowed(csv)` 把 CSV 按逗号拆开，每一项跟 `strconv.Itoa(userID)` 或 `actor.Username` 比较，命中任意一项即放行。这意味着**光给 BPMN 文件加 `candidateGroups` 并不能关掉自己批自己的口子**——`Assignee` 字段是独立解析的，只要它还落到申请人身上，申请人依然能通过 `allowed(task.Assignee)` 这一支操作任务；同理，只要申请人本人的 ID 或用户名出现在 `CandidateUsers` CSV 里（哪怕是通过 `candidateGroups` 展开进去的），也一样能通过 `allowed(task.CandidateUsers)` 放行。**这条 OR 逻辑是本设计"两条路径都必须排除申请人自己"这一要求的直接依据**（见下方"修复范围"部分一）。
- **`createUserTask`（`service/bpmn_process_engine.go:617`）的 assignee 解析优先级**：BPMN 显式声明（XML `assignee` 属性）> `requester_id`（流程变量）> `triggered_by` > `assignee_id` > `getDefaultAssigntee`（数据库规则匹配兜底）。因为工单创建时 `requester_id` 几乎总能解析成功，`getDefaultAssigntee` 这套已经写好、更合理的兜底逻辑实际上从未被执行到。**后四级都在 `if assignee == ""` 分支内**，即只有 BPMN 没有显式声明 assignee 时才会走到——这一点是本设计"审批分支该插在哪一层"的关键前提（见下方"修复范围"部分一的"作用范围"说明）。
- **`Activity_Approval` 节点没有打 `taskPurpose="approval"` 标记**——这个属性 schema 层面已经支持（`service/bpmn_types.go:86`，`xml:"taskPurpose,attr"`），但当前所有 BPMN 文件都没用，代码没法专门区分"这是审批任务"。
- **这是系统性缺口，不是个别文件的疏漏**：全仓库 13 个 BPMN 文件（含本次要改的 `ticket_general_flow.bpmn`）、21 个 id/name 里带"审批"或英文 `Approval` 的用户任务节点（CAB审批、发布审批、事件主管审批、方案审批等）都没有配置 `taskPurpose`/`candidateGroups`。**（2026-08-08 复审修正：原文写的"12 个文件、20+ 个节点"是用 `grep 'bpmn:userTask'` 固定前缀统计出来的，漏掉了 `cloud_private_ops_flow.bpmn`/`cloud_public_ops_flow.bpmn` 这两个用不带 `bpmn:` 前缀写法（`<userTask ...>` 而非 `<bpmn:userTask ...>`）的文件——补上这两个文件重新统计，得到 13 文件/21 节点这个准确数字。刨除本次要处理的 `ticket_general_flow.bpmn` 自己那 1 个节点，"非目标"里明确留到后续的是 12 个文件、20 个节点，见该节修正。）**
- **`GroupResolver`（`service/bpmn/bpmn_group_resolver.go`）的接线基础设施已经写好，且已经在被调用**——`ExpandGroupsToUsers`/`MergeCandidateUsers` 已经被 `createUserTask`（`service/bpmn_process_engine.go:664-677`，2026-06-27 由 jake.liu 提交）调用：只要某个 `BPMNUserTask.CandidateGroups` 非空，无论 `TaskPurpose` 是什么，这段代码就会把组名展开成具体用户、合并进 `candidate_users`。`GetUserGroupNames` 另外被"我的待办"接口使用。配套的 `Group` 实体有完整的 CRUD（`service/group_service.go`）和管理界面（`/admin/groups`）。**（2026-08-08 复审修正：原文这里写的是"已经写好但完全未被引用的基础设施"，这是事实错误——接线本身早就存在，唯一缺的是"没有任何 `.bpmn` 文件在 XML 里配置 `candidateGroups` 属性"，也就是接线有、触发它的数据没有。这个修正不影响本设计"复用已有 `GroupResolver`"这个结论本身，但会影响实现范围的判断：不需要新写组展开逻辑或新增接线，只需要在 XML 里配置属性、并在展开结果里补上申请人排除过滤。）**
- **`Department.manager_id`（`ent/schema/department.go:28`）已存在**，`User` 有 `department_id` + `department_ref` 边（`ent/schema/user.go:37,79`），支持"申请人所在部门 → 部门负责人"这条查询路径。
- **`service/approver/dept_manager_resolver.go` 已经实现了"部门 → manager"解析，比本设计原先打算写的版本更完善**——`DeptManagerResolver.Resolve(ctx, client, appCtx)` 接受 `*approver.ApproverContext{TenantID, DepartmentID}`，查询 `Department`（按 `tenant_id` + `id` + 未软删）；`manager_id == 0` 时不是直接报错，而是**递归查父部门**（`dept.ParentID`），一路查到有 manager 或者到根还没有才失败；查到 `manager_id` 后还会校验该用户 `tenant_id` 匹配且 `active == true`，返回 `[]*ApproverInfo{{UserID, UserName, UserEmail, Role: "department_manager", Source: "department:<id>"}}`。它已经被 `approval_service.go:940` 引用（legacy 审批链的 `dept_manager` 类型审批人解析），有 `approver_test.go` 覆盖（包括父部门回退、部门/manager 不存在、manager 未激活等分支）。**它不做"manager 是不是申请人自己"这个排除**——这一层仍然需要调用方在拿到结果后自己判断，见下方"是否复用"的决策。
- **`ticket_urgent_flow` 除了 `process_resolver.go` 里的字符串常量外，全仓库零引用**——没有 `.bpmn` 文件，没有部署清单条目，找不到就直接返回"获取流程定义失败"。
- **引擎目前没有真正可用的定时器/SLA 差异化能力**（`service/bpmn_process_engine.go`只处理 `handleElement` 里明确分支的 UserTask/EndEvent/ExclusiveGateway/ServiceTask 四种，边界定时器不执行）——所以"紧急流程"目前无法通过真实的超时/升级差异来体现紧急程度。

### 是否复用 `DeptManagerResolver`（针对复审意见的专门说明）

**决策：复用，不重新实现。** 原因：

1. `DeptManagerResolver` 比本设计原先打算写的"部门没有 manager 就直接兜底"更完善——它有父部门递归查找这一层，本设计原本没有。重新写一遍相当于把已经调好、有测试覆盖的逻辑降级重做一次。
2. CLAUDE.md 明确要求"扩展优先"、"不要在 BPMN/process binding 能表达的场景下引入第二套审批引擎"。`DeptManagerResolver` 不是"另一套审批引擎"——它是一个不带状态的纯查询（部门 ID → manager `ApproverInfo`），不依赖 legacy 审批链的任何数据模型（`approval_chain`/`approval_record` 之类的表），只是恰好现在的唯一调用方在 `approval_service.go`（legacy 审批链）里。从 BPMN 引擎（`bpmn_process_engine.go`）调用它，复用的是"部门 → manager 查询"这个能力本身，不是把 BPMN 审批接到 legacy 审批链上，两套审批的执行路径、任务模型、状态机继续保持独立，不产生新耦合。
3. 调用方式很轻：`bpmn_process_engine.go` 里的 `CustomProcessEngine` 已经持有 `client *ent.Client`（`bpmn_process_engine.go:96`），构造 `&approver.ApproverContext{TenantID: instance.TenantID, DepartmentID: requesterUser.DepartmentID}` 后直接 `approver.NewDeptManagerResolver().Resolve(ctx, e.client, appCtx)` 即可，不需要经过 `approver.ResolverRegistry`（那是给 legacy 审批链按 `assigneeType` 字符串动态查表用的分发层，`createUserTask` 这里在编译期就知道要用哪个 resolver，不需要这层间接）。

需要在 `createUserTask` 里补的、`DeptManagerResolver` 本身没有的东西：
- 查询申请人（`requester_id`）的 `department_id`，构造 `ApproverContext`（`DeptManagerResolver` 只接受部门 ID，不接受用户 ID）。
- **"manager 就是申请人自己"这层排除**——`DeptManagerResolver.Resolve` 返回值里不做这个判断，调用方拿到 `approvers[0].UserID` 后必须自己跟 `requesterID` 比较，相等则视为"未解析出有效审批人"，走候选组兜底路径（这也是本设计对复审第 1 条最核心的修复点，见下方"部分一"）。

## 目标架构

### 部分一：审批任务不再回退到申请人自己

**作用范围**：`createUserTask` 现有代码只有在 `assignee == ""`（即 BPMN 没有用 XML `assignee` 属性显式声明）时才会走自动分配这段逻辑。新的 approval 分支插在这一层内部，**BPMN 显式声明这一最高优先级不受影响，仍然照常生效**——如果确实需要脚本化指定某个审批任务的审批人，用 BPMN XML 的 `assignee` 属性，而不是流程变量（见下方"跳过范围"的说明）。

```
createUserTask() 的 if assignee == "" 分支里，遇到 task.TaskPurpose == "approval" 时，
不再走原来的 requester_id → triggered_by → assignee_id → getDefaultAssigntee 链，
改走专门的两级审批人解析：

  1. 主路径：解析申请人所在部门的 manager
     - 查申请人（流程变量 requester_id 对应的 User）的 department_id
     - 复用 service/approver.DeptManagerResolver.Resolve(ctx, client,
       &approver.ApproverContext{TenantID, DepartmentID})
       （递归查父部门，直到找到 manager 或者到根还没有；manager 必须
       tenant 匹配且 active，这两条 DeptManagerResolver 内部已经保证）
     - 解析成功且 manager.UserID != requesterID → assignee = manager.UserID，
       主路径到此结束，不再看候选组
     - 解析失败（没有 department_id / 部门和祖先都没配 manager / DB 错误）
       或者 manager.UserID == requesterID（部门负责人就是申请人自己）
       → 视为"主路径未解析出有效审批人"，走 2

  2. 兜底路径：candidateGroups="ticket-approvers"（复用已有 GroupResolver.
     ExpandGroupsToUsers + MergeCandidateUsers，这条接线已经存在，见"现状核实"）
     - assignee 保持为空，展开后的组成员写入 candidate_users，走"未领取"
       状态，等被领取（前端 /approvals/pending 页面已有"领取"按钮，
       流程与普通 candidateGroups 任务一致，不需要新 UI）
     - **展开结果里必须过滤掉申请人自己**：ExpandGroupsToUsers 返回的
       userIDs/usernames 里，任何与 requesterID（数字）或申请人
       Username/Email（authorizeTaskActor.allowed 用同一套 token 比较
       语义：token == strconv.Itoa(userID) 或 token == username）匹配的
       条目都要从最终写入 candidate_users 的列表里剔除，再调用
       MergeCandidateUsers。这一步只加在 createUserTask 的审批任务分支里，
       不改 GroupResolver 本身——GroupResolver 是通用基础设施（比如未来
       CC 组展开就不该排除申请人，CC 申请人自己是合理场景），排除逻辑
       是"审批任务"这个业务场景专属的约束，不该下沉进通用工具里。
     - 租户需要在 /admin/groups 里建 ticket-approvers 组、加审批人进去，
       否则任务无人可领——这是已知的、可接受的初始状态，不在这次修复
       范围内自动建组/加人（见"实施前提"）。**已知边界情况**：如果
       ticket-approvers 组里只有申请人自己一个人（或组不存在/没有成员），
       排除过滤后 candidate_users 会是空的——任务会变成 assignee 和
       candidate_users 都为空的孤儿任务，没有人能领取。这不是本次修复
       要自动解决的场景（跟"部门负责人就是申请人自己"一样，都是组织
       架构配置问题），但必须在"实施前提"里写清楚：ticket-approvers
       组至少要配 2 个人，否则单人department+单人group的租户会出现
       审批任务卡死。

  3. 跳过范围（对应复审第 5 条，明确写清楚，不留空白）：
     taskPurpose="approval" 的任务里，流程变量 requester_id / triggered_by /
     assignee_id 这三个、以及 getDefaultAssigntee 数据库规则兜底，
     在自动分配阶段【整个不会被读取】——不是"读了但没用上"，是这段
     代码根本不执行。如果未来有场景需要用 assignee_id 变量指定审批人
     （比如自动化脚本发起的审批），当前设计下会被完全忽略；这类场景
     应该改用 BPMN 显式 assignee 属性（不受本条影响），或者作为后续
     单独的需求再讨论是否要给 approval 分支加"显式变量覆盖"这一层。
```

### 部分二：`ticket_general_flow.bpmn` 打上 `taskPurpose="approval"` 标记

只改这一个文件的 `Activity_Approval` 节点。其余 12 个文件的同类节点（CAB审批、发布审批等，合计 20 个）现状不变，明确记录为已知问题、留到后续单独处理（见"非目标"一节）——Go 代码层面的修复是通用机制，那些文件只要以后补上 `taskPurpose="approval"` 就能立刻受益，不需要再改 Go 代码。

### 部分三：补齐 `ticket_urgent_flow.bpmn`

新建 `service/bpmn/ticket_urgent_flow.bpmn`，内容是 `ticket_general_flow.bpmn` 的副本（含本次的 `taskPurpose="approval"` 标记），只改 `process id`/`name`/`metaData` 中的描述性字段（比如流程名改成"紧急工单流程"）。不引入任何实质行为差异——`process_resolver.go` 里高/紧急优先级路由过去的这条流程，现在会是一条真正可部署、结构上等价于通用流程的独立流程定义，而不是指向一个不存在的 key。同时补上 `bpmn_template_service.go` 部署清单里对应的 case（比照 `ticket_general_flow` 那一条）。

## 实施前提

- 需要在 `/admin/groups` 手动创建 `ticket-approvers` 组并添加**至少 2 名**成员，否则以下两种情况会导致审批任务无人可领：部门（含其祖先部门）没配 manager；或者部门 manager 解析出来正好是申请人自己、而 ticket-approvers 组里又只有申请人一人。这不是代码要处理的事，写进部署/运维文档里说明即可，但"至少 2 人"这个约束必须写清楚，不能只说"建组加人"。

## 测试计划

- `createUserTask` 遇到 `taskPurpose="approval"` 且无 BPMN 显式 assignee 时：申请人所在部门有 `manager_id` 且不是申请人自己 → assignee 正确解析为该 manager。
- 部门没有 `manager_id`，父部门链一路查到根也没有 → assignee 不落到申请人，`candidateGroups` 展开的 `ticket-approvers` 组成员（排除申请人后）进入 `candidate_users`。
- 部门 `manager_id` 存在、且父部门递归能查到，但解析出的 manager 就是申请人自己 → 同样落到候选组兜底路径，不直接把 assignee 定成申请人。
- **候选组排除断言（对应复审第 1 条，本次修复最核心的安全断言）**：`ticket-approvers` 组成员里包含申请人自己时，`createUserTask` 生成的 `candidate_users` 里【不包含】申请人的 ID 也不包含申请人的 username/email——不能只断言 `assignee != requesterID`，必须同时断言 `candidate_users` 展开结果排除了申请人自己，这是复审指出的、原设计遗漏的那一半。
- 候选组排除后为空的边界情况：`ticket-approvers` 组只有申请人一个成员（或组不存在/没有成员）时，任务创建成功但 `assignee` 和 `candidate_users` 都为空——断言这种情况下不会静默把申请人塞回去，而是保持空（正确行为），同时这个场景要在日志里留下可观察的 warning，方便运维发现"审批任务无人可领"。
- 明确写一条断言："即使流程变量里 `requester_id` 有效，`taskPurpose="approval"` 的任务 assignee 也绝不等于 `requester_id`"——这是本次修复最核心的安全断言之一（跟上面的候选组排除断言合起来才是完整的"自己批自己"防护）。
- 明确写一条断言：`taskPurpose="approval"` 的任务，即使流程变量里 `assignee_id` 指向一个有效用户，也不会被用作 assignee（验证"跳过范围"里 assignee_id 被完全忽略这一条）。
- `authorizeTaskActor`：申请人尝试对自己提交工单的审批任务调用完成接口 → 拒绝（`当前用户不是该任务的审批人或候选人`），分别覆盖"申请人是部门 manager 被排除"和"申请人在候选组里被排除"两种触发路径。
- `ticket_urgent_flow` 能被 `BPMNDeploymentService`/`BPMNTemplateService` 正常发现和部署，`StartProcess("ticket_urgent_flow", ...)` 能成功创建流程实例（不再报"获取流程定义失败"）。
- 跨租户：`DeptManagerResolver` 的部门/manager 解析、`GroupResolver` 的 candidateGroups 展开都要带 tenantID 过滤（两者内部已经这么做，测试是回归验证而不是新增行为），补跨租户隔离测试。

## 非目标（本次不做）

- 不修复其余 12 个 BPMN 文件（change_normal_flow(_cn)、release_approval_flow(_cn)、incident_emergency_flow(_cn/_v1.1)、problem_management_flow(_cn)、service_request_flow(_cn)、cloud_private_ops_flow、cloud_public_ops_flow）里合计 20 个同类"审批节点没有 taskPurpose/candidateGroups"的问题——明确记录为已知的系统性缺口，Go 代码修好后这些文件后续补标记即可受益，但本次不主动去改。（2026-08-08 复审修正：原文"11 个文件、20+ 节点"统计遗漏了两个用非命名空间写法 `<userTask>` 的文件，见"现状核实"里的修正说明。）
- 不给 `ticket_urgent_flow` 设计任何区别于 `ticket_general_flow` 的真实行为差异（超时/升级规则等）——引擎目前没有可用的定时器能力支撑这类差异，属于更大的、需要单独讨论的工作。
- 不自动创建 `ticket-approvers` 组或往里加成员——这是部署/运维步骤，不是代码逻辑。
- 不涉及"审批收敛"更大范围的工作（legacy approval_controller/approval_chain_controller 清理、change CAB 会签建模等）——这些在更早的最终评审里已经被记录为独立的后续工作。
