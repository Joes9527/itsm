# WorkItem 收敛：B 批尾段 + C1 交付与状态报告

- 文档日期：2026-09-11
- 分支：`codex/refactor/workitem-convergence-relations`（worktree `.worktrees/workitem-convergence-relations`）
- 本报告所描述的交付提交：`9dcb36f9`（C1 单提交）；本报告作为紧随其后的一个 `docs` 提交存在
- 推送状态：**未推送**（领先 `main` 51 个提交，含本报告）
- 用途：供对整个开发过程做 review 的入口文档。**包含未完成事项与已知问题**，不以"全部完成"的结论呈现。

> 说明：本报告聚焦本次会话实际经手并留下证据的范围（B 批尾段 / B 批交付门禁 / C1 全部）。
> B 批更早的 B1a、B1b 等部分由其他 agent 完成，本报告只在与本次交付有因果关系时提及，
> 不声称对其负责。

---

## 1. 结论速览（先看这一节）

| 项 | 状态 | 证据 |
|---|---|---|
| B 批（关系与联动） | ✅ 完成并签收 | B2 三轮独立评审收敛为 Ready to merge: Yes；B 批交付门禁 PASS |
| C1（规范流程身份） | ✅ 完成（单个计划提交） | `9dcb36f9`，95 文件 +1567/−232；squash 前后树哈希相同 |
| 后端构建 | ✅ | `go build ./...` = 0 |
| 后端全量测试 | ⚠️ 3 个失败，**均为预先存在基线** | 逐个在旧提交复现，详见 §7 |
| 预检整合测试 | ✅ 11/11 | 含新增"截断 fail-closed" |
| 前端相关套件 | ✅ approvals 12/12；改动涉及 5 套件 42/42；type-check 干净 | lint 0 error（1 个既有无关 warning） |
| 前端**全量** `test:ci` | ⚠️ 负载下 4 套件超时，**且缺基线全量对照** | 隔离运行全通过；见 §8.2 |
| 独立复核 | ✅ 已做（C1：Critical 0 / Important 5 → 全部修复） | 见 §6 |
| swagger / API 文档重生成 | ❌ **未做**（重生成会改写约 5000 行） | 见 §8.1 |
| C2 / C3 | ❌ 未开始 | 见 §8.5 |

**一句话**：后端与前端的功能性改动已完成且可验证；**文档生成产物、前端全量测试基线对照、以及 3 个既有基线失败**是明确遗留；C2/C3 尚未启动。

---

## 2. 本次交付范围与提交结构

```text
main (2fa93c1b)
  └─ … 49 个提交 …                                    ← B 批（含其他 agent 的工作）
       b06845f5  docs(workitem): correct the MSP delivery fixture comment   ← B 批签收点
         └─ 9dcb36f9  refactor(bpmn): enforce canonical work item identity at every boundary  ← C1（本报告主体）
```

- C1 采用**单个计划提交**落地（用户决定"squash 成计划单提交"）。
- squash 前的 9 个提交已折叠；**安全点保留**：`git tag c1-pre-squash` = `c3428012`（可随时回溯）。
- 内容安全的证明：squash 前后 `HEAD^{tree}` 均为 `e6c183c65d643fcf08d541777ebd61fb85a92a33`，**逐字节一致**，即 squash 只改历史、未改内容。

### C1 新增文件（生产代码）

| 文件 | 作用 |
|---|---|
| `itsm-backend/common/workitemidentity/identity.go` | 词表与业务键格式的**唯一权威**（stdlib-only 叶子包，避免 import cycle） |
| `itsm-backend/dto/workitem_process_identity.go` | 对上述叶子包的**诚实再导出**（常量别名 + 函数委托） |
| `itsm-backend/service/workitemcutover/check.go` | 只读切换预检（`Inspect`） |
| `itsm-backend/cmd/check_workitem_cutover/main.go` | 预检 CLI（exit 0/2/1） |
| `itsm-backend/dto/workitem_process_identity_test.go` | 身份原语测试（含**拒退役键**的负例） |
| `itsm-backend/tests/integration/workitem_cutover_postgres_test.go` | 预检整合测试（11 个，含只读性与截断 fail-closed） |

