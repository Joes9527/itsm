# 任务包：Incident 域回归测试补齐

> 这是一个独立、自包含的任务包。你不需要读本仓库的其他设计文档就能执行它——所有必要的现状事实
> 和规范都已经内嵌在下面。执行完成后按"交付格式"一节的要求提交结果。

## 背景（一句话）

本仓库正在做一次大重构（把 Ticket/Incident/Problem/Change/ServiceRequest 统一到共享的 WorkItem
领域模型），Incident 域是重构目标之一，但当前测试覆盖率只有 1.3%——重构前必须先给**现有行为**
建立回归基线，否则没人知道重构改坏了什么。这个任务只是补测试，不涉及重构本身。

## 范围与边界

**只允许新增/修改**：
- `itsm-backend/handlers/incident/*_test.go`
- `itsm-backend/controller/incident_controller_test.go`（如不存在则新建）
- `itsm-backend/service/incident_service_test.go`

**禁止修改任何非测试文件**（包括你在写测试过程中发现的、看起来明显是 bug 的生产代码——见下方"发现 bug 怎么办"）。

## 现状证据（你不需要重新调查，直接采信）

- 当前覆盖率：`go test ./handlers/incident/... -cover` → 1.3%。
- Incident 的创建路径在 `itsm-backend/service/incident_service.go:59` 的 `CreateIncident` 函数，用一个 ent 事务同时创建 `Incident` 行和 `IncidentEvent` 行，**不涉及** `Ticket` 表（Incident 目前跟 Ticket 没有任何结构关联）。
- 服务目录里 `itsm_type=Incident` 的目录项，走 `itsm-backend/handlers/service_request/service.go:85-87` 的 `isIncidentCatalog` 判断，命中后直接调用 `IncidentService.CreateIncident`，完全绕过 `ServiceRequest`/`Ticket`/审批链——这是一条独立的创建入口，需要单独测。
- BPMN 流程触发在 `itsm-backend/service/incident_service.go:1733-1734`，使用 Incident 表自己的主键作为 `BusinessID`。
- Incident 的关联 Ticket 是一个可选的多对多 edge（`AddAssociations`/`RemoveAssociation`，见 `itsm-backend/handlers/problem/repository_impl.go:152` 的姊妹实现模式，Incident 侧的等价方法在 `handlers/incident/` 包里）。

## 必须覆盖的场景

1. 创建 Incident（标准路径，走 `IncidentService.CreateIncident`）。
2. 创建 Incident（服务目录 `itsm_type=Incident` 分流路径，确认不产生 `ServiceRequest`/`Ticket` 行）。
3. 状态机：acknowledge → in_progress → resolved → closed 的合法转换；至少 2-3 个非法转换（比如直接从 new 跳到 closed）应该被拒绝。
4. 跨租户隔离：租户 A 的用户不能读取/修改租户 B 的 Incident。
5. Incident 与 Ticket 的关联建立（`AddAssociations`）和解除（`RemoveAssociation`）。
6. 如果代码里存在"升级"相关逻辑（`incident_escalation_service.go` 之类），先确认它有没有被实际接线调用（在 `internal/bootstrap/app.go` 里搜索它的构造函数名）——**如果确认没有被调用**（纯定义、零构造），不用为它写测试，在交付说明里注明"确认为未接线代码，跳过"。

## 相关规范（本仓库的工程约定，摘录）

- 后端测试用 `stretchr/testify` 写表驱动测试，用 `enttest.NewClient()` 起内存/临时 DB，不要 mock ent 层。
- 每个新增查询/测试都要覆盖 tenant 隔离这一条，不能假设"业务逻辑对了 tenant 就自动对了"。
- 不要添加兼容层、桥接服务、或者"临时"的 workaround 代码——这条任务只写测试，不涉及这个问题，但如果你发现现有测试里有这类模式，不要模仿它。

## 发现 bug 怎么办（重要，务必遵守）

写回归测试的过程中，大概率会发现现有逻辑本身有 bug（这是补测试的正常副产品）。**这不是让你顺手
修复的许可**。发现的任何现有代码缺陷：

- 用 `t.Skip("已知缺陷，留给后续重构阶段处理：<具体描述 bug 和触发条件>")` 标注，不要修复它。
- 在交付说明里单独列出所有这类 `t.Skip` 项，包括为什么你认为它是 bug。
- **不允许修改任何非测试文件**，哪怕只改一行。

原因：这个任务和另外三个类似的任务包（Problem/Change/ServiceRequest 回归测试）在并行执行，如果
允许顺手改生产代码，可能会在互不知情的情况下改到共享逻辑，制造不该在这个阶段出现的冲突。

## 验收标准

- `go test ./handlers/incident/... ./service/... -run Incident -cover` 通过。
- 覆盖率相比 1.3% 有实质提升，目标不低于 40%。
- `go build ./...` 通过（确认没有破坏编译）。
- 所有新增测试独立可重复运行（不依赖执行顺序、不依赖上一个测试留下的脏数据）。

## 交付格式

提交时必须包含：

1. 完整 diff（应该只涉及上面列出的测试文件）。
2. 变更说明：新增了哪些测试，覆盖了上面 6 个场景里的哪些。
3. **实际运行验收标准里 3 条命令后的真实终端输出**（不是"应该会通过"这种描述，是真实跑出来的结果，包括覆盖率数字）。
4. 如果有任何 `t.Skip` 标注的已知缺陷，单独列出清单。
