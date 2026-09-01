# Redis 专业编号序列服务

## 模块边界

`SequenceService` 只服务专业域标识。目前唯一生产调用方是 Incident 的
`incident_number`：

- Redis key：`sequence:incident:YYYYMM`
- 展示格式：`INC-YYYYMM-NNNNNN`
- Redis 不可用时：`IncidentService` 使用 Incident 表中的专业编号作为数据库回退

WorkItem 的 `tickets.ticket_number` 不属于本服务。它由
`repository/workitemnumber.PostgreSQLAllocator` 在调用方事务中分配，格式为
`TKT-YYYYMM-NNNNNN`，PostgreSQL 是唯一事实源。

## Incident 使用方式

```go
expiredAt := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, time.UTC)
sequence, err := sequenceService.GetNextSequenceWithExpiry(
    ctx,
    fmt.Sprintf("sequence:incident:%04d%02d", year, month),
    expiredAt,
)
incidentNumber := fmt.Sprintf("INC-%04d%02d-%06d", year, month, sequence)
```

`GetNextSequence`、`GetCurrentSequence` 和 `ResetSequence` 是通用 Redis 序列操作，
不得用于生成 WorkItem 编号。

## 配置

```yaml
redis:
  host: localhost
  port: 6379
  password: ""
  db: 0
```

Redis 连接失败时构造函数返回 `nil`，由 `IncidentService` 显式进入其专业编号数据库
回退；该回退不适用于 WorkItem 编号。
