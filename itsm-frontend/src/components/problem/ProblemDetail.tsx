'use client';

/**
 * 问题详情组件
 */

import React, { useState, useEffect, useRef } from 'react';
import { App, Card, Tag, Button, Space, Skeleton, Typography, Tabs, Modal, Input } from 'antd';
import { Search } from 'lucide-react';
import { ArrowLeftOutlined, EditOutlined } from '@ant-design/icons';
import { useRouter, useParams } from 'next/navigation';

import { ProblemApi } from '@/lib/api/';
import { ProblemStatus, ProblemStatusLabels } from '@/constants/problem';
import type { Problem, ProblemAction } from '@/lib/api/problem-api';
import { useOptionalWorkItemContext } from '@/components/work-item/WorkItemContext';
import type { WorkItemActionState } from '@/components/work-item/WorkItemTypes';
import { WorkItemActionButton } from '@/components/work-item/WorkItemActionButton';
import ProblemInvestigationTab from './ProblemInvestigationTab';
import BasicInfoCard from './BasicInfoCard';

const { Title } = Typography;

interface ProblemDetailProps {
  id?: string;
  fallbackActions?: Record<string, WorkItemActionState>;
  onProblemLoaded?: (problem: Problem) => void;
}

const EMPTY_ACTIONS: Record<string, WorkItemActionState> = {};

const ProblemDetail: React.FC<ProblemDetailProps> = ({
  id: propId,
  fallbackActions,
  onProblemLoaded,
}) => {
  const params = useParams();
  const { message, modal } = App.useApp();
  const router = useRouter();
  // 支持通过props传入id，或通过useParams获取
  const id = propId || (params?.id as string);
  const workItemContext = useOptionalWorkItemContext();
  const [loading, setLoading] = useState(false);
  const [data, setData] = useState<Problem | null>(null);
  // 状态流转 loading：记录正在提交的目标状态，防止重复点击
  const [updatingStatus, setUpdatingStatus] = useState<ProblemAction | null>(null);
  const actions = data?.actions ?? workItemContext?.actions ?? fallbackActions ?? EMPTY_ACTIONS;
  const operations = useRef<Record<string,string>>({});
  const [verificationOpen, setVerificationOpen] = useState(false);
  const [verificationNote, setVerificationNote] = useState('');

  const loadData = async () => {
    if (!id) return;
    setLoading(true);
    try {
      const problem = await ProblemApi.getProblem(Number(id));
      setData(problem);
      onProblemLoaded?.(problem);
    } catch (error) {
      message.error('加载问题详情失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, [id, onProblemLoaded]);

  const handleUpdateStatus = async (action: ProblemAction) => {
    if (!id || !data) return;
    setUpdatingStatus(action);
    try {
      const key = JSON.stringify([id,data.version,action,verificationNote]);
      const operationId = operations.current[key] ??= crypto.randomUUID();
      await ProblemApi.command(Number(id), action, { version: data.version, operationId, ...(action === 'verify-resolution' ? { verificationNote } : {}) });
      delete operations.current[key];
      if (action === 'verify-resolution') setVerificationOpen(false);
      message.success('状态更新成功');
      loadData();
    } catch (error) {
      message.error('状态更新失败');
    } finally {
      setUpdatingStatus(null);
    }
  };

  const handleCloseProblem = () => {
    modal.confirm({
      title: '确认关闭问题？',
      content: '请确认永久解决方案已经验证。关闭后如问题再次出现，可以重新打开。',
      okText: '确认关闭',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => handleUpdateStatus('close'),
    });
  };

  if (loading) {
    return (
      <Card>
        <Skeleton active />
      </Card>
    );
  }

  if (!data) {
    return <Card>未找到该问题</Card>;
  }

  const tabItems = [
    {
      key: 'basic',
      label: '基本信息',
      children: <BasicInfoCard data={data} />,
    },
    {
      key: 'investigation',
      label: (
        <span>
          <Search /> 问题调查
        </span>
      ),
      children: (
        <ProblemInvestigationTab
          problemId={data.id}
          problemVersion={data.version}
          onProblemChanged={loadData}
          canEdit={actions.edit?.allowed === true}
          problemTitle={data.title}
          problemDescription={data.description}
        />
      ),
    },
  ];

  return (
    <Space orientation='vertical' style={{ width: '100%' }} size='middle'>
      {/* 操作栏 */}
      <Card styles={{ body: { padding: '16px 24px' } }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Space>
            <Button icon={<ArrowLeftOutlined aria-hidden="true" />} onClick={() => router.push('/problems')}>
              返回列表
            </Button>
            <Title level={4} style={{ margin: 0 }}>
              {data.title}
            </Title>
            <Tag color={data.status === ProblemStatus.RESOLVED ? 'success' : 'blue'}>
              {ProblemStatusLabels[data.status as ProblemStatus]}
            </Tag>
          </Space>
          <Space>
            <WorkItemActionButton
              action={actions.edit}
              actionName='edit'
              button={{
                icon: <EditOutlined aria-hidden="true" />,
                onClick: () => router.push(`/problems/${data.id}/edit`),
              }}
            >
              编辑
            </WorkItemActionButton>
            <WorkItemActionButton
              action={actions.startInvestigation}
              actionName='startInvestigation'
              button={{
                type: 'primary',
                loading: updatingStatus === 'investigate',
                disabled: updatingStatus !== null,
                onClick: () => handleUpdateStatus('investigate'),
              }}
            >
              开始调查
            </WorkItemActionButton>
            <WorkItemActionButton action={actions.verifyResolution} actionName='verifyResolution' button={{ disabled: updatingStatus !== null, onClick: () => setVerificationOpen(true) }}>验证永久方案</WorkItemActionButton>
            <WorkItemActionButton action={actions.reopen} actionName='reopen' button={{ disabled: updatingStatus !== null, onClick: () => handleUpdateStatus('reopen') }}>重新打开</WorkItemActionButton>
            <WorkItemActionButton
              action={actions.resolve}
              actionName='resolve'
              button={{
                type: 'primary',
                loading: updatingStatus === 'resolve',
                disabled: updatingStatus !== null,
                onClick: () => handleUpdateStatus('resolve'),
              }}
            >
              标记解决
            </WorkItemActionButton>
            <WorkItemActionButton
              action={actions.close}
              actionName='close'
              button={{
                loading: updatingStatus === 'close',
                disabled: updatingStatus !== null,
                onClick: handleCloseProblem,
              }}
            >
              关闭问题
            </WorkItemActionButton>
          </Space>
        </div>
      </Card>

      <Modal title='验证永久解决方案' open={verificationOpen} onCancel={() => setVerificationOpen(false)} onOk={() => handleUpdateStatus('verify-resolution')} okButtonProps={{ disabled: !verificationNote.trim(), loading: updatingStatus === 'verify-resolution' }}>
        <Input.TextArea aria-label='验证说明' value={verificationNote} onChange={e => setVerificationNote(e.target.value)} placeholder='记录回归测试、实施检查等验证证据' />
      </Modal>
      {/* Tab 内容 */}
      <Card>
        <Tabs items={tabItems} defaultActiveKey='basic' />
      </Card>
    </Space>
  );
};

export default ProblemDetail;
