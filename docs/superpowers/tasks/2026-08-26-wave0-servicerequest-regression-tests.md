# 任务包：ServiceRequest 域回归测试补齐

> 这是一个独立、自包含的任务包。你不需要读本仓库的其他设计文档就能执行它——所有必要的现状事实
> 和规范都已经内嵌在下面。执行完成后按"交付格式"一节的要求提交结果。

## 背景（一句话）

本仓库正在做一次大重构（把 Ticket/Incident/Problem/Change/ServiceRequest 统一到共享的 WorkItem
领域模型），ServiceRequest 域是重构目标之一，且已经存在一个**真实的双写 bug**（见下方现状证据），
后续重构会依赖这次测试先把"现在两边确实一致"这个不变量锁定下来，方便迁移后对比。这个任务只是
补测试，不修复 bug，也不涉及重构本身。

## 范围与边界

**只允许新增/修改**：`itsm-backend/handlers/service_request/*_test.go`。

**禁止修改任何非测试文件**（包括你在写测试过程中发现的、看起来明显是 bug 的生产代码，包括下面
提到的双写问题——见"发现 bug 怎么办"）。

## 现状证据（你不需要重新调查，直接采信）

- 当前覆盖率：`go test ./handlers/service_request/... -cover` → 64.9%。
- `ServiceRequest.ticket_id` 是唯一索引外键，状态/审批/工作流全部委托给对应的 Ticket
  （`ent/schema/servicerequest.go`）。
- **真实的双写问题**：用户提交的自定义字段值，同时写进两个地方——
  `itsm-backend/handlers/service_request/service.go:445-459` 的
  `extractServiceRequestFieldValues` 把非系统字段抽出来，通过
  `service.go:212` 的 `CreateValues(..., "ticket", createdTicket.ID, ...)` 写进
  `field_values` 表；同时完整的原始 `form_data`（含刚才抽取的那些字段）也原样存进
  `service_requests.form_data` JSON 列。这两份数据现在**应该**是一致的（同一次提交产生），
  但这只是巧合的实现结果，不是有约束保证的不变量。
- 服务目录里 `itsm_type=Incident` 的目录项会走
  `itsm-backend/handlers/service_request/service.go:85-87` 的 `isIncidentCatalog` 分流，
  直接创建 Incident，不产生 ServiceRequest/Ticket 行（这条路径的测试属于 Incident 任务包，
  这里只需要测"确认没有产生 ServiceRequest 行"这一半）。
- 审批链解析走 `ApprovalChainResolver.ResolveForServiceRequest`，只是把租户的审批策略解析成
  BPMN 流程变量，不直接创建审批记录。

## 必须覆盖的场景

1. 目录提交 → 委托创建 Ticket 的全链路（提交表单 → 生成 ServiceRequest → 生成关联 Ticket，
   状态从 Ticket 读回）。
2. **`form_data`/`field_values` 一致性断言**：提交一个包含自定义字段的请求后，断言
   `field_values` 表里能查到这些字段（`entity_type="ticket"`），且和 `form_data` JSON 里
   的同名字段值一致。这条测试是为后续重构做基线，请务必按这个精确断言写，不要简化成"提交
   成功即可"。
3. 审批链解析（`ApprovalChainResolver.ResolveForServiceRequest` 生成的流程变量正确性）。
4. `itsm_type=Incident` 分流：提交后确认**没有**产生 ServiceRequest 行（只需要这一半断言，
   Incident 那一侧的完整行为由另一个任务包覆盖）。
5. 跨租户隔离：租户 A 的用户不能读取/修改租户 B 的 ServiceRequest。

## 相关规范（本仓库的工程约定，摘录）

- 后端测试用 `stretchr/testify` 写表驱动测试，用 `enttest.NewClient()` 起内存/临时 DB，不要
  mock ent 层。
- 每个新增查询/测试都要覆盖 tenant 隔离这一条。
- 不要添加兼容层、桥接服务、或者"临时"的 workaround 代码。

## 发现 bug 怎么办（重要，务必遵守）

上面已经明确指出 `form_data`/`field_values` 双写是一个已知问题，**这个任务的目标是把现状用
测试锁定下来，不是修复它**（修复是后续重构阶段的范围）。除此之外你可能还会发现其他现有逻辑的
bug——同样不允许顺手修复：

- 用 `t.Skip("已知缺陷，留给后续重构阶段处理：<具体描述 bug 和触发条件>")` 标注。
- 在交付说明里单独列出所有这类 `t.Skip` 项。
- **不允许修改任何非测试文件**，哪怕只改一行——包括不要"顺便"把双写改成单写，这会让后续
  重构任务失去可对比的基线。

原因：这个任务和另外三个类似的任务包（Incident/Problem/Change 回归测试）在并行执行，如果
允许顺手改生产代码，可能会在互不知情的情况下改到共享逻辑，制造不该在这个阶段出现的冲突。

## 验收标准

- `go test ./handlers/service_request/... -cover` 通过，覆盖率不低于 70%。
- `go build ./...` 通过。
- 场景 2（`form_data`/`field_values` 一致性）的断言必须精确到字段级比对，不能只断言"两边
  都非空"。

## 交付格式

提交时必须包含：

1. 完整 diff（应该只涉及测试文件）。
2. 变更说明：新增了哪些测试，覆盖了上面 5 个场景里的哪些。
3. **实际运行验收标准里命令后的真实终端输出**，包括覆盖率数字。
4. 如果有任何 `t.Skip` 标注的已知缺陷，单独列出清单。
