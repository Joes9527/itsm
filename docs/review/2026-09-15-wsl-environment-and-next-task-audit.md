# WSL 环境、版本与后续任务审计（2026-09-15）

> 后续任务顺序已由[迁移验证唯一续办清单](../superpowers/plans/2026-09-15-migration-validation-ledger.md)更新：数据库目标差异是有意的迁移验证安排，不能单独判为环境故障；旧源系统并非本机 itsm 库。本文保留当时观测。

## 结论与证据范围

本报告核对 GitHub 主干 `018432df`、正在合入的 UI/端口维护分支、WSL `192.168.31.66` 的运行记录、Docker/PostgreSQL 元数据及任务文档。数据库检查仅使用只读查询；未迁移、授权、清理数据、启动历史容器或部署新主干。源码合并不等于运行环境升级或业务验收完成。

**目前能确认的是一个 WSL 开发环境，搭配多套数据库目标、克隆和测试遗留资源。没有证据证明本机存在真正的 GA、SIT、UAT 或生产环境。** 外部环境未核验，不能由本次检查推断其不存在。

`ga-*` 是历史资源名称；G-A 是验收关卡，`GARevision` 表示关卡修订，不代表 General Availability。`standard/candidate` 是执行策略，`NODE_ENV=production` 是构建模式，Git worktree 是源码副本，均不能直接计为应用环境。

## 1. 当前入口与版本

| 对象 | 当前证据 | 解释 |
|---|---|---|
| ITSM 前端 | 3010，源码 `24fe8546`，Build ID `fJAS7iqLTOgz4cevkG_pZ` | 已运行 A/C 主题；3001 已退出服务 |
| ITSM API | 8080，记录源码 `c3c880df` | 二进制沿用并校验哈希；不是本次主干重新构建 |
| Langfuse | 3000 | 不是 ITSM 前端 |
| KAF | API 8000、前端 5173 | 检查时服务停止；配置保留 |
| GitHub main（审计基线） | `018432df`，已含 PR #29、#27 | Assignment 源码已合并；不能再次列为待合并 |
| UI 集成提交 | `557ba91de` | 保留主干最新业务契约，只补有效主题差异；另有端口及运行管理改动 |

权威操作入口为 `/home/administrator/apps/itsm-kaf/stack`；运行配方及证据位于 `/home/administrator/.local/state/itsm-kaf-baseline-20260908`。当前开发环境约定见 [development-environment.md](../development-environment.md)。

原运行分支与主干存在分叉，不是简单落后。合并旧前端时若直接覆盖会丢失主干的 `catalog_task` 映射、必填版本字段、事件操作契约及较新 SLA 类型。本次冲突处理保留这些主干实现。旧后端分支的 SLA 日历、ticket type 初始化、任务权限投影和 CORS 改动仍需要与主干逐项比对；没有将旧后端分支盲目并入。

## 2. 到底有多少环境和数据库

| 统计口径 | 数量 | 不应混淆的含义 |
|---|---:|---|
| 已确认的维护应用环境 | 1 | WSL development，当前仅部分组件运行 |
| 已确认的 GA / SIT / UAT / 生产环境 | 0 | 仅限本次检查证据，不代表外部环境不存在 |
| 运行中的相关 PostgreSQL 集群 | 7 | 另有 1 个无关 Plandex，排除 |
| 已停止但保留初始化数据卷的相关集群 | 6 | 未启动，内部数据库和账本未实时查询 |
| 相关保留数据集群合计 | 13 | 不含脱离容器的孤立卷，未进行全盘扫描 |
| 额外停止的临时 PostgreSQL 容器 | 3 | 使用 tmpfs，数据目录已不存在，不计为保留数据库 |
| 7 个运行集群中的非模板逻辑库 | 31 | 含 7 个 postgres 维护库、2 个空 ai01、1 个空 langfuse 占位库 |
| 扣除上述维护/占位库后的命名库 | 21 | 其中 19 个有用户表，2 个测试命名库无用户表 |