---

## 3. B 批（关系与联动）——已签收

### 3.1 交付要点

- **关系事件唯一权威 + 事务内产出**：关系创建/删除在被拥有命令的**同一事务**内写出 Outbox 事件，连同不可变审计回执；对称关系不产生投递事件。
- **有向关系才产生可观测效果**：`investigated_by`、`resolved_by_change` 两个有向关系驱动联动；对端接收人 = **对端 assignee → 回退 requester**。
- **幂等**：复用既有 `notifications.delivery_key` 唯一索引 `(tenant_id, delivery_key, user_id)`，**不新增迁移**。
- **变更结果 → 问题验证**：`change.outcome_recorded` 事件；`RequiresProblemVerification` 只对 `successful` 为真，否则记录**声明式跳过**审计（可见，非静默）。
- **问题解决 → 通知调查中的事件处理人**：`problem.resolved` 事件；**绝不批量关闭事件**（有测试断言事件保持 `in_progress`）。
- **问题解决前置校验**：要求所有必需修复依赖具备**成功**的变更结果；缺少扩展时 fail-closed。
- **多租户/MSP 正确性**：事件携带 actor 的**原生租户**，消费者在原生租户解析 actor——修复了"MSP provider actor 事件被永久阻塞"的缺陷（经变异测试证明）。

### 3.2 三轮独立评审收敛

| 轮次 | 主要发现 | 处置 |
|---|---|---|
| 1 | 扇出首个错误掩盖可重试目标；重放测试空泛（未真正重放）；一处同义反复断言 | 引入 `preferRetryableError`；新增 `reopenOutboxEvent` 使重放真实；删除空断言 |
| 2 | MSP provider actor 的事件被终态阻塞 | 事件与消费者携带/解析原生租户；**变异证明**（去掉 `DeliveryKey` 后去重测试失败） |
| 3 | 建议给 `record_outcome` 回执加字段 | **有理由拒绝**：该比较结构是审计契约，加字段会使校验永不满足、阻塞全部事件，零安全收益 |

结论：**Ready to merge: Yes**。

### 3.3 B 批交付门禁

- 遗留消费者清单为**零**；`work_item_relations` 为**单一写入方**。
- 以 PG 集成证据支撑（详见 §7 的测试命令）。

---

## 4. C1（规范流程身份）——已交付

### 4.1 契约

依据统一 WorkItem 设计 §15.2.2：

```text
businessId   = WorkItem.ID (tickets.id)
businessType = WorkItem.recordClass
               (generic | service_request_item | incident | problem | change_request | catalog_task)
businessKey  = "{recordClass}:{workItemId}"
```

- Wave-1 旧词表（`ticket`/`change`/`service_request`）**退役**：不再写入、不再被解析。
- Release 保留其显式遗留身份 `release`，**它不是 WorkItem**，也不映射到 Change。

### 4.2 关键成果：消除"第二份词表"

这是本次最有价值的发现。**生产代码中存在两处把 recordClass 翻回旧词表的映射表**，构成 AGENTS.md 明令禁止的"双事实来源"，导致**路由用一套词表、实例身份用另一套**：

| 位置 | 原作用 | 后果 |
|---|---|---|
| `service/bpmn_creation.go` | 创建路由翻词表匹配绑定 | 绑定按 `change` 查、实例按 `change_request` 写 |
| `service/bpmn_creation_configuration.go` | 发布预检翻词表 | 目录发布校验查不到绑定 |
| 3 处测试内同款映射 | 夹具自抄一份 | 掩盖真实契约 |

两处生产映射与 3 处测试映射**全部删除**（而非"翻译"）。这也是当时大批测试失败的**主因**，而非"夹具漂移"。

另修：`WorkItemPolicy` 词表收敛，消除 `service_request_item` 与 `catalog_task` 的**身份歧义**。

### 4.3 fail-closed 行为

