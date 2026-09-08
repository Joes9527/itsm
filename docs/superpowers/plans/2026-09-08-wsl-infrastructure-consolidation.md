# KAF / ITSM WSL Infrastructure Consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in the current isolated worktree. Shared infrastructure mutations execute serially; do not dispatch concurrent migration writers. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 WSL 上 KAF / ITSM 开发 PostgreSQL、Redis、MinIO 整合为专用共享开发实例，保全现有数据、隔离 CI，并同步文档。

**Architecture:** PostgreSQL 共用实例、独立数据库与账号；Redis 共用实例、独立逻辑 DB；MinIO 共用实例、独立 bucket 与凭据。先盘点和试迁移，再在明确维护窗口暂停所有写入后切换；旧实例保留供恢复，跨应用仍使用既有 API 契约。

**Tech Stack:** WSL、Docker Compose、PostgreSQL、Redis、MinIO、KAF Qdrant、现有 Go/Ent 与 Python/Alembic 应用。

状态：accepted；任务 1 只读盘点进行中。SSH 身份已经维护者核验并恢复连接；源版本差异需要决策，尚未创建目标栈或迁移。设计依据：[已批准设计](../specs/2026-09-08-wsl-infrastructure-consolidation-design.md)。

## Global Constraints

- 本次只处理 WSL 开发环境；供 Mac / WSL 联调使用；CI 独立。
- 全部现有持久数据和有效临时状态保留；不延长过期验证码、会话和租约。
- PostgreSQL、Redis、MinIO 分别为一个共享开发实例；各自逻辑所有权不合并。
- 不执行生产切换，不复制生产数据；目标 PostgreSQL 固定为 PROD 基线 16.14 x86_64 Alpine/musl。ITSM PG17→PG16 的逻辑迁移须专项恢复验证，不改变 RLS 模式。
- 使用新开发栈；旧实例停止应用写入后保留，不自动销毁。
- 同一时刻仅一个共享数据变更执行者；不得触发未经控制的邮件、VPN 或其他外部副作用。
- Mac、WSL、后台消费者、定时任务与 CI 的连接来源均需盘点；只停止已确认归属的进程。
- 不输出 dotenv、完整进程环境、原始 Docker inspect、连接密码、用户内容或启动描述中的秘密。
- 文档同步是完成门禁；源代码交付、迁移、切换、业务验收分别报告。

## 文件与记录落点

仓库交付：

- ITSM 本计划及 `docs/superpowers/specs/2026-09-08-wsl-infrastructure-consolidation-design.md`。
- ITSM `docs/deployment/wsl-infrastructure-consolidation-runbook.md`：任务 1 后编写经过盘点的非秘密操作清单、验证和恢复说明。
- ITSM `docs/review/2026-09-08-wsl-infrastructure-consolidation-report.md`：实际迁移证据摘要与未完成项；未执行前不生成成功报告。
- ITSM / KAF 开发文档的精确路径见任务 7。

WSL 本机私有交付：`/home/administrator/.local/state/kaf-itsm-dev-consolidation-20260908/`，目录权限 0700，含秘密或数据的文件权限 0600。只有只读盘点确认主机、用户与目录归属后才创建此目录。

- `inventory.json`：源端点、版本、对象归属、进程与容量；只包含允许公开到维护者的运行元数据。
- `target-manifest.json`：新开发栈镜像摘要、端口、数据库/DB/bucket 映射、卷、凭据文件路径、资源与持久化策略。
- `compose.yaml` 和 `secrets/`：统一开发栈定义与私有凭据；不覆盖源栈配置。
- `backup-manifest.json`、`rehearsal-results.json`、`cutover-manifest.json`、`acceptance-results.json`：文件哈希、时间、源/目标标识、步骤退出状态和统计结果。
- `backups/`：原始备份；`logs/`：经过秘密过滤的执行日志。私有交付不提交仓库。

本计划不预先编造容器 ID、镜像版本、可用端口或资源容量。任务 1 的产物是后续精确命令的输入；未通过盘点门禁，不创建新栈，也不执行导入。

## Task 1：只读盘点与迁移清单

**Files:** Read 两仓开发环境文档、WSL 实际启动配置与各自 Compose；Create 私有 `inventory.json`、`target-manifest.json`、仓库 runbook。

