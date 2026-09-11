# WorkItem Controlled Retirement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在唯一Migrator中实现真实历史识别、WorkItem结构准备P与观察后的受控退役R，并证明删除后的恢复能力。

**Architecture:** 复用RegisteredMigrations、LegacyMigrations、SQL解析和schema_migrations。目录提供固定阶段依赖；Migrator负责唯一执行、锁和事务，CLI负责读取环境证据，bootstrap只在只读准入后进入获准写路径。P内结构验证与完整业务验收分离，所有反向迁移同样受依赖约束。

**Tech Stack:** Go、database/sql、PostgreSQL、Ent、现有Go集成测试及Playwright。

**Status:** draft，实施输入已编制；所有代码任务未开始。设计依据为[已修订设计](../specs/2026-09-11-workitem-controlled-retirement-design.md)。审阅代码基线fab60168；实施前记录实际HEAD，核查目录新增版本并选取两个未占用新版本。不得借本计划执行共享环境变更。

## Global Constraints

- 历史SQL与checksum保持不变；未实际执行的旧迁移不能写成applied。
- 采用现有Migrator、RegisteredMigrations、LegacyMigrations和schema_migrations，不引入第二套迁移执行器或审批引擎。
- 旧结构保留期间，运行代码只读写WorkItem新路径。
- 未知版本、重复、checksum变化、阶段缺失或回执/实际结构矛盾均拒绝。
- 禁止通配符、CASCADE或自动递归扩大删除。
- 自动化检查通过不等于实际环境授权。
- B1–B5/F1–F3/V1既有成果不重新实现；响应及时性与Change多WorkOrder继续作为backlog。
- 不修改历史业务数据、不双写、不重算SLA、不改编号/createdAt；历史流程及旧Change事件只盘点，不自动修复。
- 全部任务在既有独立worktree进行；不得重置共享Postgres/Redis或接管其他任务的容器。
- 所有任务先RED后GREEN，分别提交并独立审查；本计划编制完成不表示执行或验收完成。

---

## 文件与接口分工

下述路径相对仓库根。新增文件为拟创建，现有路径已经核对。不迁移已有测试或重构整个migrations.go。

| 文件 | 责任 |
|---|---|
| itsm-backend/migration/migration_plan.go（新） | 固定阶段目录、正反向规划、旧目录转换验证 |
| itsm-backend/migration/migration_evidence.go（新） | 目标及证据契约、摘要一致性检查 |
| itsm-backend/migration/work_item_preparation.go（新） | P的精确结构清单、校验及SQL |
| itsm-backend/migration/work_item_controlled_retirement.go（新） | R清单、依赖复核、精确删除 |
| itsm-backend/migration/migrator.go | 所有执行入口、互斥、事务、账本 |
| itsm-backend/migration/migrations.go | 原SQL保持原样；新阶段注册与022/027历史分类 |
| itsm-backend/migration/bootstrap.go | 任何写入前只读分类和准入 |
| itsm-backend/internal/bootstrap/app.go | 实际启动与就绪条件接入 |
| itsm-backend/cmd/migrate/main.go | 统一CLI规划、控制模式、回退/reset及输出 |
| itsm-backend/tests/integration/workitem_controlled_retirement_postgres_test.go（新） | P/R及完整恢复真实PG演练 |
| itsm-backend/tests/integration/workitem_migration_entrypoints_postgres_test.go（新） | 启动/CLI/直接调用一致性和零写入拒绝 |

跨任务接口在Task 1定义，后续任务只扩展实现，不另起同义类型：

```go
type MigrationStage string
const (
    StageOrdinary MigrationStage = "ordinary"
    StagePrepare MigrationStage = "workitem_prepare"
    StageRetire MigrationStage = "workitem_retire"
)
type MigrationOperation string
const (
    OpUp MigrationOperation = "up"
    OpPrepare MigrationOperation = "prepare"
    OpRetire MigrationOperation = "retire"
    OpDown MigrationOperation = "down"
    OpReset MigrationOperation = "reset"
)
type MigrationDefinition struct {
    Migration Migration
    Stage MigrationStage
    Requires []string
}
type MigrationPlan struct {
    Executable []Migration
    PendingManual []Migration
}
func PlanMigrations(catalog []MigrationDefinition, applied []Migration,
    operation MigrationOperation, requestedVersions []string) (MigrationPlan, error)
```