- 任务视图解析**拒绝**退役/畸形业务键。
- 触发器校验要求身份与规范 policy 一致。
- 缺失绑定**失败关闭**，不会静默选用别的专业类的流程。
- 预检在新旧依赖存在时**阻塞**切换，且**全程只读**（repeatable-read，defer rollback，从不提交）。
- **预检截断即阻塞**：`ScanLimit` 截断会产生 `inconclusive_truncated_scan` 阻塞项（exit 2）。理由：部分扫描"没发现阻塞"**不等于**"没有阻塞"，否则会给出该工具本该防止的**虚假绿色结论**。

### 4.4 前端

- `approvals` 链接映射与绑定编辑页下拉按 **recordClass** 取键（含 `service_request_item` / `catalog_task` 拆分）。
- **退役身份不构造业务链接**：退化为"流程实例"链接，而不是把旧身份指向某个专业详情页。
- TDD：7 个规范类的表格驱动断言 + **3 个退役身份的 fail-closed 断言**。

### 4.5 文档与注释

- `AGENTS.md` 新增"流程身份即 recordClass"原则，并**镜像到 `CLAUDE.md`**（AGENTS.md 明确要求两者同步）。
- `docs/dev-commands-reference.md` §2.5.1 记录预检用法：退出码语义、只读保证、阻塞项、Release 例外、开发环境不迁移历史。
- `ent/schema/process_instance.go` 的身份注释由"过时的 Wave-1 描述"改为事实描述，并**重生成 ent** 使生成代码一致。

---

## 5. 关键决策与理由

| 决策 | 理由 |
|---|---|
| 只通知**有向**关系的事件 | 对称关系通知会产生噪声且无明确对端 |
| 接收人 = 对端 assignee → 回退 requester | 符合"谁负责谁知晓"；无 assignee 时仍有人收到 |
| 站内通知 + 可选租户 webhook | 满足可扩展性，又不强绑外部依赖 |
| **复用** `delivery_key` 幂等，不新增迁移 | 已存在唯一索引，避免为同义能力重复建机制（AGENTS.md 禁止并行实现） |
| **不迁移历史 ticket / 绑定数据** | 用户明确：这是开发环境。故保留预检作为**门禁**而非迁移工具 |
| 预检做成**只读** | 门禁工具不得有副作用 |

### 5.1 我主动否决了计划字面要求（重要，请重点 review）

计划写的是"入口 reserved-variable **覆盖拒绝**"。我据此把两条内部完成路径改为拒绝，**实测打断 22 条既有流程**（KAF 委派、SSLVPN 完成、任务服务完成）——因为这些**内部管线**合法的完成变量里本就携带 `ticket_id`/`work_item_id`/`action` 等键。

我**回退**了改动，并改为用测试锁定**真实契约**：

- 参与者表单路径（`CompleteTaskTx`、KAF 完成）：**剥离**保留键 → 覆盖不可能生效；
- 实例变量端点（`SetTaskVariables`）：**显式拒绝**；
- 普通业务字段在两条路径都原样通过。

判断依据：设计规则约束的是**用户表单/脚本**，不是内部完成管线；现有"剥离 + 拒绝"的分工是**有意的、承重的**。**计划文字不等于正确方案。**

---

## 6. C1 独立复核与修复（请重点 review 这段）

- 方式：只读 reviewer（独立模型），范围 `b06845f5..d7701287`，92 文件；不写文件、不改分支。
- 结论：**Critical 0 / Important 5 / Minor 4，Ready to merge = With fixes**。
- reviewer **独立复现确认了我的 3 个基线失败声明**（均在 `b06845f5` 上同样失败）。

### 6.1 Important 五项（已全部修复）

