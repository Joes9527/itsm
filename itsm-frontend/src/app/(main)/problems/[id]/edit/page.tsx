'use client';
import { WorkItemClassificationSelect } from '@/components/work-item/WorkItemClassificationSelect';
import { classificationInput, classificationUpdate } from '@/components/work-item/classification';


import React, { useState, useEffect } from 'react';
import { useRouter, useParams } from 'next/navigation';
import { Button, Card, Form, Input, Select, App, Row, Col, Space, Divider } from 'antd';
import { ArrowLeft, Save } from 'lucide-react';
import { ProblemApi } from '@/lib/api/problem-api';
import { useI18n } from '@/lib/i18n';

const { TextArea } = Input;
export default function ProblemEditPage() {
  const router = useRouter();
  const params = useParams();
  const id = params?.id as string;
  const { message } = App.useApp();
  const { t } = useI18n();
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [fetching, setFetching] = useState(false);
  const [problemData, setProblemData] = useState<any>(null);

  // Fetch problem data
  useEffect(() => {
    if (!id) return;

    let cancelled = false;
    const fetchProblem = async () => {
      setFetching(true);
      try {
        const resp = await ProblemApi.getProblem(Number(id));
        if (cancelled) return;
        const data = resp as any;
        setProblemData(data);
        form.resetFields();
        form.setFieldsValue({
          title: data.title,
          description: data.description,
          priority: data.priority,

          rootCause: data.rootCause,
          impact: data.impact,
        });
      } catch (error) {
        if (cancelled) return;
        message.error(t('problems.getFailed'));
        router.push('/problems');
      } finally {
        if (!cancelled) setFetching(false);
      }
    };

    fetchProblem();
    return () => { cancelled = true; };
  }, [id, form, router]);

  const handleSubmit = async (values: any) => {
    if (!id) return;

    setLoading(true);
    try {
      const { classification, status: _status, ...payload } = values;
      await ProblemApi.updateProblem(Number(id), { ...payload, version: problemData.version, ...classificationUpdate(classification, form.isFieldTouched('classification')) });
      message.success(t('problems.updateSuccess'));
      router.push(`/problems/${id}`);
    } catch (error) {
      message.error(t('problems.updateFailed'));
    } finally {
      setLoading(false);
    }
  };

  const handleCancel = () => {
    router.back();
  };

  return (
    <div className="p-6 min-h-screen bg-gray-50">
      <div className="mb-6">
        <Button
          type="link"
          icon={<ArrowLeft />}
          onClick={() => router.back()}
          style={{ paddingLeft: 0, color: '#666' }}
        >
          返回
        </Button>
      </div>

      <Card
        title={
          <span className="text-lg font-medium">编辑问题 - #{problemData?.id}</span>
        }
        loading={fetching}
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={handleSubmit}
          initialValues={{
            priority: 'medium',

          }}
        >
          <Row gutter={24}>
            <Col span={24}>
              <Form.Item
                name="title"
                label="问题标题"
                rules={[{ required: true, message: '请输入问题标题' }]}
              >
                <Input placeholder="请输入问题标题" />
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
                <Select placeholder="请选择优先级" options={[{ value: "low", label: "低" }, { value: "medium", label: "中" }, { value: "high", label: "高" }, { value: "critical", label: "紧急" }]} />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={24}>
            <Col span={24}>
              <Form.Item name="classification" label="分类">
                <WorkItemClassificationSelect initialCategoryId={problemData?.categoryId} />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={24}>
            <Col span={24}>
              <Form.Item name="description" label="问题描述">
                <TextArea rows={4} placeholder="请详细描述问题情况" />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={24}>
            <Col span={24}>
              <Form.Item name="rootCause" label="根本原因分析">
                <TextArea rows={4} placeholder="请详细描述问题的根本原因" />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={24}>
            <Col span={24}>
              <Form.Item name="impact" label="影响范围">
                <TextArea rows={3} placeholder="请描述问题的影响范围" />
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
