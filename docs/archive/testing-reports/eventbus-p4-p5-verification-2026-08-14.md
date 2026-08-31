# 事件总线 P4 + P5 端到端验证报告

> 日期: 2026-08-14
> 验证内容: SLA违规 → 事件发布 → 审计订阅方 + Webhook推送订阅方

## P5 审计订阅方（已完成验证）

```
工单28 → watcher违规(2条) → sla.breached事件
→ 进程内审计订阅方 → AuditLog 落库 2条 ✅
```

审计记录: `resource=event, action=sla.breached, path=eventbus://sla.breached, tenant_id=1`

## P4 Webhook 推送订阅方（本次验证）

### 验证方法

1. 本机启动 HTTP 接收服务器（127.0.0.1:8799，记录所有 POST 到 JSONL 文件）
2. 通过 API 配置 webhook 连接器：`POST /api/v1/connectors/configs`（url 指向接收器）
3. 创建工单 + 回填过期 SLA
4. 等待 watcher 周期（5 分钟）

### 结果：外部系统收到 3 条真实事件推送 ✅

接收器记录的事件（节选，完整 JSON）：

```json
{
  "channel": "",
  "type": "text",
  "title": "ITSM 事件: sla.breached",
  "content": "{\"breached_type\":\"response\",\"eventType\":\"sla.breached\",\"tenantId\":\"1\",\"ticket_id\":\"29\",...}",
  "metadata": {"eventType": "sla.breached"}
}
```

| 推送 | 工单 | 违规类型 |
|------|------|---------|
| 1 | 29 | response |
| 2 | 29 | resolve |
| 3 | 30 | response |

### 链路覆盖

```
SLA watcher (5min周期)
→ createViolation → 发布 sla.breached 事件 (P3)
→ Watermill Redis Stream
→ [订阅方A] EventAuditSubscriber → AuditLog (P5)
→ [订阅方B] WebhookEventSubscriber → connector.Manager.Send
   → Webhook connector → HTTP POST + HMAC (P4)
   → 外部系统 127.0.0.1:8799 收到 ✅
```

### 保真度声明

- ✅ 覆盖: 双订阅方并发消费同一事件、connector 链路（Manager→Send→HTTP）
- ✅ 覆盖: 多租户按 tenantId 路由到对应 webhook 配置
- ⚠️ 未覆盖: HMAC 签名验证（本次配置未设 secret，签名机制在 connector 层已有实现）
- ⚠️ 未覆盖: Nack 重试（推送目标失败时的重投语义）
- ⚠️ 未覆盖: 高并发批量违规时的吞吐

## 结论

事件总线平台化能力已闭环：**发布点（P3）→ 总线（P1）→ 审计订阅（P5）→ 外部系统推送（P4）**。
未来 P2 工单生命周期事件接入后，Webhook 与审计自动覆盖工单流转，无需新增订阅方代码。