| # | 问题 | 影响 | 处置 |
|---|---|---|---|
| 1 | `service/provisioning_service.go`：手工交付的审批读取方查 `business_type=generic`，而唯一写入方写的是**实例业务类型**（服务请求恒为 `service_request_item`） | **真产品缺陷**：该 `Exist` 永不成立 → **每条已批准的服务请求都被拒**（"需要关联工单审批通过"） | 先**把夹具改诚实**（RED 精确复现拒绝），再让读取方查写入方真正产出的类 → GREEN |
| 2 | `service/bpmn_callback_security.go`：`authoritativeCallbackVariables` 仍**接受并翻译**旧词表（`case "ticket", "generic":`） | 在真实边界上继续解释已退役词表，与 C1 声称的契约相矛盾 | 只接受规范值；旧值与尚未定义交付映射的 `catalog_task` 落入 **fail-closed 默认分支** |
| 3 | `service/workitemcutover/check.go`：`ScanLimit` 截断时可报告"可切换" | 超过上限的租户可能凭**部分视图**被放行——正是该工具要防止的虚假信心 | 截断即阻塞（`inconclusive_truncated_scan`，exit 2）；`ScanLimit` 改为 var 以便测试；**新增整合测试** |
| 4 | `service/bpmn_process_binding_service.go`：Create/Update **不校验**业务类型 | 旧词表仍可经公共 API 写入，产生运行期永远匹配不到的绑定 | 两处均以 `workitemidentity.IsKnownProcessIdentity` 校验 |
| 5 | `service/ticket_authorization.go`：删除前置条件**硬编码** `RecordClassGeneric` | incident/problem/change/服务请求项带**运行中流程**时可被删除 | 改为按 `item.RecordClass` 组装业务键；未知类 fail-closed；**新增非 generic 用例** |

### 6.2 Minor 两项（已修复）

- **剥离保留键是静默的**：AGENTS.md/CLAUDE.md 要求被绕过的步骤留下可观测痕迹 → 在**单一收敛点**加 `Warnw`，**只记键名、不记变量内容**（避免敏感泄漏）。
- **`handlers/common/workitemcreation` 重复定义 recordClass 常量** → 改为对唯一权威的**别名**。

### 6.3 未采纳 / 未做

- **swagger 重生成**：尝试执行 `swag init` 后，生成产物改动约 **5000 行**（已提交的生成产物远落后于注解源），且会改动 `go.mod`。**已回退**——把无关的 5000 行塞进 C1 会淹没真实变更，且这会引入未审查的 API 文档改动。见 §8.1。
- 剩余 Minor：历史夹具混用词表、`catalog_task` 链接指向 `/service-requests/{id}`（当前休眠，因为 catalog task 流程创建仍受门控）。

---

## 7. 验证证据

### 7.1 后端

```bash
cd itsm-backend
go build ./...                       # → 0（通过）
go test ./... -count=1               # → 失败 3，全部为预先存在基线（见下表）
```

预检整合（需要一次性 PG 容器 `codex-workitem-convergence-pg-20260909`，DSN 由容器内
`POSTGRES_PASSWORD` 组装，**不打印密码、不使用共享库**）：

```bash
go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemCutover' -count=1   # → 11 PASS
```

### 7.2 前端

```bash
cd itsm-frontend
npm run type-check                                  # → 干净
npx jest --runInBand --coverage=false --runTestsByPath \
  "src/app/(main)/approvals/__tests__/page.test.tsx" # → 12/12 PASS
```

改动涉及的 5 个套件合跑：**42/42 PASS**。

### 7.3 关键过程证据（防"空泛测试"）

- **变异测试**：移除 `DeliveryKey` 去重 → B2 去重测试**失败**（证明测试有牙齿）；恢复 → 通过。
- **RED→GREEN**：保留变量契约、手工交付读取方、删除守卫非 generic 用例，均先复现失败再修。
- **只读性**：预检测试对比执行前后数据库摘要（行数 + 内容 md5）保持不变。

### 7.4 已知基线失败（3 个，均**非**本次引入）

| 失败用例 | 包 | 性质 |
|---|---|---|
| `TestRunPostSchemaMigrationsAppliesVersion007` | `internal/bootstrap` | 迁移计数漂移（期望 24，实际 28）；C1 未改任何迁移 |
| `TestDefinitionStartFrozenIncidentAssignment` | `service` | 冻结分配行为差异 |
| `TestIntakeHTTPProblemAndIncidentEntry` | `tests/integration` | problem 创建体拒 `category`（`InvalidCommand`），与身份无关 |

