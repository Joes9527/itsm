'use client';

/**
 * 服务目录申请页面
 * Bug 2 修复：原本 /service-catalog/request/[id] 路由 404
 * B10 修复：表单加上 compliance_ack / expire_at / delivery_time
 */

import React, { useState, useEffect, useRef } from 'react';
import { useParams, useRouter } from 'next/navigation';
import {
  Card,
  Form,
  Input,
  Button,
  Space,
  message,
  Typography,
  Breadcrumb,
  Checkbox,
  DatePicker,
  Select,
  Spin,
  Alert,
  Tag,
  Divider,
} from 'antd';
import { ArrowLeft, Clock, Send } from 'lucide-react';
import dayjs from 'dayjs';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import type { ServiceItem, CreateServiceRequestRequest } from '@/types/service-catalog';
import { useWorkItemCreation } from '@/lib/hooks/useWorkItemCreation';
import { CreationAttempts } from '@/components/work-item/CreationAttempts';
import { CreationRequester } from '@/components/work-item/CreationRequester';
import { CatalogProfessionalFields } from './CatalogProfessionalFields';
import { incompatibleCatalogAnswers, type IncompatibleCatalogAnswer } from './catalog-reload';
import { useAuthStore } from '@/lib/store/auth-store';

const { Title, Text, Paragraph } = Typography;
const { TextArea } = Input;

