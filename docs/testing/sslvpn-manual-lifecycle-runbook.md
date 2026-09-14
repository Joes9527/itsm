# SSLVPN 手工全流程测试手册：表单、工单、BPMN 与 KAF

状态：可执行测试手册，2026-09-14。ITSM 静态核对基线为 `a25e108d2`；KAF 本地参考基线为 `dfe304da`。**本次只核对源码并编写文档，没有登录 WSL 验证或执行部署、审批、Graph 授权。所有运行结果均待本轮执行填写。**

适用人员：部署负责人、目录/流程管理员、申请人、主管审批人、网络运维审批人、服务台工程师及测试记录人。

交给 Coding Agent 执行时，先读[Agent 执行说明](sslvpn-coding-agent-execution-guide.md)，并从[执行记录模板](sslvpn-agent-run-record-template.md)建立本轮记录。明确目标、授权和人工审批分工后再执行本文；中断后从原记录续跑。

## 1. 先看测试路线与完成条件

按顺序执行：**登记环境 → ITSM/KAF/Worker 部署核对 → 配置自定义字段 → 配置授权策略 → 发布并绑定 BPMN → 表单校验 → 拒绝路径 → 正常授权 → 关闭检查 → 查询审计 → 权限恢复**。每个阶段留证后再进入下一阶段。

本手册以 SSLVPN Requested Item（`recordClass=service_request_item`）为主线。它有一个共享 WorkItem，不是先创建 SR 再另建 Ticket。统一详情是 `/tickets/{workItemId}`；专业 Service Request ID、WorkItem ID、BPMN task ID 和流程实例 ID 分别登记，不能互换。

| 验收对象 | 本轮应证明什么 |
|---|---|
| 自定义表单 | 管理员配置的字段能保存、重新加载、正确渲染、校验、提交并在历史记录中保留原答案 |
| Ticket 生命周期 | 从创建、跟踪、审批、履约到完成与关闭检查，一直是同一 WorkItem；评论、附件、分派、审计和字段可追溯 |
| BPMN | 使用指定激活版本，先主管后网络运维；拒绝不执行授权；服务任务等待真实结果 |
| KAF | 正常身份登录、目录/表单受理、审批后受控执行、结果回写与会话恢复可查 |
| 外部授权 | 对获批目标查询确认成员关系；完成记录有验证时间与证据，重复刷新不重复授权 |

VPN 客户端登录、网络可达性、到期自动撤权、Teams/WeCom 不属于此授权闭环的已实现承诺。若需要验证，另列业务用例；不能把“组成员授权完成”记成“VPN 登录通过”。

### 1.1 执行规则与结果标记

- **PASS**：所有预期有本轮证据；**FAIL**：实际与预期不符；**BLOCKED**：身份、配置或必要能力缺失，无法继续；**NOT RUN**：尚未执行；**N/A**：本轮明确不适用并说明原因。
- UI 缺入口时记录 UI 的 BLOCKED/FAIL；管理员 API 补充验证另记结果，不能合并成“全 UI 通过”。
- 首先完成无外部授权的表单与拒绝测试，再做真实授权。任何异常不得靠直接改数据库状态、跳过审批或修改旧任务允许动作继续。
- 正向测试使用已确认的 Dev 用户和组；共享 Azure 租户中的其他真实权限不属于测试清理范围。
- 每次提交前给申请标题加本轮唯一标记，如 `SSLVPN-MANUAL-20260914-A01`。日期和序号每轮更新。

## 2. 登记环境、角色和证据

### 2.1 环境登记表（开始前填写）

