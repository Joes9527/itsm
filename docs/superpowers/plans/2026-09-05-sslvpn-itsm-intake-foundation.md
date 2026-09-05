# ITSM Unified Intake Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** draft — 实施计划待执行；设计已确认，未宣称代码完成。

**Goal:** 归并已有 Intake 成果，所有生产创建入口复用同一编号、专业规则和完整事务，并提供 KAF 所需的受权目录与创建接口。

**Architecture:** Intake 是事务所有者，专业 Creator 是领域规则入口，公共创建契约放在 `handlers/common/workitemcreation` 以阻止 import cycle。复用当前 Outbox 和 BPMN 启动机制，Controller 保持薄层。

**Tech Stack:** 当前仓库 Go、Gin、Ent、PostgreSQL、Redis、Next.js/TypeScript；不新增运行框架。

**Spec:** [设计 §6-9、§11-13](../specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md)。

## Global Constraints

- [总计划](2026-09-05-sslvpn-end-to-end-implementation.md) 的所有全局约束和 HTTP 契约适用。
- `tickets` 不改表名；专业共享字段只有 WorkItem 一份权威存储。
- Creator 不能调回 Intake，专业 Service 不能引入 `handlers/intake` 实现包。
- 包内纯测试用隔离 Ent fixture；真实 PostgreSQL 测试必须使用独立测试库，不能沿用共享开发库做清理。
- 此子计划创建/修改 API、schema、权限和工作流边界，需要独立审查者或维护者复核。

## 文件与职责

| 文件/目录 | 动作与职责 |
| --- | --- |
| `handlers/intake/` | 复用审查后的 resolver、registry、事务、幂等、审计、snapshot、work item writer；补公开 handler |
| `handlers/common/workitemcreation/{command,identity,creator,errors}.go` | 从 Intake 单向搬移跨域使用的类型/port，不保留别名或第二份 DTO |
| `service/incident_creation.go` | 从大 Incident service 提取权威创建准备/专业扩展写入逻辑 |
| `handlers/{change,problem,service_request}/creation.go` | 本域专业规则、事务内扩展写入，复用本域 repository |
| `handlers/intake/{generic_creator,problem_creator}.go` | 补 generic/Problem 分发；不复制专业规则 |
| `handlers/service_catalog/{preflight,revision}.go` | 目录发布/启用校验、定义版本计算 |
| `handlers/intake/{identity_exchange,identity_mapping_handler,handler}.go` | 审查复用早期分支 HTTP 能力，业务下沉到 service/repository |
| `handlers/intake/identity_exchange_service.go` | assertion 验证、nonce、映射与短期凭据签发的应用边界 |
| `handlers/intake/identity_repository.go` | 受信 provider/workspace/subject 映射查询与管理 |
| `docs/contracts/intake.openapi.yaml` | A/B/C 的唯一受理与错误 wire 契约 |
| `tests/contract/work_item_creation_entrypoints_test.go` | 逐入口黑盒创建契约 |
| `tests/integration/intake_creation_test.go` | PostgreSQL 事务、幂等、启动恢复 |

以下路径相对 `itsm-backend/`，除非注明仓库根目录。迁移名从本次分支注册表分配，并在 A1 输出确定清单；不硬占其他活跃迁移的号码。

## A1：固定复用范围与生产入口清单

**Files:** Create `docs/review/2026-09-05-intake-reconciliation-inventory.md`（仓库根）；只读两条来源分支和所有生产创建入口。

**Interfaces:** 输入为总计划三条 ITSM 基线；输出每行含 `sourceCommit/path/entry/targetClass/actorSource/idempotencySource/transactionOwner/disposition/test` 的清单。`disposition` 只能是 `reuse-reviewed`、`reimplement`、`remove`、`already-authoritative`、`not-production`。

- [ ] 记录 `git status --short`、HEAD、远端跟踪基线、来源分支 HEAD 和 worktree 状态；使用独立实现分支，不改动来源 worktree。
- [ ] 执行差异和入口扫描，保存到系统临时目录后将有业务意义的结果写入清单：

