# BPMN 异步 ServiceTask 执行语义设计

> 状态：Draft for review
> 日期：2026-08-29
> 范围：ITSM 自定义 BPMN 引擎（`itsm-backend/service/bpmn_process_engine.go`）新增"暂停型 service task"执行语义
> 上游依赖：[2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md](2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md) §11 P0-1（并部分解决 P0-2、P1-1）
> 驱动场景：SSLVPN 权限申请审批通过后，流程到达 `kaf_delegate` 节点应停住，直到 KAF 完成 VPN 开通才继续

## 1. 背景与问题

上游 KAF 委派设计要求"流程到达 `kaf_delegate` 节点时，ITSM 创建 `ProcessTask`……并停在该节点；KAF 最终完成关联 `ProcessTask` 后，BPMN 才沿出边继续"。但现有引擎的 `service_task` 是同步语义：`handleElement` 在 `serviceTask` 分支（`bpmn_process_engine.go:705-762`）里解析出 handler 后立即调用 `handler.Execute(...)`，返回后在同一调用栈内继续调用 `executeStep` 推进到下一节点。

如果不改造直接把 `kaf_delegate` 实现成普通 `serviceTask`，流程会在到达节点的一瞬间就被引擎当作"已完成"冲到下一个节点（通常是结束节点），即使 KAF 还没有开始处理——WorkItem 会被过早标记为完成，这是业务正确性问题，不是次要细节。

只有 `user_task` 具备"创建 `ProcessTask` 后暂停，等外部显式调用完成"的能力（`createUserTask`，`bpmn_process_engine.go:810`，恢复靠 `CompleteTask`，`bpmn_process_engine.go:318`）。本设计的目标是让 `service_task` 也能在特定 handler 类型下获得同样的暂停/恢复能力，同时不改变其余 8 个现有 handler（Ticket/Incident/Change/ServiceRequest/CC/Notification/Release/Webhook/Generic）的同步行为。

## 2. 现状梳理（设计依据）

- **恢复路径已经是通用的**：`CompleteTask(ctx, taskID, variables)` 只依赖 `ProcessTask.TaskDefinitionKey`/`ProcessInstanceID` 重新解析 BPMN 并调用 `executeStep` 继续推进，不关心当初暂停节点在 XML 里是 `userTask` 还是 `serviceTask`。
- **完成回调分发已经是元素类型无关的**：当时的完成后回调依据任务 metaData 中的 handler 类型和 action 调用 `Execute`，与 `serviceTask` 使用同一 handler 查找口径。Change 的 CAB 节点证明 UserTask 可声明同款副作用。
- **`ExtensionElements.GetMetaData(key)` 是通用键值访问器**：`ServiceTaskType()`/`ServiceTaskAction()`（`bpmn_types.go:191-197`）只是对它的两个具名封装，读任意新 metaData key（比如新增的 `allowed_actions`）不需要改 XML 解析器结构体。
- **`authorizeTaskActor`（`bpmn_process_engine.go:552`）今天有一个口子**：`ctx` 里没有注入 `BPMNUserIDContextKey`（无人类用户）时直接放行，不做任何校验——这是为平台级调用留的通道，但如果 KAF 的调用直接复用这条路径，会绕开"只能完成分配给自己的任务"这层校验。
- **`SuspendProcess`/`ResumeProcess`（`bpmn_process_engine.go:1527/1576`）是正交概念**：只是把 `ProcessInstance.Status` 整体标记为 `suspended`（管理员主动冻结整个实例），跟本设计的"某个节点级暂停等待外部完成"不是同一回事，互不影响。
- **`CompleteTask` 现有的完成更新已经是原子的**：`Where(..., StatusNEQ("completed"), StatusNEQ("cancelled")).SetStatus("completed")...Save(ctx)`，`updated != 1` 直接报错——这已经保证"任务只能被完成一次"，效果等价于乐观锁，不需要为此再加 `version` 列。

## 3. 设计方案

### 3.1 可选能力接口：`AsyncServiceTaskHandler`

在 `service/bpmn` 包新增一个独立的、非必须实现的接口：

```go
// AsyncServiceTaskHandler 是 ServiceTaskHandlerInterface 的可选扩展。
// 实现此接口且 IsAsync() 返回 true 的 handler，其对应的 serviceTask 节点
// 在流程到达时不会同步执行 Execute，而是创建 ProcessTask 并暂停，
// 直到外部通过 CompleteTask 显式完成。
type AsyncServiceTaskHandler interface {
    IsAsync() bool
}
```

