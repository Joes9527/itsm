# 事件总线接线设计（Event Bus Wiring Design）

> 状态: 设计稿（不实施——工单生命周期正在重构，避免冲突）
> 日期: 2026-08-13
> 前置依赖: 用户完成 ticket 生命周期 / 流程重构

---

## 1. 现状盘点

### 已有基础设施（全部就绪，只缺发布点）

| 组件 | 位置 | 状态 |
|------|------|------|
| Watermill + Redis Stream 总线 | `pkg/eventbus/eventbus.go` | ✅ 实现完成（Publish/Subscribe/Close） |
| 全局实例注册 | `internal/bootstrap/app.go:215` `eventbus.SetGlobalEventBus(eventBus)` | ✅ 启动时注入 |
| 事件接口 | `handlers/shared/values.go:92` `EventBus interface` | ✅ |
| **6 个域事件类型** | `service/common/event/event.go` | ✅ 已定义（见下） |
| 替代 InMemory/Redis 事件系统 | `service/common/event/event_bus.go`, `redis_event_bus.go` | ⚠️ 第二套，未接线 |

### 已定义的事件类型

| 事件 | EventType() | 构造工厂 |
|------|-------------|---------|
| `TicketCreatedEvent` | `ticket.created` | `NewTicketCreatedEvent` |
| `TicketAssignedEvent` | `ticket.assigned` | `NewTicketAssignedEvent` |
| `TicketStatusChangedEvent` | `ticket.status.changed` | `NewTicketStatusChangedEvent` |
| `SLABreachedEvent` | `sla.breached` | `NewSLABreachedEvent` |
| `ApprovalCompletedEvent` | `approval.completed` | `NewApprovalCompletedEvent` |
| `AITriageCompletedEvent` | `ai.triage.completed` | `NewAITriageCompletedEvent` |

### 关键缺口

`GetGlobalEventBus().Publish()` 全代码库**零调用**。基础设施与事件类型脱节——事件定义好了但没人发，总线建好了但没人用。

---

## 2. 三个必须先修的底层缺陷

### 缺陷 A：Topic 名用 Go 类型名，脆弱

`eventbus.go:73`：`eventType := fmt.Sprintf("%T", event)` → topic 是 `*event.TicketCreatedEvent`。一旦结构体改名/移动包，topic 断裂，已部署的订阅方静默失联。

**修**：改用稳定的 `EventType()` 字符串（`ticket.created`）作为 topic。

```go
// 修改 Publish 签名，接受 DomainEvent 而非 interface{}
func (eb *WatermillEventBus) Publish(event event.DomainEvent) error {
    topic := event.EventType()  // "ticket.created" — 稳定、可读、可文档化
    ...
}
```

### 缺陷 B：JSON 序列化丢失载荷

`BaseEvent` 的 `eventType/tenantID/occurredAt/payload` 全是**未导出字段**，`json.Marshal(event)` 直接序列化整个事件结构时这些字段被跳过；`Payload()` 恒为 nil。订阅方收到的事件 JSON 只有壳字段（ticket_id 等具体字段在，但 eventType/tenantID/occurredAt 丢失）。

**修**：定义统一的**信封（Envelope）**结构，Publish 时包装：

```go
// 信封：解决"事件元数据序列化丢失"问题
type Envelope struct {
    EventType  string          `json:"eventType"`   // "ticket.created"
    TenantID   string          `json:"tenantId"`
    OccurredAt time.Time       `json:"occurredAt"`
    Payload    json.RawMessage `json:"payload"`     // 具体事件字段
}

func (eb *WatermillEventBus) Publish(event event.DomainEvent) error {
    payload, _ := json.Marshal(event)      // 具体事件壳字段
    env := Envelope{
        EventType:  event.EventType(),
        TenantID:   event.TenantID(),
        OccurredAt: event.OccurredAt(),
        Payload:    payload,
    }
    ...
}
```

### 缺陷 C：两套事件系统并存

`service/common/event/event_bus.go`（InMemory）+ `redis_event_bus.go` 与 `pkg/eventbus`（Watermill）功能重叠，都未接线。第二套还包含一个 `TicketDomainService`（`handlers/ticket/aggregate.go:597`）试图发布事件但从未被实例化。

**决策**：**以 `pkg/eventbus`（Watermill+Redis）为唯一实现**；删除 `service/common/event/event_bus.go` + `redis_event_bus.go` 和未实例化的 `TicketDomainService` 发布逻辑（保留 `event.go` 中的事件类型定义，它是好的）。

