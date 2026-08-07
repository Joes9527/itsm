'use client';

import React from 'react';
import { Row, Col, Form, Input, Select, Switch, Button, Typography } from 'antd';
import { Plus, Delete } from 'lucide-react';

const { Text } = Typography;

export interface CustomFieldsEditorProps {
  /** Form.List 挂载用的字段名，比如父级表单里的 "customFields" */
  name: string;
}

export function CustomFieldsEditor({ name }: CustomFieldsEditorProps) {
  return (
    <>
      <Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
        提交时会额外展示这些字段；除字段名/标签/类型/是否必填外，其它元数据（placeholder、默认值等）目前后端不持久化。
      </Text>
      <Form.List name={name}>
        {(fields, { add, remove }) => (
          <>
            {fields.map(({ key, name: fieldName, ...restField }) => (
              <Row gutter={8} key={key} align="middle" style={{ marginBottom: 8 }}>
                <Col span={6}>
                  <Form.Item
                    {...restField}
                    name={[fieldName, 'name']}
                    rules={[{ required: true, message: 'Field name required' }]}
                    style={{ marginBottom: 0 }}
                  >
                    <Input placeholder="字段名，如 environment" />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item
                    {...restField}
                    name={[fieldName, 'label']}
                    rules={[{ required: true, message: 'Label required' }]}
                    style={{ marginBottom: 0 }}
                  >
                    <Input placeholder="展示标签，如 环境" />
                  </Form.Item>
                </Col>
                <Col span={5}>
                  <Form.Item {...restField} name={[fieldName, 'type']} initialValue="text" style={{ marginBottom: 0 }}>
                    <Select
                      options={[
                        { value: 'text', label: 'Text' },
                        { value: 'textarea', label: 'Textarea' },
                        { value: 'number', label: 'Number' },
                        { value: 'date', label: 'Date' },
                        { value: 'select', label: 'Select' },
                      ]}
                    />
                  </Form.Item>
                </Col>
                <Col span={4}>
                  <Form.Item {...restField} name={[fieldName, 'options']} style={{ marginBottom: 0 }}>
                    <Input placeholder="选项(逗号分隔，仅Select)" />
                  </Form.Item>
                </Col>
                <Col span={2}>
                  <Form.Item
                    {...restField}
                    name={[fieldName, 'required']}
                    valuePropName="checked"
                    initialValue={false}
                    style={{ marginBottom: 0 }}
                  >
                    <Switch checkedChildren="必填" unCheckedChildren="选填" />
                  </Form.Item>
                </Col>
                <Col span={1}>
                  <Button type="text" danger icon={<Delete size={16} />} onClick={() => remove(fieldName)} aria-label="删除字段" />
                </Col>
              </Row>
            ))}
            <Form.Item style={{ marginBottom: 0 }}>
              <Button type="dashed" onClick={() => add({ type: 'text', required: false })} icon={<Plus size={16} />} block>
                添加自定义字段
              </Button>
            </Form.Item>
          </>
        )}
      </Form.List>
    </>
  );
}
