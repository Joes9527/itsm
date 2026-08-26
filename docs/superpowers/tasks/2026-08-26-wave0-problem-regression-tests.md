# 任务包：Problem 域回归测试补齐

> 这是一个独立、自包含的任务包。你不需要读本仓库的其他设计文档就能执行它——所有必要的现状事实
> 和规范都已经内嵌在下面。执行完成后按"交付格式"一节的要求提交结果。

## 背景（一句话）

本仓库正在做一次大重构（把 Ticket/Incident/Problem/Change/ServiceRequest 统一到共享的 WorkItem
领域模型），Problem 域是重构目标之一。当前测试覆盖率 31.8%，重构前需要先给**现有行为**建立更
扎实的回归基线。这个任务只是补测试，不涉及重构本身。

## 范围与边界

**只允许新增/修改**：`itsm-backend/handlers/problem/*_test.go`。

**禁止修改任何非测试文件**（包括你在写测试过程中发现的、看起来明显是 bug 的生产代码——见下方
"发现 bug 怎么办"）。

## 现状证据（你不需要重新调查，直接采信）

- 当前覆盖率：`go test ./handlers/problem/... -cover` → 31.8%。
- Problem 创建路径在 `itsm-backend/handlers/problem/service.go:24` 的 `Service.Create`，只插入
  一条 Problem 行，不涉及 Ticket。
- Problem 与 Ticket 的关联是**后加的可选多对多 edge**（`AddAssociations`/`RemoveAssociation`，
  见 `itsm-backend/handlers/problem/repository_impl.go:152`），不是创建时必建的关系——一个
  Problem 可以完全不关联任何 Ticket。
- 已知存在"两套调查入口并存"的架构问题：`handlers/problem/handler.go` 暴露的
  `/problems/:id/investigate` 和一个独立的 `ProblemInvestigationController`。这个任务不需要
  修复这个问题，但两条入口都要有测试覆盖（因为不确定哪条会在后续重构中被保留）。

## 必须覆盖的场景

1. 创建 Problem。
2. 状态机：investigate → root-cause 分析 → known-error 发布 → resolve → close 的合法转换；
   至少 2-3 个非法转换应该被拒绝。
3. Known Error 发布入口（从 Problem 发布一个 Known Error 记录）。
4. Incident ↔ Problem 关联的建立（`AddAssociations`）和解除（`RemoveAssociation`）。
5. 跨租户隔离：租户 A 的用户不能读取/修改租户 B 的 Problem。
6. `/problems/:id/investigate` 和 `ProblemInvestigationController` 两条调查入口都要覆盖到，
   哪怕它们目前做的事情有重叠。

## 相关规范（本仓库的工程约定，摘录）

- 后端测试用 `stretchr/testify` 写表驱动测试，用 `enttest.NewClient()` 起内存/临时 DB，不要
  mock ent 层。
- 每个新增查询/测试都要覆盖 tenant 隔离这一条，不能假设"业务逻辑对了 tenant 就自动对了"。
- 不要添加兼容层、桥接服务、或者"临时"的 workaround 代码——这条任务只写测试，不涉及这个问题，
  但如果你发现现有测试里有这类模式，不要模仿它。

## 发现 bug 怎么办（重要，务必遵守）

写回归测试的过程中，大概率会发现现有逻辑本身有 bug（这是补测试的正常副产品）。**这不是让你顺手
修复的许可**。发现的任何现有代码缺陷：

- 用 `t.Skip("已知缺陷，留给后续重构阶段处理：<具体描述 bug 和触发条件>")` 标注，不要修复它。
- 在交付说明里单独列出所有这类 `t.Skip` 项，包括为什么你认为它是 bug。
- **不允许修改任何非测试文件**，哪怕只改一行。

原因：这个任务和另外三个类似的任务包（Incident/Change/ServiceRequest 回归测试）在并行执行，
如果允许顺手改生产代码，可能会在互不知情的情况下改到共享逻辑，制造不该在这个阶段出现的冲突。

## 验收标准

- `go test ./handlers/problem/... -cover` 通过。
- 覆盖率相比 31.8% 有实质提升，目标不低于 50%。
- `go build ./...` 通过。
- 所有新增测试独立可重复运行（不依赖执行顺序、不依赖上一个测试留下的脏数据）。

## 交付格式

提交时必须包含：

1. 完整 diff（应该只涉及测试文件）。
2. 变更说明：新增了哪些测试，覆盖了上面 6 个场景里的哪些。
3. **实际运行验收标准里命令后的真实终端输出**（不是"应该会通过"这种描述，是真实跑出来的结果，
   包括覆盖率数字）。
4. 如果有任何 `t.Skip` 标注的已知缺陷，单独列出清单。