requestedVersions仅用于明确down/rollback-to的目标版本集合；reset由目录和账本产生完整集合，up/prepare/retire不得接受任意子集绕过前置条件。

Migration新增可空CatalogRevision、EvidenceDigest字段，旧回执不回填。目录修订常量与P/R版本常量在Task 1核对当前注册目录后确定；版本不是阶段排序替代品。禁止接受调用者自造SQL替代已注册定义。

## Task 1：目录与真实历史转换规划

**Files:** 新migration_plan.go、migration_plan_test.go；修改migrations.go、migrator.go、migrator_test.go。
**Consumes:** 原Migration、GetMigrationSQL、既有完整active目录。
**Produces:** 上述阶段/操作/计划类型及PlanMigrations；固定旧目录校验与新阶段前置条件。

- [ ] 保存原022/027及全部已注册SQL的checksum测试基线，记录旧目录精确版本顺序；核对并选取P/R新版本。
- [ ] 写纯规划反例：非法旧空洞不能因022/027移到legacy而合法化，checksum变化拒绝，未知阶段拒绝；P后普通迁移可继续、R只在PendingManual。

```go
func TestControlledPlanUnknownStage(t *testing.T) {
    _, err := PlanMigrations([]MigrationDefinition{{
        Migration: Migration{Version: "test", Description: "test"},
        Stage: MigrationStage("unregistered"),
    }}, nil, OpUp, nil)
    require.Error(t, err)
}
```

- [ ] 执行 `go test ./migration -run 'TestControlledPlan|TestMigrationStreamAndLedger' -count=1`，确认失败来自缺少规划/行为，而非基础设施。
- [ ] 实现固定顺序：至021合法前缀 → P → 后续普通迁移 → R。没有P回执先按冻结旧目录校验；有P回执按新目录和阶段依赖校验。普通版本仍保留连续前缀约束。
- [ ] 下行规划按完整请求检查反向依赖；不可逆阶段存在时reset不能跳过它后继续回退依赖。不得等执行若干条后才发现非法计划。
- [ ] 保持中间提交可运行：Task 1先增加可测试的新目录规划器，暂不激活P/R或移动022/027；Task 5在执行路径齐备后一次切换权威目录，不长期保留双规划入口。
- [ ] GREEN并执行现有migration单元测试；提交 `feat(migration): model controlled stage dependencies`，独立审查。

## Task 2：只读准入与全部写入口的统一门禁

**Files:** 修改bootstrap.go、bootstrap_test.go、migrator.go、internal/bootstrap/app.go、cmd/migrate/main.go；新workitem_migration_entrypoints_postgres_test.go。
**Consumes:** Task 1规划器。
**Produces:** `func (m *Migrator) InspectMigrationTarget(ctx context.Context) error`，只读核验目标/schema/账本画像；启动与CLI调用同一门禁。

- [ ] 写启动spy反例：非法账本或已有目标缺账本时，Prepare/CreateSchema/EnsureMigrationsTable和Seed均未被调用。使用bootstrap_test.go既有假Migrator扩展新接口。
- [ ] 写真实PG反例：P回执存在时直接RollbackMigration及reset撤销021必须拒绝；比较完整结构与账本摘要。
- [ ] 执行 `go test ./migration -run 'Test.*Bootstrap|TestControlled' -count=1` 与受影响集成测试，记录RED。
- [ ] 在所有结构/数据写入之前InspectMigrationTarget；账本表存在时先只读识别旧字段布局，不能先ALTER再发现非法历史。明确空目标才允许受控初始化。缺schema、诱饵schema、未知账本列型失败关闭。
- [ ] 将ApplyMigration和RollbackMigration校验下沉到事务入口：注册版本/SQL、账本、操作及依赖均核验；reset在首个写入前验证完整计划，执行时锁内再核验。所有入口共用同一个数据库/schema互斥键，不只锁R。
- [ ] AutoMigrate关闭也要检查运行所需P/普通迁移；不能因为未调用up而绕过运行准入。现有运行配置控制观察前恢复写入，记录完整验收证据后才允许就绪，不新增审批引擎。
- [ ] GREEN；集成测试必须实际运行而非连接缺失SKIP。提交 `fix(migration): gate bootstrap and reverse migrations before writes`，独立审查。

