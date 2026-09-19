# ITSM 数据适配与现有环境切换交接

更新：2026-09-15 10:16 CST。用户主动要求交接，本任务停止新增实现/部署。先读本文，再读 AGENTS.md、docs/agent-engineering-governance.md 和相关技能。**尚未切换应用，尚未通过 G-B/E2E。**

## 1. 目标和用户已经确认的决定

目标是将需要保留的旧 ITSM 配置/基础数据清洗并适配到新 ITSM WorkItem 模型，在已经存在的 `itsm_ga_ready` 上用新建数据做真实 E2E；保留原 DEV，以可切换配置复用现有 Backend/Frontend，不新建整套业务环境。

- 数据库计划分三项：任务1源码/结构/账本对账；任务2配置清洗适配与功能验收；任务3把两个 PostgreSQL 实例收敛为一个实例，ITSM、KAF仍保留两个逻辑数据库和业务所有权。任务3未启动。
- `itsm_ga_ready` **不是当前DEV运行库，也不是DEV全量克隆**。它保全了13张身份基础表，后续填入配置；源DEV另外保留。
- 用户明确：DEV现有26条WorkItem及评论、附件、关系不必迁到目标；旧流程不迁、不恢复、不执行。**不迁移不等于允许删除源数据**。
- 首期聚焦功能上线，不做MSP/多租户产品扩展；现有身份、权限、租户隔离仍保留。
- 以新规范为准，无法确定的旧优先级规则保留差异，不猜测。旧路由首期不迁，使用人工分派和规范流程。
- 新PostgreSQL鉴权存储尚未接入应用启动/登录/刷新/注销，重启恢复未验证；用户接受先明确记为未完成，不能算验收通过。当前固定运行代码实际用Redis刷新/撤销。
- 禁止：源停写、源库清理、清历史任务/Redis、R(038)、真实企业写入、合main、覆盖已有checkout。环境写入只能一个协调者执行。
- 尚待用户回答的一个问题：默认Incident中的“主管审批”首期是否需要？已发异步问题，**没有回答，不推定批准删节点**。SLA覆盖范围之前也未收到确认，不能当成计时验收通过。

## 2. 现在真实运行在哪里（交接时重新核对）

> **2026-09-15 13:45 CST 起环境口径已变更**（详见
> [`docs/review/2026-09-15-environment-and-migration-closure.md`](docs/review/2026-09-15-environment-and-migration-closure.md)）：
> 由**发布环境启动器**（`itsm-kaf-baseline-20260908`）在 **8080** 提供后端、**3010** 提供 web，
> 后端连接的是**克隆库** `itsm_ga_ready`（容器 `ga-itsm-20260914`，运行时角色 `ga_runtime`）；
> **不使用 3000/3001**；DEV PostgreSQL 保持稳定（只允许只读对比）。
> 协调方的 `ga-backend-switch.py` / `ga-backend-restart.py` / `ga-frontend-switch.py` /
> `promote-ga-backend-fix.py` / `pin-ga-frontend.py` 已 retire，不得再用于启停。

2026-09-15 10:15 CST 实测（**历史快照，切换前的 DEV 状态**）：

| 项 | 当前状态 |
| --- | --- |
| DEV Backend | PID147607，8080，uid1000；`/home/administrator/.local/state/itsm-kaf-baseline-20260908/bin/itsm-api-ui-workbench-7ed97de4` |
| Backend cwd | `/home/administrator/.local/state/itsm-kaf-baseline-20260908/config/itsm` |
| DEV数据库 | `itsm-postgres-dev / itsm_config_baseline_20260908 / public` |
| DEV前端 | PID24800，3001，node；cwd `/home/administrator/project/itsm/.worktrees/ui-workbench-runtime/itsm-frontend/.next/standalone` |
| DEV前端BuildID | `wlcY0limNxk71JkwpGrDC`，与原部署证据对应 |
| 新目标 | `ga-itsm-20260914 / itsm_ga_ready / public`，内部Docker网络，无宿主PG端口；最近内部IP172.25.0.2，启动前必须重新解析 |
| KAF新结构目标 | `ga-kaf-20260914 / kaf_ga / public`；不属于本批应用切换 |
| 切换记录 | 私有目录没有ga-process.json、没有ga-frontend-process.json；没有停止或切换DEV进程 |

源Backend SHA256：`afe09f5a016a774fc3b4d17159c632f4ab8ca4d3d760860d6d1f899bcab721fb`。原二进制无VCS build metadata，来源按 `itsm-kaf-baseline-20260908/evidence/bpmn-routing-deployment.json` 的制品哈希核实，不仅凭文件名。

