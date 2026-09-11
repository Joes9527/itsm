# WorkItem Runtime Convergence Implementation Plan

> 2026-09-11 后续任务入口：[后续实施总计划](2026-09-11-workitem-next-stage.md)。保留本文件原任务及证据；剩余工作按新计划与已确认设计执行，不重做已有成果。
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> 状态：draft；依赖子计划 A、B；执行顺序 C1 → C2 → C3。

**Goal:** 统一流程身份，完成真实路径验收和无双轨的部署删除门禁。

**Architecture:** 复用 BPMN structured identity、回调 outbox、既有部署工具。历史记录不迁移；有旧契约执行依赖就阻断切换。

**Tech Stack:** Go/Ent/PostgreSQL、Next.js、Jest、Playwright、现有部署脚本。

**Spec:** [设计](../specs/2026-09-09-workitem-convergence-design.md)；[总计划](2026-09-09-workitem-convergence.md)。

## Global Constraints

- 不转换旧运行/历史实例，不允许在新引擎解释旧身份；Release 显式保留，不误映射 Change。
- 不自动取消流程或删除数据；旧依赖未排空，切换必须失败。
- 原 createdAt 不改写；SLA 周期 reset 来自 A1；此处不另建计时实现。
- 旧结构删除在完整验收与观察之后；没有观察证据不能宣布交付完成。

## C1：BPMN 规范身份和只读切换预检

**Files**
- Modify: `itsm-backend/dto/bpmn_process_trigger_dto.go`、`ent/schema/process_instance.go`；`service/bpmn_process_trigger_service.go`、`bpmn_callback_security.go`；`handlers/shared/workflowcallback/contract.go`；`service/bpmn/publication_contract.go`；`service/workflow_start_outbox.go`。
- Modify: 以上 identity 常量的全部生产引用及既有流程定义/绑定 seed；不新增兼容别名。
- Create: `itsm-backend/dto/workitem_process_identity_test.go`；`cmd/check_workitem_cutover/main.go`；`tests/integration/workitem_cutover_postgres_test.go`。
- Create: `itsm-backend/migrations/20260910_workitem_process_contract.sql`（只调整新契约结构/约束，不更新历史实例）。

**Interfaces**

```go
func WorkItemBusinessKey(recordClass string, id int) (string, error) {
    if id <= 0 { return "", errors.New("positive work item ID required") }
    switch recordClass {
    case "generic", "incident", "problem", "change_request", "service_request_item", "catalog_task":
        return fmt.Sprintf("%s:%d", recordClass, id), nil
    default:
        return "", errors.New("unsupported work item class")
    }
}
```

这是身份验证，允许按类分发，不是专业状态机。Release 使用既有显式 legacy 身份校验，不通过该函数混入 WorkItem；catalog_task 有合法身份不表示创建能力已实现，创建仍按 registry 失败关闭。

- [ ] **RED：**

```go
func TestWorkItemBusinessKey(t *testing.T) {
    got, err := WorkItemBusinessKey("change_request", 42)
    if err != nil || got != "change_request:42" { t.Fatalf("got %q, %v", got, err) }
    for _, old := range []string{"ticket", "change", "service_request", "unknown"} {
        if _, err := WorkItemBusinessKey(old, 42); err == nil { t.Fatalf("legacy class accepted: %s", old) }
    }
}
```

- [ ] Run: `go test ./dto -run '^TestWorkItemBusinessKey$' -count=1`。
- [ ] **实现：** trigger、binding matcher、publication validation、callbacks、approval snapshot、reserved variables 一起使用规范 recordClass；专业 ID 在入口解析成 WorkItem ID。删除旧常量与运行时双解释；旧词表出现在历史检测器内是合法的，不能为了文本零匹配删除预检。
- [ ] 新只读预检命令使用既有配置/凭据读取方式，默认事务只读；检查旧类型实例、专业 ID 无法与 WorkItem 一致的实例、运行/暂停旧实例、待执行旧回调及旧 binding 配置。输出只包含计数、非敏感 ID 与阻塞原因；exit 0 表示可切换，exit 2 表示有依赖阻塞。不得取消、更新、删除或自动重新触发旧实例。
- [ ] incident/problem 名称未变也必须验证 business_id 指向正确 WorkItem/专业扩展，不仅按字符串排空；无证明的实例阻断。完整历史数据不迁移，也不由新查询引入 fallback。
- [ ] **PG：** 旧 running/suspended 或 pending callback 使预检 exit 2 且数据库前后摘要不变；规范新记录 exit 0；reserved variable 覆盖拒绝；process task 和 approval snapshot 与实例身份一致；Release 现有测试通过。
- [ ] Run: `go test ./dto ./service/bpmn -count=1`；`go test ./service -run 'Test.*ProcessTrigger|Test.*Callback|Test.*WorkflowStart' -count=1`；`go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemCutover' -count=1`。
- [ ] 独立复核并提交 `refactor(bpmn): enforce canonical work item identity at every boundary`。