```bash
git diff --name-status main...worktree-unified-intake-p1-reconciliation
git diff --name-status main...feat/kaf-delegation-transactional-delivery
rg -n 'Ticket\.Create\(|CreateTicket\(|CreateIncident\(|CreateChange\(|CreateTicketFromEmail' itsm-backend --glob '*.go' --glob '!*_test.go'
rg -n 'createServiceRequest\(|createIncident\(|createTicket\(' itsm-frontend/src
git ls-tree -r --name-only worktree-unified-intake-p1-reconciliation itsm-backend/ent/schema
```

- [ ] 追踪 Problem、标准变更、飞书同步、Ticket 模板/快速入口、邮件 Connector 的调用者；对接口只有定义而无运行接线的情况明确标记，不声称端点已可用。
- [ ] 比较来源分支的 `023/024/025` 和当前注册表，记录内容冲突与执行顺序；SR 共享字段迁移必须与 A3/A4 读写切换放同一发布批次。
- [ ] 输出一份迁移文件清单及一份逐字段保留/映射/拒绝表，覆盖 Incident、SR、Change、Problem、Generic 的当前公开 DTO。
- [ ] 核对所有扫描命中都有 disposition 和测试落点后提交：`docs: inventory intake reconciliation and creation entrypoints`。

本任务不复制业务代码、不执行迁移，也不把文件数或旧测试报告作为归并完成证明。

## A2：归并契约、Registry 与依赖方向

**Files:** 复用 `handlers/intake/{command,identity,creator,errors,canonicalize}.go` 及测试；Create 公共 `workitemcreation` 文件；Create 根目录 `docs/contracts/intake.openapi.yaml`、`docs/contracts/fixtures/intake-create.json`。

**Interfaces:** 搬移现有 `CreateWorkItemCommand`、`CreateWorkItemResult`、`Identity`、`ResolvedIntake`、`CreationPlan`、`ProfessionalReference` 到公共包，唯一创建 port 为：

```go
type Application interface {
    Create(context.Context, Identity, CreateWorkItemCommand) (*CreateWorkItemResult, error)
}
type ProfessionalCreator interface {
    RecordClass() string
    Prepare(context.Context, *ent.Tx, ResolvedIntake) (*CreationPlan, error)
    CreateExtension(context.Context, *ent.Tx, *ent.Ticket, *CreationPlan) (*ProfessionalReference, error)
}
```

公共包不 import Intake、`service` 或任何专业实现包。专业准备/写入接口消费上述公共类型；bootstrap 注入实现。`intake.Service.Create` 实现 Application，Registry 实现保留一份。

- [ ] 先搬入 A1 审查通过的源码与测试，手工归并冲突；移除重复编号器、旧字段访问与来源分支的旧 bootstrap。此步骤只是归并工作区，不单独部署。
- [ ] 给未知/重复 Registry 注册写红测试（沿用原 `creator_test.go` fixture）：

```go
func TestRegistryRejectsMissingClass(t *testing.T) {
    _, err := NewCreatorRegistry().Get("unregistered")
    require.Error(t, err)
}
func TestDecodeRejectsInjectedTenant(t *testing.T) {
    _, err := DecodeCreateWorkItemCommand(strings.NewReader(`{"tenantId":2}`))
    require.Error(t, err)
}
```

