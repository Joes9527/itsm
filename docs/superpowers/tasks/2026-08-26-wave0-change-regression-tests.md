# 任务包：Change 域回归测试补齐

> 这是一个独立、自包含的任务包。你不需要读本仓库的其他设计文档就能执行它——所有必要的现状事实
> 和规范都已经内嵌在下面。执行完成后按"交付格式"一节的要求提交结果。

## 背景（一句话）

本仓库正在做一次大重构（把 Ticket/Incident/Problem/Change/ServiceRequest 统一到共享的 WorkItem
领域模型），Change 域是重构目标之一。当前测试覆盖率 46.9%（其中审批相关的部分已经有专门的端到
端回归测试，不需要你重复做）。这个任务只是补审批之外的测试缺口，不涉及重构本身，也**不涉及**
审批逻辑。

## 范围与边界

**只允许新增/修改**：`itsm-backend/handlers/change/*_test.go`。

**禁止修改任何非测试文件**。**禁止修改或围绕以下内容写新测试**（这部分已经被另一个已完成的
迁移——PR#6"Track4: 变更审批状态机迁移到 BPMN"——覆盖，有专门的端到端测试
`itsm-backend/handlers/change/service_bpmn_test.go`，重复写只会增加维护负担）：

- `SubmitChange`/`TransitionStatus` 里 `targetStatus == "approved" || "rejected"` 的分支
- `itsm-backend/service/bpmn/change_handler.go` 里跟审批相关的部分（`completeChangeApprovalTask`
  等）

## 现状证据（你不需要重新调查，直接采信）

- 当前覆盖率：`go test ./handlers/change/... -cover` → 46.9%（含 Track4 带来的审批测试）。
- `related_tickets` 字段是 `field.JSON([]string{})`——一个无结构的字符串数组，不是真正的
  外键/关系，读写都是直接操作这个 JSON 字段。
- `ChangeType`（`standard`/`normal`/`emergency`）之间状态机有差异：`emergency` 类型没有
  `scheduled` 这个中间态，`approved` 直接对接 `in_progress`（快速通道）；如果对 `emergency`
  也强行走 `scheduled`，状态机会拒绝这个转换。写状态机测试时要分别覆盖这三种类型，不能只测
  一种然后假设其它类型行为一致。

## 必须覆盖的场景

1. 非审批状态流转：`implement` → `review` → `rollback`/`close` 的合法转换，对三种
   `ChangeType`（standard/normal/emergency）分别验证。
2. `related_tickets` 字段的读写（新增关联工单编号、读回、去重等边界情况）。
3. 风险评估/CAB 之外的字段校验（比如 `justification`/`risk`/`impact`/实施计划/回滚计划这些
   字段的必填性校验）。
4. 跨租户隔离：租户 A 的用户不能读取/修改租户 B 的 Change。

**明确不要做**：不要给已经被 `service_bpmn_test.go` 覆盖的审批流程（提交审批、CAB 批准/驳回、
`isApprover` 判权）重复写测试——这会造成两套测试断言同一个行为，其中一套过时了不会被发现。

## 相关规范（本仓库的工程约定，摘录）

- 后端测试用 `stretchr/testify` 写表驱动测试，用 `enttest.NewClient()` 起内存/临时 DB，不要
  mock ent 层。
- 每个新增查询/测试都要覆盖 tenant 隔离这一条。
- 不要添加兼容层、桥接服务、或者"临时"的 workaround 代码。

## 发现 bug 怎么办（重要，务必遵守）

写回归测试的过程中，大概率会发现现有逻辑本身有 bug（这是补测试的正常副产品）。**这不是让你顺手
修复的许可**。发现的任何现有代码缺陷：

- 用 `t.Skip("已知缺陷，留给后续重构阶段处理：<具体描述 bug 和触发条件>")` 标注，不要修复它。
- 在交付说明里单独列出所有这类 `t.Skip` 项，包括为什么你认为它是 bug。
- **不允许修改任何非测试文件**，哪怕只改一行。

原因：这个任务和另外三个类似的任务包（Incident/Problem/ServiceRequest 回归测试）在并行执行，
如果允许顺手改生产代码，可能会在互不知情的情况下改到共享逻辑，制造不该在这个阶段出现的冲突。

## 验收标准

- `go test ./handlers/change/... -cover` 通过，覆盖率不低于 60%。
- **`TestChangeServiceTaskHandler_*`/`TestTransitionStatus_*` 系列现有测试必须全部继续通过**
  ——这是 Track4 审批收敛的回归信号，任何破坏都要视为严重问题立即停下，不要绕过或删除失败的
  测试。
- `go build ./...` 通过。

## 交付格式

提交时必须包含：

1. 完整 diff（应该只涉及测试文件）。
2. 变更说明：新增了哪些测试，覆盖了上面 4 个场景里的哪些。
3. **实际运行验收标准里命令后的真实终端输出**，包括覆盖率数字，以及确认
   `TestChangeServiceTaskHandler_*`/`TestTransitionStatus_*` 系列全部通过的输出。
4. 如果有任何 `t.Skip` 标注的已知缺陷，单独列出清单。
