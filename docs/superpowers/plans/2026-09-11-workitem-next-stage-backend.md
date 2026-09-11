# WorkItem Next Stage Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 收敛三域责任调整与公共变更协议，并修复流程身份、通知授权和重试缺口。

**Architecture:** 复用专业服务、workitemmutation、authorization、DirectorySnapshot 与 Outbox。通用结构承载公共事实，专业服务决定动作；每项变更同步更新全部调用方并删除被替换写路径。

**Tech Stack:** Go/Gin、Ent、PostgreSQL、现有 BPMN 与 Go testing。

> 状态：accepted（执行中；B1 已实现并验证，B2/B3 进行中，其余未执行）
> 依据：[后续设计](../specs/2026-09-11-workitem-convergence-next-stage-design.md)；[总入口](2026-09-11-workitem-next-stage.md)。命令从 itsm-backend 执行，除非另有说明。

## Global Constraints

- 代码观察基线 4660633019f10ec23835e23c7fa43578daa7ff57；开始执行时核对集成基线，不覆盖原工作树和 .superpowers/sdd。
- 每次转派原因必填；首次分配不因本轮决定额外强制原因。仅转派不清除审批或调查证据。
- 不改响应考核，不新增多 WorkOrder、审批引擎或通用状态机。
- 不迁移历史业务数据；真实 PostgreSQL 验证只使用隔离数据库/schema，不初始化共享数据库。
- 保留各专业 HTTP 契约：Incident/Problem 使用 version，Change 使用 expectedVersion；API 适配器映射同一个观察版本到 Meta.ExpectedVersion。不得新增双别名，也不为统一显示重命名现有 Change 请求字段。
- 先复现失败再修改；每任务检查 diff、运行列明测试并单独提交；不得仅因测试返回 0 而忽略未匹配测试。

## B1：Incident 分派统一到现有命令

**Files**
- Modify: `dto/incident_dto.go`、`dto/incident_command.go`、`controller/incident_controller.go`、`controller/incident_command.go`、`service/incident_service.go`、`service/incident_commands.go`、`service/bpmn/incident_command.go`。
- Test: `service/incident_commands_test.go`、`controller/incident_command_test.go`。
- Create: `tests/integration/workitem_assignment_postgres_test.go`（本任务 Incident 用例，B2/B3 追加各域）。

**Interfaces**
继续调用 `ApplyIncidentCommand(context.Context, dto.IncidentCommand) (workitemmutation.Result, error)`。AssigneeID、Reason、Meta 已存在，不另造命令总线。现有分派 HTTP 请求增加 version、operationId、reason，并映射到 Action="assign"；保留现有路由，删除旧服务中独立事务/活动写入实现。请求示例：

```json
{"assigneeId":42,"version":7,"operationId":"assign-unique-attempt","reason":"交由应用支持继续排查"}
```

- [x] 在 `service/incident_commands_test.go` 加入以下真实 fixture 测试，再扩展为空原因、陈旧版本和同键改原因的拒绝测试：

```go
func TestIncidentReassignmentPreservesProgress(t *testing.T) {
    for _, status := range []string{"assigned", "acknowledged", "in_progress"} {
        t.Run(status, func(t *testing.T) {
            client, svc, ctx := setupIncidentTest(t)
            defer client.Close()
            tenant, err := createIncidentTestTenant(ctx, client, "reassign")
            require.NoError(t, err)
            actor, err := createIncidentTestUser(ctx, client, tenant.ID, "owner")
            require.NoError(t, err)
            actor.Update().SetRole("super_admin").ExecX(ctx)
            next, err := createIncidentTestUser(ctx, client, tenant.ID, "next")
            require.NoError(t, err)
            inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "reassign")
            before := client.Ticket.UpdateOneID(inc.WorkItemID).
                SetStatus(status).SetAssigneeID(actor.ID).SaveX(ctx)
            cmd := dto.IncidentCommand{IncidentID: inc.ID, Action: "assign",
                AssigneeID: next.ID, Reason: "handover",
                Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID,
                    ExpectedVersion: before.Version, Source: "http", OperationID: "reassign"}}
            result, err := svc.ApplyIncidentCommand(ctx, cmd)
            require.NoError(t, err)
            after := client.Ticket.GetX(ctx, before.ID)
            require.Equal(t, status, after.Status)
            require.Equal(t, next.ID, after.AssigneeID)
            require.Equal(t, before.Version+1, result.Version)
            replay, err := svc.ApplyIncidentCommand(ctx, cmd)
            require.NoError(t, err)
            require.True(t, replay.Replayed)
            require.Equal(t, result.Version, replay.Version)
        })
    }
}
```

