# 任务二：配置主数据适配统一 WorkItem 执行计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 将纳入范围的旧配置主数据受控迁入G-A准入目标，交付可重复运行且有业务验证的G-B结果。
**Architecture:** 复用抽取manifest和差异工具，建立明确身份/租户映射，在独立库分批写入。WorkItem共用字段及专业生命周期继续由既有领域拥有。
**Tech Stack:** ITSM Go/Ent/PostgreSQL、KAF Python抽取/对比工具。
**Spec:** [总设计](../specs/2026-09-14-itsm-kaf-database-convergence-design.md)，§5为本任务权威范围。
**Status:** draft；可独立准备映射和离线验证；目标写入依赖G-A PASS。

## Global Constraints

- 不迁历史ticket及审批/评论/附件/流程实例，不迁旧BPMN或知识库。
- 组织/用户复用已有成果，只核对身份和引用，不全量重导或重置密码/角色。
- 不执行ITSM R(038)，不清历史，不改源库，不合main，不做实例切换或真实企业写入。
- 不创建第二份CTI权威字典、专业状态机或任意JSON兼容存储。
- 每批最多60分钟报告；独立worktree/分支、独立目标、单一共享写入者。

## 输入与文件边界

入口为`/home/administrator/project/itsm`与`/home/administrator/project/kaf`；先读各仓库AGENTS.md和治理规则，核查remote/HEAD/status/worktree并保留已有工作。独立分支建议`codex/feat/workitem-config-migration`；按治理从origin/main建立worktree，准确导入所需未合并成果，不在旧checkout直接修改。

消费任务一提交`GARevision`中的`docs/review/2026-09-14-database-reconciliation-handoff.md`。无需完整任务一对话。未收到PASS可完成离线抽取完整性、映射和fixture准备，不能对目标写入或宣称G-B通过。

复用依据：KAF提交`184f7868161794bd549a6fbe2d46e41fe0de854e`的`docs/superpowers/specs/2026-09-14-legacy-itsm-master-data-extraction-and-diff-design.md`、`scripts/fetch_itsm_master_data.py`、`scripts/diff_legacy_vs_new_itsm.py`及`tests/test_diff_legacy_vs_new_itsm.py`；ITSM提交`660087795e6efe49e89b759d2527ad1b8320a651`的`scripts/clone_itsm_migration_db.sh`仅经修复/验证后复用。

**修改边界：**抽取/对比修复留在KAF既有脚本与测试；配置写入留在ITSM拥有配置的领域/迁移入口，不能由KAF跨库写ITSM。具体写入文件由映射结果确定，先提交本任务内的小批实现清单（文件、字段、事务、实际测试命令），审查后编码，不预设不存在的目标模型。新增唯一交接`docs/review/2026-09-14-workitem-config-migration-handoff.md`；PII、导出和凭据留在受保护证据目录。

## 执行步骤

- [ ] 核验GARevision、两个制品SHA及目标指纹仍匹配；目标变化回传任务一，不自行更新上游PASS。
- [ ] 核验抽取manifest来源/环境/时间/分页及摘要，记录缺失和截断。旧dev克隆`itsm_migration_20260914`仅为对照样本，不能冒充046目标。
- [ ] 逐项盘点CTI、字典/自定义字段、优先级/矩阵、SLA/日历、分类路由；明确每项的共存、融合或替换策略，既有记录影响及纳入/排除理由。
- [ ] 建立源系统+源ID+tenant到目标ID映射，核对组织/用户引用、父层级、枚举及稳定业务键；同名冲突或缺父失败阻塞，不自动模糊合并。
- [ ] 将分类和路由映射到recordClass、目录、SLA和已登记流程；不导入BPMN，不把退役公共字段写回专业扩展。必须新增产品能力才能映射的条目作为显式范围差额，不能静默发明字段。
- [ ] 审查克隆工具已有目标/半恢复/标识符处理；如需修改，用目标已存在但不完整、恢复失败后重跑及异常标识符用例先复现失败，再修复。完整性验证覆盖纳入对象与约束，不只四表行数。
- [ ] 为写入批次固定目标身份、依赖顺序、事务边界、源摘要、映射、插入/更新/拒绝清单和恢复检查点；审查dry-run后才在获准隔离目标执行。
- [ ] 验证同批重复执行无重复/额外变更，中途失败保留检查点并可恢复；租户错配、孤立引用、未知流程动作明确失败。代码修复需对应失败用例和最小测试，不以空实现或mock成功替代持久化。
- [ ] 用新建且可追踪的验收记录验证分类、目录、SLA、专业流程及权限；保留验收记录清单，正式迁移清单不得自动包含这些演练记录，也不删除历史来制造干净结果。
- [ ] 固定最终数据摘要及配置版本，提交交接、独立审查与`git diff --check`结果；仅提交代码/脱敏证据索引，各仓库分别提交准确SHA。

## 输出接口：G-B

交接记录：`Gate: G-B`、`Status: PASS|BLOCKED`、消费的`GARevision`、实际两仓库SHA/制品、源manifest摘要及时间、目标身份/指纹、映射与纳入/排除清单、批次/检查点、幂等与中断恢复证据、新建验收记录清单、未决配置、审查者/时间。

将交接提交SHA记为`GBRevision`。必要配置未映射或任何必需验证失败均不能PASS；映射、代码、源数据或目标发生影响性变化后需发布新修订。

**完成：**G-B PASS即本任务完成，无需等待实例合并。任务三消费固定GBRevision及其GARevision；本任务不负责停源或切连接。