---

## 3. 发布点设计

### 3.1 原则

1. **发布不阻塞主流程**：Publish 失败只 `logger.Warnw`，绝不让建单失败
2. **异步可选的**：同步发布已足够快（Redis 写入 ~1ms），保持同步+错误吞噬更简单可审计
3. **租户隔离**：每个事件带 tenantID，订阅方按租户过滤
4. **发布点最小集**：只发"外部可感知的状态变化"，不发内部中间态

### 3.2 发布点清单

| # | 位置（重构后对应方法） | 事件 | 触发时机 |
|---|----------------------|------|---------|
| 1 | `TicketService.CreateTicket` 末尾 | `TicketCreatedEvent` | 工单落库成功后 |
| 2 | 自动分派成功后 | `TicketAssignedEvent` | 自动/手动指派 |
| 3 | `UpdateTicketStatus` | `TicketStatusChangedEvent` | 每次合法状态流转 |
| 4 | `ResolveTicket` | `TicketStatusChangedEvent` (newStatus=resolved) | 解决 |
| 5 | `CloseTicket` | `TicketStatusChangedEvent` (newStatus=closed) | 关闭 |
| 6 | `sla_monitor_service.createViolation` | `SLABreachedEvent` | 新违规产生 |
| 7 | BPMN 审批完成回调 | `ApprovalCompletedEvent` | 审批终态 |
| 8 | AI 分诊完成 | `AITriageCompletedEvent` | 分诊建议产生 |

示例（重构后的 CreateTicket）：

```go
// 建单成功、SLA 附加完成后
if bus := eventbus.GetGlobalEventBus(); bus != nil {
    ev := event.NewTicketCreatedEvent(
        strconv.Itoa(tenantID),
        strconv.Itoa(tkt.ID),
        tkt.TicketNumber, tkt.Title,
        string(tkt.Priority),
        strconv.Itoa(tkt.RequesterID),
    )
    if err := bus.Publish(ev); err != nil {
        s.logger.Warnw("failed to publish ticket.created event",
            "ticket_id", tkt.ID, "error", err)
    }
}
```

---

## 4. 订阅方设计

### 4.1 Webhook 连接器（第一订阅方）

利用现有 `connector/builtin/webhook/` 的配置（URL + HMAC secret），新增事件订阅：

```go
// webhook connector 初始化时
for _, topic := range []string{"ticket.created", "ticket.status.changed", "sla.breached"} {
    eventbus.GetGlobalEventBus().Subscribe(topic, handler)
}
```

handler 逻辑：按租户+事件类型匹配已配置的 webhook → HTTP POST 信封 JSON → HMAC-SHA256 签名 → 失败重试（Watermill Nack 自动重投）。

### 4.2 飞书群通知

现有硬编码 `Get(tenantID, "feishu")` 同步逻辑**迁出**核心流，改为订阅 `ticket.created` + `ticket.assigned`。

### 4.3 审计（AGENT.md 合规）

订阅 `ticket.status.changed` → 写 `AuditLog`。这同时解决审计缺口（当前流转零审计）。

### 4.4 自动化规则扩展

现有规则引擎从"创建后直接调用"改为"订阅 ticket.created 触发"——规则引擎获得独立演进能力。

---

## 5. 实施顺序（重构完成后）

| 阶段 | 内容 | 规模 |
|------|------|------|
| **P1** | 修缺陷 A（topic 稳定名）+ 缺陷 B（Envelope） | 小：只改 `pkg/eventbus` + `shared.EventBus` 签名 |
| **P2** | 发布点 1-5（ticket 生命周期，重构后的代码上） | 中：5 个发布点 |
| **P3** | 发布点 6-8（SLA/审批/AI） | 小 |
| **P4** | Webhook 订阅方 + 飞书订阅方迁移 | 中 |
| **P5** | 审计订阅方 + 删除第二套事件系统 | 小 |
| **P6** | 自动化规则改为订阅触发 | 中 |

---

## 6. 兼容与风险

- **签名变更**：`shared.EventBus.Publish(interface{})` → `Publish(DomainEvent)`。现有零调用方，无兼容负担
- **飞书同步迁出核心流**：行为等价但时序改变（同步→异步）。需要灰度验证飞书通知时效
- **Redis 依赖**：事件总线在 Redis 不可用时 Publish 报错——已被"吞噬错误"原则覆盖，但需监控告警
- **消息丢失**：Redis Stream 持久化，重启不丢；Nack 重投有次数上限需配置
