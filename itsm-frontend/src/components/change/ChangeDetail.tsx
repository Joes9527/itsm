'use client';

/**
 * 变更详情组件
 */

import React, { useState, useEffect } from 'react';
import {
  Card,
  Descriptions,
  Tag,
  Button,
  Skeleton,
  Result,
  Divider,
  List,
  Typography,
  Spin,
  Empty,
  Tabs,
  Space,
  App,
  Alert,
} from 'antd';
import { ArrowLeft, CheckCircle, XCircle } from 'lucide-react';
import { useParams, useRouter } from 'next/navigation';
import dayjs from 'dayjs';

import { ChangeApi } from '@/lib/api/change-api';
import { ChangeActions } from './ChangeActions';
import { useChangeOperation } from './useChangeOperation';
import {
  ChangeStatus,
  ChangeStatusLabels,
  ChangeTypeLabels,
  ChangePriorityLabels,
  ChangeImpactLabels,
  ChangeRiskLabels,
} from '@/constants/change';
import type { ApprovalRecord } from '@/types/biz/change';
import type { Change } from '@/lib/api/change-api';
import { getErrorMessage } from '@/lib/utils/error-message-handler';
import ChangeRiskAssessment from './ChangeRiskAssessment';
import ChangeCMDBImpactPanel from './ChangeCMDBImpactPanel';

import ChangeRollbackPlan from './ChangeRollbackPlan';
import { SafeTextBlock } from '@/components/common/SafeContent';

const { Title, Text } = Typography;

const statusColors: Record<string, string> = {
  [ChangeStatus.DRAFT]: 'default',
  [ChangeStatus.PENDING]: 'orange',
  [ChangeStatus.SUBMITTED]: 'orange',
  [ChangeStatus.APPROVED]: 'cyan',
  [ChangeStatus.IN_PROGRESS]: 'blue',
  [ChangeStatus.COMPLETED]: 'green',
  [ChangeStatus.REJECTED]: 'red',
  [ChangeStatus.ROLLED_BACK]: 'magenta',
};

interface ChangeDetailProps {
  id?: string;
  onChangeLoaded?: (change: Change) => void;
}