这是 Go 惯用的能力接口模式（非侵入式扩展）：不修改 `ServiceTaskHandlerInterface` 本身，现有 9 个 handler 不需要任何改动——它们没有实现这个接口，类型断言自然落空。只有新增的 `KafDelegateServiceTaskHandler` 实现 `IsAsync() bool { return true }`。

### 3.2 暂停路径

`handleElement` 的 `serviceTask` 分支（`bpmn_process_engine.go:710-721`）在 `findHandlerByTaskType` 拿到 handler 后，先做一次类型断言：

```go
if handler := e.findHandlerByTaskType(serviceTaskType); handler != nil {
    if asyncHandler, ok := handler.(bpmn.AsyncServiceTaskHandler); ok && asyncHandler.IsAsync() {
        return e.createDelegatedTask(ctx, instance, serviceTask, handler)
    }
    // 原有同步分支不变
    ...
}
```

新增 `createDelegatedTask(ctx, instance, serviceTask *BPMNServiceTask, handler bpmn.ServiceTaskHandlerInterface) error`，结构上对齐 `createUserTask`：

- 生成 `task_id`（复用 `createUserTask` 相同的生成方式）；
- 写入 `ProcessTask`：`process_instance_id`、`process_definition_key`、`task_definition_key = elementID`、`task_type = serviceTaskType`（即 `"kaf_delegate"`）、`status = "delegated"`（新增枚举值，与现有 `created/assigned/started/completed/cancelled` 并列）、`tenant_id`；
- `task_variables` 写入该节点的 metaData：`service_task_type`、`action`（若有）、`allowed_actions`（新识别的 metaData key，逗号分隔的动作名列表，读法与现有 `CCUserIDs` 等 CSV 字段一致，走 `ExtensionElements.GetMetaData("allowed_actions")`）；
- **不调用 `handler.Execute`**——`kaf_delegate` 场景的真正业务副作用（resolve/close 等）走上游设计 §4.3 单独的 typed action API，不经过这个 `Execute`；
- 返回 `nil`，不调用 `executeStep`。`handleElement` 顶部已经把 `instance.CurrentActivityID` 设成了这个节点，流程实例的"当前所在节点"状态和 `user_task` 暂停时完全一致。

### 3.3 恢复路径：复用 `CompleteTask`

不新增恢复函数。上游设计 §4.2 的 `complete_bpmn_task` typed action 落地时，controller 内部直接调用现有 `CompleteTask(ctx, taskID, variables)`：

- 步骤 1-4（查任务、鉴权、查实例、原子标记完成）不变，鉴权规则见 3.4；
- 步骤 5（乐观锁合并变量）、步骤 6（`executeStep` 从 `task.TaskDefinitionKey` 继续）、步骤 6.5（按任务声明找回 `KafDelegateServiceTaskHandler` 调用只做记录/审计的 `Execute`）、步骤 7（审计）全部原样复用；
- 重复调用 `complete_bpmn_task`（比如 KAF 超时重试）会撞上步骤 4 现有的 `updated != 1` 分支，返回"任务已被处理"——上游 API 契约里 `already_applied` 的判定就基于这个错误。这次设计只保证引擎层给出可区分的错误，具体映射到 `applied`/`already_applied` 的 HTTP 层语义留给上游 API 设计（不在本设计范围）。

### 3.4 鉴权：复用现有 RBAC，不新增技术账号体系

KAF 是同一应用的内部模块，不当作外部系统建独立的 scoped-token 机制。KAF 以真实的 ITSM 系统用户身份（`ent.User`，类似现有内置账号）调用 API，绑定专用角色（如 `kaf_automation`），走和其他调用方相同的认证中间件；HTTP 层用现有 `RequirePermission` 做粗粒度门禁（如 `resource="bpmn_process_task", action="complete_delegated"`）。

`authorizeTaskActor`（`bpmn_process_engine.go:552`）新增一个分支，与人工任务的 assignee/candidateUsers 校验并列、不影响后者：

```go
if task.TaskType == "kaf_delegate" {
    // assignee/candidateUsers 对机器任务没有意义（一个租户下所有 kaf_delegate
    // 任务都由同一个 kaf_automation 账号处理，不存在"候选人"）。
    // 校验换成：调用者是 kaf_automation 角色 且 task.Status == "delegated"。
    return authorizeKafAutomationActor(ctx, task)
}
```

