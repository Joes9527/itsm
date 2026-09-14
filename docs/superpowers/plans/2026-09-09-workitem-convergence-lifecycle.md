# WorkItem Lifecycle Convergence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> 状态：draft；执行顺序 A1 → A2 → A3 → A4。

**Goal:** 交付一致的公共 SLA 周期和三个专业域唯一动作路径。

**Architecture:** 领域服务拥有动作；共享命令仅提供元数据与事务公共能力。现有 TicketSLAService 负责周期计时；旧周期归档到既有不可变审计，不建立可写的第二套当前状态。

**Tech Stack:** Go/Gin/Ent/PostgreSQL，Next.js/TypeScript/Jest。

**Spec:** [设计](../specs/2026-09-09-workitem-convergence-design.md)；[总计划与公共 Meta/Result 契约](2026-09-09-workitem-convergence.md)。

## Global Constraints

- 历史数据不迁移；schema 变更仍需正式脚本。新增周期仅由新建或合法后续动作生成。
- 重开当前周期归零，原 createdAt 和旧周期结果不改写。
- 完成条件、授权、CAS、审计和事件必须在同一所属领域事务中生效。
- 同一域的 HTTP、批量、调查和回调必须同时切换；不能上线半切换版本。
- 所有路径为仓库相对路径；Run 在 itsm-backend 中执行，前端命令另行注明。

## A1：公共动作元数据、SLA 当前周期与权威读取

**Files**
- Create: `itsm-backend/handlers/shared/workitemmutation/contract.go`（总计划中的 Meta/Result）。
- Create: `itsm-backend/service/ticket_sla_cycle.go`、`ticket_sla_cycle_test.go`。
- Modify: `itsm-backend/ent/schema/ticket.go`、`auditlog.go`；`service/ticket_sla_creation.go`、`ticket_sla_service.go`、`sla_monitor_service.go`；`service/outbox_event_type_registry.go`。
- Create: `itsm-backend/migrations/20260910_workitem_sla_cycle.sql`；`tests/integration/workitem_sla_cycle_postgres_test.go`。
- Modify: `itsm-backend/dto/ticket_dto.go`、`itsm-frontend/src/lib/api/ticket-api.ts` 的现有 SLA 响应类型及实际调用者。

**Interfaces**
在 service 包新增以下值类型与函数，不导出领域状态规则：

```go
type SLACycleClock struct {
    Number int
    StartedAt time.Time
    ResponseAt *time.Time
    ResolvedAt *time.Time
    PausedMinutes int
}
func ResetSLACycle(previous SLACycleClock, at time.Time) SLACycleClock {
    return SLACycleClock{Number: previous.Number + 1, StartedAt: at}
}
func (s *TicketSLAService) ResetCycleTx(ctx context.Context, tx *ent.Tx,
    item *ent.Ticket, at time.Time) error
```

ResetCycleTx 不提交事务、不修改 recordClass 或专业状态。调用领域服务负责 CAS；cycle 更新失败同事务回滚。

- [ ] **RED：** 在 ticket_sla_cycle_test.go 导入 testing/time，新增：

```go
func TestResetSLACycle(t *testing.T) {
    oldAt := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
    reopened := oldAt.Add(4*time.Hour)
    old := SLACycleClock{Number: 1, StartedAt: oldAt,
        ResponseAt: &oldAt, ResolvedAt: &oldAt, PausedMinutes: 30}
    got := ResetSLACycle(old, reopened)
    if got.Number != 2 || !got.StartedAt.Equal(reopened) ||
       got.ResponseAt != nil || got.ResolvedAt != nil || got.PausedMinutes != 0 {
        t.Fatalf("cycle not reset: %+v", got)
    }
    if old.Number != 1 || old.ResolvedAt == nil { t.Fatal("old result overwritten") }
}
```