## Task 3：P结构准备与真实回执

**Files:** 新work_item_preparation.go、work_item_preparation_test.go、workitem_controlled_retirement_postgres_test.go；修改migrator.go。
**Consumes:** 目录/互斥与只读目标检查。
**Produces:** 同一Migrator内 `func (m *Migrator) ApplyPreparation(ctx context.Context, evidence MigrationEvidence) error`；证据类型在本任务定义。

```go
type MigrationTarget struct {
    DeploymentID string
    Database string
    Schema string
}
type MigrationEvidence struct {
    Target MigrationTarget
    CatalogRevision string
    LedgerDigest string
    ApplicationDigest string
    InventoryDigest string
    BackupDigest string
    RestoreReportDigest string
    JourneyReportDigest string
    ObservationReportDigest string
    Operator string
    ChangeRecord string
}
```

- [ ] 在既有newCutoverFixture隔离模式上建立至021合法画像：保留旧公共列和历史值，故意缺034字段；另设孤儿/重复/跨租户/同名错误约束/未知policy反例。fixture只能在隔离库创建，不用伪造回执代替实际迁移执行来证明允许路径。
- [ ] RED执行 `go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemControlledPreparation' -count=1 -v`。
- [ ] 按022/027必要结构效果逐条登记schema限定的FK/unique/NOT NULL/RLS；列保留，未知约束/trigger/grant拒绝，只退出已识别旧policy；校验事务内后置条件。
- [ ] 基线包含原记录范围、公共值及历史证据摘要。旧NOT NULL/default/CHECK只作明确登记的必要调整，使新代码不必填写旧公共列。禁止填充触发器、业务数据回填和Schema.Create补造历史。
- [ ] 隔离测试在P事务内用受限SQL列集合验证真实非owner角色的三域读写/RLS，不调用依赖034的完整Ent查询。临时测试记录在独立事务/保存点回滚；实际目标不植入测试数据。
- [ ] P DDL、证据摘要及回执同事务；失败不留成功回执。证据为空或目标/旧目录不符拒绝。结构已满足也须真实运行校验再写P回执；不补写022/027。
- [ ] 验证P后新字段变化而旧值不变：R比较基线和审计连续性，不要求旧列等于当前WorkItem。按2026-09-11复审已批准的软删除范围，所属领域软删除保留原始记录及扩展，WorkItem可更新deleted_at/updated_at/version；任何原始记录物理消失仍拒绝，新记录旧列可空。物理清理及可靠事务审计留作设计中的BL-WI-PURGE-AUDIT-01，不以HTTP审计放行。
- [ ] GREEN及针对性单元回归，提交 `feat(migration): prepare WorkItem schema without retiring history`，独立审查。

## Task 4：R证据门禁与原子精确退役

**Files:** 新migration_evidence.go、migration_evidence_test.go、work_item_controlled_retirement.go；扩展workitem_controlled_retirement_postgres_test.go与migrator.go。
**Consumes:** Task 3 MigrationEvidence/P回执；现有环境运维身份和批准记录。
**Produces:** `func (m *Migrator) ApplyRetirement(ctx context.Context, evidence MigrationEvidence) error`。这里只在已核验授权的控制入口运行，普通ApplyMigration不能自动执行R。

- [ ] 写最小证据拒绝测试，再覆盖备份/恢复/旅程/观察证据缺失、目标错配、旧清单漂移及授权来源不可信。