- [x] Run `go test ./service -run '^TestIncidentReassignmentPreservesProgress$' -count=1 -v`；记录旧行为失败。
- [x] 修改 assign 分支：new 首次分派取 assigned；合法后续转派使用当前状态。仅为有效负责人变更放行同状态动作；不能放宽 resolve/start 等同状态拒绝。校验目标资格、当前授权、非空原因；摘要包含目标与原因，回执/审计/事件同事务。直接 start 保留现有首次响应事实。
- [x] 搜索所有 `AssignIncident`、`assign_incident` 和 `AssigneeID` 写入，HTTP、BPMN、规则动作调用同一命令；更新调用方传入观察版本和稳定操作键。不能服务器读取新版本替代旧请求；不能保留旧 AssignIncident 独立逻辑作 fallback。
- [x] PostgreSQL 用例覆盖两请求同版本只有一个成功、同键重放仅一次审计、审计失败整体回滚、撤权后重放被拒绝；复用已有 WorkItem 集成 fixture 的隔离连接方式。
- [x] Run `go test ./service -run 'TestIncident.*(Command|Assign|Recovery|Start)' -count=1 -v`、`go test ./controller -run 'Test.*Incident' -count=1 -v`、`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemAssignmentIncident' -count=1 -v`。确认新增 PG 用例实际执行。
- [x] 提交：`refactor(incident): converge assignment on versioned commands`。

## B2：Change 转派原因及任务身份保护

**Files**
- Modify: `dto/change_dto.go`、`handlers/change/metadata.go`、`handlers/change/command_handler.go`、`handlers/change/handler.go`、`handlers/change/task_command.go`。
- Test: `handlers/change/metadata_test.go`、`handlers/change/change_bpmn_e2e_test.go`、`tests/integration/workitem_assignment_postgres_test.go`。

**Interfaces**
保留 `ApplyMetadata(context.Context, MetadataCommand) (workitemmutation.Result, error)`；为 `dto.UpdateChangeRequest` 增加 `AssignmentReason string`（JSON `assignmentReason`），由所有现有编辑/分派入口传递。它是动作输入，不新增 Change 扩展持久化字段。Change 请求继续使用 expectedVersion/operationId，与 MutationRequest 的严格绑定一致；公共组件的 version 由 API 适配器映射为 expectedVersion。

- [ ] 在既有 metadata fixture 中新增名为 `TestChangeReassignmentPreservesApproval` 的测试：创建已评估且具有审批快照/流程任务的 Change；转派后比较状态、AssessmentDigest、审批 decision ID、任务执行人均未改变，只允许 WorkItem 负责人/版本/审计改变。为空原因测试断言错误及版本不变。
- [ ] Run `go test ./handlers/change -run '^TestChangeReassignment' -count=1 -v`，先证明缺原因可成功的旧路径失败于新断言。
- [ ] 在 normalizeMetadataPatch 中清理原因，在已读取 item 后执行下述分支；审计与摘要保留原因，不改变既有受保护方案/窗口校验：

```go
if p.AssigneeID != nil && item.AssigneeID > 0 && *p.AssigneeID != item.AssigneeID {
    if strings.TrimSpace(p.AssignmentReason) == "" {
        return empty, common.NewValidationError("assignment reason required", nil)
    }
}
```

- [ ] 更新全部 MetadataCommand/UpdateChangeRequest 构造点和工作流映射。测试同时修改受保护方案时仍拒绝；未决回调仍按现有 settled 约束处理；不创建自动审批转派或任务移交。
- [ ] Run `go test ./handlers/change -count=1`、`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemAssignmentChange' -count=1 -v`。记录并修复新增字段导致的调用方测试变化。
- [ ] 提交：`refactor(change): audit reassignment without changing approvals`。

