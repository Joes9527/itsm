# WorkItem Experience and Validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 使公共详情和专业动作完整使用权威身份与版本，并完成跨域旅程和切换验收。

**Architecture:** 复用 WorkItem Shell、专业 Panel、现有 API 模块和 SLA 持久化事实。隔离环境验证事务/RLS与浏览器行为；运行退役沿用原 C3，不创建部署管理系统。

**Tech Stack:** Next.js、TypeScript、Jest、Playwright、Go、PostgreSQL。

> 状态：accepted（已按独立审查修订；任务未执行）
> 依据：[后续设计](../specs/2026-09-11-workitem-convergence-next-stage-design.md)；依赖[后端计划](2026-09-11-workitem-next-stage-backend.md)。前端命令从 itsm-frontend 执行。

## Global Constraints

- 统一观察版本与 operationId 语义；Incident/Problem HTTP 使用 version，Change 使用 expectedVersion，由各 API 适配器转换，禁止双别名。专业 ID 不作 WorkItem ID 或编号兜底。
- 转派原因必填，实际使用 B1–B3 的专业 API；后端决定可用动作与前提。
- 保留输入并显式处理冲突，不静默刷新版本重试。
- 不改响应考核，不新增多 WorkOrder；SLA 仍需当前周期和历史事实。
- E2E 使用隔离测试数据和账号，不操作现有 WMS 草稿或共享运行环境。

## F1：权威详情和请求契约

**Files**
- Modify: `src/components/work-item/WorkItemTypes.ts`、`src/lib/api/incident-api.ts`、`src/lib/api/problem-api.ts`、`src/lib/api/change-api.ts`。
- Modify: `src/app/(main)/incidents/[id]/page.tsx`、`src/app/(main)/problems/[id]/page.tsx`、`src/app/(main)/changes/[id]/page.tsx`。
- Create: `src/components/work-item/identity.ts`、`src/components/work-item/__tests__/identity.test.ts`。
- Backend modify: `dto/problem_dto.go`、`handlers/problem/handler.go`、`handlers/problem/repository_impl.go`；其余域现有详情映射只在缺少契约时修改。

**Interfaces**
WorkItemCommon 增加 `version: number`。从专业 DTO 投影使用同一个无专业逻辑的身份函数：

```ts
export function workItemIdentity(dto: { workItemId: number; number: string; version: number }) {
  if (!Number.isInteger(dto.workItemId) || dto.workItemId <= 0 ||
      !dto.number?.trim() || !Number.isInteger(dto.version) || dto.version <= 0) {
    throw new Error('Invalid WorkItem identity');
  }
  return { id: dto.workItemId, number: dto.number, version: dto.version };
}
```

仅统一新前端消费的权威 number 字段，后端明确从关联 TicketNumber 读取，不从专业 ID 拼装。现有项目响应包裹和错误格式不变。

- [ ] 写入并运行失败测试：

```ts
import { workItemIdentity } from '../identity';
test('preserves work item identity and observed version', () => {
  const dto = { id: 4, workItemId: 91, number: 'PRB-0091', version: 7 };
  expect(workItemIdentity(dto)).toEqual({ id: 91, number: 'PRB-0091', version: 7 });
});
test('rejects missing identity instead of falling back', () => {
  expect(() => workItemIdentity({ workItemId: 0, number: '', version: 7 })).toThrow();
});
```

- [ ] Run `npm test -- --runInBand --coverage=false --reporters=default --runTestsByPath src/components/work-item/__tests__/identity.test.ts`。
- [ ] 实现上述函数；三个真实页面使用它，再组装其余公共字段。更新后端缺失的 number/workItemId/version 投影、前端 DTO 及 B1–B3 请求字段；删除被替代的编号/ID 兜底。
- [ ] 在相邻页面测试中 mock 返回专业 ID=4、WorkItem ID=91，断言真实编号 PRB-0091、公共评论/附件/SLA使用 WorkItem ID=91；专业动作路由仍使用专业 ID=4，观察版本为7，由后端解析至WorkItem91。另建专业记录91作为哨兵，断言动作不会误改它；不能要求专业动作 URL也使用91。
- [ ] Run 定向测试及 `npm run type-check`；提交 `refactor(workitem): project authoritative identity and version`。

## F2：公共转派交互和冲突

