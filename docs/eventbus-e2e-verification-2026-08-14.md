# 事件总线端到端验证报告

> 日期: 2026-08-14
> 验证内容: SLA违规 → 事件发布 → Redis Stream → 订阅方消费 完整链路

## 验证方法（真实生产路径）

1. 通过 API 创建工单（`POST /api/v1/tickets`，priority=high, type=incident）
2. SQL 回填 SLA 截止时间为过去（3h/2h 前）——模拟真实超时
3. 等待后台 SLA watcher 周期（5 分钟）
4. 检查违规记录 + Redis Stream + 独立订阅程序消费

## 结果：全链路通过 ✅

| 环节 | 结果 | 证据 |
|------|------|------|
| Watcher 检测违规 | ✅ | 2 条违规记录（response_time + resolution_time） |
| 事件发布到 Stream | ✅ | `XLEN sla.breached` = 2 |
| 信封结构 camelCase | ✅ | `{"eventType":"sla.breached","tenantId":"1","occurredAt":"...","payload":{...}}` |
| 载荷内容正确 | ✅ | `ticket_id:27, sla_policy_id:2, breached_type:response/resolve` |
| 订阅方消费 | ✅ | 独立 Go 订阅程序收到消息并解析信封成功 |
| 消息元数据 | ✅ | `metadata: {event_type: sla.breached}` |

## 真实消费的消息

```json
{
  "eventType": "sla.breached",
  "tenantId": "1",
  "occurredAt": "2026-08-14T11:08:13.720710828+08:00",
  "payload": {
    "ticket_id": "27",
    "sla_policy_id": "2",
    "breached_type": "response",
    "breached_at": "2026-08-14T11:08:13.715857096+08:00"
  }
}
```

## 验证覆盖与未覆盖（保真度声明）

- ✅ 覆盖: 后端 watcher → createViolation → Publish → Redis Stream 持久化 → 独立订阅方消费 → 信封解析
- ✅ 覆盖: 多租户元数据（tenantId）在信封中正确传递
- ⚠️ 未覆盖: Watermill 的 Nack/重试语义（消费失败重投）
- ⚠️ 未覆盖: 高并发下的消息顺序保证
- ⚠️ 未覆盖: Webhook 订阅方（P4 尚未实现，本次用独立程序模拟订阅方）

## 过程中发现并修复的问题

**旧二进制未重启**：首次验证时 Stream 为空，根因是 08:30 启动的后端进程未包含发布代码（重启命令曾超时失败）。重新构建+重启后链路立即打通。教训：部署后必须验证进程启动时间与二进制构建时间一致。