```go
func TestRetirementEvidenceRejectsEmpty(t *testing.T) {
    err := ValidateRetirementEvidence(MigrationEvidence{})
    require.Error(t, err)
}
```

本任务实现 `func ValidateRetirementEvidence(e MigrationEvidence) error` 负责必填及格式检查；它不代表审批真实性。

- [ ] 执行 `go test ./migration -run 'TestRetirementEvidence' -count=1`；PG执行 `go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemControlledRetirement' -count=1 -v`，记录RED。
- [ ] CLI证据文件必须逐项读取并验证内容摘要、目标和环境变更记录；不接收approved=true作为授权。采用既有运维执行身份和环境审批渠道；若目标环境无法提供可验证授权来源，控制模式拒绝并列明缺项，不能用自签JSON放行。隔离测试使用测试专用授权fixture，禁止生产默认通过。
- [ ] 恢复观察写入后再次暂停并固定最终恢复点，验证该恢复点覆盖新增数据；观察前备份不够。清单必须绑定该时点、应用digest、账本和目标，锁内复核任何变化。
- [ ] 在同一互斥/事务内按运行手册精确清单及逐项批准的依赖删除，标识符安全引用并限定schema。禁止CASCADE、通配及自动扩大依赖。未登记依赖失败，明确lock/statement超时。
- [ ] 删除和R回执同事务；首次缺对象但无已批准空清单拒绝，规范新环境可按明确空清单记录无删除结果。丢失提交响应后读取相同版本/证据/结构，返回已有结果；不同证据冲突。
- [ ] 注入DDL失败、回执写失败、并发两执行者、锁超时、丢响应重试；失败前后对象与账本摘要保持不变。验证跨schema诱饵及依赖未改动。
- [ ] 按已批准软删除范围，以真实所属领域删除入口验证保留行和P基线不变、R可执行；物理消失与保留值改动仍阻断，并覆盖R后正常写入、历史检查及同证据重试。
- [ ] GREEN；提交 `feat(migration): execute evidence-bound WorkItem retirement`，独立审查。

## Task 5：激活唯一目录与控制CLI，完成完整业务准入

**Files:** 修改migrations.go、migrator.go、bootstrap.go、cmd/migrate/main.go、internal/bootstrap/app.go、work_item_retirement_guard.go及相关原测试；扩展workitem_migration_entrypoints_postgres_test.go。
**Consumes:** Tasks 1–4完整能力。
**Produces:** 唯一阶段目录实际启用；在既有布尔flag风格下新增控制CLI `-prepare-workitem` / `-retire-workitem`，`-evidence-file`输入路径；现有up/down/reset/status/dry-run使用同一目录。

- [ ] RED覆盖所有入口：普通up不能执行P/R；合法P后普通迁移继续且R显示pending_manual；已完成022/027不重放；未知阶段/不完整请求/绕过直接调用拒绝。
- [ ] 将022/027移至Legacy且保留SQL解析和checksum；注册Task 1确定的新P/R版本；删除临时规划接线，使旧检查调用新唯一规划器。原历史SQL保护测试保留为历史直接SQL范围，更新活动入口断言，不删除覆盖。
- [ ] 其他012/013/014/017/028/029历史删除若仍有目标对象，提前阻断；不要静默跳过又补回执。新空环境逐个确认无实际删除效果，再执行获准普通迁移。
- [ ] 新控制flag与up/down/reset/fresh/seed互斥，多操作组合在任何连接写入前拒绝；沿用既有flag风格，不增加第二套action解析。fresh属于显式销毁开发库的旧操作，含P/R或非空目标不得作为本方案绕过恢复/回退门禁；本任务不执行fresh。
- [ ] 同步CLI输出的可执行、待手工阶段、拒绝原因及退出码；版本展示不能单靠最大版本号证明依赖完成。直接控制入口也检查目标、证据和真实运维身份；普通调用无控制证据拒绝。
- [ ] 至021画像执行P后再执行获准普通迁移，034等完成后才启动完整三域/Requested Item/V1；缺任何普通结构或业务验收失败保持未就绪，P真实回执不删除。
- [ ] 编译CLI运行真实退出码验证，禁止用go run外层退出码代替子程序；执行 `go test ./migration ./internal/bootstrap`、`go test -tags=migrate ./cmd/migrate` 及两个新集成组。
- [ ] GREEN，提交 `feat(migration): activate controlled retirement entrypoints`，独立审查。