**Files**
- Modify: `src/components/work-item/WorkItemShell.tsx`、`src/components/work-item/WorkItemContext.tsx`、`src/components/incident/IncidentDetail.tsx`、`src/components/problem/ProblemDetail.tsx`、`src/components/change/ChangeDetail.tsx`、三域详情 page.tsx。
- Create: `src/components/work-item/WorkItemAssignment.tsx`、`src/components/work-item/__tests__/WorkItemAssignment.test.tsx`。
- Modify: `src/components/work-item/__tests__/WorkItemShell.test.tsx`。

**Interfaces**
公共组件只收集输入和呈现状态，submit 回调由专业页面调用对应 API，不能在组件内 switch recordClass 重做业务规则。

```ts
export type AssignmentInput = {
  assigneeId: number;
  reason: string;
  version: number;
  operationId: string;
};
export type AssignmentProps = {
  currentAssigneeId?: number;
  version: number;
  allowed: boolean;
  disabledReason?: string;
  candidates: Array<{ id: number; label: string }>;
  submit: (input: AssignmentInput) => Promise<void>;
};
```

候选人员沿用现有授权目录查询，不硬编码角色、不自行扩大 MSP 候选资格。Change/Problem 回调把 reason 映射为 assignmentReason；Incident 使用 reason。Change 把组件 version 映射为 expectedVersion 并移除 version；Incident/Problem保留version。专业字段之外的请求格式差异只在 API 适配处处理。

- [ ] 组件测试覆盖：已有负责人时空白原因禁用提交；选择新负责人并填写原因后只提交一次；disabledReason 可见；后端409后输入仍在且不自动再次 submit。模拟 submit Promise reject 后断言调用次数为1、原因文本仍存在。
- [ ] Run `npm test -- --runInBand --coverage=false --reporters=default --runTestsByPath src/components/work-item/__tests__/WorkItemAssignment.test.tsx`，确认未实现行为失败。
- [ ] 实现交互：打开时保留观察版本；一次逻辑提交生成一次 operationId，网络不确定重试使用原键。收到409后保留原因和目标，用户刷新确认后创建新请求。成功刷新权威详情和时间线，不本地推导新状态。
- [ ] 用 API 请求测试冻结路由与载荷：Incident `/incidents/4/assign` 请求含 version=7，Change现有专业路由含 expectedVersion=7且不含version，Problem现有更新路由含version=7；三者均使用专业ID4而非WorkItem91，并传入本域原因字段。后端严格 binder 测试同时验证真实请求被接受。
- [ ] 三域接入公共组件并删除原重复转派 modal/提交逻辑。保留专业动作与后端 action reason；原页面若缺少转派入口，由公共组件承接，不新增平行专业详情页面。
- [ ] 核查 WorkItemComments/Attachments/History 的专业 Panel 重复入口，存在实际重复才移除；断言仍使用 WorkItem ID及原内部/公开可见规则。
- [ ] Run 新组件与 Shell 测试、`npm run type-check`、`npm run lint:check`；提交 `refactor(workitem): unify reassignment and conflict experience`。

## F3：SLA 当前周期与历史展示

**Files**
- Modify: `src/components/work-item/WorkItemTypes.ts`、`src/components/work-item/WorkItemSLA.tsx`、`src/components/work-item/__tests__/WorkItemSLA.test.tsx`、三域 API/详情映射。
- Backend inspect/modify: `service/ticket_sla_cycle.go`、`service/ticket_sla_service.go`、`dto/ticket_dto.go`、`controller/ticket_controller.go` 及其现有调用方。
- Backend test: `service/ticket_sla_cycle_test.go`、`tests/integration/workitem_sla_cycle_postgres_test.go`（现有套件，追加所需断言）。

**Interfaces**
复用已有 GET /tickets/:id/sla、TicketSLAInfoResult 与 dto.TicketSLAInfo：后端已经返回 cycleNumber、cycleStartedAt、pausedMinutes、appliedPolicy、history。WorkItemSLAState 同步这些字段，history 元素按 dto.SLACycleResult 映射 number、startedAt、endedAt、responseAt、resolvedAt、两个 deadline、pausedMinutes、两个 breached、policy、actorId、source、correlationId；时间为 ISO 字符串或对应 null。不得另建历史查询或存储。无需 SLA 与策略缺失错误要可区分。