- [ ] Run: `go test ./service -run '^TestResetSLACycle$' -count=1`。先确认接口缺失，再用旧计时行为建立 PG 回归：旧周期 08:00 建立、09:00 按时解决、12:00 重开，新的 60 分钟响应目标应到 13:00，不是 09:00。
- [ ] **Schema：** Ticket 新增当前周期编号、起点、暂停累计及 applied SLA policy snapshot（已应用目标时长、日历配置与版本）。已有 deadline 字段仍是唯一当前截止时间；不复制到新可写表。SLADefinition 是可编辑模板，applied snapshot 是该周期冻结合同，两者不是双写同一字段。已有首次响应／解决字段表示当前周期，旧值及政策快照写不可变审计事件。旧记录不回填 snapshot；若后续需重开但合同缺失，按设计阻断并要求显式授权策略应用。
- [ ] **实现：** ResetCycleTx 先保存旧周期最终事实到既有审计，再用 snapshot 和既有 calculateDeadlineWithBusinessHours 计算两个新截止时间；清除当前完成投影及暂停累计。新的创建和显式策略重算同时写 snapshot、起点和 deadline。SLA 缺失且声明无需 SLA 时不造计时器；绑定存在但 snapshot 缺失时返回明确配置错误。
- [ ] **单一读取：** GetTicketSLAInfo 删除 getSLADefinition 二次匹配，读取已落库 deadline；判断使用 ResponseAt/ResolvedAt 或当前时间。monitor 与统计复用同一完成／暂停投影。通知不在此函数直接发送；周期事件进入既有 Outbox 注册表。
- [ ] **PG：** 新测试复用 incident_effects_postgres_test.go 的隔离 schema 模式；注入审计写失败，验证周期及 Ticket 版本全部回滚；两次相同 OperationID 只产生一次新周期；旧违约仍保留；他租户不可读取 cycle snapshot；规则修改不改变旧结果。
- [ ] Run: `go generate ./ent`；`go test ./service -run 'TestResetSLACycle|TestTicketSLA' -count=1`；`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemSLACycle' -count=1`。PG 测试名统一前缀 TestWorkItemSLACycle，DSN 未提供必须明确失败。
- [ ] 审核所有 TicketSLAInfo 类型调用，前端展示 current cycle 与历史结果，不能把旧周期违约覆盖为新周期达标。执行 type-check。
- [ ] `git diff --check`；独立复核后提交 `refactor(sla): persist authoritative resettable work item cycles`。

## A2：Incident 唯一动作、重开和入口 CAS

> 2026-09-11 决策补充：[Incident 分配与处理动作收敛](../specs/2026-09-11-workitem-incident-assignment-convergence-design.md) 已确认首次分配、同状态转派和可选确认语义。后续实施以该文档为准更新入口与验收，不能机械复用旧 assign 的“必须状态变化”假设；本补充不表示 A2 缺口已修复。

**Files**
- Create: `itsm-backend/service/incident_commands.go`、`incident_commands_test.go`。
- Modify: `service/incident_service.go`、`incident_work_item_authority.go`、`service/bpmn/incident_handler.go`；`dto/incident_dto.go`；`controller/incident_controller.go`；`authorization/workitem.go`。
- Create: `tests/integration/workitem_incident_lifecycle_postgres_test.go`。
- Modify: `itsm-frontend/src/lib/api/incident-api.ts`、`src/components/incident/IncidentDetail.tsx` 及相邻测试。

**Interfaces**

```go
type IncidentCommand struct {
    Meta workitemmutation.Meta
    IncidentID int
    Action string
    Reason string
    Resolution string
}
func ValidateIncidentRecovery(resolution string) error {
    if strings.TrimSpace(resolution) == "" { return errors.New("recovery evidence required") }
    return nil
}
func (s *IncidentService) ApplyIncidentCommand(ctx context.Context,
    cmd IncidentCommand) (workitemmutation.Result, error)
```

Consumes A1 Meta/Result 和 ResetCycleTx；领域 ID 仅在边界解析，审计聚合 ID 是 WorkItem ID。

- [ ] **RED：** 新增单元测试：

```go
func TestIncidentRecoveryEvidence(t *testing.T) {
    if ValidateIncidentRecovery(" ") == nil { t.Fatal("empty recovery accepted") }
    if err := ValidateIncidentRecovery("workaround restored WMS; opening verified"); err != nil { t.Fatal(err) }
}
```

- [ ] Run: `go test ./service -run '^TestIncidentRecoveryEvidence$' -count=1`。
- [ ] **实现动作：** acknowledge、resolve、close、reopen 调用既有合法状态判定，统一在 ApplyIncidentCommand 的所属事务处理；reopen 目标沿用 IncidentStatusInProgress，调用 A1 ResetCycleTx。resolve 不读取 Problem 完成状态作为前提。关闭策略由已有配置提供，不能硬编码宽限天数。
- [ ] 在同一事务按 ExpectedVersion 做 Ticket CAS、更新 Incident 专业字段、审计/事件/幂等事实；重复 OperationID 先比摘要，不能因状态不同创建第二周期。对不同键的陈旧版本返回 409。删除原 ResolveIncident/CloseIncident/ForWorkflow 等重复状态执行体，所有实际调用者直接使用新命令；不保留转发别名。
- [ ] 通用 UpdateIncident/UpdateIncidentTx 不再接受 status 写入，普通编辑也必须有预期版本；controller 不接受客户端 actor/tenant。BPMN Handler 读取可信任务身份和版本并构造同一命令，不能直接调用 Ent。同步切换前端所有动作请求。
- [ ] **PG 与入口测试：** 参考 newIncidentEffectsFixture；以相同初态分别走 HTTP 和回调，比较状态、version、审计数和周期；未完成 Problem 不阻止 Incident resolve；并发两请求只能一个提交；审核 failure 注入无半写入；跨 tenant 404/拒绝；close/reopen 均需现有权限；旧 Update(status) 明确拒绝。
- [ ] Run: `go test ./service ./controller ./authorization ./service/bpmn -run 'TestIncident|TestWorkItem' -count=1`；`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemIncidentLifecycle' -count=1`。前端 `npm test -- --runInBand --runTestsByPath src/components/incident/__tests__/IncidentDetail.test.tsx`，若基线没有该测试文件就在本任务创建。
- [ ] 独立复核并提交 `refactor(incident): route all lifecycle actions through domain commands`。