### 运行中的七个相关集群

| 容器 | 主机端口 | 数据库及用途 |
|---|---|---|
| itsm-postgres-dev | 5432 | itsm、itsm_baseline_20260908、itsm_config_baseline_20260908、itsm_migration_20260914、itsm_intake_test、itsm_p1_integration_verify_20260901 |
| kaf-dev-postgres | 5434 | control_plane、kaf_baseline_20260908、kaf_config_baseline_20260908；另有空占位库 |
| acp-postgres | 5433 | control_plane、langfuse；另有空 ai01 |
| ga-itsm-20260914 | 无宿主机映射 | itsm_ga、itsm_ga_ready，以及两者的 restore_verify 副本 |
| ga-kaf-20260914 | 无宿主机映射 | kaf_ga，独立 schema 对齐目标 |
| gb-remediation-test-pg-20260914 | 无宿主机映射 | gb_replay_review、ga_acl_rehearsal_20260915、gb_review_test、gb_ticket_types_test |
| codex-workitem-assignment-test-20260914 | 127.0.0.1:36444 | sslvpn_test；当前无用户表 |

停止且保留数据的六个容器为：`itsm-candidate-20260914-pg`、`codex-handoff-live-pg-20260909`、`codex-workitem-convergence-pg-20260909`、`codex-handoff-pg-20260909`、`kaf-itsm-pg16-rehearsal-20260908`、`codex-intake-fields-pg-20260908`。历史端口映射不等于当前监听端口。

### 当前配置的数据目标

- ITSM canonical YAML：`172.25.0.2:5432 / itsm_ga_ready`，身份 `ga_runtime`，Redis `6389 / DB12`。
- KAF 配置：`127.0.0.1:5434 / kaf_config_baseline_20260908`，Redis `6380 / DB10`。配置存在不代表服务已运行。
- `itsm_ga_ready` 精确聚合计数：工单 3、用户 7862、目录 8；原 `itsm` 库：工单 18、用户 7834、目录 25。未读取用户或工单内容。
- 因此“换版本后数据不一样”不能只归因于 UI；数据库目标本身不同。不可将历史计划中的目录 ID 清单直接用于当前 8 个目录的目标库。
- 进一步核验：8080 的 PID 1896647、可执行文件/配置 SHA256 与 canonical recipe 一致；该进程 socket 的客户端端口 44666 与数据库会话精确对应，证实它实际连接 `itsm_ga_ready`，角色为 `ga_system`。readyz 调用后另外出现 `ga_runtime` 会话。两者均无迁移账本 SELECT，`ga_inspection` 有该权限。

### 克隆来源与残留

已确认 candidate 从 `itsm_config_baseline_20260908/public` 备份恢复；GB replay 从 `itsm_ga_ready-pre-b0.pgdump` 恢复；两个 restore_verify 库来自各自目标库的历史恢复验证。其他历史测试卷具体来源未全部证明。

原 `itsm` 库含 1140 个带数字后缀的迁移测试 schema；baseline 库含 1013 个，migration 克隆含 1104 个。这是测试残留及随克隆复制的对象，不是数千个环境。恢复后的统计估计为零不能证明表为空；上述业务数量使用单独 COUNT 验证。

## 3. 迁移和就绪状态的关键矛盾

`itsm_ga_ready` 账本含 36 条记录，最高为 046，已单独确认不存在 047；restore_verify 副本仍停留在历史 021。原 itsm 账本最高 019，config_baseline 最高 031。不能根据库名相似推断 schema 一致，最高编号也不能代替完整迁移依赖检查。

当前 API health 可用但 readyz 返回 503，原因是无权读取 `schema_migrations`。响应中的 `requiredSchemaVersion=038_work_item_controlled_retirement` 不能证明应该执行 038：它连账本都没有成功读取。

最新主干将 037 定义为人工准备阶段、038 定义为单独的受控退休阶段；普通运行检查排除 retirement。039–047 与准备证据的依赖必须按当前迁移计划验证。**不得用执行 038 的方式修复这次 readiness 失败。** 先核对运行二进制、检查身份的最小读取权限及就绪状态实现。`-status/-dry-run` 内部会 EnsureMigrationsTable，不能当作零写入检查。

