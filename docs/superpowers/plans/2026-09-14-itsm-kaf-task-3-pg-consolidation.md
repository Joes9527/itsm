# 任务三：单 PG 实例、双逻辑库合并执行计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 将获准内容部署至一个PG实例内的两个独立逻辑库，完成真实ITSM/KAF联合验收、恢复验证及G-C交接。
**Architecture:** 沿用ADR-1A与DB-1～DB-4，两库独立owner/migration/runtime身份及迁移任务。先隔离演练，再经最终源截止点对账和明确窗口切换，始终只有一侧业务写入者。
**Tech Stack:** PostgreSQL、Docker/现有部署工具、ITSM API/Worker、KAF、Redis、对象存储。
**Spec:** [总设计](../specs/2026-09-14-itsm-kaf-database-convergence-design.md)，§6–7为本任务权威范围。
**Status:** draft；可独立做拓扑/角色方案和只读盘点；数据搬迁依赖G-A/G-B PASS，切换另需明确授权。

## Global Constraints

- 保留两个逻辑数据库及业务所有权，不引入跨库SQL/FDW/共用高权限账号。
- 不追加历史ticket导入，不执行ITSM R(038)，不清队列/回执，不合main或发送真实企业动作。
- 不将候选捕获接收器、旧60分钟观察或PG鉴权表存在当作真实业务/恢复验收。
- 每批最多60分钟报告；独立worktree与资源，单一协调后的共享写入者；生产权限缺失时不得声明生产部署完成。

## 输入与文件边界

入口仓库为`/home/administrator/project/itsm`和`/home/administrator/project/kaf`；先读各自AGENTS.md/治理，检查remote/HEAD/status/worktree并保留已有成果。独立分支建议`codex/chore/pg-instance-consolidation`；依据治理建立worktree并准确引入未合并依赖，不改既有运行配置作为准备动作。

消费两个交接提交：GARevision的`docs/review/2026-09-14-database-reconciliation-handoff.md`、GBRevision的`docs/review/2026-09-14-workitem-config-migration-handoff.md`，且后者引用同一GARevision。未收到PASS时只可做目标容量/版本调查、角色配置设计、操作步骤准备，不搬迁业务内容或启用候选应用。

读取ITSM `docs/superpowers/specs/2026-09-03-sslvpn-worker-production-readiness-design.md` ADR-1A/§11，以及总设计固定证据索引中的T3/T4/T5和运行手册。已有配置只读识别，部署文件修改必须在选定拓扑后列明实际路径、受影响服务和凭据引用供复核，不能把旧候选Compose自动当新部署。

**产物：**新增`docs/deployment/itsm-kaf-shared-pg-runbook.md`作为本拓扑启停/备份/恢复权威手册；新增`docs/review/2026-09-14-pg-consolidation-handoff.md`作为G-C事实交接。运行配置在各所属仓库维护，凭据和原始备份不入Git。

## 执行步骤

- [ ] 验证GARevision与GBRevision匹配、制品和目标指纹未漂移；上游失效回传对应任务，不能沿用旧PASS。
- [ ] 盘点目标PG版本/扩展、时区/排序规则、存储/WAL、备份、连接预算、共同故障域。固定两库实际名称、owner/migration/runtime及受限system身份，不强制按示例改名。
- [ ] 为各服务规划独立secret注入、一次性迁移及连接池；runtime无DDL/owner，常驻应用关闭自动迁移。将实际部署路径、版本、启停与恢复命令写入手册，命令审查后再运行。
- [ ] 盘点全部API/Worker/定时任务/Outbox/回调消费者及其他写入者；明确Redis DB、对象桶、本地附件引用、未决执行与回执的一致性范围。
- [ ] 在授权隔离资源演练纳入内容复制与恢复，验证对象/权限/序列、单库和整实例恢复影响；不得以高权限手工补表掩盖迁移缺陷。
- [ ] 使用实际runtime/migration身份验证正向连接、跨库CONNECT拒绝、runtime DDL拒绝；验证错误库/账号/缺secret失败退出及双Worker并发预算。
- [ ] 使用真实ITSM与KAF进程完成批准/拒绝、投递、回执、重复投递、恢复与未知动作拒绝；外部副作用使用明确隔离目标，捕获接收器不能替代KAF业务执行。
- [ ] 固定最终源→目标纳入/排除清单、演练记录清单、快照时间和两侧停写截止点；选择已演练的最终副本转换或增量对账路径。若保全在用系统必须导入被禁止的旧ticket，阻塞并提交范围决策。
- [ ] 在实际切换前提交完整可审阅手册、预检结果、准确目标、窗口、影响、恢复负责人及授权记录。无窗口授权只交付演练结果，不停源或切连接；旧备份授权不能复用。
- [ ] 获准后停写/冻结全部已盘点写入者，保护备份；按既定路径制作最终副本或应用增量，复核源截止点数据、未决任务、回执及序列。差额未解释则保持目标停写。
- [ ] 对最终副本重验G-A/G-B相关门禁，确认演练后增量无遗漏且排除数据未混入；更新连接，仅启动目标一侧获准写入者，保留准确切换时间/版本。
- [ ] 按手册预先固定的观察时长与场景运行联合业务和重启/恢复验收。时长不得短于继承的候选3600秒，实际发布要求更长时采用更长值；服务或配置改变后重验受影响范围，不冒用旧拓扑观察。
- [ ] 验证回切边界：尚无新写入先停新侧再回源；已有新写入则保全两侧并增量对账，经恢复责任人确认后执行，禁止直接旧快照覆盖。未知外部结果保留blocked/manual及审计。
- [ ] 提交手册、G-C事实、独立复核和`git diff --check`结果；披露PG鉴权A3/A4、Incident人工流程及宿主重启根因限制，不自动关闭既有R4或宣称所有上线条件满足。

## 输出接口：G-C

交接记录：`Gate: G-C`、`Status: PASS|BLOCKED`、GARevision/GBRevision、两仓库完整SHA/制品、拓扑/配置指纹、最终源/目标及截止点、角色权限、迁移/切换/恢复/联合业务/观察证据、已授权范围、维护者验收与剩余限制。

将交接提交SHA记为`GCRevision`；明确结果是“隔离演练通过”还是“指定环境已切换并验收”，两者不得互换。只有计划要求的实际目标、验证及授权全部满足才报告对应G-C PASS；生产就绪仍须符合既有发布门禁。

**完成：**指定范围G-C验收及手册交接完成即结束本任务，不追加鉴权改造、飞书或其他功能项目。
