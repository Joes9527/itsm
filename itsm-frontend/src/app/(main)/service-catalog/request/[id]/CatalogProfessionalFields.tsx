'use client';
import { DatePicker, Form, Input, Select } from 'antd';
import type { WorkItemRecordClass } from '@/lib/api/work-item-creation';
const levels = ['low', 'medium', 'high', 'critical'].map(value => ({ value, label: value }));
const text = (section: string, name: string, label: string, required = false) => (
  <Form.Item key={name} name={[section, name]} label={label} rules={[{ required }]}>
    <Input.TextArea rows={2} />
  </Form.Item>
);
const choice = (
  section: string,
  name: string,
  label: string,
  options = levels,
  required = false
) => (
  <Form.Item key={name} name={[section, name]} label={label} rules={[{ required }]}>
    <Select options={options} />
  </Form.Item>
);
const jsonRule = {
  validator: (_: unknown, value: string) => {
    try {
      if (
        value &&
        (typeof JSON.parse(value) !== 'object' ||
          Array.isArray(JSON.parse(value)) ||
          JSON.parse(value) === null)
      )
        throw new Error();
      return Promise.resolve();
    } catch {
      return Promise.reject(new Error('请输入有效 JSON 对象'));
    }
  },
};
export function CatalogProfessionalFields({ targetClass }: { targetClass?: WorkItemRecordClass }) {
  return (
    <>
      {targetClass !== 'service_request_item' && (
        <Form.Item name='ciIds' label='关联配置项 ID'>
          <Select mode='tags' tokenSeparators={[',']} />
        </Form.Item>
      )}
      {targetClass === 'generic' && text('generic', 'category', '工单分类')}
      {targetClass === 'problem' && (
        <>
          {text('problem', 'category', '问题分类')}
          {text('problem', 'rootCause', '根本原因')}
          {text('problem', 'impact', '影响范围')}
        </>
      )}
      {targetClass === 'incident' && (
        <>
          {choice(
            'incident',
            'type',
            '事件类型',
            ['incident', 'service_request', 'security_event', 'alert'].map(value => ({
              value,
              label: value,
            })),
            true
          )}
          {choice(
            'incident',
            'source',
            '事件来源',
            [
              { value: 'manual', label: '手工' },
              { value: 'user', label: '用户' },
            ],
            true
          )}
          {choice('incident', 'severity', '严重程度')}
          {choice('incident', 'impact', '影响程度')}
          {choice('incident', 'urgency', '紧急程度')}
          {text('incident', 'category', '事件分类')}
          {text('incident', 'subcategory', '事件子分类')}
          <Form.Item name={['incident', 'detectedAt']} label='发现时间'>
            <DatePicker showTime />
          </Form.Item>
          <Form.Item
            name={['incident', 'metadata']}
            label='事件补充数据（JSON）'
            rules={[jsonRule]}
          >
            <Input.TextArea />
          </Form.Item>
          <Form.Item
            name={['incident', 'impactAnalysis']}
            label='影响分析（JSON）'
            rules={[jsonRule]}
          >
            <Input.TextArea />
          </Form.Item>
        </>
      )}
      {targetClass === 'change_request' && (
        <>
          {text('change', 'justification', '变更理由', true)}
          {choice(
            'change',
            'type',
            '变更类型',
            ['normal', 'standard', 'emergency'].map(value => ({ value, label: value })),
            true
          )}
          {choice('change', 'impactScope', '影响范围', levels.slice(0, 3), true)}
          {choice('change', 'riskLevel', '风险等级', levels.slice(0, 3), true)}
          {text('change', 'category', '变更分类')}
          {text('change', 'implementationPlan', '实施计划', true)}
          {text('change', 'rollbackPlan', '回退计划', true)}
          <Form.Item name={['change', 'plannedStartDate']} label='计划开始时间'>
            <DatePicker showTime />
          </Form.Item>
          <Form.Item name={['change', 'plannedEndDate']} label='计划结束时间'>
            <DatePicker showTime />
          </Form.Item>
          <Form.Item name={['change', 'affectedCis']} label='受影响配置项'>
            <Select mode='tags' tokenSeparators={[',']} />
          </Form.Item>
          <Form.Item name={['change', 'relatedTicketNumbers']} label='关联工单编号'>
            <Select mode='tags' tokenSeparators={[',']} />
          </Form.Item>
        </>
      )}
    </>
  );
}