## A3：Problem 调查、验证与状态权威

**Files**
- Create: `itsm-backend/handlers/problem/lifecycle.go`、`lifecycle_test.go`。
- Modify: `handlers/problem/service.go`、`repository.go`、`repository_impl.go`、`handler.go`、`entity.go`；`service/problem_investigation_service.go`；`controller/problem_investigation_controller.go`；`ent/schema/problem.go`；`internal/bootstrap/app.go`；`router/router.go`。
- Create: `migrations/20260910_problem_investigation_completion.sql`；`tests/integration/workitem_problem_lifecycle_postgres_test.go`。
- Modify: `itsm-frontend/src/lib/api/problem-api.ts`、`problem-investigation.ts`；`src/components/problem/ProblemDetail.tsx`、`ProblemInvestigationTab.tsx`；相邻测试。

**Interfaces**

```go
type ResolutionEvidence struct {
    RootCause string
    PermanentSolution string
    Verified bool
    VerificationNote string
}
func ValidateResolution(e ResolutionEvidence) error {
    if strings.TrimSpace(e.RootCause) == "" || strings.TrimSpace(e.PermanentSolution) == "" ||
       !e.Verified || strings.TrimSpace(e.VerificationNote) == "" {
        return errors.New("verified permanent resolution required")
    }
    return nil
}
type Command struct {
    Meta workitemmutation.Meta
    ProblemID int
    Action string
    Reason string
    VerificationNote string
}
func (s *Service) ApplyCommand(ctx context.Context, cmd Command) (workitemmutation.Result, error)
```

ResolutionEvidence 是读取权威记录后的临时校验值，不是第二份持久化 rootCause。客户端不能自报 Verified=true 绕过持久化验证记录。

- [ ] **RED：**

```go
func TestProblemRequiresPermanentVerification(t *testing.T) {
    if ValidateResolution(ResolutionEvidence{RootCause:"known"}) == nil { t.Fatal("workaround-only accepted") }
    e := ResolutionEvidence{RootCause:"bad lock", PermanentSolution:"fixed lock", Verified:true, VerificationNote:"WMS regression passed"}
    if err := ValidateResolution(e); err != nil { t.Fatal(err) }
    e.Verified = false
    if ValidateResolution(e) == nil { t.Fatal("unverified fix accepted") }
}
```

- [ ] Run: `go test ./handlers/problem -run '^TestProblemRequiresPermanentVerification$' -count=1`。
- [ ] **Schema 和唯一事实：** 采用已合并 RCA 修复的 Problem rootCause。Problem resolution 作为已选永久方案正文权威；调查 solutions 管理候选方案，选中时以明确领域动作形成最终决定，不双写保持同步。验证新增针对当前 WorkItem version/方案摘要的结果、验证人、验证时间和说明；正文或方案变更使旧验证不再满足 resolve。调查表的字段依据现有 DTO/SQL 明确建立 investigations、steps、solutions，actor/reviewer 均验证同 tenant，强制 RLS。其他未启用调查能力不能建空成功接口。
- [ ] ApplyCommand 支持 investigate、verify_resolution、resolve、close、reopen。reopen 目标 investigating，并调用 A1；直接 open→resolved 在缺根因/方案/验证时拒绝。有必需修复 Change 时读取权威关系与结果（B 尚未发布前不杜撰旧关系含义），不能用 Change 成功替代 verify_resolution。
- [ ] 删除调查服务 UPDATE tickets 状态 SQL；创建调查需要开始状态时通过 Problem 域同一事务执行。将必要调查持久化移入现有 Problem repository 事务边界，禁止 Ent 与 sql.DB 各自开启事务后串联。保留调查服务只负责无状态副作用的读取也应按职责命名；被替代方法同批删除。
- [ ] 通用 Problem Update(status) 禁止旁路；HTTP/前端直接调用领域动作接口。验证记录由后端在 verify_resolution 权限检查后写入，复用当前域权限策略，不凭 admin 显示名授权。RCA 修改同步 WorkItem version，确保 verify 后并发编辑不能成功 resolve。
- [ ] **PG：** 空表正式 schema 可用；原始 resolved/closed 记录无验证时不批量迁移，后续 close 拒绝缺证据；合法 reopen 新周期；候选 workaround 不满足永久方案；验证与根因并发修改冲突；调查完成不改变 Problem 状态；同租户正常、异租户 analyst/reviewer 拒绝；审计失败回滚。
- [ ] Run: `go test ./handlers/problem ./service ./controller ./router -run 'TestProblem|TestRCA' -count=1`；`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemProblemLifecycle' -count=1`；前端 `npm test -- --runInBand --runTestsByPath src/components/problem/__tests__/ProblemDetail.test.tsx`。
- [ ] 独立复核并提交 `refactor(problem): enforce verified resolution through domain lifecycle`。