## 3. 固定分支/成果与未提交工作

所有工作树基于 `/home/administrator/project/itsm/.worktrees/`，不得覆盖入口main。

| 工作树 / 分支 | HEAD及状态 | 用途 |
| --- | --- | --- |
| config-launch-integration / codex/feat/config-launch-integration | 代码基线 `c3c880df3331e4d1e09e3db7d1f1d4b3b115388d`，其后仅交接文档提交 | 当前后端集成源；closure更新及本文一并保存于本独立分支 |
| database-reconciliation / codex/chore/database-reconciliation | `93a9e6b6066aa01e4cace05911c82d656d97d71c`，clean | DEV文档与数据库清单已更新到本批初始化/运行准入阶段 |
| ga-workbench-task-contract / codex/fix/ga-workbench-task-contract | `546dcb930583f6f13e4ac44a610b35fa79e947d7` | 后端有界修复源，已审已cherry-pick入集成树 |
| ga-workbench-client-contract / codex/fix/ga-workbench-client-contract | `93480226fc91e9f4a9f929cca0b5ce453800b18d`，clean | 保留当前UI的兼容前端；独立审查通过，生产构建状态看本文末尾补充/私有构建证据 |
| ga-canonical-request-flow / codex/fix/ga-canonical-request-flow | 创建时基于c3c880df；10:15核对仍clean同HEAD，之后需复核代理停工反馈 | 服务请求默认BPMN模板修复准备，尚未合入/应用目标 |
| config-migration-review / codex/fix/config-migration-review | `e636bbc8` | 已修配置迁移工具和重放手册 |

G-A固定修订：`d91b587fe3ab40cc863321346d217d258a3a96d8`。G-A源码基线ITSM `0788a9bb196ab37a8389b3f366bed9877b2f72c3`，KAF `23f01476b8ea7293c423d608329241477a5336a5`。G-A是结构/配置迁移准入，不是完整应用运行准入。

重要旧成果：ITSM R1–R4 `7c8cee6fae181400573308bfb7e73043d11ed0b9`，KAF R5–R8 `e6fd8a50368a15505c98c8826dee5af975cef5c2` 已独立复审。真实隔离PG语义15例、Node30例、KAF相关68例通过；五批真实备份重放、回滚预演和幂等复跑通过。不要重做已完成批次或给原目标补造历史收据。

## 4. 本批确实执行过的目标变更（不是仅计划）

唯一私有根目录：`/home/administrator/.local/state/itsm-backend-switch-20260915/`。所有密码、DSN、argv/env、备份、原始数据只留这里，不贴到对话/Git。

### 4.1 备份与恢复演练

- 写入前备份 `backup/itsm_ga_ready-before-switch.pgdump`，1415258字节。
- SHA256 `9474f069b55b10d1f5183c2266536dc53bcada4b56535acbb9133d13ac6989ee`。
- 实际恢复至既有测试容器 `gb-remediation-test-pg-20260914` 新测试库 `ga_acl_rehearsal_20260915`。这是ACL演练库，不是新业务目标；保留原有gb_review_test、gb_replay_review、gb_ticket_types_test三库。
- 演练库新建ga_owner/runtime/system/inspection测试角色和随机私有凭据；不影响真实目标角色。恢复完成后运行真实角色准入检查通过。

### 4.2 ticket_types已初始化12条

- 定向工具源 `8ba80e186322d878d6801ff95b320b718f5fc356` 已审，本集成树原集成提交99909766。
- `ga-initialize-ticket-types` 制品SHA256 `01657ef5eb70dd88a6c60eb22254015ffe541643d56dcab817f30627e31c145a`。
- 原生有效admin id1/super_admin授权；plan→apply→即时repeat：12新增，复跑0新增/12保留，tickets仍0。
- 13张身份表、schema_migrations、四核心表ACL摘要不变。`ticket-types-execution-result.json`及before/plan/apply/repeat证据齐全。
- 只有operator CLI使用ga_owner/RLS_MODE=off。首次operator误用enforce被主动拒绝后，核对源码修正；**没有关闭数据库RLS，应用始终ga_runtime/enforce**。
- `/home/administrator/apply-ga-ticket-types.py`是一次性脚本，断言初始types0，不要再作为健康检查运行。

### 4.3 最小运行ACL与standard绑定已提交

