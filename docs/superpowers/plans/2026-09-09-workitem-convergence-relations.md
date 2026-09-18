# WorkItem Relations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> 状态：draft；依赖子计划 A；执行顺序 B1 → B2。

**Goal:** 移除旧关联读写，通过 WorkItemRelation 和既有事件机制实现跨域协作。

**Architecture:** 关系服务只拥有关系语义与事务，不能执行三个专业状态机。目标创建继续由现有 intake/专业 creator 事务完成；关系结果通过既有 Outbox 发出。

**Tech Stack:** Go/Gin/Ent/PostgreSQL、TypeScript/Jest。

**Spec:** [设计](../specs/2026-09-09-workitem-convergence-design.md)；[总计划契约](2026-09-09-workitem-convergence.md)。

## Global Constraints

- 历史业务关系不迁移、不合并查询、不猜测因果。
- 新关系只有 WorkItemRelation 一个权威来源，actor/tenant 从可信上下文取得。
- 默认通知或验证提示；自动推进只经显式配置和目标专业服务。
- 旧结构在观察验收之后删除，不能借观察继续旧应用读写。

## B1：统一关联已有记录与创建目标

**Files**
- Create: `itsm-backend/service/work_item_relation_service.go`、`work_item_relation_service_test.go`。
- Modify: `itsm-backend/common/work_item_relation.go`、`ent/schema/work_item_relation.go`；`handlers/problem/creation.go`、`repository_impl.go`、`repository.go`；`handlers/change/creation.go`、`repository_impl.go`；`handlers/common/workitemcreation/command.go`；`router/router.go`。
- Modify: `itsm-backend/ent/schema/problem.go`、`incident.go`、`change.go` 的旧关系 edges，生成对应 Ent 文件。
- Create: `itsm-backend/tests/integration/workitem_relations_postgres_test.go`；`migrations/20260910_workitem_relation_retirement.sql`（删除脚本在 C3 门禁后执行）。
- Modify: `itsm-frontend/src/lib/api/problem-api.ts`、`change-api.ts` 和关联组件；新增 `src/lib/api/__tests__/workitem-relations.test.ts`。

**Interfaces**

```go
type RelationCommand struct {
    Meta workitemmutation.Meta
    SourceID int
    TargetID int
    Type string
}
func ValidateRelationClasses(kind, source, target string) error {
    allowed := kind == "related_to" ||
        (kind == "investigated_by" && source == "incident" && target == "problem") ||
        (kind == "resolved_by_change" && (source == "incident" || source == "problem") && target == "change_request")
    if !allowed { return errors.New("unsupported relation classes") }
    return nil
}
func (s *WorkItemRelationService) AddTx(ctx context.Context, tx *ent.Tx, cmd RelationCommand) error
func (s *WorkItemRelationService) RemoveTx(ctx context.Context, tx *ent.Tx, cmd RelationCommand) error
```

SourceID/TargetID 都是 WorkItem ID；类来自已授权实体而不是客户端。上述 helper 只展示本批三类，生产入口先验证所有类来自既有受约束注册表；不得因 related_to 接受未知类。其余既有关系类型保留其原有显式规则，不用默认 true 吞掉。

- [ ] **RED：** 在同包测试加入：

```go
func TestInvestigatedByDirection(t *testing.T) {
    if err := ValidateRelationClasses("investigated_by", "incident", "problem"); err != nil { t.Fatal(err) }
    if ValidateRelationClasses("investigated_by", "problem", "incident") == nil { t.Fatal("reversed relation accepted") }
    if ValidateRelationClasses("unknown", "incident", "problem") == nil { t.Fatal("unknown relation accepted") }
}
```

