# Dev恢复与同库验证schema：完整实施合同

**Status:** draft（完整设计已完成并经独立复核，供实施审查）；维护者已确认总体方向并要求本轮完成设计。本文冻结实现选择，独立审查是实现前门槛，不把详细设计委托给执行Agent。未部署的接口/表/字段在下文均为本次新增契约，不能当成现有能力调用。

**权威关系：** 本文补充[恢复设计](2026-09-16-dev-restoration-two-database-design.md)§7，取代任务包A/B此前“设计由执行Agent补齐”的条目。AGENTS.md的领域、租户、审计、真实迁移回执规则保持生效。任务顺序见[A](../plans/2026-09-16-dev-workflow-recovery.md)、[B](../plans/2026-09-16-schema-validation-clone.md)，实际状态只写[唯一清单](../plans/2026-09-15-migration-validation-ledger.md#two-database-execution)。遇源码或源对象不符合冻结输入时报告具体差异，不自行改变业务或另选架构。

## 1. 目标、范围和固定取舍

- 原Dev保持`itsm_config_baseline_20260908.public`；验证使用同库`migration_validation`，不新增PG容器。不移动Dev，不合并ITSM/KAF业务表。
- 克隆准入新增规范迁移，编号由合并时注册表下一个空位确定；这是唯一允许执行人根据并发事实确定的编号，不允许更改迁移语义。新迁移创建下述准入表并修复schema绑定基础设施；源实际升级前仍标047。
- 克隆来源必须是正常P准备路径的Dev，不支持“克隆的克隆”。R038、clone目标down/reset均拒绝；修复采用前向普通迁移，刷新采用封存后重新克隆。不要为本任务扩展任意拓扑迁移平台。
- 历史失败实例保留且停止无效重试，不新建通用回调修正/跳过/取消框架。最终清理针对冗余数据库/临时资源，保留在Dev中的有审计失败样本不阻止无关旧库退出。

## 2. A：Dev流程和冻结失败的确定方案

### 2.1 实际输入与新流程版本

2026-09-16只读核对：tenant1 definition65，key=`ticket_general_flow`，version=`1.3.0`，XML SHA256=`6d7c436bb06acfef500df259d9b82e605b53b08bbf18dc8b33f8e48939d6a893`。该图包含assign、条件审批、handle、条件escalate、resolve、notify_requester；`Flow_Reject`实际流向handle，末尾EndEvent_1名称为“工单关闭”但无关闭命令。这是配置缺陷，不能原样保留为新验收标准。

本次创建tenant1的新key `ticket_general_flow_v2`、初版`1.0.0`（若已存在不同内容则冲突停止），从已核对源版本派生而不修改原XML。只切换tenant1当前绑定825、827的definition key，保持其条件/默认/优先级/业务类型及其它字段，使用现有领域API与CAS审查；tenant2绑定830保持原状态，列为未适配而非自动复制。实际ID及摘要漂移必须重新对照，不按命名猜测。

新图固定如下（节点ID沿用原图，新增节点显式列出）：

| 节点 | 新行为与条件 |
| --- | --- |
| StartEvent_1→Activity_Assign | 无handler fulfillment，`assigneeSource=work_item_assignee`，名称“确认接单”；实际分派通过现有工单分派命令；无owner拒绝完成 |
| Gateway_Approval | 沿用approval_required可信配置；true进审批，false进处理。缺失按原明确默认false；客户端临时任务变量不得覆盖创建时冻结值 |
| Activity_Approval | 保留approval目的和现有审批参与者解析；禁止owner-bound；缺合法审批人明确阻塞，不能自批或按当前owner补人 |
| Gateway_ApprovalResult | approved→Activity_Handle；rejected→新增EndEvent_Rejected（拒绝结束）；未取得权威审批结果不选成功分支。修正原Flow_Reject，禁止拒绝后继续处理 |
| Activity_Handle | 无handler owner-bound fulfillment“履约处理”；必须有权限，工单处于in_progress，记录处理证据后才可完成 |
| Gateway_Escalate | 沿用可信need_escalate布尔配置；true→Activity_Escalate，false→Activity_Resolve。用户不能在complete payload临时改条件跳过 |
| Activity_Escalate | 无handler owner-bound“确认升级处理”；完成前验证本流程开始后通过现有`POST /tickets/:id/escalate`产生的成功领域操作receipt（同WorkItem/tenant）。不得以version变化或自由文本证明已升级 |
| Activity_Resolve | 无handler owner-bound“确认解决记录”；完成前验证当前WorkItem为resolved、resolution非空，且本流程阶段内存在成功领域解决receipt |
| Activity_NotifyRequester | 移除旧ticket_task通知callback，由既有领域解决命令产生唯一通知意图；在新XML变更说明明确取代关系，验收有通知intent且无重复。外发仍关闭，不能把intent称发送成功 |
| 新增Activity_Close | 无handler owner-bound“确认关闭”；WorkItem已通过领域命令closed才可完成，随后进入EndEvent_1；EndEvent_1重命名“流程完成” |

正常UI：创建→分派/转派（原因）→接单确认→审批（若要求）→编辑为in_progress→履约任务完成→升级（若要求）→编辑为resolved并填resolution→解决确认→关闭弹窗→关闭确认。现有TicketDetail使用版本化`PUT /tickets/:id`的status/resolution/version/operationId；不切去遗留resolve/close实现。升级使用现有版本化escalate命令；UI若缺入口只添加此领域命令的窄表单（原因、当前版本、operationId），不在前端推导权限。

### 2.2 单一流程约束和生命周期门禁

新增定义级`workItemLifecycleContract=generic_fulfillment_v1`及任务级枚举`workItemPrerequisite=assigned|in_progress|escalated|resolved|closed`，仅上述新generic定义启用；未知值、专业class、handler与该枚举混用在发布时拒绝。不是任意表达式平台。

合同通过实例固定的不可变定义版本引用（ProcessDefinitionID + tenant）读取；当前 state_snapshot 不是定义快照，不为本次另建副本，不回填旧实例。现有更新服务禁止修改定义 XML/ProcessVariables，被实例引用的定义不能删除；这是应用服务约束，不宣称数据库防篡改。新版本启用不得改变旧实例的合同。BPMN命令、只读UI actions与Ticket领域命令使用同一服务内纯规则实现（建议`service/generic_workflow_gate.go`），依赖当前事务client，不能HTTP回调自己或从GET补写descriptor。

门禁：进入in_progress须原审批通过/明确无需审批且当前节点为Handle；resolve须Handle已完成、必要升级receipt存在、当前等待Resolve确认；close须Resolve确认完成且当前等待Close。检查权威流程/task/领域receipt，不信任客户端approvalResult或instance旧assignee快照。对于该新合同，公开complete/set_variables等入口拒绝改写approval_required、need_escalate、approvalResult；前两项来源于冻结定义配置，approvalResult只能由现有审批领域结果投影，不能从普通任务变量读取未验证值。拒绝结束的流程不可经直接status/resolve/close/batch接口绕过；取消等既有领域规则不被此处授予额外权限。所有能改变generic状态的领域入口统一经过此门禁，旧定义没有该合同则沿用原逻辑，不全局重写所有业务。

同一事务先按WorkItem ID锁权威WorkItem，再锁关联instance及必要task，所有触及此合同的状态/任务/分派路径统一锁序；并发状态变化或重复完成必须版本/CAS及operation receipt去重。任务完成不自动冒充工单解决/关闭。终态actual actor与responsible user按既有绑定合同冻结。

#### 2026-09-17 承载方式裁定（实现前独立复核）

- 在 BPMNProcess 增加与任务相同的可选 ExtensionElements，流程级 metaData 承载 workItemLifecycleContract；任务级 metaData 承载 workItemPrerequisite。不新增数据库列，不改变条件表达式 chardata 语义，也不开始执行历史流程级 service_task_type/action。
- 声明读取必须区分缺失、显式空值、重复和未知值；现有 GetMetaData 合并缺失/空值且取首项，不能单独用于严格合同校验。无声明兼容，显式非法声明拒绝。
- 真实 recordClass 在绑定/启动业务上下文验证，不能用定义 category 推断。实例引用缺失、跨租户或解析失败必须失败关闭，不能退回最新定义或无合同路径。
- 对新合同，approval_required/need_escalate 的唯一配置来源为实例所引用定义行的 ProcessVariables 同名字段，仅接受 JSON boolean；缺失均按 false，显式 null/字符串/数字在发布及启动校验时拒绝。验收 v2 明确保存两个 false，审批/升级分支测试使用各自不可变测试定义的 true 配置。公开启动、complete、set_variables 输入携带任一保留键（含 approvalResult）即拒绝，即使值与配置相同；不得通过绑定 overrides 或普通输入覆盖。启动服务在同一事务从固定定义规范化配置并写入新实例，后续门禁从同一固定定义取得条件；approvalResult 仅由现有审批结果投影提供。旧定义不改变既有变量规则。
- 测试覆盖声明边界、新版本启用不影响旧实例、XML/ProcessVariables 更新拒绝、旧实例零回填及启动/后续变量覆盖负例。

### 2.3 输入校验与历史callback2处置

新任务命令：assign缺少合法正整数assignee_id，在写task完成前按用户输入错误拒绝；update_status缺失new_status时保留现有in_progress默认，显式提供时必须为非空字符串（对齐A1已裁定兼容边界）；未知/非法状态仍由领域规则拒绝。定义缺陷保留既有blocked-plan合同，不能将所有blocked变HTTP错误。简单UI无法提供且固定配置未满足输入时后端complete=false，GET只读。

已持久化回调：执行器领取后、调用handler前，用同一handler契约验证冻结payload。对已知handler/action且缺失/无效必需参数的持久回调，返回现有`handler_contract`阻塞效果，由现有outbox outcome持久化blocked和审计，停止重试；不填参数、不调用业务handler、不推进process。未知基础设施故障仍按既有重试规则，不能把timeout/未知效果一概blocked成无效果。

工单29/流程27/任务33/callback2按该路径被标blocked；原payload、execution key、attempt history、task completed记录不变，记录这次“执行前契约拒绝”的原因。不要宣称先前尝试都无效果，只证明本次未调用handler。实例保留running+blocked的真实状态，UI显示阻塞原因和人工处理提示；禁止TerminateProcessTx绕过未决回调。所有旧失败记录保留，可在最终清理报告列为具名保留测试证据，无需新增恢复API。本次需求不要求让该旧实例成功结束。

验收：正常/审批拒绝/需要升级/无owner/旧owner/跨租户/缺权限/直接PUT绕过/并发任务与状态命令/重复operationId/GET零写入/旧callback停止重试；从正常UI确认工单closed及流程success结束两项独立成立。原definition65与旧运行引用保持不变。

## 3. B：克隆准入表、信任边界及版本生命周期

### 3.1 数据合同

新增规范表`work_item_clone_admissions`，限定部署级证据，不含业务个人行数据，不提供应用业务API。字段：`id uuid PRIMARY KEY`（本次操作ID），`generation uuid NOT NULL`，`event_kind text CHECK IN ('admit','revoke')`，`admission_id uuid`（revoke引用admit，admit为空），`target_schema text NOT NULL`，`payload jsonb NOT NULL`，`digest char(64) NOT NULL`，`created_at timestamptz NOT NULL`；唯一`(generation,event_kind)`，增加UNIQUE(id,generation)及复合自引用FK(admission_id,generation)，CHECK约束限定admit为空/revoke非空，受审触发器验证revoke引用的是admit事件而非revoke；不能用跨行CHECK表达这种关系。owner/migrator仅通过受审CLI写，runtime/system无任何表权限，inspection SELECT；ACL校验拒绝额外grants/membership。记录append-only，命令无UPDATE/DELETE；数据库触发器拒绝修改/删除已有行，且不能以TRUNCATE/表owner作为运行身份绕过。刷新通过整个目标schema受控重建，不篡改表内历史。

admit payload固定version=1，包括source(database/schema/deployment)、target(database/schema/deployment)、sourceArtifact、sourceBackupSHA256、sourceLedgerRowsSHA256、sourcePContentSHA256、sourceCutoff、sourceManifestSHA256、objectMappingSHA256、roleMap、targetInitialFullStructureSHA256、targetPGuardSHA256、targetStableACL_SHA256、targetDataSHA256、cloneToolArtifactSHA256、operator、changeReference。digest采用现有canonical JSON/evidenceDigest算法；operator来自OS可信身份，不接受JSON自报。revoke记录reason、操作者、时间及被撤销digest。保持源schema_migrations和P附件原值；它们证明源执行历史，目标admit证明本次复制及映射，不生成假的目标P执行。

### 3.2 可信配置与CLI

扩展现有受保护MigrationControlConfig：`CloneAdmissionID`、`CloneGeneration`、`CloneAdmissionDigest`（均可选但必须全有或全无）。正常Dev无这些值并且表内无admit；clone必须三者与实际目标一致，不能用单一bool放行。来源必须正常Dev无clone标记；拒绝链式clone及源R回执。

在现有cmd/migrate追加互斥操作：`-clone-admit -clone-manifest <0600 JSON> -dry-run`、相同操作去掉dry-run后提交；`-clone-revoke -clone-admission-id <uuid> -reason <text>`。原`-status`保持只读并显示source/target/generation/revoked状态。退出码：0成功/同输入幂等；2输入/身份/证据/已存在冲突；3运行/连接/事务失败；4未知提交结果（必须按operationId查询，不盲重试）。路径不是DSN，秘密使用既有受保护CWD/control/password文件。具体CLI help及测试必须随实现提交。

admit只有在完整恢复与权限验证之后才可写；在现有schema迁移锁内，事务验证当前target与manifest、原账本/P、结构、数据和权限后INSERT一次。相同id及digest返回原记录，不同内容冲突；同schema已有另一代admit拒绝。无需放宽普通Migrator的准入来引导：承载表先作为普通迁移在Dev真实创建，然后备份复制；目标仅该显式离线admit命令能在尚未admitted时执行，其输入验证不依赖自声明成功。

### 3.3 不变证据与可演进结构

初次完整结构/数据摘要仅证明快照恢复，不作为日常运行数据或全结构永远不变的要求。日常target校验同时满足：可信配置pin与无revoke；源P/源账本复制前缀字节摘要不变；所有追加迁移是当前canonical注册项、checksum正确、依赖完整；目标准入后固定P-owned结构guard与权限边界仍成立；运行准入及整批升级完成时现有`inspectCurrentRequiredStructure`验证当前代码所需普通表/列/类型；它不是完整目录漂移检测。克隆必须保留`validatePreparationShapeMode`（运行时structuralOnly、管理验证时完整模式）及`validatePreparationTriggers`等现有精确检查，不能以摘要替代。所有execution SECURITY DEFINER函数按获准模板验证正文、参数、owner、proconfig和ACL。targetPGuard只覆盖原preparationStructure拥有的那组约束/索引/RLS/grants/ownership，以target标识原生计算；不修改原source P内容或原摘要。初次admit必须以有限源→目标映射证明该target guard来自源真实结构，而非仅把任意当前状态签名。

普通Up按现有事务/锁运行：前置仅验证pin/未撤销、源历史前缀、P guard、已应用账本及其版本对应的结构/触发器条件，不能在Up前要求尚未执行迁移才产生的当前代码完整结构。执行每条规范迁移，新增真实目标回执，提交前验证该迁移后置条件及稳定P guard/执行角色隔离；整批Up完成和启动时才运行当前代码完整required structure。失败整个本条迁移回滚。普通非P字段变化不使guard失效。需要改变P-owned表面或稳定角色权限的未来迁移明确拒绝，必须另作契约变更；不是运行时自动更新guard。source与target可分别前向升级；同次UI验证要求两者与所选代码兼容，不假设随时版本一致。

clone的R/down/reset在计划预检和事务入口均拒绝，含原始P错配但调用者不提供clone配置的情况；不删除承载表来绕过。恢复失败采用前向修复或撤销并重建，不设计一般回滚。普通Dev原有受控迁移规则不变。

### 3.4 状态、发布配置和刷新

状态由数据库事实+可信配置共同决定：absent → restored_unadmitted → admitted_unconfigured → active → revoked。任何不完整组合均不能启动。

- 先停目标服务/全部消费者并join；操作人持有stack profile独占锁。恢复成功写admit事务；之后原子替换受保护control文件，再启动。DB成功、配置未发布时保持不可启动；配置先发布而DB未提交也拒绝。服务运行期间不热换DB池/身份。
- 撤销前停/join并验证无目标runtime/system会话；不强杀未知会话。写revoke事件，移除配置pin；旧pin即使残留也因revoke而拒绝。中断后按记录恢复此顺序。
- 刷新前归档目标完整备份、admit/revoke、验证成果及增量差异，验证可恢复；以具名schema清单枚举并删除目标自有对象，再DROP SCHEMA RESTRICT。存在外部依赖停止，不CASCADE。生成新generation，从Dev重新快照恢复，不覆盖旧admit历史为新结果。

## 4. 确定恢复算法：复用现有演练资源，不改写dump文本

使用现有已获准临时恢复容器/数据库（由交接登记其实际identity，不能另起PG容器）；完成后退出。该资源仅作机械转换，不是第三个长期数据目标。操作前验证无其他Agent占用、无应用进程、PG server/client主版本与源一致。

1. 停源写入者及消费者，保存源码/角色/extension清单，使用pg_dump custom格式捕获源和行/序列/目录/账本/P摘要；完成一致截止点后恢复Dev。备份原件只读保留，后续源新增不混入本次快照。
2. 在空演练数据库原schema public恢复，保留与源对应的角色元数据用于验证；应用不启动，不执行Ent或历史迁移。完整核对原P/账本/对象/行/序列。
3. **仅在演练库**执行`ALTER SCHEMA public RENAME TO migration_validation`，让PG维护OID绑定的外键、视图和regclass依赖。绝不在Dev执行该命令。
4. 对函数/procedure正文或proconfig中schema引用，采用有限受审模板注册表：key为源schema+对象名+参数类型+源定义SHA256，value为schema参数化的完整目标定义。模板必须保留语言、volatility、strictness、security、owner及安全search_path；未知hash/对象不转换、不回退正则替换。模板仅覆盖本次已盘点对象，新增对象走代码审查及测试，不引入通用SQL解析框架。
5. RLS策略的role和ACL用目录身份映射；恢复目标不带源owner/ACL。extension对象不随意复制：固定extension名称/版本/namespace依赖清单，允许共享的类型/纯函数逐项授权；无法映射的extension对象在最终写入前拒绝。不得挪动Dev extension或把全部public业务对象授予目标。
6. 对演练库target schema导出新custom归档，核验TOC只包含目标及显式允许的依赖；在最终同库不存在目标schema时，用target-only migration owner执行`pg_restore --no-owner --no-acl --single-transaction --exit-on-error`。不用--clean，不恢复全局角色，不替换archive SQL字符串。
7. 安装受审目标ACL/角色和新执行scope绑定，核对数据、序列、关联与映射后结构/P回执；其后执行clone-admit。恢复失败事务回滚，已有非空目标直接拒绝。源public在整个最终恢复期间无对象/数据写入。

结构转换模板落点`migration/clone_schema_templates.go`，由Go既有Migrator提供只读转换清单/DDL产物；Python `scripts/migration/clone.py`仅编排dump/restore与调用Migrator，不成为第二套准入业务逻辑。输入object manifest只描述对象和摘要，不允许携带任意DDL。恢复会运行DDL，因此必须校验备份来源、TOC和模板hash，不能执行用户任意SQL文件。

测试夹具包含循环FK、sequence默认值、view、RLS、SECURITY DEFINER、%ROWTYPE、字符串regclass、动态SQL、扩展依赖。未知字符串引用拒绝；已支持模板在目标执行不能访问源。restore初始摘要与源一致，源随后新增/目标随后变更分别不互相传播。

## 5. 消费者和权限：复用candidate scope

当前execution helpers有硬编码public；本次必须修改`database/execution_policy.go`、`execution_scope.go`、`execution_worker.go`、`execution_admission.go`及对应注册/检查函数，所有对象限定名来自启动时验证过的单一DB_SCHEMA，不接受request参数/GUC决定schema。Ent选择器 `.Schema("public")`也一并替换。同时修改`migration/preparation_candidate_trigger.go`及相关shape检查：未应用新迁移的源仍严格验证039原public函数体；应用新迁移后按该真实回执选择新canonical schema模板，不能放宽旧版本校验。SQL identifier安全引用，不修改历史迁移SQL；需要更新函数时由新规范迁移提供canonical schema参数化定义，source/target都经同一实现。

复制源行/序列摘要先按快照验证；新增scope/runtime binding是明示目标初始化差异，逐行记录于manifest的allowedTargetAdditions，仅允许本代新deployment/角色/tenant的获准新增，不把“忽略四张执行表”当比对方法。旧行保持原样。

目标使用新deployment（每次generation唯一）、独立runtime/system/inspection/migration角色及每tenant新scope UUID。保留复制的旧binding/member原值；新角色不能命中旧binding。通过现有tickets AFTER INSERT机制仅对新WorkItem原子入scope，历史工单禁止追加membership，target系统身份不获源schema业务表权限。候选runtime强制RLS，不是以role字符串替代tenant校验。

首次验收仅启用scoped outbox、callback、event_audit、request_async（仅当旅程需要）；notification、KAF外部执行、webhook、embedding、connector_poll、cloud_discovery、cmdb_import_export、connector_diagnostics、SLA/escalation后台扫描先disabled。需要测试的专业域同步升级命令仍走领域授权，不等于开启后台扫描。后续启用任何额外消费者必须在同次测试证明其scan/claim/retry/complete均scope约束，否则保持未验收。

具体验收：复制pending outbox/callback/notification/SLA/tool/KAF行的状态、attempt、lease保持不变；新合成WorkItem能入scope并完成允许流程；伪造scope GUC/旧scope/错误role/跨tenant均拒绝；目标显式读写public业务表与Dev读写target均拒绝。若需要共享extension schema USAGE，只授特定非业务依赖，不能因此授表权限。

## 6. Toolkit API/SQL绑定与切换竞态

profile增加必填database/schema/deployment/generation；target.py移除public硬编码，使用安全identifier及只读事务。旧profile缺schema只允许显示配置错误，不自动落public。保持--apply拒绝，直到此节全部实现。

新增受限`POST /api/v1/admin/migration-target-session`：采用现有可信登录、CSRF、super_admin检查并注册独立权限资源`migration_target:read`（permission registry与受审管理员授权同步）；输入仅nonce和请求实体范围users/departments。返回从实际连接验证的database/schema/deployment/admissionDigest、代码制品摘要、serverRunId、actor/tenant及5分钟有效的migrationToken。使用现有JWT签发/验证机制，增加`token_kind=migration`、target claims、允许路由/动作范围和RunId；不能自建第二套登录体系。服务启动每次生成新RunId并保持DB池不可热切换，旧token在新进程拒绝。token和签名密钥禁止日志输出。

Toolkit先用SQL只读身份验证target metadata和admit，再取得session；差异立即零写入。写入使用独立HTTP客户端，不携带登录Cookie或普通JWT，只用migrationToken作Authorization Bearer；缺token直接401，没有回落普通会话。登录客户端只用于取得/刷新受限token，不可用于实体写入。

认证层识别已验签token_kind=migration后，验证时效、实际immutable DB池身份、当前RunId、admission、actor/tenant及精确route/method白名单；每次重新核验当前用户仍有效且具备原实体写权限。仅允许现有users/departments获准创建/更新路径，不能调用任意管理接口；复用原控制器/领域服务，不造第二套创建逻辑。普通cookie/JWT业务请求继续原有权限，不能提供caller自报target来获得迁移身份。测试必须证明工具无token时不发送写请求，伪造kind/target、过期、撤销、换租户/角色和无token的无cookie客户端都零写入。

API进程不热换DB池；stack切换停止旧进程后启动新profile，RunId改变。Toolkit批次持有stack同一profile切换锁；即使锁被外部绕开，新进程也拒绝旧token。单次事务使用已验证的同一pool，不能验完元数据再获取另一个池。token过期只允许在重新SQL/API对齐后续期，未知写入结果按既有稳定业务键/operationId查询，不直接重复创建。

## 7. 顺序、验收与停止条件

顺序：A输入/只读UI修复→新generic合同/流程→历史invalid callback进入blocked→A与原阶段2的身份/配置/Change/Service Request代表性验收；B schema绑定/准入代码与隔离测试可并行，实际源新迁移→备份→演练转换→最终恢复→准入→Toolkit差异验证→窗口UI→回Dev→旧库逐项清理。KAF物理整合使用既有任务三，本文只提供新的schema/角色/版本交接。

旧五批384对象是比较来源，不全量重放；相同稳定键且内容一致跳过，新增/冲突逐项输出，再用已审工具写目标。身份密码/权限保留；占位示例不默认补入。最终报告区分新目标业务通过、未适配专业配置、保留失败样本和已删除旧资源。

失败处理已定：源码/源摘要变化重新生成同算法制品；未知对象/extension/权限拒绝就地停止对应步骤，不能改架构或跳检查；无效用户输入不落任务；已冻结invalid payload显式blocked；未知外部结果不重试冒充无效输入；目标恢复不完整不启动；克隆坏了前向修复或撤销重建；Dev新增写入后禁止旧快照覆盖。

完成门槛：A真实UI和权限/并发负例通过；B首次准入、普通Up后重启、篡改拒绝、撤销与刷新、复制pending不消费、新记录可执行、SQL/API错目标及切换竞态零写入、跨schema/RLS隔离、备份恢复和回Dev通过。down/reset/R拒绝是预期行为，必须有测试。不能用文档、单测或健康检查单独宣布整个任务完成。

## 8. 设计复核结论

独立复核已检查并修正：普通Up执行前不可要求尚未迁移的当前结构；clone不能漏掉原shape/trigger精确检查；新旧函数模板按真实迁移回执选择；迁移写客户端不得回落普通Cookie；审批决策变量不能被公开入口覆盖。复查这些条款未发现新的实质阻塞。此结论仅为设计，不代表实现/部署/业务验证成功。

执行Agent需要产出的XML、DDL、schema函数模板、受影响代码、fixture和真实验收证据都是本合同的实现制品，不再承担架构或业务方案选择。未知源对象/hash变化走既定拒绝与差异上报，不由执行Agent任意转换。当前唯一待选的运行事实是执行窗口、空闲的既有演练资源身份和下一可用迁移编号，均须现场核对，不可在文档虚构。
