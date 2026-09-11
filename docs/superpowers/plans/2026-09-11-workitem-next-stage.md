# WorkItem Next Stage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完成已审阅 WorkItem 后续设计的开发与验收，保留已完成成果并退出剩余旧路径。

**Architecture:** 三批交付，共享 WorkItem事实、专业域动作、现有 BPMN/Outbox/授权与公共页面。按实际权威边界拆任务，不按入口分别实现业务规则。

**Tech Stack:** Go/Gin、Ent/PostgreSQL、Next.js/TypeScript、Jest、Playwright。

> 状态：draft（实施计划已编制，未执行）
> 日期：2026-09-11
> 设计：[accepted 后续设计](../specs/2026-09-11-workitem-convergence-next-stage-design.md)
> 决策历史：[开发输入](../specs/2026-09-11-workitem-convergence-development-input.md)

## Global Constraints

- 保留代码观察基线 4660633019f10ec23835e23c7fa43578daa7ff57 和原工作树的证据；文档基于 review 工作树，不将文档 HEAD 当作部署版本。
- 不重做已有 A 阶段；但全部操作入口仍须核查。原 A/B/C 任务和报告是证据来源，不是豁免清单。
- tickets 为公共权威；专业生命周期不合并。不得添加双写、旧身份兼容解释或第二套审批/SLA/关系实现。
- Incident 首次分配推进 assigned，后续合法转派保留进度；Change 保留审批/阶段，Problem 保留进度/证据；转派原因必填。
- BL-RESP-01、BL-CHG-WO-01 不实现；已有 SLA、审批和任务正确性仍在本轮。
- 本计划不授权共享数据库写入、推送、合并或部署。代码任务使用隔离工作树与测试环境；环境切换须单独准入。

## 1. 执行前准入

- [ ] 在 WSL 核查原代码与 review 分支 HEAD、工作区修改和依赖状态；保留 .superpowers/sdd。按工程治理确认最新 origin/main 与原分支的关系，先比较路径，不直接 pull/reset 覆盖其他 agent 工作。
- [ ] 使用 using-git-worktrees 创建实施工作树，分支前缀 `codex/refactor/`；明确纳入的原实现提交及本轮设计提交。不同历史有冲突时记录已解决路径，不能凭提交数量判断成果是否存在。
- [ ] 将入口清单作为本计划执行记录附表：域、操作、HTTP/BPMN/自动化/普通编辑调用点、领域方法、写入表、版本来源、回执、待删除路径、对应测试。用 `git grep` 逐项追踪实际引用。
- [ ] 对受影响最小测试建立基线，保存环境与真实测试输出到已忽略证据目录。三个已知失败单独重现：

```bash
go test ./internal/bootstrap -run '^TestRunPostSchemaMigrationsAppliesVersion007$' -count=1 -v
go test ./service -run '^TestDefinitionStartFrozenIncidentAssignment$' -count=1 -v
go test ./tests/integration -run '^TestIntakeHTTPProblemAndIncidentEntry$' -count=1 -v
```

这些命令从 itsm-backend 执行；使用隔离测试配置。迁移数量断言需按已注册迁移集合修正，不能只把24换成新的魔法数字；冻结分派测试按 B1 及真实工作流变量修复；intake 测试更新为现有 category 契约并检查持久化分类，不降低响应断言。

## 2. 子计划与依赖

| 顺序 | 子计划任务 | 依赖 | 完成输出 |
|---|---|---|---|
| 1 | [后端 B1](2026-09-11-workitem-next-stage-backend.md)：Incident | 准入 | 分派所有入口统一、并发/审计验证 |
| 2 | 后端 B2：Change | 准入，共同 Meta已存在 | 原因与审计，审批及任务身份不变 |
| 3 | 后端 B3：Problem | 准入，共同 Meta已存在 | 普通编辑/责任调整的唯一事务与证据保护 |
| 4 | 后端 B4：BPMN/seed | 现有 C1 | 保留变量与真实配置入口闭合 |
| 5 | 后端 B5：通知 | 现有 B 关系事实 | 当前授权、真实 MSP RLS、错误分类与幂等 |
| 6 | [页面 F1–F3](2026-09-11-workitem-next-stage-experience-validation.md) | 所用 B1–B5协议稳定 | 权威身份/版本、转派交互、冲突、SLA周期 |
| 7 | 验收 V1 | 前两批集成 | 完整三域和 generic/Requested Item旅程 |
| 8 | 验收 V2 | V1及现有C1预检 | 隔离切换/恢复、runbook、实际运行门禁 |

B1–B3 修改请求契约时必须在同一可审查变更中更新现有前端/API和流程调用方；F1–F3负责公共页面整合，不允许等待第二批才修复已经破坏的客户端。存在独立实施条件不代表自动启动其他 agent；共享文件和数据库写入由单个执行者协调。

## 3. 与原计划关系

本总入口组织“剩余工作”，不重写历史完成记录。[原总计划](2026-09-09-workitem-convergence.md)、[生命周期计划](2026-09-09-workitem-convergence-lifecycle.md)、[关系计划](2026-09-09-workitem-convergence-relations.md)继续承载已有任务/成果。

[原 runtime 计划](2026-09-09-workitem-convergence-runtime.md) C1 的已有成果保留，遗漏通过 B4修复；C2由 F1–F3/V1细化；C3由V2执行，其完整环境门禁继续有效。若重复定义技术选择，以已确认设计和本轮计划的明确补充为准，例如公共身份 helper 采用 F1 的单一组件目录实现，不再另建一份 Problem专用重复 helper。

## 4. 设计覆盖与完成判断

| 设计章节 | 任务 |
|---|---|
| 3.1–3.3 所有者、责任调整、版本/事务/重放 | B1–B3与入口盘点 |
| 3.4 流程和实际配置 | B4 |
| 3.5 通知、授权、重试 | B5 |
| 4.1 身份和动作 | F1/F2 |
| 4.2 冲突和协作 | F2/V1 |
| 4.3 SLA | F3/V1 |
| 5.1 三域、权限、重复和回归 | 各任务定向检查与V1 |
| 5.2 切换、备份、历史、删除 | V2及原C3 |

每项任务保存提交、检查命令、实际结果、失败归因和被替换旧路径。检查通过后进行独立审查或维护者复核。报告分别说明设计接受、代码完成、集成验证、隔离演练、实际部署/观察/删除；不能将前四项完成宣称为实际运行交付完成。