## A4：Change 结果、授权与回调收敛

**Files**
- Create: `itsm-backend/handlers/change/commands.go`、`commands_test.go`。
- Modify: `handlers/change/service.go`、`workflow_callback.go`、`repository_impl.go`、`handler.go`、`entity.go`；`common/change_lifecycle.go`；`handlers/shared/workflowcallback/contract.go`；`service/bpmn/change_handler.go`。
- Create: `tests/integration/workitem_change_lifecycle_postgres_test.go`。
- Modify: `itsm-frontend/src/lib/api/change-api.ts`、`standard-change-api.ts` 与实际动作调用者；新增 `src/lib/api/__tests__/change-command.test.ts`。

**Interfaces**

```go
type Command struct {
    Meta workitemmutation.Meta
    ChangeID int
    Action string
    Outcome string
    Evidence string
}
func IsSuccessfulOutcome(outcome string) bool { return outcome == "successful" }
func (s *Service) ApplyCommand(ctx context.Context, cmd Command) (workitemmutation.Result, error)
```

- [ ] **RED：**

```go
func TestChangeClosureIsNotSuccess(t *testing.T) {
    for _, outcome := range []string{"closed", "failed", "rolled_back", ""} {
        if IsSuccessfulOutcome(outcome) { t.Fatalf("%q counted as success", outcome) }
    }
    if !IsSuccessfulOutcome("successful") { t.Fatal("success missing") }
}
```

- [ ] Run: `go test ./handlers/change -run '^TestChangeClosureIsNotSuccess$' -count=1`。
- [ ] 将 submit、assess、authorize、schedule、implement、review、close/cancel 的既有规则归入同一领域命令执行路径，保留 common/change_lifecycle.go 类型化状态转换。命令 action 由显式允许集合验证；未知 action 拒绝。标准变更预授权有模板依据，紧急变更不能跳过既有要求的记录或追认。
- [ ] 关闭校验 PIR 与实施结果；successful/failed/rolled_back 表示专业结果，不复用 status 兼任。实施时间归专业扩展，公共更新/version/审计同事务。保留失败事实，不自动 reopen/resolve 关联项。Change 不新增任意重开直返实施入口。
- [ ] 删除 workflow_callback.go 中另一份直接 Ticket.SetStatus 实现；ApplyChangeWorkflowCallback 转为现有 Handler 边界输入映射，实际执行调用 ApplyCommand（若不再有实际消费者则连旧接口一并删除）。普通 UpdateChange 不允许提交 status。所有回调都校验传入预期版本，禁止以当前 DB version 覆盖陈旧预期。
- [ ] HTTP、CAB、阶段完成和前端更新统一切换；后台仍由系统身份的受约束专业授权执行，不能信任 BPMN 表单 actor。必需的流程完成失败回滚／可见阻断，不能当作“没有任务”静默成功。
- [ ] **PG：** HTTP/回调同初态相同结果；未授权实施拒绝；标准和紧急类型回归；两个回调竞争只有一个版本提交；PIR 未完成不能 close；failed/rolled_back 不发 successful 结果；无租户权限拒绝。
- [ ] Run: `go test ./handlers/change ./service/bpmn -count=1`；`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemChangeLifecycle' -count=1`；前端 `npm test -- --runInBand --runTestsByPath src/lib/api/__tests__/change-command.test.ts`。
- [ ] 独立复核并提交 `refactor(change): unify governed commands and explicit outcomes`。

## A 批交付门禁

- [ ] 搜索三域生产文件的 SetStatus/UPDATE tickets，逐项证明只剩领域所有者、创建事务或明确公共投影写入；不是要求全仓文本零匹配。
- [ ] generic 与 Requested Item 创建、评论、附件、现有审批回归通过；前端 type-check/lint:check/build 通过。
- [ ] 记录所有受影响入口和已删除方法；无兼容转发别名、无调查状态旁路；结构脚本已在 disposable PG 验证，无历史数据回填。
- [ ] 本批代码可以独立接受评审；部署仍遵守 C3 共用发布门禁，不将中间提交单独部署。