## B3：Problem 责任调整与普通编辑权威路径

**Files**
- Modify: `handlers/problem/service.go`、`handlers/problem/repository.go`、`handlers/problem/repository_impl.go`、`handlers/problem/handler.go`、`handlers/problem/lifecycle.go`、`handlers/problem/authorization.go`、`handlers/problem/entity.go`、`dto/problem_dto.go`。
- Create: `handlers/problem/metadata.go`、`handlers/problem/metadata_test.go`。
- Test: `handlers/problem/lifecycle_test.go`、`handlers/problem/authorization_test.go`、`tests/integration/workitem_assignment_postgres_test.go`。

**Interfaces**
新文件承载真正的普通编辑事务，复用 `authorizeCommand` 和 workitemmutation，不以空转发层包旧 repository.Update。定义：

```go
// package problem
// dto.UpdateProblemRequest 新字段（保留已有 Version 与其他字段）：
// OperationID string `json:"operationId" binding:"required,max=200"`
// AssigneeID *int `json:"assigneeId,omitempty" binding:"omitempty,gt=0"`
// AssignmentReason string `json:"assignmentReason"`
// Workaround *string `json:"workaround"`
// Resolution *string `json:"resolution"`
type MetadataCommand struct {
    Meta workitemmutation.Meta
    ProblemID int
    Patch dto.UpdateProblemRequest
}
// 方法返回 workitemmutation.Result，事务与授权位于此方法。
// func (s *Service) ApplyMetadata(ctx context.Context, cmd MetadataCommand) (workitemmutation.Result, error)
```

输入省略保持原值，指针字段显式空字符串表示清空对应正文（专用根因接口仍保持原有非空校验）；清空/修改被验证内容必须使当前验证失效。专用 `UpdateProblemResolutionRequest` 的 Solution、Workaround、Resolution 同步改成 `*string` 保留字段是否出现；Version仍为int并增加OperationID。handler仅在Resolution==nil时使用Solution；Resolution显式空字符串优先表示清空，不回退到Solution。Workaround单独映射为Workaround。这里明确细化原string请求的空值语义，不能声称仍按空字符串自动fallback；不新增另一个持久化字段。映射代码如下：

```go
resolution := req.Resolution
if resolution == nil {
    resolution = req.Solution
}
patch := dto.UpdateProblemRequest{
    Version: req.Version, OperationID: req.OperationID,
    Workaround: req.Workaround, Resolution: resolution,
}
```

HTTP断言覆盖仅workaround时永久方案保持、resolution显式空且solution非空时仍清空并使验证失效、resolution省略且solution非空时使用原solution输入，以及所有正文均省略时明确拒绝无业务变更。清空不允许绕过既有终态/专业约束。