## C2：统一编号、动作前提和完整前端路径

**Files**
- Modify: `itsm-frontend/src/app/(main)/problems/[id]/page.tsx`、`src/components/work-item/WorkItemTypes.ts`；`src/lib/api/problem-api.ts`、`incident-api.ts`、`change-api.ts`。
- Modify: `itsm-backend/handlers/problem/entity.go`、`repository_impl.go`；`handlers/change/entity.go`、`repository_impl.go`；公共详情动作投影的实际调用者。
- Create: `itsm-frontend/src/components/work-item/__tests__/convergence-contract.test.ts`；`tests/e2e/business-flows/workitem-convergence.spec.ts`。

**Interfaces**
专业详情 DTO 明确返回 workItemId、number、version；number 从关联 Ticket.TicketNumber 取得。前端动作提交 expectedVersion 和操作键，actor/tenant 不从表单决定。SLA 展示 A1 current cycle 与已有历史事实投影，不创建第二个页面计时器。

- [ ] **RED：** 相邻 contract 测试先写真实映射断言：

```ts
it('keeps professional and work item identity separate', () => {
  const dto = { id: 4, workItemId: 91, number: 'PRB-0091', version: 7 };
  const common = mapProblemToWorkItem(dto);
  expect(common).toEqual({ id: 91, number: 'PRB-0091', version: 7 });
});
```

测试导入 `@/app/(main)/problems/[id]/work-item-mapper` 的 mapProblemToWorkItem；页面必须使用同一个函数。该新模块导出以下最小身份映射，原页面其他字段在其后保留，不能改用专业 ID 兜底：

```ts
export function mapProblemToWorkItem(dto: { workItemId: number; number: string; version: number }) {
  if (dto.workItemId <= 0 || !dto.number || dto.version <= 0) throw new Error("Invalid WorkItem identity");
  return { id: dto.workItemId, number: dto.number, version: dto.version };
}
```

增加真实页面渲染断言，确保旧 #problem.id 行为失败，避免只测未使用的辅助函数。

- [ ] Run: `npm test -- --runInBand --runTestsByPath src/components/work-item/__tests__/convergence-contract.test.ts`，确认旧编号渲染失败后切换后端 DTO 和页面映射。
- [ ] 完成按钮显示后端缺失前提：无恢复说明、缺永久方案验证、PIR 未完成、陈旧版本均可区分；409 保留用户未提交内容并要求刷新确认，不能自动覆盖重试。关联按钮明确“创建关联问题/变更”。
- [ ] **E2E：** 使用 Playwright business-flows 现有登录 fixture；只在隔离测试环境创建唯一前缀数据。记录 Incident ID 与 WorkItem ID 不相等；恢复后解决 Incident；创建/关联 Problem；只有 workaround 时 resolve 失败；Change 失败不解决 Problem；成功后仍需验证；验证通过才解决；重开后当前 SLA 归零、旧周期结果保留；同键重复提交不重复重开。断言 API 持久化结果，不只看 toast。
- [ ] 含 generic/Requested Item 创建、评论、附件、任务审批回归；无权用户不能靠直接 API 绕过隐藏按钮。每个新 mutating 测试以已授权隔离账号执行，不操作现有 WMS 草稿。
- [ ] Run: `npm run type-check`；`npm run lint:check`；`npm run build`；`npx playwright test tests/e2e/business-flows/workitem-convergence.spec.ts --project=business-flows`。
- [ ] 独立复核并提交 `refactor(frontend): project authoritative work item actions and identity`。