- [ ] 复用现有 cycle fixture，构造旧周期违约、合法重开后新周期未完成；断言详情同时返回旧结果和新周期，createdAt 未改变。再构造原日历版本不可用，断言重开失败且状态/周期/版本未改变。
- [ ] Run 后端 cycle 定向测试及现有 PostgreSQL WorkItemSLACycle 套件，确认缺投影或语义回归的具体失败；已有正确计时逻辑不重写。
- [ ] 将已保存周期事实投影到详情；页面展示历史周期及当前周期，已完成周期使用完成时间判断结果。保持查询只读，不查询时二次匹配策略，不给 start/assign 补 FirstResponseAt。
- [ ] 修改 WorkItemSLA 组件测试，断言两个周期结果同时可见、无需 SLA 可见、配置缺失不显示正常达标；执行 `npm test -- --runInBand --coverage=false --reporters=default --runTestsByPath src/components/work-item/__tests__/WorkItemSLA.test.tsx`。
- [ ] Run `npm run type-check`、`npm run build`；提交 `refactor(workitem): expose current and historical SLA cycles`。

## V1：完整旅程与旧入口退出证据

**Files**
- Create: `tests/e2e/business-flows/workitem-convergence.spec.ts`（复用原 C2 计划的目标路径，不新建另一套旅程）。
- Backend modify tests: `tests/integration/workitem_assignment_postgres_test.go`、`tests/integration/workitem_relation_events_postgres_test.go`。
- Update: 原 runtime 计划 C2 状态及执行证据链接。

**Interfaces**
使用现有 Playwright business-flows 登录 fixture；沿用权威 API 返回的专业 ID、WorkItem ID、version、operationId，不硬编码 fixture 数值相等。测试必须断言 API持久化结果。

- [ ] 按原 runtime 计划 C2 的完整旅程建立 E2E：Incident workaround 恢复→关联 Problem→无永久验证拒绝解决→Change失败/回滚保持 Problem→Change成功仍需验证→验证后显式解决→合法重开与周期保留。
- [ ] 增加 B1–B3 转派矩阵：原因缺失被后端拒绝、三域保留各自事实、两浏览器版本冲突保留输入、同键不重复变更。每次写入唯一前缀测试数据，仅通过测试自建ID清理。
- [ ] 增加 generic/Requested Item 的创建、权限、分派、评论、附件和现有审批回归；直接 API 越权同样拒绝。
- [ ] Run `npx playwright test tests/e2e/business-flows/workitem-convergence.spec.ts --project=business-flows`；同时运行受影响 Go集成及前端类型/构建检查。记录实际测试数，浏览器截图不是持久化证据的替代。
- [ ] 归因修复三个已知后端失败，见总入口；核对入口清单每个被替换写路径已退出。保留历史检测器而非追求旧词表全库零匹配。
- [ ] 提交 `test(workitem): verify reassignment and cross-domain journeys`。

## V2：隔离切换、恢复与运行门禁

**Files**
- Create: `docs/deployment/workitem-convergence-cutover.md`、`itsm-backend/tests/integration/workitem_retirement_postgres_test.go`。
- Modify: `docs/superpowers/plans/2026-09-09-workitem-convergence-runtime.md` 的 C3 执行记录；原有正式 schema 脚本和 migration registry 只按实际缺口修改。

**Interfaces**
完整执行原 runtime 计划 C3，使用现有 `check_workitem_cutover` exit0/exit2 协议。原 C3 的备份、只读盘点、观察、恢复以及历史免迁移规则全部继承，不另造删除命令体系。

- [ ] 在 disposable PostgreSQL 构造旧依赖；运行只读预检，断言 exit2 和前后记录摘要相同。缺少备份或观察证据时，门禁不得执行删除。
- [ ] 编制明确表/列/约束及消费者清单，逐项验证已无运行读写；runbook 写入实际配置方式、版本、暂停范围和恢复步骤。不得用通配符或 CASCADE 扩大删除范围。
- [ ] 演练允许路径：备份恢复验证→新结构→唯一新路径→V1旅程→观察场景完成→清单删除→健康检查；另演练失败恢复，发生新写入后协调 DB与应用版本并列明补偿数据。
- [ ] Run `go test -tags=integration_postgres ./tests/integration -run '^TestWorkItem(Cutover|Retirement)' -count=1 -v`，并执行 V1；确认 PG连接为隔离目标，未向共享库执行迁移。
- [ ] 提交 `test(workitem): prove cutover and retirement gates`。
- [ ] 实际运行切换作为独立环境准入步骤记录；未经环境授权不执行。若仅代码/演练完成，明确保留实际部署与观察待办，不将整体状态标记 implemented。
