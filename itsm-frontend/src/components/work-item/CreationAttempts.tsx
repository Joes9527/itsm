'use client';
import { Alert, Button, Space } from 'antd';
import type { WorkItemCreation } from '@/lib/hooks/useWorkItemCreation';
import { creationReceiptMessage } from '@/lib/api/work-item-creation';

export function CreationAttempts({ creation }: { creation: WorkItemCreation }) {
  return (
    <Space orientation='vertical' style={{ width: '100%' }} className='mb-4'>
      {creation.attempts
        .filter(attempt => attempt.state !== 'sending')
        .map(attempt => (
          <Alert
            key={attempt.key}
            type={attempt.state === 'committed' ? 'success' : 'warning'}
            showIcon
            title={
              attempt.receipt
                ? creationReceiptMessage(attempt.receipt)
                : `${attempt.title}：${attempt.state === 'rejected' ? '服务器未接受' : '提交结果尚未确认'}`
            }
            description={
              attempt.receipt ? undefined : (
                <>
                  <p>{attempt.error}</p>
                  <p>申请标识：{attempt.key}。重试保留原已确认内容；编辑表单不会修改此申请。</p>
                  <Space wrap>
                    {attempt.state === 'unknown' && (
                      <Button
                        disabled={creation.submitting}
                        onClick={() => void creation.retry(attempt.key)}
                      >
                        重试原申请
                      </Button>
                    )}
                    <Button disabled={creation.submitting} onClick={creation.newConfirmation}>
                      以当前表单重新确认新申请
                    </Button>
                  </Space>
                </>
              )
            }
          />
        ))}
    </Space>
  );
}