- [ ] 在既有 Problem fixture 建立有 RCA、永久方案与验证证据的记录；调用新 metadata 方法仅改 AssigneeID。测试比较状态、RCA/方案/验证人/验证时间/验证依据均保持；验证版本审计前进。另测空原因、越权、同键改目标和失败回滚。
- [ ] Run `go test ./handlers/problem -run '^TestProblemMetadata' -count=1 -v`；新方法未实现时应编译失败，落地后不得移除断言。
- [ ] 实现与 Change metadata 同一协议的 RR 事务：当前授权→回执→观察版本→字段/专业规则→CAS→审计回执→提交。负责人变化且已有负责人时要求原因；目标按既有域规则校验。根因/方案内容变化仍调用既有证据有效性规则，不把转派造成的 WorkItem 版本增长当作内容证据变化。
- [ ] 将现有普通更新、RCA、方案编辑 HTTP/内部调用者携带 Meta 接入权威事务；根因/方案专用请求增加 operationId 并更新调用方，不保留无 actor 的可写服务签名。需要返回完整详情的 HTTP 路由在成功后走授权详情读取，不用返回值迫使第二次写入。
- [ ] 同步修正证据有效性与动作投影：当前 lifecycle 与 authorization 分别使用 VerifiedVersion==Version 和 Version-1，单纯转派后会失效。新增领域内 `CurrentResolutionVerification(rootCause, resolution, digest, note string, verifiedBy, verifiedVersion int, verifiedAt time.Time) bool`，复用现有 resolutionDigest 的同一摘要算法，要求非空根因/方案/说明、有效验证人/时间、正验证版本和内容摘要相符；VerifiedVersion 保留验证当时的审计版本，不随转派伪造更新。它不替代状态、权限、必需 Change 结果校验。
- [ ] `Problem` 查询投影增加 VerificationDigest、VerifiedBy、VerifiedAt，均读取现有扩展字段；执行命令与 BuildProblemActions 共用上述证据判断。根因/永久方案实际变化、重新选择方案、重开时在同一事务清除当前验证字段并保留历史审计；不能只删版本比较而让 A→B→A 内容回退恢复旧验证。调查证据变更入口沿用其专业失效规则并逐项纳入入口清单。
- [ ] 新增串行验收：verify→合法转派→actions.resolve可用→resolve→actions.close可用→close；验证人员、时间与原验证版本不变。另测正文 A→B→A仍须重新验证、reopen后不得复用旧验证、workaround-only更新不覆盖永久方案、专用方案 HTTP 输入完整落库。
- [ ] 为 Problem 增加明确的 `actions.assign` 投影与终态拒绝原因，在 metadata 写入时执行相同状态/资格前提；不能将 edit 权限直接当成允许转派。测试 current session撤权、终态、无可用目标时组件与直接 API 均拒绝。
- [ ] 从 repository.Update 移除被替换公共写路径；禁止生命周期和 metadata 各自更新同一操作。盘点时发现的其他操作若违反版本/审计契约，归入本任务并增加对应真实调用测试，不用一层 wrapper 宣告完成。
- [ ] Run `go test ./handlers/problem -count=1`、`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemAssignmentProblem' -count=1 -v`；验证原调查/方案流程回归。
- [ ] 提交：`refactor(problem): converge metadata and preserve handover evidence`。

## B4：拒绝保留变量覆盖并修正实际配置入口

**Files**
- Modify: `service/bpmn_process_trigger_service.go`、`config/seed/default.json`、`pkg/seeder/seeder.go`。
- Test: `service/bpmn_process_trigger_service_test.go`、`pkg/seeder/workitem_identity_test.go`（新建）。

**Interfaces**
复用现有 ProcessTriggerRequest 和 canonical identity helper；不新增旧类型别名。原 C1 所有发布、绑定、回调校验继续生效。

- [ ] 将评审归档中 trigger overlay 的真实启动反例转为相邻正式测试；对请求保留变量逐键覆盖，断言启动拒绝且未产生实例。普通非保留变量仍成功。
- [ ] 为真正从 default.json 加载并覆盖嵌入 seed 的路径编写测试；断言所有 WorkItem binding 使用 canonical 类，历史检测器与显式 Release 值按现有规则处理。
- [ ] Run `go test ./service -run 'Test.*Trigger.*Identity|Test.*Reserved' -count=1 -v`、`go test ./pkg/seeder -run 'Test.*WorkItem' -count=1 -v`；确认反例失败。
- [ ] 在 req.Variables 合并之前使用现有保留变量集合逐键拒绝；修正维护的 JSON 和实际入库校验。不得修改历史实例或让非 API seed 绕过 validator；避免再维护一套保留键常量。
- [ ] Run `go test ./dto ./common/workitemidentity ./service/bpmn ./pkg/seeder -count=1` 和上述服务测试；回归原 C1 PostgreSQL cutover 测试及 Release。
- [ ] 提交：`fix(bpmn): reject identity overrides and validate loaded seed bindings`。

## B5：当前授权、MSP RLS 与重试分类

**Files**
- Modify: `authorization/lifecycle_actor.go`、`service/work_item_relation_delivery.go`、`service/work_item_relation_outcome_delivery.go`、`service/problem_resolved_delivery.go`、`internal/bootstrap/app.go`。
- Test: `authorization/tenant_session_test.go`、`tests/integration/workitem_relation_events_postgres_test.go`。
- Create: `service/work_item_delivery_authorization.go`、`service/work_item_delivery_authorization_test.go`（三消费者共同使用的实际查询/授权能力，不是新权限引擎）。