**Interfaces:** 输入为当前 WSL 运行环境；输出清单必须包括 `sources`、`consumers`、`ciDependencies`、`capacity`、`targetMappings` 和 `backupMethods`。每个资源记录 engine/version、host/port、database/Redis DB/bucket、owner、凭据文件来源，不记录凭据值。

- [x] 验证已有 SSH 公钥连接。候选连接来自当前环境文档，失败时停在连接核对，不猜密码或改网络配置：

```bash
ssh -o BatchMode=yes -o ConnectTimeout=10 -p 22222 administrator@192.168.31.66 'hostname; id -un; uname -s'
```

- [ ] 读取资源元数据：

```bash
ssh -o BatchMode=yes -o ConnectTimeout=10 -p 22222 administrator@192.168.31.66 'df -h /home/administrator; free -m; docker ps --format "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Ports}}"'
```

- [ ] 对已发现容器定向读取镜像摘要、卷挂载、Compose project/service 标签；不返回 `Config.Env`。依据启动描述和实际 PID/cwd 确认 Mac/WSL/Worker/定时任务与实例的绑定。
- [ ] PostgreSQL 通过已有受保护凭据读取服务端版本、数据库大小、扩展、角色、表/序列/权限统计；Redis 使用 SCAN 和 TYPE/PTTL 统计类型与过期时间，不用 KEYS 或打印键值；MinIO 获取 bucket、版本管理、对象数量及大小清单，不输出对象内容。
- [ ] 核对 PostgreSQL/Redis 镜像版本及容量预算、MinIO 对象和 CI 归属；检查 ITSM 本地附件回退目录；记录 Qdrant 集合与引用。KAF Redis DB10、ITSM DB11 仅是历史线索，不作为未核验值使用。
- [ ] 在 runbook 中固化源/目标映射、可用端口、停止对象、备份/恢复工具及版本、磁盘预算和预估维护窗口。目标 Redis 持久化与淘汰策略依据实际状态用途确定；若双方语义冲突，先解决冲突，不静默丢弃状态。
- [ ] 检查所有目标均无端口/卷冲突，CI 拆分范围可追溯，源中每类数据均有恢复方法；否则任务保持未完成。

**验证与提交：** inventory 字段完整且无密码、消费者覆盖齐全；runbook 本地引用可解析、`git diff --check` 通过。只提交脱敏 runbook。

## Task 2：备份、保全与恢复工具验证

**Files:** Create 私有 `backups/`、`backup-manifest.json`；Modify runbook 的命令和验证部分。

**Interfaces:** 输入任务 1 清单；输出每项备份的 sourceId、startedAt/finishedAt、toolVersion、file、bytes、sha256、恢复验证结果。

- [ ] 为已识别的源创建非破坏性备份；使用受保护凭据文件，PostgreSQL 按数据库导出并保全需要的角色/权限定义，不把密码放入 argv。
- [ ] Redis 保全原始持久化/逻辑备份以及捕获时间、绝对到期信息、Stream 消费状态。选定的导出方式须在试迁移中证明能合并两源映射，不能用覆盖目标 RDB 的方式先后“恢复”两套数据。
- [ ] MinIO 保全应用需要的对象、元数据、历史版本/删除标记；记录文件附件路径和数据库引用。对启用 multipart 或加密的对象采用内容核验或工具支持的校验值，不以 ETag 等同 MD5。
- [ ] Qdrant 使用当前版本支持的快照/恢复方式保全，并记录集合设置、数量与关联文件来源。
- [ ] 私有保存启动描述、实际运行二进制、dotenv 和 Git 版本基线；ITSM 专用修复二进制保持原状。
- [ ] 核对哈希和完整性，实际恢复演练前不得标记备份可用。任何导出失败立即停止，禁止以空文件继续。

**验证：** 所有源资源存在备份条目；备份可读取且校验一致；秘密未进入仓库或日志。无需写模拟业务测试来代替真实恢复验证。

## Task 3：创建目标栈并试迁移

**Files:** Create 私有 `compose.yaml`、`secrets/`、`rehearsal-results.json`；Modify runbook。

**Interfaces:** 输入已核验版本、资源映射和备份；输出隔离目标栈与恢复结果。只接受 target-manifest 中的容器、卷和端点。

