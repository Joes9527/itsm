# WorkItem Convergence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> 状态：draft；日期：2026-09-09；计划待评审，未执行业务改造。

**Goal:** 收敛 Incident、Problem、Change 的动作、关系、SLA 和 BPMN，删除被替代的旧读写。

**Architecture:** tickets 继续作为 WorkItem 公共权威，专业服务分别校验专业动作。复用现有 Outbox、BPMN、SLA 服务和授权边界；新增类型只用于必要的事务命令契约，不创建 Manager、Facade 或第二套引擎。

**Tech Stack:** Go（go.mod 当前 1.25.12）、Gin、Ent、PostgreSQL、Next.js、TypeScript、Jest、Playwright；前端 Node >=22、npm >=10。

**Spec:** [已评审并按维护者意见修订的设计](../specs/2026-09-09-workitem-convergence-design.md)。执行者先读 AGENTS.md、CLAUDE.md、docs/agent-engineering-governance.md 和 docs/DEVELOPMENT_GUIDE.md。

## Global Constraints

- “本次不迁移历史业务数据”。禁止关系回填、历史流程身份转换、历史状态重写和伪造验证证据；正式 schema 变更不在豁免范围。
- “重开统一归零并开启新周期，不提供‘继续旧周期’的并行策略。”原 createdAt 不变，旧周期结果及违约不擦除。
- “每个被替换概念在对应批次交付时仅有一套有效读写。”观察期旧结构也不允许应用读写。
- “调查完成仅代表调查活动完成，不能直接写 Problem resolved。”
- Problem 解决必须根因明确、永久方案已实施并验证通过。
- 旧结构在唯一新读写切换、完整验收、观察和再次核对之后删除，删除仍属于同批交付门禁。
- actor、tenant、source、权限取可信上下文；预期版本不是权限。缺配置、未知回调、无权限均失败关闭。
- 本计划不授权删除用户数据、重置共享库、取消旧流程、提交用户草稿或部署。

## 1. 交付结构与依赖

这是一个总计划和三个可分别评审的子计划，不是一份无限扩展任务单。

| 顺序 | 子计划 | 独立验收成果 |
| --- | --- | --- |
| A | [专业动作、SLA 周期基础与事务](2026-09-09-workitem-convergence-lifecycle.md) | 三域统一动作入口，重开原子归零，旧状态旁路删除 |
| B，依赖 A | [关系与结果联动](2026-09-09-workitem-convergence-relations.md) | 新关系单一权威、原子关联和幂等通知 |
| C，依赖 A、B | [流程身份、切换与总验收](2026-09-09-workitem-convergence-runtime.md) | BPMN 规范身份、旧实例排空、完整发布与删除门禁 |

SLA 当前周期和权威读取从原第三批提前到 A1：Incident/Problem 重开不能先发布而让计时仍走旧实现。C 负责跨域最终验收，不在 C 才补重开基础。各子计划内部允许多个小提交，但不能部署尚有两个权威入口的中间提交。

## 2. 基线与执行准入

- [ ] 执行 `git fetch origin`、`git log -1 origin/main`、`git worktree list`、`git status --short`，从最新 main 建立每个子计划的 codex/refactor 分支与独立 worktree。
- [ ] 用 `git merge-base --is-ancestor 3b07110e origin/main`、`git merge-base --is-ancestor b304cf50 origin/main` 检查分类/RCA 依赖。若 squash 合并，以 PR diff 和源码逐项确认，不能仅凭退出码判断缺失。未整合的必需依赖先协调合并；不复制运行目录，不重新做 RCA 权威修复。
- [ ] 当前文档基线为 a25e108d；审计参考 b304cf50 和本地集成 9ca7c0d。执行前比较接口，若已改变，更新计划接口与测试后继续，不机械覆盖新代码。
- [ ] 所有 PG 集成测试只用显式 disposable DSN。复用 `tests/integration/incident_effects_postgres_test.go` 的 `INTAKE_POSTGRES_TEST_DSN` 和每测试 schema 隔离；其 fixture 要求本地 36444/sslvpn_test，不得传共享库 DSN。
- [ ] 记录依赖服务与端口，按 docs/DEVELOPMENT_GUIDE.md 启动隔离环境；缺运行时只能报告阻塞，不能把 skipped 当通过。

## 3. 跨任务命令契约

A1 创建 `itsm-backend/handlers/shared/workitemmutation/contract.go`，仅放输入和值类型，不放专业状态 switch：

```go
package workitemmutation

type Meta struct {
    TenantID int
    ActorID int
    ExpectedVersion int
    Source string
    CorrelationID string
    OperationID string
}
type Result struct {
    WorkItemID int
    Version int
    Status string
    Replayed bool
}
```

专业命令分别定义在各域文件，结果复用该值类型。HTTP 的 version 为必填正数，元数据由已有认证上下文构造；OperationID 对应既有幂等／审计存储的唯一动作键。相同键不同命令摘要返回冲突，相同键相同摘要返回原结果；动作提交与幂等事实同事务。不建立第二套回执服务。A1 在既有 auditlog schema 为动作审计增加可空 operation_id、request_digest、result_version、result_status；新动作以 (tenant_id, actor_id, operation_id) 非空部分唯一索引去重，普通历史审计不回填。请求摘要只涵盖规范化业务命令且不记录秘密；审计保留策略必须覆盖允许重放窗口。不得依赖可清理 Outbox 作为回执，也不得按当前终态猜测某请求已经成功。

领域入口默认事务由所属 service/repository 拥有。需要共享事务的辅助函数接受同一 *ent.Tx；跨领域动作以结果事件驱动，不在一个控制器串行写两个独立事务假装原子。

## 4. 计划使用与验证规则

每个任务的纯函数示例是新增测试的首个可执行切片，不能替代随后的真实持久化测试。代码块中列出的新签名在该任务建立；未列出的既有 fixture 必须从指定测试文件复用，不建立产品代码里的测试辅助层。

每任务：写失败测试 → 用该任务 Run 命令确认业务断言失败 → 实现并切换所有调用者 → 运行同一命令与受影响契约测试 → diff 检查 → 独立审查 → Conventional Commit。编译错误可证明新增接口缺失，不能作为业务回归唯一 RED 证据。

前端基线／验收：在 itsm-frontend 执行 `npm run type-check`、`npm run lint:check`、`npm run build`。仅文档任务不运行应用构建；本计划的完成不表示这些检查已通过。

## 5. 需求覆盖

| 设计章节 | 任务 |
| --- | --- |
| 1–3 范围与依赖 | 总计划准入；C3 发布门禁 |
| 4 三域完成条件 | A2 Incident；A3 Problem；A4 Change |
| 5 事务、版本、权限、审计 | A1–A4；B1/B2 |
| 6 关系与联动 | B1/B2 |
| 7 SLA 归零与权威 | A1，C2/C3 回归 |
| 8 BPMN 身份与回调 | A2/A4 回调归域；C1 身份切换 |
| 9–10 删除与历史豁免 | 每任务旧路径删除；C3 最终结构删除 |
| 11 验收矩阵 | 所有任务 PG/契约测试；C2/C3 E2E |
| 12 竞品、13 评审 | 设计文档引用；计划审批与独立审查 |

## 6. 完成定义

完成不是新路径“可用”：必须所有入口已切换，旧运行读写为零，用户指定的三个决策均有回归证据，结构删除经过观察门禁；历史免迁移不掩盖在线旧依赖。保留部署前备份与协调恢复路径，发生新写入后禁止只回滚二进制。