**Interfaces**
消费者构造器显式接收已有 `database.DirectorySnapshot`；WorkItem 读写仍使用 Tenant client。新增共用函数 `authorizeWorkItemDelivery(ctx context.Context, tx *ent.Tx, directory database.DirectorySnapshot, actorID, tenantID int, endpointIDs []int) (*ent.User, error)`：查询并校验当前 actor 和所有端点，返回当前 actor 或保留 cause 的错误；接收人资格仍在发送前按现有通知授权规则检查。按 `ResolveLifecycleActor`、`RequireCurrentPermission`、`ResolveWorkItemIdentity` 的真实签名组合，不把管理员 client 作为业务 client 注入。

- [x] 将原 MSP 对照测试的 handler/notifier 换成实际 runtime.Tenant 注入，保留 fixture admin 仅做建数与独立断言；运行 `go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemRelationEvents' -count=1 -v`，记录合法提供方人员被 blocked 的旧反例。
- [x] 给身份查询注入暂时错误/超时，证明不得被归类 forbidden/blocked。当前 ResolveLifecycleActor 最后会将查询错误统一映射 forbidden；修改为 NotFound、inactive、确定性 tenant denial 才拒绝，基础设施错误保留 cause。修改同时回归三域命令，防止权限放宽。
- [x] 三消费者在一致快照内使用窄 directory 身份查询，检查当前 tenant/MSP allocation、域读取权限、行可见性和目标接收资格；客户业务记录查询始终 tenant-scoped。receiver/actor 撤权的测试必须与临时错误测试分开。
- [x] 已证实不可见/无权限→blocked；基础设施失败→返回带 cause 的 error，由现有 worker MarkRetry；未知错误不伪造确定性拒绝。审计事件结构无效仍 blocked。
- [x] 正文取得 MutationWorkItemID 对应记录用于描述，发送目标仍是 counterpart。为纯正文函数新增直接断言：正文包含变化端编号，不把收件端编号当变化端；覆盖新增/解除关系及结果事件。
- [x] 对相同 DeliveryKey 重放验证通知数量不增；对发后回执失败验证既有通知幂等，不建立新去重表。
- [x] Run `go test ./authorization -count=1`、`go test ./service -run 'Test.*(Relation|Delivery|Outbox)' -count=1 -v`、上述实际 RLS 集成测试；运行三域受影响授权测试。
- [x] 提交：`fix(workitem): authorize relation delivery under runtime RLS`。

## 后端交付检查

- [ ] 更新受影响 API 文档与调用方；B1–B3 变更不能只落服务端而留下旧请求格式。
- [ ] `go build ./...`；执行以上定向检查及总入口的已知失败归因。
- [ ] `git diff --check`；独立审查或维护者复核权限、事务和旧路径删除清单。
- [ ] 提供各任务提交、实际执行测试名称、环境与未通过项；本计划勾选不替代证据。

## B1 执行记录（2026-09-11）

实施基线 `3064ea5e` 包含原业务实现 `46606330` 与全部接受的设计；隔离分支 `codex/refactor/workitem-next-stage`。原工作树及证据未改动。实时远端 main 与本地 origin/main 均为 `a25e108d2a08a55469fa5ad547aac5a9adc251ff`，是实施基线祖先。