- 私有原草案 `runtime-acl-review/` 有作者只读源码/目录审查，保留REVIEW_ONLY+ROLLBACK护栏，不能直接执行。
- 根任务复核后生成 `runtime-acl-operational.sql`：51个具名普通表目标、15个序列USAGE；从草案排除未实际接入bootstrap的auth_state两表；ga_system零增量。
- 新建唯一绑定 `ga_runtime / itsm-ga-ready-20260914 / standard`；runtime仅SELECT准入元数据，不能自行写binding/enrollment。
- 先在恢复库预演/提交/真实登录账号准入，再在目标事务回滚预演，最后提交。
- 除新增binding外全部原表数据摘要、core4权限/目录、角色、P037控制文件不变。无schema迁移，无ledger/证据改写。
- 目标实际uid1000运行 `InitRuntimeDatabases` + `ValidateExecutionRuntime` + `InspectRuntimeDatabase` 成功。证据 `acl-target/{before.json,committed.json,admission.stdout}`。
- 核验辅助工具曾误调用操作员 `InspectRuntimeMigrations` 导致ledger权限拒绝；已改为真正应用入口 `InspectRuntimeDatabase`→独立ga_inspection，**没有给runtime追加ledger/evidence权限**。不要重复这一误用。
- 权限只覆盖当前有界路径，不等于所有页面CRUD/上传/外部连接器都可用。出现permission denied要追具体源码，不得GRANT ALL。

### 4.4 配置/存储已准备，未启动

- `profiles/ga/config.yaml`：8080，ga_runtime+ga_system、RLS enforce，autoMigrate/autoSeed=false，private部署。
- 限定环境 `profiles/ga/environment.json` 含独立inspection DSN和控制文件；禁止继承旧DEV环境或_*FILE覆盖。
- execution standard，仅callback/event_audit启用；outbox/notification/kaf_worker/sla/escalation/webhook/tool_queue/request_async/embedding/connector_poll/cloud_discovery/cmdb_import_export/connector_diagnostics均关闭。队列持久化不等于投递成功。
- Redis复用127.0.0.1:6389，目标DB12已PING验证且0key；源DEV DB11未动，不允许FLUSH。
- 已创建独立MinIO桶 `itsm-ga-e2e-20260915`，已验证为空；原DEV附件未动。
- 新JWT secret仅目标配置，原DEV原secret保留切回。SMTP关闭，AI URL本地不可用地址；不计外部投递/AI验收。
- CORS白名单显式localhost/127.0.0.1:3001。当前前端其实是same-origin rewrite，NEXT_PUBLIC_API_URL空，ITSM_BACKEND_URL=http://127.0.0.1:8080。
- state根/必要profile/log目录仅uid1000可访问0700，config0600；原始env/凭据/备份仍受保护。预检已用真实uid1000，不只root。

## 5. 为什么必须适配前后端

原运行Backend7ed97de4与目标94分叉；原UI也不能直接被94旧前端覆盖。94前端有旧mock/缺失任务组件，而当前UI已修工作台、附件、会话/CSRF。因此保持现有UI，修必要契约，不用94前端整棵替换。

### 后端已审、已集成、已构建

- 源c0f022ea：任务DTO.uiActions，owning repeatable-read只读事务内复用实际command授权/目录快照/candidate scope；写命令仍重验。显式candidate任务不再回落requester作assignee。
- 源546dcb93：CORS允许Idempotency-Key。
- root集成1c6d49f4/c3c880df。制品 `itsm-api-ga-workbench`，SHA256 `62aefd8d0c3258158b07001b8cf5e9d9a13c159b2c7c85858a2b36a238045db6`。
- 实现红绿、相关service/DTO/CORS、独立复跑通过；真实独立PG12组scope/动作投影通过且读前后全表snapshot不变。临时PG容器/socket已清理。
- 不声称全后端测试通过：dto旧Force字段测试不编译；部分通知/escalation/SQLite锁等基线故障已复现。

### 前端已审固定93480226

- 基线c33479922，主补丁7f7862b9，后续93480226修静态确认框跨会话旧命令风险、批量部分成功intent回执复用。
- 保留工作台UI；canonical recordClass；ticket/incident更新version+operationId、失败复用、成功收据后刷新；close reason；必要列表调用点。
- 8套件156测试独立通过。typecheck同基线3条MenuItems.test.tsx缺icon，没有新增。
- **撤回的审查误判**：曾误称94 TicketEditFields无status，实际DTO:51有，ticket_service:514校验、541写入。所有基于误判的状态删除/拒绝补丁已完整撤回，原功能保留。不要再次根据旧消息删状态入口。
- Kanban里旧handleStatusChange只声明未挂接，当前可达编辑走TicketDetail；不要为死路径另造专业状态机。
- 创建、service-request API、Change/Problem退役动作未重构。没测的专业动作不能自动验收。