- [ ] 使用盘点过的版本/镜像摘要创建专用开发 Compose project、持久卷和资源预算；不复用 CI 卷、不指定源容器名。秘密文件由主机私有目录提供。
- [ ] 先渲染并检查 Compose 的端口、卷和 secrets 引用，再启动目标数据服务。渲染结果在私有目录检查，只输出脱敏摘要。
- [ ] PostgreSQL 目标固定为 16.14 x86_64 Alpine/musl，核对 pgvector 等实际扩展兼容性；使用 PG17 兼容导出工具取得 ITSM 源备份，在隔离 PG16 目标验证 SQL/数据/权限/排序规则，不直接复制数据卷，不忽略恢复错误。
- [ ] PostgreSQL 创建双方独立数据库；按现有权限模型配置 runtime/system/migration，恢复数据并核验归属、序列、约束、索引、扩展和权限。禁止启动时使用全局超级用户替代角色。
- [ ] Redis 按目标逻辑 DB 映射恢复全部永久键和仍有效状态；到期时间扣除备份与恢复耗时。验证所有实际出现的数据类型、队列顺序及消费组，禁止运行应用清理任务。
- [ ] MinIO 创建独立 bucket 与受限凭据，恢复对象、元数据与应用所需版本语义；验证从数据库/知识索引引用能定位到正确对象。
- [ ] 验证 Qdrant 快照可恢复及检索；保留原服务时不主动重建集合。
- [ ] 隔离目标应用消费者和外部写入，验证源/目标数量、关键值、关系及代表性文件内容。记录时长、占用空间与失败补救。

**门禁：** 恢复成功、数据与权限核验通过、容量足够、CI 无影响，才能准备正式窗口。测试环境成功不表示正式数据已切换。

## Task 4：正式窗口准备与最终迁移

**Files:** Create 私有 `cutover-manifest.json`；Modify 已核验的 Mac/WSL 启动配置与连接文件，仅在本任务窗口内修改。

**Interfaces:** 输入试迁移证据与维护者确定的窗口；输出最终快照、目标配置指纹、所有消费者暂停记录与迁移核验结果。

- [ ] 将演练测得的时长、窗口开始/结束、停止对象、值守与恢复责任向维护者确认。此前“允许停机”不等于当前任意时间可以中断其他开发者。
- [ ] 按实际 PID/cwd 逐项暂停 Mac/WSL API、前端写入来源、Worker、定时任务及其他消费者；检查外部写入与 `delivery_unknown` 清单，确认源无继续写入。
- [ ] 获取最终数据库、Redis、对象、附件和 Qdrant 一致性基线；所有写入方停止后再确认快照完成。
- [ ] 在目标重做最终恢复；仅清理已在 target-manifest 中声明的试迁移数据，不运行全局 Docker prune、FLUSHALL 或通用 fresh/bootstrap。
- [ ] 执行任务 3 同等数据、TTL、权限、引用核验；任何不一致不开放新环境写入。
- [ ] 更新应用实际使用的配置来源，保留原配置备份；核对连接目标和凭据归属，不更新原专用 API 二进制，不执行无关版本升级。

**门禁：** 所有切换条件通过，才进入任务 5。此时未开放新业务写入，可通过恢复原连接和原应用回退。

## Task 5：应用恢复、受控验收与回滚决策

**Files:** Create 私有 `acceptance-results.json`；Create 仓库脱敏迁移报告。

**Interfaces:** 输入最终迁移结果与任务清单；输出每项验收的目标、时间、结果及未覆盖项。

- [ ] 先恢复应用，验证 Mac/WSL 的登录、目录、流程、历史工单、会话、附件下载与知识检索；检查实际连接落到新端点。
- [ ] 逐个恢复消费者；确认只有目标消费者运行，旧消费者保持停止。核对队列、待执行任务、过期租约与执行账本，不绕过现有恢复规则。
- [ ] 验证 Intake 创建/读取、BPMN 审批与结果投影。真实 VPN 加组或邮件必须先确定受控对象、允许的副作用和清理责任，不在健康检查中顺带触发。
- [ ] `delivery_unknown` 进入现有对账流程，不强制重发；核对任何已发生副作用与业务状态。
- [ ] 验证 CI 不接入开发数据，原 CI 对象仍可用；记录解除原 MinIO 共用依赖的具体证据。
- [ ] 若验收失败，在开放写入前恢复旧连接；开放写入后必须暂停并备份新状态，比较新增数据和外部动作，再制定恢复操作。不得直接切回旧库。

**门禁：** 必须区分数据恢复、应用健康与真实业务验证；未执行的受控外部验证保持未完成，不标记“全部通过”。

## Task 6：稳定观察与旧实例保留

**Files:** Modify cutover/acceptance 记录及迁移报告。