- [ ] Run: `go test ./service -run '^TestInvestigatedByDirection$' -count=1`。
- [ ] **实现：** AddTx 查询双方权威 Ticket，验证 tenant、deletedAt、read scope、源记录关系操作权限与 Meta.ExpectedVersion；禁止自关联、重复活跃 tuple；investigated_by 遵循现有未软删除部分唯一索引。关联与解除都在同一事务写关系、源 WorkItem version、审计及 Outbox，解除采用软删除。
- [ ] 关联目标创建继续从现有 ProfessionalCreator 的 Prepare/CreateExtension 事务完成，新增 SourceWorkItemID/RelationType 的受信任内部计划值（HTTP 原始参数先授权），不能控制器先 Create 再 Add 两个事务。Incident→Problem 保留原记录和历史。Problem/Incident→Change 经 Change creator 创建；同租户约束、版本与 relation failure 整体回滚。
- [ ] 删除 Problem AddIncidentIDs/AddChangeIDs 与 WithIncidents/WithChanges 合并查询；Change 普通关联统一从 WorkItemRelation 读取。专业 ID 只在公开领域 API 边界解析一次，内部不混用数字同值。删除旧 edges 的代码生成引用；历史表数据不回填，物理 DROP 在 C3 验收后执行。
- [ ] **PG：** 同一源/不同 target 竞争 investigated_by，只有一个成功；跨租户 target 拒绝；创建目标后注入关系/审计错误，Ticket 和扩展计数不增加；解除再关联成功；历史旧关联只存在旧表时新 API 不读出；没有历史 UPDATE/INSERT SELECT 回填。
- [ ] Run: `go generate ./ent`；`go test ./service ./handlers/problem ./handlers/change -run 'Test.*Relation|Test.*Association|Test.*Creation' -count=1`；`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemRelations' -count=1`。前端 `npm test -- --runInBand --runTestsByPath src/lib/api/__tests__/workitem-relations.test.ts`。
- [ ] 校验三域关联页面显示关系类型与权威编号，创建入口写“创建关联问题/变更”，不暗示改变源 recordClass。
- [ ] 独立复核并提交 `refactor(workitem): consolidate cross-domain relation ownership`。

## B2：结果通知、验证提示和显式自动推进

**Files**
- Modify: `itsm-backend/service/outbox_event_type_registry.go`、`outbox_delivery_worker.go`、`outbox_event_repository.go`；`service/bpmn/bpmn_callback_registry.go`；三个域的 A 任务命令文件。
- Create: `itsm-backend/service/work_item_relation_events.go`、`work_item_relation_events_test.go`；`tests/integration/workitem_relation_events_postgres_test.go`。
- Modify: `itsm-frontend/src/components/work-item/WorkItemTypes.ts` 及真实时间线/动作投影消费者。

**Interfaces**

```go
type RelationEvent struct {
    EventID string
    TenantID int
    SourceWorkItemID int
    SourceVersion int
    Outcome string
}
func RequiresProblemVerification(outcome string) bool { return outcome == "successful" }
```

事件由领域事务写入既有 Outbox；consumer 经现有 registry 注册，幂等键由 EventID+目标 WorkItemID+动作组成。事件消费者不是可绕过授权的第二套自动化引擎。

- [ ] **RED：**

```go
func TestChangeOutcomeRequestsVerification(t *testing.T) {
    if !RequiresProblemVerification("successful") { t.Fatal("missing validation prompt") }
    for _, outcome := range []string{"failed", "rolled_back", "closed"} {
        if RequiresProblemVerification(outcome) { t.Fatalf("%s treated as repair", outcome) }
    }
}
```

- [ ] Run: `go test ./service -run '^TestChangeOutcomeRequestsVerification$' -count=1`。
- [ ] **实现：** 成功 Change 仅对 resolved_by_change 来源生成验证提示，Problem resolve 对 investigated_by Incident 通知处理人；related_to 不推导自动动作。模板、通知渠道和规则使用现有配置，不创建硬编码通知人员。
- [ ] 仅当显式 BPMN 定义自动推进时调用 A 域命令；每目标重新取可信服务 actor 授权和版本。未知类型阻断，已经终态/缺证据/冲突返回可见单条结果，不能转换成全部成功。重放使用同一操作键，相同键不同载荷拒绝。
- [ ] 验证 Problem 对必需修复 Change 的前置读取通过统一关系服务的查询接口，不跨域直接调用 repository；只有标记为必需的修复依赖阻塞，不把所有 related_to 当必需。必需性保存在既有 relation.metadata 的 typed DTO 字段，禁止在另一个 JSON 列再存关系 ID。
- [ ] **PG：** 重复投递只有一个提示/动作；成功 Change+失败验证保持 Problem 未解决；Problem resolve 不批量关闭 Incident；通知失败 Outbox 重试但业务不回滚；关联权限已撤销时消费者拒绝；部分目标冲突不重复处理已成功目标。
- [ ] Run: `go test ./service -run 'TestChangeOutcomeRequestsVerification|Test.*Outbox' -count=1`；`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemRelationEvents' -count=1`。
- [ ] 独立复核并提交 `refactor(workitem): deliver auditable relation outcomes through existing outbox`。

## B 批交付门禁

- [ ] 查询新旧关系的消费者清单逐项归零；不创建兼容 union 或历史 fallback。
- [ ] 创建目标+关系同事务、单条重放幂等和两端租户授权有真实 PG 证据。
- [ ] 旧结构仅等待 C3 定义的同批部署观察后删除，不再参与任何应用读写。