今天"ctx 无用户上下文即放行"的口子保持只对非委派任务生效，不能被新任务类型绕过。

`task.TaskType == "kaf_delegate"` 目前是硬编码判断，因为当前只有这一个异步 handler 类型；如果未来出现第二个异步 handler 类型，这里需要改成"查 `task.TaskType` 对应的 handler 是否实现 `AsyncServiceTaskHandler`"这种通用判断，而不是继续枚举字符串——这是本设计明确的 YAGNI 取舍，不在这次一并做。

### 3.5 数据模型改动

只新增一个字段：`ProcessTask.correlation_id`（string，可空，用于跨系统关联查询，需要一次 ent schema 迁移）。

不新增：
- `allowed_actions` 字段——走 3.2 节的 `task_variables` JSON，不需要新列；
- `version` 字段——3.4 节已说明现有的条件更新语句已提供等价的原子保护；
- WorkItem 的直接外键——`ProcessTask` 与 WorkItem 的关联继续经 `ProcessInstance.business_id/business_type` 间接引用，本设计不改变这一点（上游设计如果需要更强的直接关联，应在 API/查询层通过 `ProcessInstance` 联查解决，不是这里的问题）。

## 4. 错误处理

- 节点声明的 `service_task_type` 找不到已注册 handler：当时的约定是记警告并视为 NoOp；`kaf_delegate` 必须在部署前注册，本设计不改变该约定。
- 重复 `complete_bpmn_task`：见 3.3，复用现有"任务已被处理"错误。
- 非 `kaf_automation` 角色调用 `complete_bpmn_task`：`authorizeKafAutomationActor` 拒绝，返回权限错误，不触及任务状态。
- `task.Status != "delegated"`（比如任务已被取消）：`authorizeKafAutomationActor` 一并拒绝。

## 5. 测试要求

**回归**（确保零行为变化）：
- 现有 9 个 handler 的同步执行路径（`serviceTask` 分支）行为不变；
- 现有 `user_task` 创建、完成和完成后 handler 回调行为不变；
- 现有全部 BPMN 流程模板（SSLVPN 双级审批、Change CAB 审批等）端到端跑通，不因新增的类型断言分支引入回归。

**新增**：
- 异步 handler 声明 `IsAsync()==true` 时，流程到达节点后创建 `ProcessTask`（`status="delegated"`）且实例 `CurrentActivityID` 停在该节点，不继续推进；
- 通过 `CompleteTask` 完成该任务后，流程沿正确出边继续并触发 handler 的 `Execute`，且不产生额外业务副作用；
- 重复调用 `CompleteTask` 完成同一任务返回"已处理"错误，不重复推进流程；
- 非 `kaf_automation` 角色调用被 `authorizeKafAutomationActor` 拒绝；`kaf_automation` 角色但 `task.Status != "delegated"` 也被拒绝；
- `allowed_actions` metaData 能正确从 BPMN XML 经 `ExtensionElements.GetMetaData` 读出并写入 `task_variables`。

## 6. 非目标

本设计只解决"引擎如何暂停/恢复"这一层机制，不包含：

- 上游设计 §4.1-4.3 的任务范围 HTTP API 契约（`GET kaf-context`、`POST actions`）本身；
- Outbox 事件发布（`KafDelegateRequested`）；
- HTTP 层的幂等键去重机制；
- `AuditMiddleware` 挂载；
- Workflow Designer 前端对 `kaf_delegate` 节点类型、`allowed_actions` 配置项的 UI 支持。

这些仍是上游设计 §11 里独立的 P0-3/P0-4/P0-5 及 UI 相关条目，需要各自的实现计划。

## 7. 对上游设计 §11 的影响

- **P0-1（BPMN 引擎不支持暂停型 service task）**：本设计给出了完整方案，状态可以从"待设计"改为"待实现"。
- **P0-2（ITSM 无任务范围权限模型）**：本设计的 3.4 节解决了引擎内部这一侧（`authorizeTaskActor` 分支 + 复用现有角色/权限模型，不新增技术账号体系），但 HTTP 层的路由权限声明（`RequirePermission` 具体资源名/角色种子数据）仍需要在实现计划里补上，不算完全解决。
- **P1-1（ProcessTask schema 缺字段）**：范围从"缺 version/correlation_id/allowed_actions 三项"缩小为"只缺 correlation_id 一项"。