参考：`itsm-backend/migration/migration_plan.go`、`migrator.go`、[数据库对齐交接](2026-09-14-database-reconciliation-handoff.md)。

## 4. Assignment 报告与下一步

已读取 [2026-09-15-work-item-task-assignment-report.md](2026-09-15-work-item-task-assignment-report.md)。报告第 57 行要求先合并 #29/#27，目前已完成。状态应是：**源码已合并，目标库迁移、匹配版本部署和真实浏览器验收待完成。**

发现真实验收脚本契约漂移：`itsm-frontend/tests/e2e/flows/work-item-assignment.spec.ts:66` 使用 `businessType=service_request`，主干 DTO 要求 `service_request_item`，后端任务查询按 businessType 精确匹配。新建 Requested Item 后脚本可能取不到任务，先于 A→B 断言失败。类型检查和用例列表通过并不验证这个跨端语义契约。

| 优先级 | 下一任务 | 完成条件 |
|---|---|---|
| P0 | 固定唯一发布候选及目标数据身份 | 对照主干与旧后端分叉逐项保留有效行为；前后端提交、构建、数据库、Redis、环境名称写入同一记录 |
| P0 | 修正 readiness/迁移预检 | 只读核实账本及准备证据，纠正旧 038 提示；提出最小权限和 047 迁移方案，不顺带执行退休 |
| P1 | 修复 Assignment 浏览器夹具 | 使用 canonical recordClass，补能捕获查询词表漂移的验证 |
| P1 | 匹配版本迁移、部署和人工验收 | 先落实目标、备份、回滚边界；A→B 后 A 失权、B 可完成，经理/申请人职责不变，终态审计及 Outbox 可核验 |
| P1 | 目录流程新版本 | 先对照目标库实际目录身份，再处理历史计划中的九项通用目录；保留 Copilot/SSLVPN 专用审批，Change/Incident 不混入 Requested Item；仅影响新实例 |
| P1 | Helpdesk 解决/关闭及审批驳回 | 普通工单详情接入现有 resolve/close 动作并收集 resolution；Requested Item、Incident、Problem、Change 分别调用所属领域动作，独立验证审批驳回、真实 UI、持久化及权限；BPMN 完成不代表工单已解决 |
| P2 | 姓名投影、申请人进度、专用表单、并发领取 | 使用受控后端投影，不能通过放宽任务/用户目录权限解决展示问题 |

源码证据：`TicketDetail.tsx` 当前编辑提交使用 updateTicket，表单没有 resolution；`ticket-api.ts` 已有 resolve API；`TicketProcessTasks.tsx` 仍显示处理人 ID，任务列表只代表当前身份可见任务，专用 formKey 入口未建立。历史 HTTP500 不是本次重新实测结果。

## 5. 文档为什么会反复误导 agent

1. 旧环境指南及 WSL 根 HANDOFF 仍写 3001、旧启动器和旧版本；本次维护分支将 3010 与 stack 管理器作为当前操作权威。历史手工脚本此前已禁用。
2. candidate runtime 文档仍称“准备中”，但后续交付账本已经记录 T3/G2/G3；G-B 交接仍写 G-A 缺失，而后续 G-A 证据已存在。应保留历史正文，并增加“已被何文件替代”的状态提示。
3. 源码、构建、运行进程、数据库版本和验收状态被混用；单个分支名或“测试通过”无法证明全部一致。
4. 环境资料缺少统一的所有者、保留期限、克隆来源及清理决策。下一轮应建立按资源登记的台账，先明确用途和依赖，再单独批准清理，不能按 ga/dev 名称批量删除。

本报告是带日期的审计快照；持续操作以环境指南和实际 stack 状态为准。停止数据库内容、孤立卷及外部生产环境仍未核验；本次未执行登录后的业务验收。
