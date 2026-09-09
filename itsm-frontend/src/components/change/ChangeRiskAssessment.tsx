'use client';

import React, { useEffect, useState } from 'react';
import { Alert, Button, Form, Input, Select } from 'antd';
import type { RiskAssessmentData } from '@/lib/api/change-api';

interface ChangeRiskAssessmentProps {
  changeId?: number;
  initialData?: Partial<RiskAssessmentData>;
  onSave?: (data: RiskAssessmentData) => void | Promise<void>;
  readOnly?: boolean;
}
export default function ChangeRiskAssessment({
  initialData,
  onSave,
  readOnly = false,
}: ChangeRiskAssessmentProps) {
  const [form] = Form.useForm<RiskAssessmentData>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    if (initialData) form.setFieldsValue(initialData);
  }, [initialData, form]);
  return (
    <Form
      form={form}
      layout='vertical'
      disabled={readOnly || saving}
      onFinish={async values => {
        setSaving(true);
        setError('');
        try {
          await onSave?.(values);
        } catch (e) {
          setError(e instanceof Error ? e.message : '保存失败');
        } finally {
          setSaving(false);
        }
      }}
    >
      {error && <Alert type='error' title={error} />}
      <Form.Item name='riskLevel' label='风险等级' rules={[{ required: true }]}>
        <Select
          options={[
            { value: 'low', label: '低' },
            { value: 'medium', label: '中' },
            { value: 'high', label: '高' },
          ]}
        />
      </Form.Item>
      {(
        [
          { name: 'riskDescription', label: '风险描述' },
          { name: 'impactAnalysis', label: '影响分析' },
          { name: 'mitigationMeasures', label: '缓解措施' },
          { name: 'contingencyPlan', label: '应急计划' },
          { name: 'riskOwner', label: '风险负责人' },
        ] as const
      ).map(field => (
        <Form.Item key={field.name} name={field.name} label={field.label}>
          <Input.TextArea rows={3} />
        </Form.Item>
      ))}
      {!readOnly && (
        <Button htmlType='submit' type='primary' loading={saving}>
          保存风险评估
        </Button>
      )}
    </Form>
  );
}