## 6. 流程准入与具体阻塞

真实目标选取：`selected-flow-catalog-private.json`保存XML，`selected-flow-catalog.json`及`selected-flow-independent-review.md`/`actual-parser-review.log`/`reachability-review.json`记录独立核对。

- generic：ID39 `ticket_general_flow`，XML SHA256 `6d7c436bb06acfef500df259d9b82e605b53b08bbf18dc8b33f8e48939d6a893`。实际Go parser/registry检查通过，固定图未发现直接外部调用，允许进入受控E2E；未声称生命周期已过。
- incident：ID27 `incident_emergency_flow`。`Activity_ManagerApproval`用incident_task/manager_approval，handler不支持；XML也未声明原生approval purpose。不能映射为acknowledge或静默no-op。等待用户确认首期是否需要主管审批。其notify generic回调也有canonical类型差额。**（2026-09-19 更新：XML 其实声明了——该节点带节点级 `<bpmn:metaData name="approval_required">true</bpmn:metaData>`，PR #87 起被解析为 `TaskPurpose="approval"`，"未声明原生 approval purpose"不再成立。相应地"首期是否需要主管审批"不再是文档层面的待确认项：它现在就是审批任务、会进审批待办；产品若要改回，应改流程定义本身（重新发布属共享库写操作，需单独授权），不要靠文档假设。契约见 AGENTS.md「Approval declaration contract」。）**
- requested item：ID34 `service_request_flow`。generic_task/complete_service和拒绝通知路径不支持canonical service_request_item，实际会TargetTypeMismatch。最小修复设计：完成用现有service_request_task/complete_request；拒绝先service_request_task/reject_request再ticket_task/notify_requester。不改专业状态机。正在独立工作树准备，目标尚未更新。
- callback=enabled可经webhook_handler直接HTTP，并不受execution.webhook=false完整约束！因此只按冻结的已审流程执行，不测试webhook/KAF/access-grant/provider动作。目标当前pending callback0。
- 云流程cloud_private_ops_flow、cloud_public_ops_flow存在原有XML格式错误，已记未验收；不要在此批静默扩大范围修全部20模板。
- 不运行itil-core/LoadAndDeployTemplates来修单个流程：它会同步全部模板。后续目标配置变更需精确定向、备份/回滚/幂等/证据。
- SLA：7个binding的sla_policy_id仍空，不会生成创建deadline；日历代码已集成，19补班/89假日2024–2026新配置尚未应用。SLA不计通过。

## 7. 启动/回退工具与剩余工作

私有辅助脚本在 `/home/administrator/`；均不是自动化守护进程。

- `ga-backend-switch.py check` 已通过，不停止/启动应用。
- `ga-backend-switch.py switch` 才会停止精确DEV PID并按新profile启动；`restore`按目标记录精准停目标并恢复原DEV。**接任者先重新核对所有准入和进程，不要一读文档就执行switch。**
- 脚本固定制品/config/env哈希、拒绝profile .env、环境白名单、真实uid1000运行；只TERM已确认PID，不强杀。start/记录/健康异常会恢复原DEV，5条模拟路径经独立检查；后来增加8080 socket归属PID验证，语法通过。真实启停/恢复尚未演练。
- `admitted-launch.json`固定后端制品和generic流程范围，**不是G-B通过，也不是前端已部署**。
- `dev-launch-snapshot.json`为原DEV二进制/argv/cwd/env恢复快照，含秘密勿输出；`dev-frontend-launch-snapshot.json`保存原3001 node/server/cwd/env及hash。
- **前端切换/回退脚本还没写，前后端协调回退还没实际执行**。需保留原`.next/standalone`不覆盖，用新worktree独立构建运行在原3001；若前端切换失败，恢复原前端并将Backend切回DEV。
- 原Backend/前端PID变化后必须重新核对快照，不能用旧PID贸然signal或把旧元数据当新启动源。
- 已准备但未执行 `browser/ga-smoke.spec.ts` + playwright.config.ts，复用仓库auth-utils，目标3001；仅初步登录/页面/read检查，不能当完整E2E。fixture凭据离线匹配目标admin哈希，未登录、未改密码，证据e2e-credential-precheck.json。不要把密码/token写日志。