- [ ] 运行 `go test ./handlers/intake -run 'Registry|Decode|Canonical' -count=1`；记录当前失败，不把 import/fixture 错误当作预期业务失败。
- [ ] 单向移动公共类型并更新全部调用方，删除原类型定义和兼容别名；增加 `recordClass/confirmation/catalogVersion/formSchemaVersion`，完整参与 canonical digest。保留现有 Incident/Change typed input；为 generic 和 Problem 加对应 typed input，字段来自 A1 已固定映射表，禁止 `map` 吞掉专业输入。
- [ ] 扩展 `CanonicalizeCommand` 测试：修改任一指派、分类、专业字段、期限、目标组、目录版本都改变摘要；仅 JSON map 键顺序变化不改变摘要。更新摘要版本并令旧版本重放显式失败。
- [ ] 用 `go list -deps ./...` 检查依赖图，专业包无 Intake 实现依赖、无循环；运行上面的测试和 `go build ./...`。
- [ ] 同提交保存 OpenAPI/fixture 和公共 port：`refactor(intake): establish one creation contract and registry`。

## A3：专业创建规则与完整事务

**Files:** Modify `handlers/intake/{service,work_item_creator,incident_creator,change_creator,service_request_creator}.go`；Create `generic_creator.go`、`problem_creator.go`；Modify `service/incident_service.go`、`handlers/{change,problem,service_request}/{service,repository_impl}.go`；Create 各专业 `creation.go`；Modify `service/field_value_service.go`、相关 schema/迁移。

**Interfaces:** Intake 调用 ProfessionalCreator 的 Prepare/CreateExtension，tx 只能由 Intake 创建。专业 `creation.go` 接收同一事务与公共计划，复用专业校验/仓储；WorkItem writer 使用现有 `workitemnumber.Allocator.Allocate(ctx, tx.Client(), tenantID, issuedAt)`。动态字段沿用 `CreateValuesTx(ctx, tx, tenantID, definition, definitionID, entityType, entityID, values)`。

- [ ] 先扩展来源分支已有 `TestServiceCreateRollbackFaultMatrix`、`installIntakeMutationFailure`、`assertNoIntakeGraph`；对基表、扩展、字段、SLA、审计、snapshot、启动记录分别注入失败。既有 helper 迁入同一 `service_test.go`，公共类型 import 随 A2 更新。

```go
func TestServiceRequestFieldFailureRollsBackCreation(t *testing.T) {
    f := newResolverFixture(t)
    svc := newServiceUnderTest(t, f)
    cmd := f.catalogCommand(f.serviceCatalog.ID)
    installIntakeMutationFailure(f.client, "field value")
    _, err := svc.Create(context.Background(), f.identity(), cmd)
    require.Error(t, err)
    assertNoIntakeGraph(t, f.client, cmd.IdempotencyKey, cmd.Title)
}
```

复用现有 `resolverFixture.identity()`、必填 `device_count` 定义和 `catalogCommand`；将该命令 helper 补齐 A2 新增的确认/版本字段。故障目标必须使用 helper 识别的 `field value`，并断言错误包含 `injected field value failure`，防止前置校验失败造成假阳性。

- [ ] 运行 `go test ./handlers/intake -run 'Rollback|ServiceRequestField|AuthoritativeGraph' -count=1`，确认失败点来自事务边界。
- [ ] 将 SR 当前 commit 后的动态字段保存移入 Intake 事务，移除只 Warn 的失败路径；将流程触发改为事务内启动记录。专业准备保留审批链、资源必填、关联 CI、优先级、初始状态和自定义字段定义校验。
- [ ] 删除各专业 repository 的独立基表创建事务，改由专业 Creator 写专业扩展；Generic 无扩展时以明确 generic 规则处理，不创建假扩展。状态机方法保留原专业 Service 归属。
- [ ] 创建记录、SLA、字段、audit 和 workflow-start event 均使用 tx.Client；外部 CI/云资源动作移到既有受治理异步入口，不能在事务内调用外部 API。
- [ ] 使用可审计的领域事件替换创建后必须可靠执行的自动化副作用；不得在 AfterCommit 内直接执行不可恢复的必要步骤，也不因 Intake 重放重复执行 Incident 自动化。
- [ ] 更新 SR schema 与读取 DTO 的共享字段投影，删除双写列依赖；执行隔离库迁移测试。运行 `go test ./handlers/intake ./handlers/service_request ./handlers/change ./handlers/problem ./service -count=1`。
- [ ] 提交 `refactor(workitem): make professional creation atomic`。

