-- Migration: 20260815_ticket_template_priority_normalize
-- Description: 修正 ticket_templates.priority 的历史脏数据。
--
-- 根因：历史遗留的模板 priority 用了 SLA 优先级代码（P0-P4），
-- 而工单创建接口（CreateTicketRequest.Priority）要求标准优先级
-- （low/medium/high/critical/urgent）。前端把模板 priority 原样传给后端，
-- 导致「选择了模板的工单创建」报 400（Priority oneof 校验失败）。
--
-- 映射依据 SLA 定义（seeder）：P0=urgent, P1=high, P2=medium, P3=low, P4=low。

UPDATE ticket_templates
SET priority = CASE priority
    WHEN 'P0' THEN 'urgent'
    WHEN 'P1' THEN 'high'
    WHEN 'P2' THEN 'medium'
    WHEN 'P3' THEN 'low'
    WHEN 'P4' THEN 'low'
    ELSE priority
END
WHERE priority IN ('P0', 'P1', 'P2', 'P3', 'P4');