接任顺序：
1. 读AGENTS/治理和本文，核对工作树/未提交文件/构建进程；保留本文与closure更新。
2. 核实前端93480226生产build是否成功、standalone public/static是否就绪；独立审查已过，不必重复全部旧测试。
3. 固定前端制品/启动参数，补精确3001切换和失败回退，确认原3001保存完好；重新跑后端check和存储/目标/流程哈希核对。
4. 准入满足后切换现有8080/3001，仅新建generic先跑真实UI/API：登录、创建、幂等复跑、详情/刷新、分派/评论/任务动作，核数据库持久化。新增权限缺口逐项源码审查，不改core4/P037/ledger。
5. 处理Incident待确认问题、完成服务请求模板补丁独立review+真实回调测试，定向应用目标后再验收这两专业流程。不要把generic通过当它们通过。
6. 验证当前Redis登录/刷新/注销及应用重启会话行为，PostgreSQL新鉴权仍另列未完成；更新最终EnvironmentRevision与G-B差额。
7. 应用验收完成后才讨论任务3单PG实例/两个逻辑库收敛。现在不合main、不退役或删除任何库。

每批最多60分钟报告差额，上一批从09:23 CST开始，本次用户10:15左右主动要求handoff；没有后台自动继续部署任务。不要无声扩成全部产品修复。

## 8. 可复用入口文档与证据

- 本树 docs/review/2026-09-14-config-launch-closure.md（与本文一并交接保存）
- database-reconciliation/docs/development-environment.md、docs/review/2026-09-14-postgresql-database-register.md（93a9e6b6已提交）
- database-reconciliation/docs/review/2026-09-15-dev-workitem-data-preservation-audit.md（旧保留候选被最终不迁旧数据决定覆盖）
- docs/review/2026-09-14-database-reconciliation-handoff.md（固定G-A）
- ga-workbench-client-contract/docs/review/2026-09-15-ga-workbench-client-contract.md
- `/home/administrator/.local/state/itsm-task2-remediation-20260914/ga-workbench-task-contract/results.md`、frontend-independent-review-final.log
- `/home/administrator/.local/state/itsm-task2-remediation-20260914/frontend-ga-contract/production-build-93480226.log`、build-evidence-93480226.json
- 私有批次environment-revision-preflight.json是“预检通过、应用/E2E待执行”，不能用于验收或切换完成声明。

## 9. 操作提示

宿主是Windows PowerShell，Linux工作经 `wsl -d Ubuntu -- ...`；普通git/go/npm使用administrator，读root私有凭据/执行协调脚本才 `-u root`。不要给root全局safe.directory放开所有仓库；需要查询git可用subprocess user=1000。

WSL shell会吞多层引号；复杂SQL用Python subprocess参数数组+stdin，避免SQL $$被shell扩成PID。不要打印完整docker inspect/env/DSN、原始用户行。根目录没有rg可用时用git grep/Python。所有真实目标变更已列明；一次性apply脚本不要重放。新代理需重新建立团队上下文，不能假定旧子代理仍自动运行或消息永久可用。


## 10. 用户中断后最终冻结核对（10:18 CST）

- 前端93480226生产构建**成功**：build-evidence记录exit_code=0，完成于10:06:04 CST；日志末尾Standalone runtime prepared。
- 新前端BuildID：`DT5sd124Ku_V1aYniJlbU`。
- 新server.js：`/home/administrator/project/itsm/.worktrees/ga-workbench-client-contract/itsm-frontend/.next/standalone/server.js`，SHA256 `14bcde2ef3d286eaf6b067c9e402dd9fe35b0929661afe516be3e97976dc6da5`。standalone/public和standalone/.next/static均已就位。**未启动此制品**。
- 服务请求模板工作树仍clean、HEAD仍c3c880df：最小设计已批准，但实现尚未落盘，接任者从红测试开始，不要假定已有补丁。
- 团队状态：后端/前端独立复审任务已完成；前端构建任务与服务请求准备任务被用户中断。已检查相应工作树没有仍运行的next build/go test/Jest/npm build进程。没有后台自动部署。
- 原DEV PID147607与24800仍存在且cwd/exe匹配。两新应用进程记录均不存在。
- 完整HANDOFF和closure更新保存在config-launch-integration独立分支；仓库入口HANDOFF.md只是用户可直接引用的新增指针，不改main业务代码。