## A4：切换全部创建入口及客户端

**Files:** Modify A1 清单中的 `controller/{ticket,incident}_controller.go`、`handlers/{change,problem,standard_change,service_request}/*.go`、`service/{ticket_service,tool_queue,feishu_sync_service}.go`、`service/bpmn/incident_handler.go`、`connector/builtin/email/service.go`、真实邮件/AI 接线；Modify `internal/bootstrap/app.go`、`router/router.go`；Modify 前端 `src/lib/api/{ticket-api,incident-api,service-catalog-api}.ts` 及 A1 确认的实际提交页面；Create `tests/contract/work_item_creation_entrypoints_test.go`。

**Interfaces:** 所有入口只消费 A2 Application。现有 HTTP DTO 映射到统一 command；内部来源键格式为 `provider:stableSourceID:action`，actor/requester 来自原有合法身份解析。参数中不得新增任意可信 tenant/actor 的公网入口。

- [ ] 逐入口加测试，注入 Application spy，断言仅调用一次、完整字段和来源传入。spy 的最小实现：

```go
type creationSpy struct {
    calls int
    identity workitemcreation.Identity
    command workitemcreation.CreateWorkItemCommand
}
func (s *creationSpy) Create(_ context.Context, i workitemcreation.Identity, c workitemcreation.CreateWorkItemCommand) (*workitemcreation.CreateWorkItemResult, error) {
    s.calls++
    s.identity, s.command = i, c
    return &workitemcreation.CreateWorkItemResult{WorkItemID: 51, Number: "TKT-202609-000051", RecordClass: c.RecordClass}, nil
}
```

spy 只用于调用映射测试；另以真实数据库验证业务写入，不能用 spy 证明事务正确。

- [ ] 红测试覆盖：HTTP 缺键拒绝、同键同内容重放、同键异内容冲突；模板/快速建单同样要求键；BPMN 重放同任务不重复创建；邮件同 provider message ID 不重复；AI 不能使用技术执行身份伪装申请人；标准变更真实创建 Change。
- [ ] 运行 `go test ./tests/contract ./controller ./handlers/standard_change ./connector/builtin/email -count=1`。
- [ ] 通过依赖注入切换 Application，删除绕过调用与直接 Ticket.Create。邮件接口当前缺消息 ID，修改为传递既有 EmailMessage 的稳定消息标识和受信来源；不能从 subject/时间猜键。飞书同样使用消息/事件 ID，不依赖重试时间。
- [ ] 现有授权支持的内部执行场景保留原主体/来源；修改公共 Identity 校验为区分已认证用户创建与受信内部创建，并由入口验证的上下文决定，不能由 JSON 标志切换。不得直接移除 actor=requester 约束后接受任意代理建单。
- [ ] 前端生成键放在一次提交状态中；相同已确认内容重试复用键，用户明确修改后重置。实际 Catalog 页面是 `src/app/(main)/service-catalog/request/[id]/page.tsx` → `ServiceCatalogApi.createServiceRequest`，不能只改没有调用方的同名客户端。
- [ ] 增加客户端请求级测试：首次请求丢响应后再次提交，捕获两次键相等；变更字段后新确认键不同；普通权限 403 不自动重试。
- [ ] 重跑 A1 扫描，除唯一 WorkItem writer、生成代码和标注非生产项外无基表直写；运行受影响 Go、Jest、`npm run type-check`，提交 `refactor(intake): converge all production creation entrypoints`。

## A5：Catalog 发布校验与快照版本

**Files:** Create `handlers/service_catalog/{preflight,revision}.go` 及相邻测试；Modify `service.go`、`repository_impl.go`、`entity.go`、`handler.go`、`handlers/intake/resolver.go`；Modify `dto/service_dto.go`、前端 Catalog API 类型。