**Interfaces:** 输入目标栈运行与验收数据；输出旧实例和备份的保留清单及恢复指引。

- [ ] 检查运行错误、Redis 内存/淘汰、数据库连接与任务年龄，确保双方不会因共用资源持续影响对方。
- [ ] 证明旧应用没有写入旧实例；记录新环境开始接受写入的时间和备份基线。
- [ ] 保留原容器/卷/备份及配置，不自动清理；清理期限和动作由维护者另行决定。
- [ ] 将新环境纳入可恢复的周期备份说明；不未经请求创建定时自动化。

## Task 7：同步两仓文档和架构图

**Files:**
- Modify ITSM `docs/development-environment.md`、`docs/DEVELOPMENT_GUIDE.md`、`docs/development.md`、`docs/README.md`。
- Modify KAF `docs/kaf2/development/local-wsl-environment.md`、`docs/06-development.md`、`docs/kaf2/operations/10-remote-dev-vscode-workflow.md` 及适用入口。
- Modify 本计划、设计实施状态、runbook 和脱敏迁移报告。
- Modify 本机 `/Users/julian/.codex/visualizations/2026/09/08/kaf-itsm-architecture/architecture.json` 与生成的 HTML、证据及回执。

**Interfaces:** 只使用实际验收通过的拓扑事实；生成两仓可评审的文档提交和一致的图形证据。

- [ ] 为 KAF 文档任务建立独立 worktree 和分支，保留其本地未推送提交与产物；不直接在 main 提交。
- [ ] 更新实际端点、数据库、Redis DB、bucket、配置来源、启停、备份与 CI 边界；不记录秘密。
- [ ] 将公共环境事实收敛为 ITSM 环境文档的权威来源；KAF 保留项目特定操作并引用公共来源。跨仓链接使用仓库地址，避免个人绝对路径成为团队入口。
- [ ] 更新架构图，清楚表示共享实例和独立逻辑数据；保留应用组件与 VPN 示例，不把 Qdrant、pgvector 或两套业务库合成一种存储语义。
- [ ] 使用 archify 执行 validate、deliver、visual-check 和深浅色视觉复核，更新文件哈希、代码/环境证据与交付回执。
- [ ] 更新开发迁移 backlog 和旧实例保留条件；生产 backlog 保持独立。补充实际结果和未覆盖验证，未执行项不标记完成。
- [ ] 检查文档链接、重复事实、占位内容和秘密；各仓 `git diff --check` 通过后分别提交。按仓库规则提交评审，不自动合并或推送无关提交。

## 最终交付与自检

- [ ] 设计各章节均有对应任务：目标/边界 → 1、3；保全 → 2；切换 → 4；回滚/验收 → 5、6；文档 → 7；PROD 排除范围 → Global Constraints。
- [ ] 本计划只读盘点、共享环境写入、正式停机与外部副作用四个阶段明确分开。
- [ ] 私有清单之间资源 ID 与路径一致；任何版本/端口/权限不再依赖未核验历史默认值。
- [ ] 最终交付同时提供两仓提交、实际基础设施状态、验证摘要、遗留项、恢复路径及图形产物。
- [ ] 只有全部必要验收和文档任务通过，才声明开发整合完成；部分任务失败时按实际阶段报告。

## 执行记录：2026-09-08

已完成计划自检和本地提交。首次 SSH 预检使用严格主机身份校验，返回 `Host key verification failed`；未执行任何远端命令，未修改 known_hosts 或目标环境。下一步由维护者通过 WSL 本机控制台核对主机公钥指纹，匹配后再受控更新本机信任记录并继续任务 1。该连接阻塞不影响设计结论，也不能视为迁移失败或数据异常。

后续记录：维护者提供的 WSL 本机指纹与远端一致，本机备份并定向更新信任条目后，严格校验连接通过。源实例版本、运行配置和资源容量已部分核对，见 [实际盘点记录](../../deployment/wsl-infrastructure-consolidation-runbook.md)。发现 PostgreSQL 16/17、Redis 7.2/7.4、MinIO 2024/2025 差异，需要在创建目标栈前确定版本统一范围。任务 1 未完成，未改动远端数据或配置。

版本决定已收敛：维护者要求 PostgreSQL 对齐 PROD 16.14 Alpine。ITSM 17→16 的完整逻辑恢复与业务兼容性是任务 3 的硬门禁；Redis、MinIO 版本尚未确定。