前两个我在更早的提交上复现过；第三个在 `6006e4a4`（C1b 之前）上同样失败。reviewer 亦独立确认三者均在 `b06845f5` 复现。

---

## 8. 未完成事项（分级，含建议归属）

### 8.1 swagger / API 文档重生成（**未做，建议独立变更**）

- 现状：`itsm-backend/docs/{swagger.yaml,swagger.json,docs.go}` 是**已提交的生成产物**，且明显落后于注解源。
- 影响：`dto.BusinessType` 的枚举仍向集成方宣传**已退役词表**（`ticket/change/service_request`，`x-enum-varnames` 亦为旧的 `BusinessTypeTicket` 等）。
- 为什么没做：重生成一次性改写约 5000 行，混入 C1 会让 review 失去焦点，且会引入未经审查的接口文档变更。
- 建议：**单独一个 PR**：只做生成、单独 review、单独验证。

### 8.2 前端全量 `test:ci` 的基线对照（**未做**）

- 现象：全量运行（223 套件）时有 4 个套件失败（`TicketNotificationSection`、`ChangePIRPage`、`ChangeDetail`、`IncidentDetail`），特征为 **10–15 秒级超时**；**隔离运行全部通过**（已用 2 vs 2 的等价对照验证：带我的改动单独跑 2 个套件 11/11 通过）。
- 缺口：我**没有**跑一次"基线全量对照"，因此**不能断言**该负载超时与本次改动无关。
- 建议：在 `b06845f5` 上跑一次全量 `test:ci` 并留证。

### 8.3 三个基线失败（建议各自开工单）

见 §7.4。它们不应阻塞 C1，但**不应长期无归属**。

### 8.4 其余 review Minor（低优先）

- 历史/预检夹具中仍混用词表（预检夹具**有意**使用旧词表，属正确；活跃路径夹具可机会性规范化）。
- `approvals/page.tsx` 中 `catalog_task` 链接指向 `/service-requests/{id}`——当前休眠，待 catalog task 流程创建解禁时再定语义。

### 8.5 C2 / C3（未开始）

- **C2**：跨域完整授权旅程 + 编号/动作前置条件。
- **C3**：观察期与删除门禁（物理删除）+ 前端全量覆盖门禁。
- 需要你决定排期。

### 8.6 与本次无直接关系但已识别的工作流（需你决定是否另开）

- **sslvpn 迁移漂移**：readiness 返回 503、ledger 停在 019 而需要 022、worker 副本为 0、delegation 配置缺失。

---

## 9. 风险与注意事项

1. **分支未推送**：领先 `main` 50 个提交，包含 B 批与 C1。推送范围需你确认（B 批与 C1 是否同批推）。
2. **squash 已改写历史**：`c1-pre-squash` 标签保留；若你希望保留细粒度历史，可基于该标签重建。
3. **生成的 API 文档与实际契约不一致**（§8.1）：在重生成前，集成方若严格按 swagger 生成客户端，会得到**旧词表**。
4. **预检只读且 fail-closed**：这是门禁；**开发环境不迁移历史数据**意味着老库里若存在活跃旧依赖，切换会被**阻塞**而非自动修复。
5. **前端全量测试在负载下不稳**（§8.2）：CI 上可能出现与本改动无关的红色，需区分"断言失败"与"超时"。
6. **环境依赖**：PG 集成测试依赖一次性容器与 `INTAKE_POSTGRES_TEST_DSN`；不要在共享库上运行。

---

## 10. 复核建议的关注顺序

按"风险 × 不确定性"排序，建议优先看：

1. **§6.1 #1**：手工交付读取方的修复——这是**真实产品缺陷**，且暴露了"夹具伪造掩盖缺陷"的模式。请确认修复方向与新增断言是否真的覆盖生产链路。
2. **§5.1**：我否决计划字面要求的那次判断——请确认"表单路径剥离 / 实例端点拒绝"的分工确实正确，以及我给出的理由是否成立。
3. **§4.2**：两处生产"第二份词表"的删除——请确认没有遗漏第三处（这类问题我第一次 grep **漏掉了**，见 §11）。
4. **§4.3 / §6.1 #3**：预检的 fail-closed 语义（尤其截断）、只读性保证。
5. **§8.1**：swagger 与契约不一致的处置是否同意"另开变更"。