**Interfaces:** `ValidateForPublication(ctx context.Context, tenantID int, catalog *ServiceCatalog) error` 属 Catalog 应用服务；`Revision(input []byte) string` 对已经 canonical 的公开定义计算 SHA-256，放 `revision.go`。定义 canonical 字段固定包含目标类、表单字段/选项/必填、期限策略、SLA、流程键/版本、委派能力引用；不含显示更新时间、调用者或秘密。

- [ ] 写纯函数红测试和服务组合测试：

```go
func TestRevisionChangesWithPolicy(t *testing.T) {
    before := []byte(`{"durationSeconds":2592000,"targetClass":"service_request_item"}`)
    after := []byte(`{"durationSeconds":7776000,"targetClass":"service_request_item"}`)
    require.NotEqual(t, Revision(before), Revision(after))
    require.Equal(t, Revision(before), Revision(append([]byte(nil), before...)))
}
```

- [ ] 运行 `go test ./handlers/service_catalog -run 'Revision|Publication' -count=1`。
- [ ] 实现 Revision（标准库 `crypto/sha256`、`encoding/hex`）；由 Repository 读取一份一致定义、按字段 ID/option key 稳定排序后 `json.Marshal`。所有影响契约的写路径使用事务和目录版本条件，禁止读到部分新表单/旧策略形成混合版本。
- [ ] 发布测试逐项删除配置：未知 targetClass、缺必填定义、SSLVPN 授权能力无期限或配置无限期、无流程、缺 handler、无候选解析配置、跨租户 SLA、声明授权能力却缺授权目标、声明 KAF 委派却缺必需配置均拒绝；draft 可保存结构未完整草稿但不能启用。
- [ ] 通用 preflight 按目录声明的履约能力调用校验策略；仅声明外部授权的目录要求有限期限与目标组，不能让普通硬件/咨询目录强制配置 SSLVPN 策略。C1 注册授权策略后补该组合测试。
- [ ] Create/Update 直接写目标类，删除 `ComputeTargetClass` 和 `itsm_type` 活跃依赖，合并来源分支已经完成的部分，不再实现一遍。
- [ ] 创建时比较用户确认的目录/表单版本；差异返回 OpenAPI 规定的版本冲突，不自动用新配置代替。流程实例绑定版本，实际审批权限仍实时验证。
- [ ] 若缺 KAF 可查询能力健康接口，ITSM 只验证既有委派配置与 required capability 引用，部署 gate 补实际健康验证；未知必需能力 fail closed，不拉取完整 Procedure 注册表建立副本。
- [ ] 运行 `go test ./handlers/service_catalog ./handlers/intake -count=1`、前端类型检查，提交 `feat(catalog): validate executable configuration before publication`。

## A6：身份交换、用户读契约与 Intake 路由

**Files:** 审查复用早期分支 `handlers/intake/{handler,identity_exchange,identity_mapping_handler}.go` 及测试；Create `identity_exchange_service.go`、`identity_repository.go`；Modify `middleware/auth.go`、`router/router.go`、`config/config.go`、`internal/bootstrap/app.go`；Create `tests/contract/intake_identity_contract_test.go`、根目录 `docs/contracts/fixtures/intake-identity-signature.json`。

**Interfaces:** 保留 `IdentityAssertion` 字段与顺序，外部映射依据 provider/workspace/subject；禁止以邮箱模糊匹配补救。写交换签发 `aud=itsm-intake`/`intake:create`；用户目录/状态读取严格使用总计划 §3.2 定义的只读交换路由、scope 和响应投影；同一 nonce store 跨两种交换用途拒绝重放。技术自动化 token 仅用于既有 task API。

- [ ] 迁入旧 identity exchange 测试，增加 provider/channel 允许列表、包含换行的字段、nonce 重放、Redis 不可用、停用映射、跨 workspace、错误 audience、伪造 subject 和 role 拒绝用例。
- [ ] 跨语言 fixture 用固定测试秘密与 nonce（仅测试文件），生成并保存准确 HMAC；A/B 读取同一文件。实现/测试流程：