export default function ServiceCatalogRequestPage() {
  const params = useParams();
  const router = useRouter();
  const id = Number(params?.id);
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [catalog, setCatalog] = useState<ServiceItem | null>(null);
  const [revision, setRevision] = useState(0);
  const catalogRef = useRef<ServiceItem | null>(null);
  const [incompatibleAnswers, setIncompatibleAnswers] = useState<IncompatibleCatalogAnswer[]>([]);
  const creation = useWorkItemCreation();
  const serviceRequestTarget = catalog?.targetClass === 'service_request_item';
  const [fetching, setFetching] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const user = useAuthStore(state => state.user);

  useEffect(() => {
    if (!id) {
      setFetching(false);
      setFetchError('服务标识无效，请返回服务目录重新选择');
      return;
    }
    setFetching(true);
    setFetchError(null);
    let cancelled = false;
    ServiceCatalogApi.getService(String(id)).then(data => {
      if (cancelled) return;
      if (!data.catalogVersion || !data.formSchemaVersion || !['generic', 'incident', 'problem', 'change_request', 'service_request_item'].includes(data.targetClass || '')) {
        throw new Error('目录缺少有效目标或确认版本');
      }
      const incompatible = catalogRef.current
        ? incompatibleCatalogAnswers(catalogRef.current, data, form.getFieldsValue(true)) : [];
      setIncompatibleAnswers(incompatible);
      catalogRef.current = data;
      setCatalog(data);
      if (incompatible.length === 0) creation.newConfirmation();
    }).catch(() => { if (!cancelled) { setCatalog(null); setFetchError('服务信息加载失败，请重新读取并确认目录'); } })
      .finally(() => { if (!cancelled) setFetching(false); });
    return () => { cancelled = true; };
  }, [id, revision]);

  useEffect(() => {
    if (user && serviceRequestTarget) {
      form.setFieldsValue({ contactName: user.name, contactEmail: user.email });
    }
  }, [form, user, serviceRequestTarget]);

  const onFinish = async (values: any) => {
    setLoading(true);
    try {
      if (fetching) throw new Error('请等待读取目录完成');
      if (incompatibleAnswers.length) throw new Error('请先核对并确认舍弃不兼容答案');
      if (!catalog?.targetClass || !catalog.catalogVersion || !catalog.formSchemaVersion) throw new Error('请先读取并确认目录');
      const customFieldValues = (catalog.fields || []).map(field => ({ name: field.name, value: values.customFields?.[field.name] }))
        .filter(field => field.value !== undefined && field.value !== null && field.value !== '');
      const payload: CreateServiceRequestRequest = {
        catalogId: id, recordClass: catalog.targetClass, catalogVersion: catalog.catalogVersion, formSchemaVersion: catalog.formSchemaVersion,
        title: values.title, reason: values.reason, priority: values.priority, requesterId: values.requesterId,
        formData: { customFieldValues },
      };
      if (serviceRequestTarget) {
        Object.assign(payload, { contactName: values.contactName, contactEmail: values.contactEmail, quantity: Number(values.quantity || 1), expectedAt: values.expectedAt?.toISOString() });
        if (catalog.requiresInfraFields) Object.assign(payload, {
          costCenter: values.costCenter, dataClassification: values.dataClassification, needsPublicIp: !!values.needsPublicIp,
          sourceIpWhitelist: values.sourceIpWhitelist?.split(',').map((value: string) => value.trim()).filter(Boolean),
          complianceAck: !!values.complianceAck, expireAt: values.expireAt?.toISOString(),
        });
      } else {
        payload.ciIds = values.ciIds?.map(Number);
        if (payload.ciIds?.some(value => !Number.isInteger(value) || value <= 0)) throw new Error('配置项 ID 必须为正整数');
        if (catalog.targetClass === 'generic') payload.generic = values.generic || {};
        if (catalog.targetClass === 'problem') payload.problem = values.problem || {};
        if (catalog.targetClass === 'incident') payload.incident = { ...values.incident,
          detectedAt: values.incident?.detectedAt?.toISOString(),
          metadata: values.incident?.metadata ? JSON.parse(values.incident.metadata) : undefined,
          impactAnalysis: values.incident?.impactAnalysis ? JSON.parse(values.incident.impactAnalysis) : undefined,
        };
        if (catalog.targetClass === 'change_request') {
          const change = values.change;
          if (change?.plannedStartDate && change?.plannedEndDate?.isBefore(change.plannedStartDate)) throw new Error('结束时间必须晚于开始时间');
          payload.change = { ...change, plannedStartDate: change?.plannedStartDate?.toISOString(), plannedEndDate: change?.plannedEndDate?.toISOString() };
        }
      }
      await creation.submit(payload, ServiceCatalogApi.createServiceRequest, receipt => router.push(`/tickets/${receipt.workItemId}`));
    } catch (e: any) {
      message.error('提交失败：' + (e?.message || '未知错误'));
    } finally {
      setLoading(false);
    }
  };

  if (fetching && !catalogRef.current) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <Spin size="large" />
      </div>
    );
  }

  return (
    <div className="max-w-3xl mx-auto p-6">
      <Breadcrumb
        items={[
          { title: '服务目录', href: '/service-catalog' },
          { title: '提交申请' },
        ]}
        className="mb-4"
      />
      <CreationAttempts creation={creation} />
      <Card>
        <Space className="mb-4">
          <Button icon={<ArrowLeft />} onClick={() => router.push('/service-catalog')}>
            返回
          </Button>
          <Title level={3} style={{ margin: 0 }}>
            申请服务
          </Title>
        </Space>

        {(fetchError || creation.attempts.some(attempt => attempt.state === 'rejected' || attempt.state === 'unknown')) && (
          <Alert type="warning" className="mb-4" title="请重新读取目录并核对后再确认申请" description="重试不会自动替换原确认版本；重载将保留兼容答案，不兼容内容需您明确核对。"
            action={<Button loading={fetching} disabled={fetching || incompatibleAnswers.length > 0 || creation.submitting} onClick={() => setRevision(value => value + 1)}>重新读取目录</Button>} />
        )}
        {incompatibleAnswers.length > 0 && (
          <Alert type="warning" className="mb-4" title="目录变更需要重新核对已填内容"
            description={<>
              <p>以下答案不再适用于新目录定义。确认后将舍弃这些答案，请在新表单重新填写；其他答案及原申请快照保留。</p>
              <ul>{incompatibleAnswers.map(answer => <li key={JSON.stringify(answer.path)}>
                {answer.label}：{typeof answer.value === 'string' ? answer.value : JSON.stringify(answer.value)}
              </li>)}</ul>
            </>}
            action={<Button onClick={() => {
              incompatibleAnswers.forEach(answer => form.setFieldValue(answer.path, undefined));
              setIncompatibleAnswers([]);
              creation.newConfirmation();
            }}>确认舍弃上述不兼容答案并重新填写</Button>} />
        )}
        {fetchError && (
          <Alert
            type="error"
            showIcon
            className="mb-4"
            message={fetchError}
            action={<Button onClick={() => router.push('/service-catalog')}>返回服务目录</Button>}
          />
        )}

        {catalog && (
          <Alert
            type="info"
            showIcon
            className="mb-4"
            message={
              <Space>
                <Text strong>{catalog.name}</Text>
                {catalog.availability?.responseTime != null && (
                  <Tag icon={<Clock />} color="blue">
                    交付时长 {catalog.availability?.responseTime} 天
                  </Tag>
                )}
                {catalog.category && <Tag>{catalog.category}</Tag>}
              </Space>
            }
            description={catalog.fullDescription}
          />
        )}

        <Divider />

        <Form form={form} layout="vertical" onFinish={onFinish} disabled={fetching || incompatibleAnswers.length > 0}>
          <CreationRequester />
          <Alert type="info" title={`确认目标：${catalog?.targetClass || '未加载'}`} />
          {serviceRequestTarget && <>
          <div className="grid grid-cols-2 gap-4">
            <Form.Item
              name="contactName"
              label="联系人"
              extra="默认取当前登录用户，如代他人提交可修改"
              rules={[{ required: true, message: '请输入联系人姓名' }]}
            >
              <Input placeholder="联系人姓名" />
            </Form.Item>
            <Form.Item
              name="contactEmail"
              label="联系邮箱"
              rules={[
                { required: true, message: '请输入联系邮箱' },
                { type: 'email', message: '请输入合法的邮箱地址' },
              ]}
            >
              <Input placeholder="联系邮箱" />
            </Form.Item>
          </div>
          </>}
          <Form.Item
            name="title"
            label="申请标题"
            rules={[{ required: true, message: '请输入申请标题' }]}
          >
            <Input placeholder="一句话说明申请目的" maxLength={200} />
          </Form.Item>

          <Form.Item
            name="reason"
            label="申请理由"
            rules={[{ required: true, message: '请输入申请理由' }]}
          >
            <TextArea rows={4} placeholder="请详细说明申请原因、业务场景、紧急程度" maxLength={500} />
          </Form.Item>

          {serviceRequestTarget && <>
          <div className="grid grid-cols-2 gap-4">
            <Form.Item name="quantity" label="数量" initialValue={1}>
              <Input type="number" min={1} max={100} />
            </Form.Item>
            <Form.Item name="expectedAt" label="期望交付时间">
              <DatePicker showTime style={{ width: '100%' }} />
            </Form.Item>
          </div>

          </>}
          {serviceRequestTarget && catalog?.requiresInfraFields && (
            <>
              <div className="grid grid-cols-2 gap-4">
                <Form.Item name="costCenter" label="成本中心">
                  <Input placeholder="例如 CC-1001" />
                </Form.Item>
                <Form.Item
                  name="dataClassification"
                  label="数据分级"
                  initialValue="internal"
                >
                  <Select
                    options={[
                      { label: '公开 (public)', value: 'public' },
                      { label: '内部 (internal)', value: 'internal' },
                      { label: '机密 (confidential)', value: 'confidential' },
                      { label: '绝密 (restricted)', value: 'restricted' },
                    ]}
                  />
                </Form.Item>
              </div>

              <Form.Item name="needsPublicIp" valuePropName="checked">
                <Checkbox>需要公网 IP</Checkbox>
              </Form.Item>

              <Form.Item
                name="sourceIpWhitelist"
                label="来源 IP 白名单（多个以英文逗号分隔）"
                dependencies={['needsPublicIp']}
              >
                <Input placeholder="例如 1.2.3.4, 10.0.0.0/8" />
              </Form.Item>

              <Divider />

              <Form.Item
                name="expireAt"
                label="申请有效期"
                extra="填写期望的有效期限，实际执行以服务流程为准"
              >
                <DatePicker
                  showTime
                  style={{ width: '100%' }}
                  disabledDate={(d) => d && d.isBefore(dayjs().startOf('day'))}
                />
              </Form.Item>

              <Form.Item
                name="complianceAck"
                valuePropName="checked"
                rules={[
                  {
                    validator: (_, value) =>
                      value
                        ? Promise.resolve()
                        : Promise.reject(new Error('请确认已知悉相关合规与安全要求')),
                  },
                ]}
              >
                <Checkbox>
                  我已知悉本服务的合规要求与安全策略，并承诺仅将资源用于申请所述的合法业务场景
                </Checkbox>
              </Form.Item>
            </>
          )}

          <Form.Item name="priority" label="优先级"><Select options={['low', 'medium', 'high', 'critical'].map(value => ({ value, label: value }))} /></Form.Item>
          <CatalogProfessionalFields targetClass={catalog?.targetClass} />
          {Array.isArray(catalog?.fields) && catalog.fields.length > 0 && (
            <>
              <Divider>该服务的补充信息</Divider>
              {catalog.fields.map((field: { name: string; label: string; type: string; required: boolean; options?: Array<{ label: string; value: string }> }) => (
                <Form.Item
                  key={field.name}
                  name={['customFields', field.name]}
                  label={field.label}
                  rules={field.required ? [{ required: true, message: `请填写${field.label}` }] : []}
                >
                  {field.type === 'textarea' ? (
                    <TextArea rows={3} />
                  ) : field.type === 'select' ? (
                    <Select options={field.options} />
                  ) : field.type === 'number' ? (
                    <Input type="number" />
                  ) : field.type === 'date' ? (
                    <DatePicker style={{ width: '100%' }} />
                  ) : (
                    <Input />
                  )}
                </Form.Item>
              ))}
            </>
          )}

          <Form.Item>
            <Space>
              <Button
                type="primary"
                htmlType="submit"
                icon={<Send />}
                loading={loading}
                disabled={fetching || !catalog || !!fetchError || incompatibleAnswers.length > 0}
              >
                提交申请
              </Button>
              <Button onClick={() => router.push('/service-catalog')}>取消</Button>
            </Space>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
}