const ChangeDetail: React.FC<ChangeDetailProps> = ({ id: propId, onChangeLoaded }) => {
  const params = useParams() as { id?: string };
  const id = propId || params?.id;
  const router = useRouter();
  const { message } = App.useApp();
  const operation = useChangeOperation();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [change, setChange] = useState<Change | null>(null);
  const [approvals, setApprovals] = useState<ApprovalRecord[]>([]);
  const [riskAssessment, setRiskAssessment] = useState<any>(null);
  const [riskReadFailed, setRiskReadFailed] = useState(false);
  const [rollbackPlan] = useState<any>(null);
  const [assessmentLoading, setAssessmentLoading] = useState(false);
  useEffect(() => {
    if (id) {
      void loadDetail().catch(() => {});
    }
  }, [id]);

  const loadDetail = async () => {
    if (!change) setLoading(true);
    try {
      const data = await ChangeApi.getChange(Number(id!));
      setError('');
      setChange(data as Change);
      onChangeLoaded?.(data as Change);

      // Try to load approval summary
      try {
        const summary = await ChangeApi.getChangeApprovals(Number(id));
        setApprovals(summary as unknown as ApprovalRecord[]);
      } catch (e) {
        setError(getErrorMessage(e) || '审批记录加载失败');
      }
    } catch (error) {
      // console.error(error);
      setError(getErrorMessage(error) || '加载变更详情失败');
      throw error;
    } finally {
      setLoading(false);
    }
  };

  // 加载风险评估数据
  const loadRiskAssessment = async () => {
    if (!id) return;
    setAssessmentLoading(true);
    try {
      const data = await ChangeApi.getRiskAssessment(Number(id));
      setRiskAssessment(data);
      setRiskReadFailed(false);
    } catch (error) {
      setRiskReadFailed(true);
      setError(getErrorMessage(error) || '风险评估加载失败');
    } finally {
      setAssessmentLoading(false);
    }
  };

  // 保存风险评估
  const handleSaveRiskAssessment = async (data: any) => {
    if (!id || !change) return;
    try {
      const {
        riskLevel,
        riskDescription,
        impactAnalysis,
        mitigationMeasures,
        contingencyPlan,
        riskOwner,
      } = data;
      const facts = {
        riskLevel,
        riskDescription,
        impactAnalysis,
        mitigationMeasures,
        contingencyPlan,
        riskOwner,
      };
      await ChangeApi.updateRisk(Number(id), {
        ...facts,
        ...operation.identity(`${id}/risk`, facts, change.version),
      });
      operation.clear();
      await loadDetail();
      message.success('风险评估保存成功');
      loadRiskAssessment();
    } catch (error) {
      message.error(getErrorMessage(error) || '保存失败');
      throw error;
    }
  };

  // 保存回滚计划
  const handleSaveRollbackPlan = async (data: any) => {
    if (!id || !change) return;
    try {
      const facts = { rollbackPlan: JSON.stringify(data) };
      await ChangeApi.updateChange(Number(id), {
        ...facts,
        ...operation.identity(`${id}/metadata`, facts, change.version),
      });
      operation.clear();
      await loadDetail();
      message.success('回滚计划保存成功');
    } catch (error) {
      message.error(getErrorMessage(error) || '保存失败');
      throw error;
    }
  };

  if (loading && !change)
    return (
      <Card>
        <Skeleton active />
      </Card>
    );

  if (!change) {
    return (
      <Card>
        <Result
          status='404'
          title='404'
          subTitle='抱歉，您访问的变更不存在'
          extra={
            <Button type='primary' onClick={() => router.push('/changes')}>
              返回列表
            </Button>
          }
        />
      </Card>
    );
  }

  return (
    <Space orientation='vertical' style={{ width: '100%' }} size='large'>
      <Card>
        <div style={{ marginBottom: 24 }}>
          <Button
            icon={<ArrowLeft />}
            onClick={() => router.push('/changes')}
            style={{ marginBottom: 16 }}
          >
            返回列表
          </Button>
          <div
            style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}
          >
            <Title level={3}>{change.title}</Title>
            <Tag color={statusColors[change.status]} style={{ padding: '4px 12px', fontSize: 14 }}>
              {ChangeStatusLabels[change.status as ChangeStatus]}
            </Tag>
          </div>
        </div>

        <ChangeActions key={change.id} change={change} onRefresh={loadDetail} />
        {error && <Alert type='error' title={error} />}
        <Button
          disabled={!change.actions?.metadata?.allowed}
          onClick={() => router.push(`/changes/${id}/edit`)}
        >
          编辑变更
        </Button>
        <Descriptions bordered column={2}>
          <Descriptions.Item label='变更编号'>{change.number || '编号不可用'}</Descriptions.Item>
          <Descriptions.Item label='版本'>{change.version}</Descriptions.Item>
          <Descriptions.Item label='实施结果'>{change.outcome || '尚未记录'}</Descriptions.Item>
          <Descriptions.Item label='实施依据'>{change.outcomeEvidence || '-'}</Descriptions.Item>
          <Descriptions.Item label='审查依据'>{change.reviewEvidence || '-'}</Descriptions.Item>
          <Descriptions.Item label='变更类型'>
            {ChangeTypeLabels[change.type as keyof typeof ChangeTypeLabels]}
          </Descriptions.Item>
          <Descriptions.Item label='优先级'>
            {ChangePriorityLabels[change.priority as keyof typeof ChangePriorityLabels]}
          </Descriptions.Item>
          <Descriptions.Item label='风险等级'>
            {ChangeRiskLabels[change.riskLevel as keyof typeof ChangeRiskLabels]}
          </Descriptions.Item>
          <Descriptions.Item label='影响范围'>
            {ChangeImpactLabels[change.impactScope as keyof typeof ChangeImpactLabels]}
          </Descriptions.Item>
          <Descriptions.Item label='负责人'>{change.assigneeName || '未分配'}</Descriptions.Item>
          <Descriptions.Item label='计划起始'>
            {change.plannedStartDate
              ? dayjs(change.plannedStartDate).format('YYYY-MM-DD HH:mm')
              : '-'}
          </Descriptions.Item>
          <Descriptions.Item label='计划截止'>
            {change.plannedEndDate ? dayjs(change.plannedEndDate).format('YYYY-MM-DD HH:mm') : '-'}
          </Descriptions.Item>
        </Descriptions>

        <Tabs
          defaultActiveKey='1'
          style={{ marginTop: 24 }}
          onChange={activeKey => {
            if (activeKey === '3' && !riskAssessment) loadRiskAssessment();
          }}
          items={[
            {
              key: '1',
              label: '基础信息',
              children: (
                <>
                  <Title level={5}>变更原因 / 理由</Title>
                  <SafeTextBlock content={change.justification} fallback='无' />

                  <Title level={5}>变更描述</Title>
                  <SafeTextBlock content={change.description} fallback='无' />

                  <Divider />

                  <Title level={5}>实施计划</Title>
                  <SafeTextBlock
                    content={change.implementationPlan}
                    fallback='未提供实施计划'
                    preserveNewlines
                  />

                  <Title level={5}>回滚计划</Title>
                  <SafeTextBlock
                    content={change.rollbackPlan}
                    fallback='未提供回滚计划'
                    preserveNewlines
                  />
                </>
              ),
            },
            {
              key: '2',
              label: '审批记录',
              children:
                approvals.length > 0 ? (
                  <List
                    itemLayout='horizontal'
                    dataSource={approvals}
                    renderItem={record => (
                      <List.Item>
                        <List.Item.Meta
                          avatar={
                            record.status === 'approved' ? (
                              <CheckCircle style={{ color: '#52c41a', fontSize: 24 }} />
                            ) : (
                              <XCircle style={{ color: '#ff4d4f', fontSize: 24 }} />
                            )
                          }
                          title={
                            <Space>
                              <Text strong>{record.approverName}</Text>
                              <Tag color={statusColors[record.status]}>
                                {ChangeStatusLabels[record.status]}
                              </Tag>
                              <Text type='secondary'>
                                {record.createdAt
                                  ? dayjs(record.createdAt).format('YYYY-MM-DD HH:mm')
                                  : '-'}
                              </Text>
                            </Space>
                          }
                          description={record.comment || '无意见'}
                        />
                      </List.Item>
                    )}
                  />
                ) : (
                  <Empty
                    description={
                      change.standardTemplateId
                        ? '无人工审批记录；标准模板预授权由服务端策略校验'
                        : '暂无审批记录'
                    }
                  />
                ),
            },
            {
              key: '3',
              label: '风险评估',
              children: (
                <Spin spinning={assessmentLoading}>
                  <Button onClick={() => void loadRiskAssessment()}>刷新风险评估</Button>
                  <ChangeRiskAssessment
                    changeId={Number(id)}
                    readOnly={!change.actions?.risk?.allowed || assessmentLoading || riskReadFailed}
                    initialData={riskAssessment ?? { riskLevel: change.riskLevel }}
                    onSave={handleSaveRiskAssessment}
                  />
                </Spin>
              ),
            },
            {
              key: '5',
              label: '回滚计划',
              children: (
                <Spin spinning={assessmentLoading}>
                  <ChangeRollbackPlan
                    changeId={Number(id)}
                    readOnly={!change.actions?.metadata?.allowed}
                    initialData={rollbackPlan}
                    onSave={handleSaveRollbackPlan}
                  />
                </Spin>
              ),
            },
            {
              key: '7',
              label: 'CMDB 影响摘要',
              children: <ChangeCMDBImpactPanel changeId={Number(id)} />,
            },
            {
              key: '6',
              label: '实施后审查 (PIR)',
              children: (
                <div className='py-4'>
                  <p className='text-muted mb-4'>评估变更实施结果，总结经验教训</p>
                  <Button type='primary' onClick={() => router.push(`/changes/${id}/pir`)}>
                    查看 / 编辑 PIR
                  </Button>
                </div>
              ),
            },
          ]}
        />
      </Card>
    </Space>
  );
};

export default ChangeDetail;