| 入口 | 权威行为与被移除写路径 | 验证 |
|---|---|---|
| Incident HTTP assign | 专业 ID + version/operationId/reason → ApplyIncidentCommand；删除旧 AssignIncident 服务事务 | 非等值身份 HTTP、400 非法目标、409 陈旧/改原因、重放、撤权 |
| BPMN assign_incident | 持久化白名单保留 reason，冻结观察版本，回调调用同一命令 | 实际启动/持久化回调首次分配与转派及重放 |
| AssignmentAction | 可信 actor/source/operation identity + 观察版本，复用外层事务命令核心 | 进度保持、一次审计/事件 |
| IncidentEscalationService | 显式 Meta，升级/必要转派/通知接收共用 RR 事务；无目标资格或通知接收失败回滚 | 合法状态、冻结候选、重放、撤权、通知失败重试、跨 WorkItem SLA 违规不触发 |
| Incident 普通编辑 | 显式拒绝 AssigneeID；无旁路负责人写入 | 版本与审计不变 |
| TicketService | assign/batch/update-owner/escalate/MSP assign 拒绝 Incident | 真实服务边界反例与修复 |
| TicketAssignmentService / Smart | 策略、转派、批量、自动分配拒绝 Incident；混合批次先完整检查 | 其他类保留行为、混合批次零写入 |
| TicketWorkflow/Lifecycle / BPMN ticket assign | 接单/所有权移交/升级和通用流程分派拒绝 Incident | 真实服务/持久化处理器边界测试 |
| 三个 Incident 前端调用点 | API 同步观察版本、稳定操作键及转派原因，不取服务器新版本代替用户观察 | 类型检查、38 个 API 测试、改动文件 ESLint |

行为 RED 记录包括：进度转派被拒、缺原因仍成功、普通编辑/规则/通用 Ticket/MSP 绕过、BPMN 丢失原因、未知状态分配、非法目标 HTTP 500、无可信升级 actor，以及其他 WorkItem SLA 违规触发升级。已分别修复；新增状态范围采用明确白名单，保留首次响应事实。

验证：service 全包通过；controller 与 service/bpmn 全包通过；最终 Incident/Ticket assignment 定向回归通过；新增 SLA 范围及原升级事务测试通过。隔离 PostgreSQL `127.0.0.1:36444/sslvpn_test` 三个实际子场景验证并发竞争/同键重放/审计失败回滚及撤权，所有临时 schema 已清理（remaining=0）。这些 PG 结果不代替 B5 的生产权限角色 RLS 验证。前端类型检查、38 个 API 测试（定向 coverage=false）及改动文件 ESLint 通过。

独立审查发现并关闭 BPMN reason、通用 Ticket 旁路及 MSP 旁路；第二次静态复核未发现其他确定性 P1/P2。原 SQLite 并发钩子在新 RR 事务内造成锁竞争，已改为保留旧观察版本的陈旧快照测试；真实并发由隔离 PostgreSQL 双请求测试承担。冻结流程旧 fixture 同步补 version 和合法 Incident 权限，没有放宽生产授权。

证据保存在实施工作树已忽略的 `.superpowers/sdd/workitem-next-stage/`。测试期间的一次全 service 运行命中升级通知 fixture 配置中间态，修正后全包通过；定向 Jest 首次误用全仓库覆盖门槛，38 测试本身通过，随后以 coverage=false 重跑通过。未推送、合并或部署。

F3 必须继续处理 SLA 违规的周期归属：SLAViolation 只有 ticket_id、违规时间和解决标记；现有重开周期不清理旧违规。本批只修正 WorkItem 归属，不能宣称周期隔离完成。B2/B3 尚须把同一通用写入口保护扩展至 Change/Problem。


## B5 执行记录（2026-09-11）

三个通知消费者复用窄 DirectorySnapshot 获取当前身份，业务记录、权限和通知继续使用 Tenant client。确定性撤权进入 blocked；数据库/超时保留错误原因，沿用现有 worker 重试。通知描述变化端编号，接收关联端；现有 DeliveryKey 保持唯一去重来源。

真实 runtime PostgreSQL 角色确认 super=false、bypass=false。旧关系事件、Change 结果、Problem 解决通知和新授权套件合跑通过（50.856s）；新增 15 个场景最终通过（21.689s），覆盖角色/读取权限/租户分配/接收者撤权、目录和端点暂时失败恢复、通知发送后回执失败重放仅一条。所有自建 schema 和角色清理 remaining=0。authorization 全包、关系/Delivery/Outbox 定向及三域授权回归通过，bootstrap 编译通过。

独立审阅确认实际断言通知行、接收人、正文及重放数量，无新增确定性 P1/P2。授权快照与通知事务仍按现有设计分开；未扩大跨租户接收人资格。证据为 `.superpowers/sdd/workitem-next-stage/workitem-b5-pg-green.log`，实际共享环境未操作。
