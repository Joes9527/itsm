'use client';
import { WorkItemClassificationSelect } from '@/components/work-item/WorkItemClassificationSelect';
import { classificationInput, classificationUpdate } from '@/components/work-item/classification';


import React, { useState, useEffect } from 'react';
import { useRouter, useParams } from 'next/navigation';
import { Button, Card, Form, Input, Select, message, Row, Col, Space, Divider } from 'antd';
import { ArrowLeft, Save } from 'lucide-react';
import { IncidentAPI } from '@/lib/api/incident-api';
import type { Incident, UpdateIncidentRequest } from '@/lib/api/incident-api';
import { useI18n } from '@/lib/i18n';

const { TextArea } = Input;

export default function IncidentEditPage() {
  const router = useRouter();
  const { t } = useI18n();
  const params = useParams();
  const id = params?.id as string;
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [fetching, setFetching] = useState(false);
  const [incidentData, setIncidentData] = useState<Incident | null>(null);

  // Fetch incident data
  useEffect(() => {
    if (!id) return;

    let isMounted = true;
    const fetchIncident = async () => {
      setFetching(true);
      try {
        const resp = await IncidentAPI.getIncident(Number(id));
        if (!isMounted) return;
        const data = resp as any;
        setIncidentData(data);
        form.resetFields();
        form.setFieldsValue({
          title: data.title,
          description: data.description,
          priority: data.priority,
          severity: data.severity,
        });
      } catch (error) {
        if (isMounted) {
          message.error(t('common.getFailed'));
          router.push('/incidents');
        }
      } finally {
        if (isMounted) {
          setFetching(false);
        }
      }
    };

    fetchIncident();
    return () => {
      isMounted = false;
    };
  }, [id, form, router]);

  const handleSubmit = async (values: any) => {
    if (!id || !incidentData) return;

    setLoading(true);
    try {
      const { classification, classificationReason, status: _status, ...payload } = values;
      const classificationTouched = form.isFieldTouched('classification');
      // 分类调整必须说明原因：与后端同一契约，避免"界面成功、后端拒绝"。
      if (classificationTouched && !String(classificationReason ?? '').trim()) {
        message.error('调整分类时必须填写原因');
        return;
      }
      await IncidentAPI.updateIncident(Number(id), { ...payload, version: incidentData.version, ...classificationUpdate(classification, classificationTouched, classificationReason) });
      message.success(t('incidents.updateSuccess'));
      router.push(`/incidents/${id}`);
    } catch (error) {
      message.error(t('incidents.updateFailed'));
    } finally {
      setLoading(false);
    }
  };

  const handleCancel = () => {
    router.back();
  };

  return (
    <div className="min-h-screen bg-page p-[16px] text-[13px] text-foreground md:p-[24px]">
      <div className="mb-6">
        <Button
          type="link"
          icon={<ArrowLeft />}
          onClick={() => router.back()}
          style={{ paddingLeft: 0, color: 'var(--color-text-secondary)' }}
        >
          返回
        </Button>
      </div>

      <Card
        title={
          <span className="text-lg font-medium">编辑事件 - {incidentData?.incidentNumber}</span>
        }
        loading={fetching}
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={handleSubmit}
          initialValues={{
            priority: 'medium',
            severity: 'medium',
            status: 'new',
          }}
        >
          <Row gutter={24}>
            <Col span={24}>
              <Form.Item
                name="title"
                label="事件标题"
                rules={[{ required: true, message: '请输入事件标题' }]}
              >
                <Input placeholder="请输入事件标题" />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={24}>
            <Col span={12}>
              <Form.Item
                name="priority"
                label="优先级"
                rules={[{ required: true, message: '请选择优先级' }]}
              >
                <Select placeholder="请选择优先级" options={[
                  { value: 'low', label: '低' },
                  { value: 'medium', label: '中' },
                  { value: 'high', label: '高' },
                  { value: 'urgent', label: '紧急' },
                ]} />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={24}>
            <Col span={12}>
              <Form.Item
                name="severity"
                label="严重程度"
                rules={[{ required: true, message: '请选择严重程度' }]}
              >
                <Select placeholder="请选择严重程度" options={[
                  { value: 'low', label: '低' },
                  { value: 'medium', label: '中' },
                  { value: 'high', label: '高' },
                  { value: 'critical', label: '严重' },
                ]} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="classification" label="分类">
                <WorkItemClassificationSelect initialCategoryId={incidentData?.categoryId} />
              </Form.Item>
              <Form.Item
                name="classificationReason"
                label="分类调整原因"
                tooltip="仅在调整分类时必填；后端会连同前后完整路径一起留痕"
              >
                <Input placeholder="例如：报障入口选错分类" maxLength={500} />
              </Form.Item>

            </Col>
          </Row>

          <Row gutter={24}>

            <Col span={12}>
              <Form.Item name="source" label="来源">
                <Select placeholder="请选择来源" allowClear options={[
                  { value: 'manual', label: '手动创建' },
                  { value: 'monitoring', label: '监控系统' },
                  { value: 'email', label: '邮件' },
                  { value: 'phone', label: '电话' },
                  { value: 'chat', label: '在线聊天' },
                  { value: 'api', label: 'API' },
                ]} />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={24}>
            <Col span={24}>
              <Form.Item name="description" label="事件描述">
                <TextArea rows={6} placeholder="请详细描述事件情况" />
              </Form.Item>
            </Col>
          </Row>

          <Divider />

          <Form.Item>
            <Space>
              <Button type="primary" htmlType="submit" icon={<Save />} loading={loading}>
                保存
              </Button>
              <Button onClick={handleCancel}>取消</Button>
            </Space>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
}