```python
import hashlib, hmac, json
fields = ["kaf", "workspace-test", "subject-test", "kaf_web", "submission-test", "1788566400", "nonce-test"]
canonical = "\n".join(fields)
fixture = {"fields": fields, "secret": "test-only-key", "canonical": canonical,
           "signature": hmac.new(b"test-only-key", canonical.encode(), hashlib.sha256).hexdigest()}
print(json.dumps(fixture, indent=2))
```

- [ ] 运行 `go test ./handlers/intake ./tests/contract -run 'Identity|Assertion|Audience|Scope' -count=1`。
- [ ] 将旧 Handler 中直接 Ent 查询、签名/nonce/审计业务下沉到服务和 repository；保持 Handler 绑定、限长、调用、DTO 返回。字段拒绝 CR/LF，消除 newline canonicalization 歧义；时间窗、TTL、允许 provider/channel 从已有配置机制注入。
- [ ] nonce store 使用 provider/channel/workspace 范围，验签通过后原子 claim；存储失败即拒绝。身份映射管理使用权限与版本条件，审计不记录 assertion、token、秘密和个人明文。
- [ ] 注册 create、identity exchange、映射管理及最小用户目录/当前状态读路由，分别验证 scope 和对象权限。创建 201/200、错误 envelope 与 B 共用 OpenAPI；Intake token 不可访问任意通用 API。
- [ ] 配置使用角色受限 secret 文件；exchange secret 与 JWT 签名密钥、webhook HMAC、automation token 分开；缺失时相关能力明确不可用。
- [ ] 运行包测试与 route/ACL 契约测试，提交 `feat(intake): bind user identity exchange and scoped APIs`。

## A7：PostgreSQL、迁移与全入口集成门禁

**Files:** Create `tests/integration/intake_creation_test.go`、`tests/contract/work_item_creation_entrypoints_test.go` 的剩余数据库用例；Modify A1 固定的 schema/migration/verify 文件、`internal/bootstrap/post_schema_migrations_test.go`；记录证据到总计划指定报告。

**Interfaces:** 使用现有集成测试隔离数据库策略与 `RLS_TEST_DSN`/测试 DSN 机制；不能在普通 `go test` 下隐式连共享库。真正的 PostgreSQL 测试标签沿现有文件构建标签。

- [ ] 增加同请求 20 个并发创建：所有成功结果同一 WorkItem、同一扩展、一个启动事件；不同请求独立编号；任一贡献者失败无部分记录。
- [ ] 增加双 Worker 重放同启动事件：只有一个流程实例；事务提交后模拟返回错误再次重放仍返回原申请。禁止用睡眠等待碰运气，使用 barrier/注入故障和轮询断言。
- [ ] 在空库和隔离恢复库执行完整 schema + post-schema stream，保留命令/版本/退出码；旧列读写零命中，新 FK/唯一键/租户角色负例通过。复用 `cmd/check_work_item_integrity` 验证一对一约束。
- [ ] 运行最终命令：

```bash
go test ./... -count=1
go build ./...
go test -tags integration ./handlers/intake ./tests/integration ./repository/workitemnumber -count=1
go test -tags integration_rls ./database/rls -count=1
```

集成命令必须配置指向隔离库的实际 DSN，并确认测试没有全部 skip；错误标签或无测试匹配不能记作通过。前端执行 `npm run type-check`、`npm run lint:check`、`npm run test:ci`、`npm run build`。

- [ ] 逐行关闭 A1 清单，验证所有错误映射和 DTO 字段，检查 `git diff --check`，提交 `test(intake): verify atomic creation across all entrypoints`。

**完成定义：** A2-A6 的功能与 A7 的真实数据库证据均通过；无遗漏入口、无测试豁免伪装通过。此时才能向 B 提供可集成的 ITSM 基线。