---

## 11. 过程中我犯的错误（诚实记录）

> 列出来是为了让你判断我的结论可信度，而不是自我批评。

1. **机械替换误伤负例夹具**：批量把废弃词表替换为规范值时，把我自己写的"**必须拒绝**退役键"的负例测试里的键也替换成了合规值，等于**悄悄废掉了那个测试**（它随后变红才被发现）。已恢复并补正例。**教训**：批量替换必须排除负例夹具。
2. **一次不对等的对照实验**：我先用"2 个套件基线通过 vs 4 个套件带改动失败"下结论，这是不等价比较。改成 2 vs 2 后证明我的改动**没有**破坏它们。**教训**：对照实验必须变量唯一。
3. **`git stash pop` 失败**：因测试产物 `test-results/junit.xml` 冲突而恢复失败，我的前端改动一度只在 stash 里。已确认 stash 仍在并成功恢复（无丢失），此后把"恢复成功"作为必须显式确认的步骤。
4. **按计划字面实施导致 22 条流程中断**：见 §5.1。已回退并改为锁定真实契约。
5. **第一次 grep 漏掉"第二份词表"**：我最初只搜了 `BusinessType("旧词")` 这类调用，**漏掉了把旧词当 map value 的写法**，因此一度得出"生产端旧词表已清零"的**错误结论**。改用更广的搜索模式后才找到那两处生产映射。**教训**：验证"没有残留"时，搜索模式必须覆盖多种表达形式。
6. **夹具迁移掩盖了产品缺陷**：我把手工交付的测试夹具改成 `generic` 以"适配"新契约，结果**掩盖了 §6.1 #1 的真缺陷**。是独立复核抓出来的——这正是坚持独立复核的价值。

---

## 12. 如何复现本次验证

```bash
# 0) 环境
cd /home/administrator/project/itsm/.worktrees/workitem-convergence-relations
git log --oneline -1                       # 期望 9dcb36f9
git status --porcelain                     # 期望为空

# 1) 后端
cd itsm-backend
go build ./...                             # 期望 0
go test ./... -count=1                     # 期望仅 3 个已知基线失败（§7.4）

# 2) 预检（需要一次性 PG 容器）
#    DSN 由容器 POSTGRES_PASSWORD 组装；不要打印密码、不要用共享库
go test -tags=integration_postgres ./tests/integration -run '^TestWorkItemCutover' -count=1   # 期望 11 PASS
go run ./cmd/check_workitem_cutover       # 干净库期望 exit 0；有旧依赖期望 exit 2

# 3) 前端
cd ../itsm-frontend
npm run type-check                         # 期望干净
npx jest --runInBand --coverage=false --runTestsByPath \
  "src/app/(main)/approvals/__tests__/page.test.tsx"        # 期望 12/12
```

---

## 13. 相关材料索引

| 材料 | 位置 |
|---|---|
| C1 交接与状态流水 | `.superpowers/sdd/2026-09-09-workitem-convergence/task-7-c1b-handoff.txt`（gitignored） |
| B2 任务报告与三轮评审 | 同上目录 `task-6-report.md`、`task-6-review-round{1,2,3}.md` |
| C1 独立复核报告 | `/root/review_c1_identity/task-7-c1-review.md`、`REVIEW-BRIEF.md`、`c1.diff` |
| 统一 WorkItem 设计 | `docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md`（§15.2.2 为流程身份契约） |
| 收敛设计 / 计划 | `docs/superpowers/specs/2026-09-09-workitem-convergence-design.md`、`docs/superpowers/plans/2026-09-09-workitem-convergence*.md` |
| 预检命令手册 | `docs/dev-commands-reference.md` §2.5.1 |
| 身份契约（agent 必读） | `AGENTS.md` / `CLAUDE.md` 的 WorkItem 契约小节 |
| squash 前重建点 | `git tag c1-pre-squash`（= `c3428012`） |
