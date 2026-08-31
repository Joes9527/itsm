# ITSM 业务完整性与可扩展性评估

> **[已归档]** 本文档已被 [architecture-and-roadmap-assessment-2026-08-26.md](../reviews/architecture-and-roadmap-assessment-2026-08-26.md)
> 逐条重查，其事件总线、Incident→Problem UI 等结论又被
> [architecture-product-assessment-2026-08-30.md](../../architecture-product-assessment-2026-08-30.md) 进一步核实更新，
> 请以 08-30 文档为准。本文档保留作为历史记录。

> 评估日期: 2026-08-13
> 方法: 三路并行代码审计（ITIL 流程闭环 / 平台能力 / 可扩展性）+ 生产环境 API 实测
> 视角: 真实用户能否走完流程？平台能否不改核心代码扩展？

---

## 一、ITIL 六大流程业务闭环

| 流程 | 状态 | 核心结论 |
|------|------|---------|
| **变更 Change** | ✅ 完整 | 唯一全生命周期可跑通的流程：RFC→风险→CAB审批→排程→实施→回滚→PIR |
| **发布 Release** | ✅ 完整 | 构建→BPMN审批桥→排程→实施→完成 |
| **服务请求 SR** | ✅ 完整 | 目录→动态表单→审批链解析→关联工单→履约→关闭 |
| **工单 Ticket** | 🟡 主链路通 | 创建→指派→SLA→处理→升级→解决→关闭均通；**但审批按钮坏、评价无UI入口** |
| **事件 Incident** | 🟡 有断裂 | 生命周期通；**但 UI 无关闭按钮、无"转问题"按钮、升级规则引擎是死代码** |
| **问题 Problem** | 🟡 有断裂 | CRUD+根因分析通；**但发布已知错误无路由（死代码）、从事件转换无UI** |

### 工单流程断裂点（实测确认）

```
✅ 创建 → new
✅ new → in_progress（首响时间自动记录）
✅ resolve 端点（强制填解决方案）
✅ → closed
✅ SLA 正确附加（Incident-P2-中, 120min/480min）
✅ BPMN 自动触发（high→urgent_flow, medium→general_flow）
❌ TicketDetail 的 批准/拒绝 按钮调用 updateTicketStatus('approved')
   → 状态机判非法 → 必然报错（正确通道是 workflow/approve，无UI入口）
❌ 工单评价 TicketRatingSection 无任何页面引用
```

### 跨流程联动断裂（ITIL 的核心价值所在）

| 联动 | 后端 | 前端 |
|------|------|------|
| 事件→问题转换 | ✅ 端点完整 | ❌ 零按钮 |
| 问题→已知错误发布 | ❌ 死代码无路由 | ❌ 独立CRUD无关联 |
| 已知错误→知识库 | ❌ 无端点 | — |
| 工单→BPMN审批 | 🟡 approve端点可用 | ❌ my-approvals 无批准按钮 |

---

## 二、平台能力端到端

| 能力 | 状态 | 关键发现 |
|------|------|---------|
| **BPMN 设计器** | ✅ 最强能力 | 设计→部署→版本→绑定→触发→实例→任务→审计全链路真实；bpmn-js 建模器 + 自定义引擎 + 审批节点（会签/或签/阈值/角色解析） |
| **SLA 管理** | ✅ 完整 | 定义→附加→监控（5min）→违规→升级（15min）→报表→仪表盘全通 |
| **服务目录** | 🟡 | 表单动态字段真实可用；**但 ITSMType 字段从未被设置（Incident/Change 路由配置不可达）、目录项无法绑定 SLA** |
| **CMDB** | 🟡 | CI类型/关系/拓扑/影响分析/对账真实；**但云发现任务只插 pending 行，无执行器无SDK——永远不会执行** |
| **知识库+RAG** | 🟡 | 混合检索+引用真实（pgvector 1536维+关键词降级）；**但版本控制/审批/评论前端页面全在而后端路由 404、KB 搜索不是 RAG、删除文章不移除向量** |
| **仪表盘报表** | 🟡 | 主仪表盘/工单/SLA/BPMN 仪表盘真实数据；**但 widget 定制、报表生成、导出全部是硬编码 stub** |

---

## 三、架构可扩展性（能否不改核心代码扩展）

| 扩展面 | 状态 | 结论 |
|--------|------|------|
| **自定义字段** | ✅ 坚实 | 唯一真正的扩展点：field_definitions+field_values 租户隔离、6+类型、事务替换、服务端必填校验 |
| **BPMN 自定义任务** | 🟡 | ServiceTaskHandler 注册机制真实可用；**但只执行4种元素类型**（userTask/endEvent/exclusiveGateway/serviceTask），脚本任务/并行网关/边界事件/子流程解析了但不执行 |
| **连接器** | 🟡 | 接口契约好（Manifest/Capability/Registry）；**但注册是编译期的、能力声明从未被派发、核心流硬编码 "feishu" 名字、NotifyTicketUpdate 是空stub、入站Router从未接线** |
| **AI Skill** | 🟡 | SkillRegistry 零注册、无声明式加载、无热插拔；**ToolRegistry 是硬编码 switch，写工具未实现**（权限/审计层反而扎实） |
| **自动化规则** | 🟡 | DB驱动+条件评估真实；**但只有创建时触发、条件无自定义字段、动作无 webhook/tag** |
| **多租户** | 🟡 | MSP API 存在；**但 CreateTenant 只建空壳，24 个种子函数全硬编码 default 租户——新租户零默认配置** |
| **事件总线** | ❌ | Watermill+Redis Stream 就绪；**但 Publish 零调用——没有任何核心流发事件**。这掐死了 webhook 订阅、skill 钩子、连接器生命周期联动 |

### Top 5 可扩展性阻塞

1. **事件总线死基础设施** — 零发布者。所有"订阅工单创建事件"类扩展不可能
2. **连接器/Skill 无运行时加载** — marketplace InstallItem 停在 DB 行（TODO 注释在）
3. **BPMN 只执行 4 种元素** — 业务用户建模能力受限
4. **工具/技能硬编码** — 每加一个工具都要改核心
5. **新租户零默认配置** — MSP 上架新租户要手工脚本

---

## 四、结论与建议顺序

### 业务完整性视角

**最痛的不是缺功能，是"后端做了、前端没接线"的断裂**——事件转问题、问题发已知错误、工单审批，全部是"服务端代码就绪、用户点不到"。

建议顺序：
1. **补前端入口**（1-2天）：IncidentDetail 加关闭+转问题按钮、TicketDetail 审批改走 workflow/approve、my-approvals 加批准按钮、问题页加"发布已知错误"
2. **接线死代码**（1天）：CreateKnownErrorFromProblem 注册路由
3. **权限种子补缺**（半天）：ticket:escalate、change:rollback、release:approve 等缺失项

### 可扩展性视角

**事件总线是杠杆点**——把 ticket 生命周期事件发布出去，连接器/webhook/自动化规则全部自动获得"响应事件"能力。

建议顺序：
1. **事件发布**（2-3天）：CreateTicket/StatusChange/Resolve/Close 发布到 eventbus；Webhook 连接器订阅
2. **连接器能力派发**（2-3天）：核心流从硬编码 "feishu" 改为按 Capability 分发；NotifyTicketUpdate 实现
3. **多租户种子参数化**（1-2天）：24 个 seed 函数的 default 租户改为参数，CreateTenant 触发全量种子