2026-09-08 的环境文档记录 ITSM 前端 3001/API 8080、KAF 前端 5173/API 8000；这些是**历史起点，不是本轮实测**。先按[本机 WSL 环境](../development-environment.md)和[部署手册第 0 节](../deployment/sslvpn-wsl-deployment-and-manual-verification.md#wsl-dev-entry)核对。

| 项目 | 本轮记录内容 |
|---|---|
| 测试轮次、时间、负责人 | 唯一轮次；使用的时区；执行人及恢复负责人 |
| ITSM | 浏览器 URL、API URL、源码 SHA、实际二进制路径与哈希、进程 PID/cwd/启动时间 |
| KAF | 浏览器 URL、API URL、源码 SHA、运行目录、配置文件路径、进程 PID/cwd/启动时间 |
| Worker | 每个 Worker 的二进制/版本、PID、健康端口、目标 ITSM 库和 webhook 目标 |
| 依赖 | 两侧数据库 host/port/name、Redis DB、Qdrant collection/维数、embedding 与 LLM 模型；不记密码 |
| 身份与目录 | ITSM tenant ID、目录 ID、`catalogVersion`、`formSchemaVersion`、policy ID/version、KAF workspace UUID/slug |
| 流程 | process key、激活版本、definition ID、发布 XML 哈希 |
| 固定授权对象 | Dev fixture 引用、身份映射 ID/version、测试前成员关系证据；原始 Object ID 存受限记录 |
| 恢复 | 私有配置/数据备份位置、原运行版本、原目录/流程绑定、权限恢复负责人 |

实际运行 SHA 不等于工作目录的 `git HEAD`，还需二进制来源证据；当前开发环境文档曾记录 main 之外的目录修复，不能从旧 main 重建覆盖。

### 2.2 角色准备

| 角色代号 | 使用方式 | 检查 |
|---|---|---|
| A：管理员 | 独立浏览器配置文件 | 本租户目录、字段、用户/组、BPMN 发布权限 |
| R：申请人 | 独立浏览器配置文件，同时登录 ITSM 和 KAF | 普通用户；有目录申请和本人 WorkItem 读取权限；KAF workspace member |
| M：主管 | 第三个浏览器配置文件 | `dept_manager` 候选组成员；能读申请并领取、批准/拒绝任务 |
| N：网络运维 | 第四个浏览器配置文件 | `network_eng` 候选组成员；不能提前处理一级待办 |
| E：工程师 | 有分派与协作权限的账号 | 依后端动作权限操作，不能替代 M/N 审批 |
| O：运行/恢复负责人 | 受限运维/API 会话 | 检查 Worker、执行记录和固定测试对象恢复 |

两个无痕窗口可能共享登录态，使用独立配置文件或不同浏览器。每个窗口先核对账号和租户。候选组的同名角色不能替代真实 group membership。KAF automation 是单独机器账号，不用于人工申请。身份映射、五类独立密钥、workspace 与 Procedure 配置见[部署手册 5.2–5.4 节](../deployment/sslvpn-wsl-deployment-and-manual-verification.md#52-四类人类账号与机器账号)。

### 2.3 统一留证方式

每步记录时间、角色、WorkItem 编号/ID、页面路径、预期、实际结果和截图/去敏响应引用。浏览器开发者工具的 Network 用于检查请求与返回值，不导出未经去敏的 HAR、cookie、token 或 provider body。

页面信息不足时：先重新读取同一对象，再查看相应 GET 响应；只有 O 才核对服务端日志/账本。HTTP 200 还需检查业务返回码；创建 HTTP 202 或 KAF 确认卡只证明受理进度，不证明已经履约。

## 3. WSL 部署与启动验收（D01–D06）

本章包含 KAF 部署验收；详细配置和命令只维护在[部署手册](../deployment/sslvpn-wsl-deployment-and-manual-verification.md)，避免复制出另一组环境配置。

| 用例 | 谁操作、做什么 | 预期与证据 |
|---|---|---|
| D01 环境核对 | O 按环境登记表逐项核对当前进程、源码、数据库和端口 | 所有项均明确；当前 Dev 与旧 C4 隔离环境没有混用 |
| D02 必要部署 | 已满足版本要求则登记“复用”；否则按部署手册第 0 节依次准备独立发布目录、依赖、配置、迁移、构建、切换与回滚 | ITSM、KAF 和 Worker 的兼容版本成套登记；没有覆盖未知来源的专用修复 |
| D03 基础健康 | 检查 ITSM `/api/v1/readyz`、KAF `/health`、每个 Worker `/readyz`，检查启动未降级 | 必要依赖可用；KAF health 单独不构成业务就绪 |
| D04 正常身份 | R 正常登录两端；A/O 验证 KAF 创建/读取映射及 Graph 获批主体映射 | 同一实际请求者、正确租户/workspace；无 JWT 注入或管理员冒充 |
| D05 KAF 能力 | R 在目标 workspace 新对话发起 SSLVPN 咨询，检查目录和有效期选项；O 核对真实 Procedure、工具、LLM/RAG 与冻结目标配置 | 能取得本租户有效目录；确认前无新申请、无授权；Procedure 为 `graph_vpn_access_grant` |
| D06 队列与基线 | O 检查旧 outbox/delivery/itsm_job，核对固定 Dev 对象的非成员基线及恢复负责人 | 没有来源不明的可执行任务；unknown 任务未被重置；开始正向授权条件齐备 |

**放行条件：D01–D06 必需项均 PASS。**如果只做表单和拒绝检查，允许先不运行授权消费者，但登记覆盖范围；正向履约前必须完成 D06 和 Worker 就绪验证。已有共享 Worker 不得为测试随意停止。

<a id="form-configuration"></a>

## 4. 自定义字段与表单配置（F01–F09）

### 4.1 当前界面能力边界

配置入口：A 打开 `/admin/service-catalogs` → 目标目录“编辑” → “自定义字段” → “添加自定义字段”。

| 配置项 | 当前可用方式 | 测试要求 |
|---|---|---|
| 字段标识、展示标签、类型、必填 | UI 可配置 | 保存后重新进入编辑器，逐项与输入对比 |
| 类型 | `text`、`textarea`、`number`、`date`、`select` | 五种分别验证，不将后端其他类型当成 UI 已支持 |
| Select 选项 | UI 输入英文逗号分隔；新输入生成相同的 label/value | SSLVPN 的 `30d`/`30天` 这种编码和标签分离需要管理员 API 配置 |
| 顺序 | 保存时按当前字段列表顺序写 `sortOrder` | 核对渲染顺序；目前无拖拽排序按钮，不写“拖动字段”步骤 |
| 默认值、placeholder、条件显隐、正则、数字范围、分组/多栏布局 | 当前编辑器没有完整配置入口 | 记能力缺口，不预填“支持”或要求寻找不存在的按钮 |
| 数字/日期输入 | 数字控件与 DatePicker | 数字服务端验证数值；日期不能仅凭选择器推断后端有完整格式/范围校验 |
| SSLVPN accessPolicy、`serviceType=access` | 当前管理员 UI 不完整，使用既有目录 API | 完成 4.3 配置并回读；不能选择 `custom` 冒充 access |

字段定义属于目录；提交的字段值通过共享 WorkItem 保存快照，详情在“业务扩展参数”展示。当前该区域直接显示原始值，例如 Select 可能显示 `30d`，不保证自动翻译成 `30天`。审批人需打开关联 WorkItem 查看表单；审批列表本身不展开整张表单。

### 4.2 创建测试目录和字段样例

使用专用测试目录，避免修改日常申请目录。A 在 `/admin/service-catalogs` 点击“新建服务目录”，填写名称 `SSLVPN 手工验收-本轮标记`、描述、现有可选分类；交付时间填数字 `1`（天），不要使用输入框示例中的“1-3个工作日”，当前服务端按整数天校验。工作项类型选“服务请求”，需要审批选“是”，状态先选“禁用”。服务类型的 access 配置在 4.3 完成。

按下面顺序添加字段。标识使用英文小写与下划线，且在同一目录中唯一。

| 顺序 | 字段标识 | 标签 | UI 类型 | 必填 | 正常输入 |
|---|---|---|---|---|---|
| 1 | `office_location` | 办公地点 | Text | 是 | 上海 |
| 2 | `business_context` | 远程办公用途 | Textarea | 是 | 本轮 SSLVPN 验收，需要远程访问已批准的内部服务 |
| 3 | `device_count` | 设备数量 | Number | 是 | 1 |
| 4 | `planned_use_date` | 计划使用日期 | Date | 否 | 执行日之后的一个日期；不是授权到期时间 |
| 5 | `access_duration` | 授权期限 | Select | 是 | UI 暂填选项 `30d`；最终以 4.3 已批准策略为准 |

`title`、申请理由、联系人/邮箱是已有基础字段，不重复建立同名自定义字段。`expectedAt` 是期望交付时间；授权期限来自 `access_duration` 的策略，不用日期控件替代。

**F01 保存回读**：保存禁用目录 → 刷新列表 → 重新编辑 → 核对五个字段及顺序；Network 中查看 `GET /api/v1/service-catalogs/{catalogId}` 的 `fields`、`catalogVersion` 和 `formSchemaVersion`。字段丢失、类型改变、必填丢失或选项乱码均 FAIL。

**F02 管理端校验**：在这个测试目录上尝试空标识/空标签，必须被拦截；尝试重复标识，检查保存是否被明确拒绝、没有覆盖另一字段。若未拦截，记录配置校验缺陷并恢复正确字段，不让损坏定义进入正向测试。

### 4.3 管理员补齐 SSLVPN 策略

由 A/O 使用已认证的 API 客户端执行。完整身份/CSRF 操作见[部署手册 5.2 节](../deployment/sslvpn-wsl-deployment-and-manual-verification.md#52-四类人类账号与机器账号)。下面是**请求体模板**，环境变量样例必须替换，不能原样提交。不得在文档里填写真实秘密。

1. GET 当前目录，保存当前五个字段和 `expectedCatalogVersion` 所需版本。
2. PUT `/api/v1/service-catalogs/{catalogId}`，保持 `status=disabled`，使用下列内容。`fields` 必须包含本轮完整字段集合，不能只传有效期字段导致其他字段被替换。

```json
{
  "expectedCatalogVersion": "执行前刚读取的 catalogVersion",
  "status": "disabled",
  "targetClass": "service_request_item",
  "serviceType": "access",
  "requiresApproval": true,
  "fields": [
    {"name":"office_location","label":"办公地点","type":"text","required":true,"sortOrder":0},
    {"name":"business_context","label":"远程办公用途","type":"textarea","required":true,"sortOrder":1},
    {"name":"device_count","label":"设备数量","type":"number","required":true,"sortOrder":2},
    {"name":"planned_use_date","label":"计划使用日期","type":"date","required":false,"sortOrder":3},
    {"name":"access_duration","label":"授权期限","type":"select","required":true,"sortOrder":4,
     "options":[{"value":"30d","label":"30天"}]}
  ],
  "accessPolicy": {
    "provider":"graph",
    "externalSystem":"本轮已配置的 externalSystem",
    "groupId":"本轮固定 Dev security group Object ID",
    "durationField":"access_duration",
    "durationOptions":[{"key":"30d","label":"30天","seconds":2592000}]
  }
}
```

3. GET 回读确认 policy ID/version、目录版本及字段版本。409 表示配置已变化，重读并人工合并本轮修改，不盲目重发覆盖。
4. 按第 5 节发布引用此 policy ID 的流程，随后重新 GET 目录，带最新 `expectedCatalogVersion` PUT `processDefinitionKey=sslvpn_approval_flow`、`status=enabled`。
5. 再次 GET 确认目录、策略、字段、流程 key 全部一致。若 disabled 状态下某步仍被发布校验拒绝，记录返回的具体缺失项并停止，不删除校验。

示例 30 天是字段与策略一致性的样例；实际策略必须经环境负责人确认。**F03 配置闭环**：上述读写/绑定均成功且关联目标准确。后续用 UI 编辑此目录时，保存前后尤其核对 `serviceType=access`、`requiresApproval=true`、process key 和编码/标签没有被表单适配覆盖；如果变化，记 FAIL 并用最新版本恢复测试配置。

### 4.4 申请表单校验

R 进入 `/service-catalog`，查找本轮目录，打开申请页 `/service-catalog/request/{catalogId}`。先补齐联系人、合法联系邮箱、申请标题、申请理由，避免基础字段错误掩盖自定义字段结果。

| 用例 | 手工操作 | 预期与证据 |
|---|---|---|
| F04 渲染 | 查看“该服务的补充信息” | 五个标签、类型、必填和顺序正确；日期有选择器、期限有已配置选项；没有其他目录字段 |
| F05 必填 | 保持任一必填字段空白，其他填好，点击“提交申请”；逐个重复 | 对应字段提示，未创建 WorkItem；再单独测试纯空白 text/textarea 的服务端拒绝 |
| F06 数字与选项 | device_count 输入 1；尝试输入非数字。授权期限选择合法选项 | 合法值可提交；非法数值或选项须拒绝。浏览器阻止非法输入只记 UI 校验，服务器检查按第 10 节另记 |
| F07 可选与日期 | planned_use_date 留空提交一个拒绝测试申请；另一个申请填写日期 | 空值不造成必填错误；有值时保存后可辨认且没有日期偏移；不把日期格式边界标为已验 |
| F08 数据快照 | 完成 F07 的合法提交，记录回执并打开 `/tickets/{workItemId}` | “业务扩展参数”有原字段标签与答案；刷新/重新登录仍一致；不要求在 `formData` 中再存一份 |
| F09 按角色回显 | R、M、N、E 各用本人的权限查看同一 WorkItem | 有权者能核对原答案，无权者拒绝；审批前后及履约后值保持一致。仅看到 HTTP 成功、但页面没有字段，记 UI FAIL |

F07/F08 使用第 6 节的拒绝申请，不需要为了字段检查额外做真实授权。日期的序列化格式和时区以本轮 Network/详情证据记录；当前源码没有全面日期服务端校验，非法日期附加检查若通过进入系统，应记录校验缺口。

## 5. BPMN 配置、发布与版本验证（W01–W05）

### 5.1 发布实际 SSLVPN 模板

A 以仓库 [SSLVPN BPMN 模板](../../itsm-backend/service/bpmn/sslvpn_approval_flow.bpmn)制作本轮发布副本。仅替换 `CATALOG_ACCESS_POLICY_REQUIRED` 为 4.3 回读的 policy 数字 ID。若此环境同一 process key 被其他目录共用，先协调发布窗口；不能激活指向本轮 policy 的新版本影响日常目录。

| 节点 | 必须核对的配置 |
|---|---|
| `UserTask_DeptManagerApproval` | 上级领导初审，`taskPurpose=approval`，候选组 `dept_manager` |
| `Gateway_ManagerDecision` | `approvalResult=approved` 去二级；`rejected` 去拒绝终点 |
| `UserTask_L2NetworkOpsApproval` | 网络运维技术复审，候选组 `network_eng` |
| `Gateway_SecurityDecision` | 批准去授权服务任务；拒绝去拒绝终点 |
| `Activity_KafDelegate` | `service_task_type=kaf_delegate`，`action=external_group_grant`，policy 引用正确 |
| allowed actions | `complete_bpmn_task,record_execution_failure`，两者都存在 |
| 终点 | `EndEvent_1` 履约完成；`EndEvent_Reject` 申请被拒绝 |

**W01 发布**：使用现有流程模板 owner 管理入口 `/admin/workflows` 核对 process key 已存在。通过管理员 API `POST /api/v1/bpmn/versions` 发布，body 字段为 `processDefinitionKey`、`name`、`bpmnXml`、`changeLog`。`bpmnXml` 是上述完整发布副本。不要把 XML 内的 `1.1.0` 当成返回版本号。

**W02 激活**：打开 `/workflow/versions` 选择该 process key，查看刚返回的版本 → “激活”；或者 `PUT /api/v1/bpmn/versions/{key}/{returnedVersion}/activate`。回读定义与 XML，保存哈希；最后按 4.3 启用目录。

版本页当前负责查看、激活、比较、回滚，不能假设提供新版本 XML 上传入口。设计器可用于查看/编辑副本，但导出 XML 后必须重新核对服务任务扩展元数据，不能丢失 policy 或 allowed actions。

### 5.2 运行中观察

**W03 绑定**：R 创建本轮申请后，A/O 在 `/workflow/instances` 定位同一 WorkItem 的实例，核对 process key、实际 definition/version、当前节点。仅画布看起来一样不足以证明使用了正确发布版本。

**W04 条件与顺序**：一级未通过时 N 没有该申请二级可办任务；一级通过后才产生二级；任何一级拒绝均到拒绝终点，不能再到 KAF。

**W05 版本隔离（扩展测试）**：在无外部授权的专用测试时段，用 V1 创建待审批申请，再发布/激活保留相同授权约束的 V2，创建第二个待审批申请；旧实例仍用 V1，新实例用 V2。两张均走拒绝收尾。没有独立发布窗口则记 NOT RUN，不能在共享流程上随意激活版本。结束后恢复原绑定前需核对仍在运行的实例。

`/workflow/ticket-approval` 是审批流程**设计器**。人工处理任务使用 `/approvals`。

### 5.3 审批链配置、预解析展示与实际审批（AC01–AC05）

**审批链需要纳入测试，但不要求为了 SSLVPN 再新建一条租户级审批链。**当前存在以下三个不同入口/信息源，不能只凭名称认定它们是同一份执行配置：

| 内容 | 入口或来源 | 本轮如何使用 |
|---|---|---|
| 后台审批链配置 | `/admin/approval-chains` | SR 创建仍会查询本租户 `entity_type=service_request`、active 的审批链，解析步骤，写入创建上下文/流程变量；损坏配置可能阻止创建 |
| “审批链（预解析）” | WorkItem 详情 → “审批链”页签 | 展示申请创建时保存的 `_approval_chain`；是配置解析快照，不证明审批已经执行 |
| 真实待办与审批记录 | `/approvals`、流程实例及同一页签的决策卡片 | 由 BPMN ProcessTask 和 ProcessApprovalDecision 提供，是实际审批人、顺序、结果与时间的核对依据 |

本基线 SSLVPN 模板显式定义主管 `dept_manager`、网络运维 `network_eng` 和两个 `approvalResult` 分支，没有读取 `approval_chain` 变量来动态生成这些节点。仅修改后台审批链不能据此声称 SSLVPN 审批人、层级或会签已改变；必须核对实际发布 XML 和新实例。

SR 创建解析器目前按**租户 + service_request + active**查询首条审批链，没有按当前 catalog ID 精确选择，也不应把页面中的审批链 ID 当成已证实的目录专属绑定。无 active 配置时返回空解析结果，不等于 SSLVPN 无需审批：目录仍要求审批，绑定 BPMN 的两级节点仍需执行。有多条 active 时，先记录配置歧义并交负责人确认，不凭 UI 列表排序猜哪条生效。

| 用例 | 操作 | 预期与证据 |
|---|---|---|
| AC01 配置基线（必做） | A 在 `/admin/approval-chains` 查本租户服务请求配置，记录 ID、状态、步骤、角色；对照实际创建上下文及 SSLVPN XML | 能区分无配置、唯一配置、多条配置与解析错误；不为测试直接停用全租户规则 |
| AC02 尚无决策（必做） | R 提交新申请后，先打开“审批链”页签，再由 M 检查真实待办并由 A 查看当前实例节点 | 即使没有决策卡片，也不能认定没有审批流程。当前组件在无决策或读取失败时可能显示“该工单未走审批流程”；若实际已有待办，记录展示缺陷 |
| AC03 两级记录（必做） | M/N 按主流程分别批准或拒绝；每次刷新详情“审批链”页签 | 真实卡片的节点、操作者、意见、时间与本次 BPMN 决策一致；预解析角色不冒充实际审批人，预解析步骤不冒充已完成 |
| AC04 配置/执行差异（必做） | 比较预解析展示与实际 SSLVPN 双级节点 | 差异被明确记录，不能静默当作一致；实际路径按已发布 BPMN 验收。无预解析卡片本身不判失败，但实际待办和决策必须可查 |
| AC05 规则能力（独立扩展） | 如需测试审批链新增/编辑、金额阈值、组织条件、会签/或签，使用隔离租户与专用流程，逐项检查保存回读、解析结果及 BPMN 是否消费 | UI 有选项不等于全链路支持；尤其组织/风险条件、会签不能仅靠配置成功判 PASS。未执行则 NOT RUN，不改变共享租户配置来补齐本次 SSLVPN 主线 |

审批链步骤解析为空或配置读取失败，与“没有任何审批要求”是不同情况。不得通过删除配置、关闭目录审批开关或改成 no_process 来解决创建失败。

静态核对依据：[SR 创建解析](../../itsm-backend/handlers/service_request/creation.go)、[租户审批链解析器](../../itsm-backend/service/approval_chain_resolver.go)、[预解析展示](../../itsm-frontend/src/components/ticket/ServiceCatalogApprovalChain.tsx)、[真实决策展示](../../itsm-frontend/src/components/ticket/ProcessApprovalDecisionCards.tsx)。这些结论仍需按本轮 WSL 运行版本复核。

## 6. 两个受理入口和拒绝路径（R01–R04）

### 6.1 ITSM 表单入口：一级拒绝

| 步骤 | 操作角色与动作 | 预期/留证 |
|---|---|---|
| R01.1 | R 完成 F04–F08，标题带 `FORM-REJECT`，提交一次 | 创建回执有 WorkItem ID/编号，跳转统一详情；记录专业 ID。结果待定时保留原提交，不新建替代 |
| R01.2 | R 在 `/tickets` 清除旧筛选后查编号，打开详情 | 找到同一条；字段、申请人正确；专业履约显示“待审批”，没有验证结果 |
| R01.3 | M 打开 `/approvals`，按“业务单据”编号和流程 key 定位“上级领导初审”；打开关联详情 | 能核对五种字段答案，再返回原任务。链接若定位错误，记录缺陷并用已登记 WorkItem URL 核对；不能审批错误对象 |
| R01.4 | M 点击“领取”，然后“拒绝”；先留空意见尝试提交，再填 `本轮手工测试：主管拒绝` | 空意见被拒绝；有意见后任务结束，审批记录有 M、时间和原因 |
| R01.5 | R 刷新同一详情；N 刷新待办；O 核对实例与外部日志 | 专业状态 rejected；无二级授权委派、无 Graph add；字段快照保留 |

审批中心当前只取前 100 条任务并在客户端筛出待办，没有全量搜索保证。找不到任务时 A/O 按本轮流程/业务对象查询并检查分页（见第 10 节），不要立刻认定“没有任务”。即使审批列表有“批准”按钮，也必须以服务端授权和正确任务为准。

### 6.2 KAF 对话入口：二级拒绝

| 步骤 | 操作角色与动作 | 预期/留证 |
|---|---|---|
| R02.1 | R 在 KAF `it-support` 新会话输入“申请本轮 SSLVPN 手工验收目录，用于远程办公”；提供办公地点、用途、设备数量、计划日期和期限 | 能找到正确目录并补齐必填信息；不在确认前创建申请或执行授权 |
| R02.2 | R 核对原确认卡的服务、理由、期限、身份及补充字段，确认一次 | 记录 session/card/action、WorkItem ID/编号；两端对应同一申请 |
| R02.3 | R 在 ITSM 详情核对完整自定义字段 | 若 KAF 卡片不展示或无法收集某字段，分别记录受理 UI 缺口；未收齐必填字段不能创建成功，不能从 API 造数据替代 KAF 用例 |
| R03.1 | M 在 `/approvals` 定位、领取并批准，意见填 `本轮手工测试：主管同意，转网络复审` | M 任务完成、N 二级任务出现；尚无外部授权 |
| R03.2 | N 打开关联详情核对原表单，领取二级任务并拒绝，填写原因 | 二级拒绝记录可追溯；服务任务不执行 |
| R04 | R 在原 KAF 卡刷新详情，离开再重开原会话；ITSM 刷新同编号；O 核对 Graph | 两端均显示拒绝，不出现“已开通”；本申请 0 add；原对话/编号关联仍存在 |

拒绝分支中流程实例可能技术上已结束；应以专业 rejected 状态和决策判断业务结果，不能把 BPMN `completed` 一词直接解释为“授权成功”。

## 7. 正常审批与 KAF 履约（P01–P07）

先确认第 3 节全部放行，固定对象是本轮可操作的非成员基线。正向至少跑一次 ITSM 表单入口和一次 KAF 入口；**两次串行执行，每次先完成第 11 节权限恢复再开始下一次**，或使用分别确认的独立 Dev 对象。不能把第二次“权限已存在”当作第二次新增授权通过。

| 用例 | 操作角色与动作 | 预期/留证 |
|---|---|---|
| P01 创建 | R 使用选定入口填写五种自定义字段，标题标记 `FORM-PASS` 或 `KAF-PASS`；确认一次 | 只有一个新 WorkItem 和专业扩展；创建回执、字段值、目录版本、流程实例均关联 |
| P02 协作 | E 在允许的阶段打开详情，“分派”给本轮工程师；在评论页签写本轮备注，上传不含敏感数据的小文本附件并重新下载 | 后端允许的操作成功；分派不改变 M/N 审批权限；评论/附件保留，文件内容相同；若动作被禁用记录原因 |
| P03 一级 | M 核对表单，领取并批准 | 实例推进到 N；仍处审批阶段，无 accessResult；无需人为“开始交付” |
| P04 二级 | N 再核对身份、用途、期限与字段，领取并批准 | 到 `Activity_KafDelegate`；Worker 出队并调用 KAF；专业状态 fulfilling；仅 HTTP 202/投递成功不能标完成 |
| P05 执行 | O 观察本申请 task/event/action/run 与 KAF delivery；R 刷新详情 | Graph 只对冻结获批目标操作；原 POST 后只读查询确认成员关系；没有重复 add |
| P06 完成 | R 查看统一详情中的“服务申请与规格参数”；O 核对实例与回执 | `fulfillmentState=completed`；outcome=granted；有 verifiedAt、expiresAt；WorkItem 原始 status=resolved；流程和服务任务完成 |
| P07 恢复查看 | R 多次刷新同一详情；KAF 入口创建的申请在原卡刷新并重新进入会话；M/N 查看审批历史 | 始终同一编号与原字段；验证时间/到期时间不变；无第二次 Graph add；KAF 与 ITSM 的履约结果一致 |

ITSM 表单入口创建的申请未必有原 KAF 对话卡；此轮验证 KAF 后台履约与 ITSM 结果即可。原卡恢复要求适用于 KAF 入口创建的申请，不虚构一张卡片。

### 7.1 成功判定分层

| 层次 | 验收点 |
|---|---|
| WorkItem | 同一 ID/编号；专业 class 不变；成功回执推进 resolved 并记录时间 |
| 专业履约 | `completed` 加有效 `accessResult`，不能仅靠共享 status |
| BPMN | 两个真实批准决策，正确服务任务与流程实例完成；拒绝/失败未混入成功 |
| Graph | 固定 subject/group 的原授权及查询证据；不是任意用户已在任意组 |
| 有效期 | 首次 verifiedAt 加获批 durationSeconds；重复读取/回执不续期 |
| 展示 | “业务扩展参数”是原提交答案；“授权已验证”带时间；没有泄露内部凭据/原始错误 |

如果测试前已为成员，应走 `already_present` 结果，`managed=false`、不制造 expiresAt，也不新增/移除已有权限；此分支另记，不能替代非成员新增授权的 P05 验收。授权到期时间是记录，不是自动撤权承诺。

## 8. 完成、关闭与生命周期补充（L01–L06）

**resolved、closed 和专业 completed 是不同检查项。**正常 SSLVPN 模板没有额外“用户确认关闭”节点；本基线的详情页主要提供分派/编辑/抄送/删除，没有独立“关闭/重开”按钮。不要为了完成手册而手改状态下拉框绕过流程。

| 用例 | 怎么测 | 如何判断 |
|---|---|---|
| L01 完成后归档 | P06 后刷新详情、字段、历史、审批和附件 | 已完成结果、原答案和审批保留；记录共享 status=resolved 与专业 completed，不写成 closed |
| L02 关闭入口 | 在本轮实际 WSL 版本检查专业详情/后端动作权限是否提供正式关闭动作 | 若 UI 无入口，记“UI 关闭 BLOCKED”；不能把 API 成功当 UI 通过 |
| L03 关闭接口补充 | 仅在授权已确认、无活动审批/履约任务、没有 unknown、具备 ticket:update 且本版本允许此路径时，由 O 用现有 `POST /api/v1/tickets/{workItemId}/close`，body `{"feedback":"本轮手工验收完成"}` | 读取同一 WorkItem：status=closed、关闭时间、操作者/历史可追溯，原 accessResult 和字段保持；403/领域拒绝则记录边界，不改表绕过。这是 API 补充用例 |
| L04 关闭后重开 | 检查 UI 提供的动作；按第 10 节在独立无授权测试记录验证 closed→in_progress 是否被拒绝 | 当前状态表把 closed 视为终态，不预期可重开；若不同入口允许则记一致性问题。不能重开原授权申请触发第二次 grant |
| L05 申请人取消 | 用新建、未到最终审批的测试申请，检查申请人正式取消入口及允许动作 | 无入口记 BLOCKED；若有，取消后待办/实例/专业状态应一致，无授权。表单底部“取消”只返回目录，不是撤销已提交申请 |
| L06 流程终止 | A 在专用未批准申请的 `/workflow/instances` 选择“终止”，核对确认框中的实例 | 这是管理员流程终止，不冒充申请人取消。检查任务不可继续、专业投影、原因/历史以及 0 add；若只终止实例却留下正常待审批展示，记 FAIL |

L03 是为本地现存关闭接口设置的补充验证，不定义新的 SR 状态机。实际版本若已改成专业关闭命令，使用专业 owner 提供的入口并记录版本差异。UI 缺口和审计/时间字段缺失必须保留为未通过项。

### 8.1 SLA 与协作

SLA 测试只使用本轮目录/专用策略，不修改全租户阈值。记录提交、首次响应、审批、履约、解决、关闭时间，对照实际 SLA 策略及工作日历检查计时。审批等待是否暂停、哪个动作构成首次响应、终态何时停表由绑定策略决定；目录页面填了响应/解决分钟数并不证明全链路生效。

超时升级单列长时用例：设定专用测试策略后自然等待，核对告警接收者、升级和审计；本轮时间不足则 NOT RUN。不要修改数据库时间或系统时钟制造通过。通知分别登记站内/邮件/其他渠道；未启用邮件不能记邮件 PASS。

## 9. 表单变更、查找与异常分支

### 9.1 表单版本与历史（V01–V04）

在拒绝测试目录上执行；修改前备份完整 fields/policy/版本，不修改已在真实履约中的策略。

| 用例 | 操作 | 预期 |
|---|---|---|
| V01 历史快照 | 已有提交后，把 office_location 标签改为“办公城市”，保存并回读；重新查看旧 WorkItem | 旧申请仍显示原“办公地点”和原答案；新表单使用新标签；不会追随当前字段定义重写历史 |
| V02 旧页面提交 | R 打开旧表单并填好但不提交；A 修改一个字段定义并保存；R 从旧页面提交 | 旧版本被拒绝或要求重新确认，无半成品 WorkItem；保留原尝试记录 |
| V03 兼容答案 | R 点击“重新读取目录” | 兼容字段答案保留，版本更新；需要新的确认后再提交，不静默将旧确认绑定到新定义 |
| V04 不兼容答案 | 新一轮先打开旧页填值，A 修改字段类型或删除该字段；R 提交/重载 | 显示“目录变更需要重新核对已填内容”，列出将舍弃的答案；明确点击“确认舍弃上述不兼容答案并重新填写”后才能继续；旧已提交记录不被删改 |

审批中不应悄悄用修改后的字段重写原获批内容。KAF 再做一次旧确认卡与新目录版本冲突检查：原卡失败后重新获取当前目录并确认，不能修改旧卡的原始版本冒充原确认。

### 9.2 查找与关联（Q01–Q05）

| 用例 | 操作 | 预期 |
|---|---|---|
| Q01 目录查找 | R 在 `/service-catalog` 搜本轮目录名称，再清除搜索；检查禁用/无权目录 | 找到可申请条目；无权或禁用条目不能被成功申请 |
| Q02 Ticket 查找 | `/tickets` 清除之前保存的筛选，按准确编号及本轮唯一标题分别查；组合状态筛选再清除 | 定位同一 WorkItem；不能因旧筛选误判丢单；匹配异常记搜索缺陷 |
| Q03 SR 关联 | `/service-requests` 找本轮申请；点击进入详情 | 最终指向 `/tickets/{workItemId}`；专业 ID 不当成 WorkItem ID；旧详情重定向丢单需记缺陷 |
| Q04 流程查找 | `/approvals` 用业务编号/流程 key 核对待办；`/workflow/instances` 查看本轮实例 | 正确节点、候选组、任务和 WorkItem 关联；必要时用 GET 分页核对，不能靠第一页断言不存在 |
| Q05 审计与隔离 | 从统一详情查看审批、历史、关联、通知；另用无权限账号直接访问本轮 URL | 决策人、时间、字段、结果可追溯；无权拒绝且不泄露内容。跨租户测试需事先准备独立测试账号与对象 |

不默认支持按任意自定义字段检索。若有此需求，先登记“按 office_location 查找”为扩展能力，再检查是否有真实筛选入口及服务端支持。

### 9.3 异常场景（X01–X07）

| 用例 | 操作及适用边界 | 预期 |
|---|---|---|
| X01 重复确认 | 专用拒绝申请提交按钮连续点击，或对同一次待确认结果使用原尝试重试 | 同一确认不产生两条 WorkItem/两个流程；不要创建全新卡片来声称重放通过 |
| X02 越权审批 | 非候选测试账号对本轮审批任务调用已核实的决策接口 | 拒绝、状态/决策不变；UI 显示按钮不能证明有权限 |
| X03 Worker 未处理 | 仅在已协调的独立消费者测试环境安排最终审批时 Worker 不运行，然后按原配置恢复 | 在等待阶段不显示授权完成；恢复后同一任务继续，无重复 add；共享 Worker 不做随意停机实验 |
| X04 无效目录/policy/映射 | 在独立负向目录中移除必要配置或使用无效版本，尝试发布/创建 | 明确失败，不降级成无需审批或自由 grant；字段和专业记录不半创建 |
| X05 未知服务任务 | 仅测试流程使用未注册类型，先尝试验证/发布；若可发布则用无外部权限对象运行 | 验证拒绝或执行阻塞并可观察，不能跳过节点后报告成功 |
| X06 外部结果未知 | 实际发生 unknown 时，保存 task/event/correlation 和时间；O 只读核对 | ITSM/KAF 显示待核查，没有伪造 verifiedAt/completed；后续 GET 为成员也不补造原首次验证 |
| X07 原回执重放 | 原完成 payload 确实已保存时，按部署手册 7.3 节由 O 重放原动作 | 只有原结果；verifiedAt/expiresAt 不变，原执行不再调用 grant。页面刷新不是协议重放测试 |

X06 不要求人为打断真实 Graph 写操作。故障注入在无副作用独立环境另做；本轮没发生就记 NOT RUN。unknown 不允许 force-resend/requeue、改成 pending、换新 run/step/key 重做授权。`reconcile` 会写审计结论，不属于只读诊断；具体处置按部署手册 7.4 节。

## 10. 管理员 API 补充核对

这些操作由具备目标权限的账号使用已登录的 API 客户端执行，API base 以本轮登记为准。cookie 模式使用同会话 CSRF；不要把真实 bearer 填到手册。响应证据只保留必要去敏字段。

| 用途 | 方法与路径（均以 `/api/v1` 为前缀） | 注意 |
|---|---|---|
| 目录定义 | GET `/service-catalogs/{catalogId}` | fields、版本、policy、process key |
| 更新目录 | PUT `/service-catalogs/{catalogId}` | 必须最新 expectedCatalogVersion；字段变更提交完整 fields |
| 工单详情 | GET `/tickets/{workItemId}` | recordClass、status、customFields、actions、时间字段 |
| 专业详情 | GET `/service-requests/by-ticket/{workItemId}` | id/ticketId、fulfillmentState、accessResult、customFields |
| 任务分页 | GET `/bpmn/tasks?processDefinitionKey=sslvpn_approval_flow&page=1&pageSize=100` | 按返回分页继续，核对业务编号/业务 ID；不解除租户授权 |
| 领取 | PUT `/bpmn/tasks/{taskId}/claim`，body `{}` | 使用任务列表原 ID，不猜测编号 |
| 决策 | POST `/bpmn/tasks/{taskId}/decisions`，body `{"action":"approve","comment":"本轮审批意见"}` | 拒绝用 reject；必须有意见；不用 complete 伪造批准变量 |
| 决策历史 | GET `/tickets/{workItemId}/approval-decisions` | 两级真实审批 actor/decision/time |
| 委派观测 | GET `/delegated-executions?eventId={eventId}` | 仅 O；event ID 来自本申请，不全租户导出 |
| 关闭补充 | POST `/tickets/{workItemId}/close` | 严格遵守 L03 的阶段与权限限制 |

**服务端字段负向校验**：在本轮禁用外部执行的测试阶段，保留一个合法、尚未提交的新申请请求体。在 API 客户端分别删除必填值、将 number 改为 `abc`、将 select 改为未配置选项、添加未定义字段，提交到 `POST /api/v1/service-requests`。每个不同请求使用独立 `Idempotency-Key`；原封不动重试同一请求才复用原 key。必须保留当前目录/字段版本和其他合法基础字段，避免只测出版本或身份错误。

预期业务校验拒绝，并核对没有本次 WorkItem/专业记录/流程；错误信息指向该字段。非法日期单列观察，不预设已具备完整服务端格式验证。此类请求不得绕过正常授权；若意外创建了测试记录，记录缺陷、隔离其流程并由负责人处理，不让它进入真实授权。

**状态边界负向校验**：在独立、无授权测试记录上，使用已核实接口 `PUT /tickets/{workItemId}/status` 验证不允许的目标状态，body 如 `{"status":"in_progress"}`。L04 仅用于已关闭测试记录；记录响应和状态未变化。不要把这个接口当成推进 SSLVPN 正向流程的操作步骤。

## 11. 本轮收尾与恢复（C01–C04）

| 用例 | 谁操作、做什么 | 通过证据 |
|---|---|---|
| C01 归属核对 | O 比较测试前非成员基线、原 add/可能写入、本轮 subject/group | 能明确哪些权限由本轮产生；unknown 也必须核对外部状态 |
| C02 权限恢复 | 恢复负责人按[部署手册第 8 节](../deployment/sslvpn-wsl-deployment-and-manual-verification.md#8-固定测试权限恢复)调用既有 remove 流程，只移除本轮测试新增权限 | 保留实际 DELETE 响应及后续非成员查询；不能只看工具 success；不反复执行删除 |
| C03 配置恢复 | A/O 核对本轮新增目录与激活版本；在不影响运行实例的前提下禁用专用目录或恢复原绑定 | 原配置可追溯，不删除历史 WorkItem/审批/字段/成功回执；不批量清理库 |
| C04 最终复查 | R 再开历史申请；O 检查本轮无遗留可执行任务和未交接 unknown | 历史完成/拒绝状态保留；恢复权限不回写“申请失败”；遗留问题有负责人 |

## 12. 可复制的测试执行记录

把下表复制到本轮私有验收记录，按用例逐行填写。空白是执行人填写栏，不是预先通过。截图、完整响应和身份资料保存在受限证据目录，仓库仅保留去敏结论。

| 用例 ID / 入口 | 角色 | WorkItem 编号/ID | 步骤/时间 | 预期 | 实际结果 | PASS/FAIL/BLOCKED/NOT RUN/N/A | 证据引用 | 问题/负责人 |
|---|---|---|---|---|---|---|---|---|
| D01 | | | | 环境与版本可核对 | | | | |
| F01 | | | | 五类字段保存回读一致 | | | | |
| F08 | | | | 字段快照保存且页面回显 | | | | |
| R01 | | | | 一级拒绝、0 add | | | | |
| R03 | | | | 二级拒绝、0 add | | | | |
| AC01–AC04 | | | | 审批链配置/快照/真实决策区分且可追溯 | | | | |
| P01–P07 / FORM | | | | 表单入口完整授权履约 | | | | |
| P01–P07 / KAF | | | | 对话入口完整授权履约 | | | | |
| L02 / L03 | | | | 分别记录 UI 与 API 关闭 | | | | |
| V01–V04 | | | | 版本冲突与历史快照 | | | | |
| Q01–Q05 | | | | 查找与权限隔离 | | | | |
| C01–C04 | | | | 本轮权限恢复及历史保留 | | | | |

验收结论分别填写：**环境部署、表单 UI、表单 API、BPMN、Ticket 生命周期、KAF 受理、真实履约、关闭/取消、异常恢复、测试清理**。核心步骤 BLOCKED/FAIL 时不能写“全流程通过”；扩展测试未执行则逐项列出，不能用历史测试报告代替。

## 13. 维护依据与相关文档

本手册的代码定位是为了复核步骤，不要求普通测试人员阅读代码：

- 字段编辑与申请：[CustomFieldsEditor](../../itsm-frontend/src/components/common/CustomFieldsEditor.tsx)、[目录管理](../../itsm-frontend/src/app/(main)/admin/service-catalogs/page.tsx)、[申请页](../../itsm-frontend/src/app/(main)/service-catalog/request/[id]/page.tsx)。
- 回显与审批：[TicketDetail](../../itsm-frontend/src/components/ticket/TicketDetail.tsx)、[ServiceRequestPanel](../../itsm-frontend/src/components/ticket/ServiceRequestPanel.tsx)、[审批中心](../../itsm-frontend/src/app/(main)/approvals/page.tsx)。
- 字段服务端：[字段定义校验](../../itsm-backend/service/field_definition_creation.go)、[字段值校验及快照](../../itsm-backend/service/field_value_service.go)、[专业详情](../../itsm-backend/handlers/service_request/handler.go)。
- 履约结果：[成功回执](../../itsm-backend/handlers/service_request/access_completion.go)、[专业状态投影](../../itsm-backend/handlers/service_request/access_fulfillment.go)、[验证完成契约](../contracts/kaf-verified-access-completion.md)。
- 分模块用例：[Ticket](test-cases/TC-TICKET.md)、[服务目录](test-cases/TC-SERVICE-CATALOG.md)、[服务请求](test-cases/TC-SERVICE-REQUEST.md)、[流程引擎](test-cases/TC-WORKFLOW.md)。
- KAF 源码在独立仓库：`src/acp/config.py`、`frontend/vite.config.ts`、`scripts/procedures/graph_vpn_access_grant.md`、`docs/kaf2/operations/database-provisioning.md`；执行时按已登记的 KAF SHA 核对。