## Task 6：完整恢复与三个时点的业务验证

**Files:** 扩展workitem_controlled_retirement_postgres_test.go、既有workitem_retirement_postgres_test.go；复用itsm-frontend/tests/e2e/business-flows/workitem-convergence.spec.ts及其fixtures；更新docs/deployment/workitem-convergence-cutover.md和docs/README.md。
**Consumes:** 唯一目录和控制CLI；现有V1隔离运行方法。
**Produces:** 删除后完整恢复证据、三时点业务结果、目标环境执行手册；不执行真实目标环境变更。

- [ ] RED新增恢复反例：只有观察前备份缺观察中新数据、缺附件对象、漏流程/审计/回执、错误应用版本或消费者配置均不能标记恢复通过。
- [ ] 只创建本任务拥有的独立数据库/容器和非owner角色，使用显式环境变量白名单；LLM/邮箱/连接器真实凭据不得从宿主自动继承。记录资源归属，不清理别人的环境。
- [ ] 在P及普通迁移后保留旧结构运行V1；恢复写入形成新数据，暂停并生成最终备份，先在独立目标验证恢复。然后真执行R，再恢复R前最终备份到另一独立目标，核对记录/内容摘要/时间/编号/租户/关系/流程/回执/审计/附件。
- [ ] 分别在“P+普通迁移后保留旧结构”“R后”“R后恢复完成”运行同一V1，验证三域专业行为及generic/Requested Item；RLS使用真实业务角色。
- [ ] 备份后新增数据和外部副作用单独演练补偿清单：不宣称数据库恢复撤销了通知/外部动作；未捕获数据必须使零损失结论失败。
- [ ] 执行 `go test -tags=integration_postgres ./tests/integration -run '^TestWorkItem(Controlled|MigrationEntrypoints|Retirement|Cutover)' -count=1 -v`。前端在每个阶段执行 `npx playwright test tests/e2e/business-flows/workitem-convergence.spec.ts --project=business-flows`。Go命令在itsm-backend，Playwright命令在itsm-frontend；按既有fixture要求提供隔离目标配置。
- [ ] 保存脱敏结果、实际提交/镜像、测试数量及SKIP说明、备份及恢复摘要、清理资源清单；清理仅自建资源。任何SKIP不得计为成功演练。
- [ ] 更新手册区分“代码/隔离验证完成”与“目标环境准入/部署/观察/退役待执行”，保留历史测试来源。执行git diff --check、独立审阅后提交 `test(migration): verify controlled retirement and complete recovery`。

## 设计覆盖与交付检查

| 设计章节/审阅项 | 实施任务 |
|---|---|
| 历史SQL、目录修订、真实账本、旧画像转换 | 1、3、5 |
| Important 1：P内验证与后续业务验收分离 | 3、5、6 |
| Important 2：回退/reset反向依赖 | 1、2、5 |
| Minor：写入前只读分类 | 2 |
| 精确schema、未知依赖、非owner RLS、旧值基线 | 2、3、4 |
| R授权来源、最终备份、观察和证据绑定 | 4、6 |
| 并发、锁、原子回执、丢响应幂等 | 2、4 |
| 删除后恢复、新数据与外部副作用 | 6 |
| 历史业务豁免、其他破坏性旧迁移阻断 | 3、5、6 |

- [ ] 执行前再次检查AGENTS.md、治理文档、当前分支及并行目录改动；保留现有未提交工作。
- [ ] 每批独立审查通过再进入下一批，不能用单元测试代替真实PG或完整业务验收。
- [ ] 最终仅在实际证据支持时更新任务勾选；本稿所有任务均未执行。
- [ ] 目标环境部署、观察和R操作仍须该环境单独准入；不推送、合并或部署。