## C3：结构切换演练、观察和删除门禁

**Files**
- Create: `docs/deployment/workitem-convergence-cutover.md`（固定步骤、需执行时记录的环境事实、恢复门禁）。
- Create: `itsm-backend/tests/integration/workitem_retirement_postgres_test.go`。
- Modify: A1/A3/B1/C1 正式结构脚本，现有 migration registry（按项目实际注册入口）；`docs/README.md`、`AGENTS.md`、`CLAUDE.md` 同步已实现摘要。

**Interfaces**
复用 C1 `check_workitem_cutover` 的 exit 0/2 契约。所有环境命令由现有 docs/DEVELOPMENT_GUIDE.md 和维护的部署方式提供；不能为本任务另建常驻部署管理器。

- [ ] **RED：** 在隔离 PG 建旧表和至少一条记录，测试：备份未验证或 C1 有阻塞时删除步骤不执行；new app 不读取旧表；历史记录不发生 UPDATE/INSERT SELECT 回填。使用事务只读角色运行预检并比较计数/摘要。
- [ ] Run: `go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemRetirement' -count=1`。必须看到门禁违规被测试拒绝，不能先直接 DROP 再检查。
- [ ] 编写 runbook：确认环境/数据库/schema/应用版本与备份恢复；排空旧依赖；暂停相关写入和消费者；应用新 schema；核对保留记录计数及约束；切换唯一新读写；C2 完整验收；恢复新路径写入和观察；再次核对备份、消费者、审查证据；最后删除旧结构并健康检查。观察以三域真实动作、SLA 重开、回调重试场景全部通过且无未解释错误为退出条件。
- [ ] 删除脚本只针对经过清单核对的废弃表/列，显式 schema，不用通配符或 CASCADE 自动扩散。缺少备份记录、未清消费者、未通过观察任一条件阻断；本计划不替代实际环境变更批准。
- [ ] **历史豁免验证：** 旧 Problem 不补证据、不自动重开；旧 SLA 不重算；旧关系不搬运；旧流程不取消。若新约束无法安装而需要改写历史，报告阻塞并重新确认范围，不能以迁移脚本偷偷清洗。
- [ ] **恢复演练：** 删除前后分别测试协调恢复 DB+应用版本；发生新写入后不只回滚 binary。备份恢复后的新数据补偿必须有清单，不能声称零损失。演练只在 disposable PG，实际共享库执行由单个协调任务进行。
- [ ] Run: `go test -tags=integration_postgres ./tests/integration -run '^TestWorkItem(SLACycle|IncidentLifecycle|ProblemLifecycle|ChangeLifecycle|Relations|RelationEvents|Cutover|Retirement)' -count=1`；执行 C2 E2E；`git diff --check`。
- [ ] 同步设计状态仅对具备证据的已完成范围标记 implemented，删除旧入口文档并更新 docs 索引；提交 `test(workitem): verify cutover and old structure retirement gates`。

## 最终交付证据

- [ ] 每批 PR 有独立审查、实际测试输出、历史豁免声明、旧路径删除清单和风险。
- [ ] 执行记录包含实际 baseline、依赖合并顺序、运行镜像/二进制版本；不能用 main commit 冒充已部署版本。
- [ ] 若只完成文档或代码未部署，报告对应状态，不宣称运行环境缺口全部消失。


## 2026-09-11 后续执行记录

原 C1 实现保留，后续 B4 修复真实 seed/保留变量边界，原 C1 11 项隔离 PG 回归通过。C2 由 F1–F3/V1 实施，真实三域 API 矩阵已验证，完整浏览器旅程仍在执行；不能将本记录当作 C2 全部完成。

C3/V2 已建立自动迁移的旧结构拒绝及真实 CLI exit2/0 只读验证，并在独立数据库完成 schema 备份恢复、原 WorkItem 内容/时间一致和备份后新写入不在恢复结果中的补偿验证。022 的历史 CASCADE 不能直接当作准入删除方案；历史 checksum 保留，允许退役路径待单独决策。实际部署、观察和旧结构删除均未执行。具体范围、顺序和未完成项见 [切换与恢复手册](../../deployment/workitem-convergence-cutover.md)。
