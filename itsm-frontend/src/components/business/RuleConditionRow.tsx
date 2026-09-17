'use client';

import React from 'react';
import { Col, Form, Input, Row, Select } from 'antd';
import { CTISelector } from './CTISelector';

/**
 * 工单规则条件行的共享呈现。
 *
 * 两个规则编辑器（分派规则、自动化规则）此前各自内联渲染同一组字段，
 * 分类条件的"仅当前 / 包含下级"语义必须与后端一致，因此集中到这里：
 * - 分类取值使用共享 CTISelector（与录入、维护界面同一数据契约）；
 * - 分类操作符只提供后端支持的四种，避免选出运行期必然失败的组合；
 * - scope 只在分类条件下出现，切换字段时清空，避免脏 scope 影响其它字段。
 */

export const RULE_CONDITION_FIELDS = [
  { value: 'status', label: '状态' },
  { value: 'priority', label: '优先级' },
  { value: 'category_id', label: '工单分类' },
  { value: 'department_id', label: '部门' },
  { value: 'requester_id', label: '申请人' },
  { value: 'assignee_id', label: '处理人' },
];

export const RULE_OPERATORS = [
  { value: 'equals', label: '等于' },
  { value: 'not_equals', label: '不等于' },
  { value: 'contains', label: '包含' },
  { value: 'in', label: '属于' },
  { value: 'not_in', label: '不属于' },
  { value: 'greater_than', label: '大于' },
  { value: 'less_than', label: '小于' },
];

/** 分类条件只支持后端已定义语义的操作符（其余算子会失败关闭）。 */
export const CTI_CONDITION_OPERATORS = RULE_OPERATORS.filter(option =>
  ['equals', 'not_equals', 'in', 'not_in'].includes(option.value),
);

export const CTI_CONDITION_SCOPE_OPTIONS = [
  { value: 'exact', label: '仅当前分类' },
  { value: 'subtree', label: '包含下级' },
];

export interface RuleConditionRowProps {
  /** Form.List 的行下标。 */
  index: number;
  /** Form.List 提供的行 key/name 载体。 */
  field: { key: number; name: number };
  /** 当前行的字段值（category_id 时展示分类选择器与范围）。 */
  fieldValue?: string;
  /** 字段切换回调：用于在离开分类条件时清理 scope。 */
  onFieldChange?: (value: string) => void;
}

export function RuleConditionRow({ index, field, fieldValue, onFieldChange }: RuleConditionRowProps) {
  const isCategory = fieldValue === 'category_id';
  return (
    <Row gutter={8} align="middle">
      <Col span={isCategory ? 6 : 8}>
        <Form.Item {...field} name={[field.name, 'field']} rules={[{ required: true }]}>
          <Select
            placeholder="选择字段"
            options={RULE_CONDITION_FIELDS}
            onChange={value => onFieldChange?.(value)}
          />
        </Form.Item>
      </Col>
      <Col span={isCategory ? 5 : 6}>
        <Form.Item {...field} name={[field.name, 'operator']} rules={[{ required: true }]}>
          <Select placeholder="操作符" options={isCategory ? CTI_CONDITION_OPERATORS : RULE_OPERATORS} />
        </Form.Item>
      </Col>
      <Col span={isCategory ? 8 : 10}>
        <Form.Item {...field} name={[field.name, 'value']} rules={[{ required: true }]}>
          {isCategory ? (
            // requiredDepth=0：规则可以指向任意层级节点；子树匹配由 scope 决定。
            <CTISelector requiredDepth={0} placeholder="选择分类节点" id={`rule-condition-category-${index}`} />
          ) : (
            <Input placeholder="值" />
          )}
        </Form.Item>
      </Col>
      {isCategory ? (
        <Col span={5}>
          <Form.Item {...field} name={[field.name, 'scope']} initialValue="exact" tooltip="仅当前分类：只命中该节点；包含下级：命中该节点及其所有下级">
            <Select options={CTI_CONDITION_SCOPE_OPTIONS} />
          </Form.Item>
        </Col>
      ) : null}
    </Row>
  );
}
