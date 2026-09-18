# B4 SLA 业务日历落地执行证据

- 状态：**数据已写入（EXECUTED 2026-09-14）；截止时间计算验收 = BLOCKED（能力差额）**。
- 目标：`ga-itsm-20260914 / itsm_ga_ready`，租户 `tenant_id=1`。
- 依据：`docs/review/2026-09-14-config-mapping-workbook.md` §3。
- 生成器：`scripts/migrate_config_seed/generate_b4_sql.py`；数据：`data/b4_business_hours.json`、`data/b4_makeup_days.json`。
- 生成 SQL sha256 `ff3f472b64015b9fd9f03f1d3d50be7ea07f992d673b9af2869ac8ab3462f82c`；写入前备份 `itsm_ga_ready-pre-b4.pgdump`（sha256 `a644aeeb01d6805142771ca6f18d8686598ca62c94a1398311a7ff733500d421`）。

## 1. 采用的日历（用户确认）

| 项 | 值 |
| --- | --- |
| `work_days` | `[1,2,3,4,5]`（周一至周五） |
| `start_time` / `end_time` | **`09:00` / `18:00`**（用户选择新系统默认时段） |
| `time_zone` | `Asia/Shanghai`（写入，但**运行时不消费**，见 §3） |
| `holiday_list` | **89** 天：2024=28、2025=28、2026=33（与补班交集 0） |

- 已写入 7 条 seed `sla_definitions`（`UPDATE 7`）；复核：7/7 含 `holiday_list`、长度均 89、结构为 `{work_days,start_time,end_time,time_zone,holiday_list}`。
- 幂等复跑：`UPDATE 0`（守卫 `business_hours IS DISTINCT FROM '<json>'`）。Phase 1 不变量与账本不变。

## 2. 补班日（19 天，已登记，**当前不可表达**）

`data/b4_makeup_days.json` 记录 2024–2026 的 19 个补班日（如 2024-02-04、2025-01-26、2026-02-28…）。解析器 `work_days` 为固定星期集合，**无法表达"周末上班"**，故未写入 `business_hours`，以免制造"已生效"的假象。

## 3. 能力差额（阻塞 SLA 截止计算验收）

| 差额 | 证据 | 影响 |
| --- | --- | --- |
| `time_zone` 不被消费 | `service/ticket_sla_service.go` 内 `time_zone` 仅出现在注释；`parseBusinessHoursConfig` 只读 `work_days`/`start_time`/`end_time`/`holiday_list` | 跨时区部署时截止时刻可能偏移 |
| 午休不可表达 | 旧日历为 `08:30–12:00` + `13:00–17:30`（净 8h）；解析器为单一连续时段 | 日净工时不同（本次 09:00–18:00 = 9h） |
| 周末补班不可表达 | `work_days` 为固定星期 | 补班日截止时间为 0（不计算） |

**结论**：本次仅落地 JSON 数据；**不得据此宣称 SLA 截止计算与新系统等价**。按总设计 G7，上述差额存在时相关 SLA 验收阻塞，需产品侧新增"分段时段/补班例外/时区"能力或经批准接受差异后再验收。

## 4. 代码级佐证（本分支）

在 `itsm-backend` 运行既有单元测试，佐证解析器确实消费 `work_days` 与 `holiday_list`：

```
go test ./service/ -run 'TestTicketSLAService_calculateDeadlineWithBusinessHours_EmptyCalendarUses24x7|TestBusinessHoursAdjustment|TestBPMNSLAService_BusinessHoursCalculation' -count=1
→ PASS（工作时间计算-跨越周末 / 工作时间内 / 超过当天工作时间；空日历=24x7）
```

> 说明：该佐证在本任务分支上执行；目标运行时为固定制品，相关路径假定等价，但 **§3 差额不受此佐证影响**。
