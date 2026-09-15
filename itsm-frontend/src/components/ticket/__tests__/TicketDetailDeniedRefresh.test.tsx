import React from 'react';
import { App } from 'antd';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import {
  DetailRefreshProvider,
  useDetailRefresh,
  useDetailRefreshEntry,
} from '@/components/business/detail-tabs/DetailRefreshContext';
import type { DetailReadResult } from '@/components/business/detail-tabs/useDetailResource';
import { AttachmentPanel } from '@/components/business/detail-tabs/AttachmentPanel';
import { CommentPanel } from '@/components/business/detail-tabs/CommentPanel';
import { ticketCommentAdapter, ticketAttachmentAdapter } from '@/components/business/detail-tabs';
import { TicketNotificationSection } from '@/components/business/TicketNotificationSection';
import { TicketHistoryList } from '../TicketHistoryList';
import { TicketCommentStream } from '../TicketCommentStream';
import { TicketRelationCards } from '../TicketRelationCards';
import { TicketProcessTasks } from '../TicketProcessTasks';
import ServiceCatalogApprovalChain from '../ServiceCatalogApprovalChain';
import ServiceRequestPanel from '../ServiceRequestPanel';
import { useTicketDetailResource } from '../useTicketDetailResource';
import { TicketApi } from '@/lib/api/ticket-api';
import { TicketCommentApi } from '@/lib/api/ticket-comment-api';
import { TicketRelationsApi } from '@/lib/api/ticket-relations-api';
import { TicketNotificationApi } from '@/lib/api/ticket-notification-api';
import { BPMNWorkflowApi } from '@/lib/api/bpmn-workflow-api';
import { ServiceCatalogApi } from '@/lib/api/service-catalog-api';
import { serviceRequestAPI } from '@/lib/api/service-request-api';
import { ApiError } from '@/lib/api/http-client';
jest.mock('next/navigation', () => ({ useRouter: () => ({ push: jest.fn() }) }));
jest.mock('@/lib/api/ticket-api');
jest.mock('@/lib/api/ticket-comment-api');
jest.mock('@/lib/api/ticket-relations-api');
jest.mock('@/lib/api/ticket-notification-api');
jest.mock('@/lib/api/bpmn-workflow-api');
jest.mock('@/lib/api/service-catalog-api');
jest.mock('@/lib/api/service-request-api');
function MainRead() {
  const resource = useTicketDetailResource(101, () => false);
  return (
    <div>
      {resource.data?.title}
      {resource.error && <p>{resource.error}</p>}
    </div>
  );
}
function Controls({ slow }: { slow: () => Promise<DetailReadResult> }) {
  const controller = useDetailRefresh()!;
  useDetailRefreshEntry({
    key: 'sibling',
    label: 'Slow sibling',
    reload: slow,
    isWriting: () => false,
  });
  return (
    <>
      <button onClick={() => void controller.refresh()}>页面刷新</button>
      <output data-testid='report'>{JSON.stringify(controller.report)}</output>
    </>
  );
}
const attachment = {
  id: 1,
  fileName: 'protected attachment',
  fileSize: 1,
  mimeType: 'text/plain',
  createdAt: '2026-09-15',
};
const comment = { id: 1, userId: 1, content: 'protected comment', createdAt: '2026-09-15' };
const access = { canRead: true, canUpload: false, canDelete: false };
const attachments = { ...ticketAttachmentAdapter, list: jest.fn() };
const cases = [
  {
    key: 'history',
    label: '历史流转',
    read: TicketApi.getTicketHistory as jest.Mock,
    seed: [{ id: 1, action: 'protected history' }],
    text: /protected history/,
    count: true,
    panel: (onCountChange: jest.Mock) => (
      <TicketHistoryList ticketId={101} onCountChange={onCountChange} />
    ),
  },
  {
    key: 'catalog-approval-chain',
    label: '目录审批链',
    read: ServiceCatalogApi.getServiceRequestByTicketId as jest.Mock,
    seed: { formData: { ApprovalChain: [{ level: 1, name: 'protected chain' }] } },
    text: /protected chain/,
    panel: () => <ServiceCatalogApprovalChain ticketId={101} />,
  },
  {
    key: 'service-request',
    label: '服务申请',
    read: ServiceCatalogApi.getServiceRequestByTicketId as jest.Mock,
    seed: { id: 1, serviceName: 'protected request' },
    text: /protected request/,
    panel: () => <ServiceRequestPanel ticketId={101} />,
  },
  {
    key: 'comments',
    label: '评论',
    read: TicketCommentApi.getComments as jest.Mock,
    seed: { comments: [comment], total: 1 },
    text: /protected comment/,
    count: true,
    panel: (onCountChange: jest.Mock) => (
      <TicketCommentStream ticketId={101} onCountChange={onCountChange} />
    ),
  },
  {
    key: 'comments',
    label: '评论',
    read: TicketCommentApi.getComments as jest.Mock,
    seed: { comments: [comment], total: 1 },
    text: /protected comment/,
    panel: () => <CommentPanel targetType='ticket' targetId={101} adapter={ticketCommentAdapter} />,
  },
  {
    key: 'attachments',
    label: '附件',
    read: attachments.list,
    seed: [attachment],
    text: /protected attachment/,
    count: true,
    panel: (onCountChange: jest.Mock) => (
      <AttachmentPanel
        targetType='ticket'
        targetId={101}
        adapter={attachments}
        permissions={access}
        onCountChange={onCountChange}
      />
    ),
  },
  {
    key: 'notifications',
    label: '通知',
    read: TicketNotificationApi.getTicketNotifications as jest.Mock,
    seed: {
      notifications: [
        { id: 1, content: 'protected notification', type: 'assigned', channel: 'in_app' },
      ],
      total: 1,
    },
    text: /protected notification/,
    panel: () => <TicketNotificationSection ticketId={101} canSend={false} />,
  },
  {
    key: 'relations',
    label: '关联关系',
    read: TicketRelationsApi.getTicketRelations as jest.Mock,
    seed: [
      {
        id: 1,
        relationType: 'RELATES_TO',
        sourceTicketId: 101,
        targetTicketId: 2,
        targetTicket: { id: 2, title: 'protected relation' },
      },
    ],
    text: /protected relation/,
    count: true,
    panel: (onCountChange: jest.Mock) => (
      <TicketRelationCards ticketId={101} onCountChange={onCountChange} />
    ),
  },
  {
    key: 'process-tasks',
    label: '流程任务',
    read: BPMNWorkflowApi.listUserTasks as jest.Mock,
    seed: {
      items: [
        {
          id: 1,
          businessType: 'generic',
          businessId: 101,
          taskName: 'protected task',
          status: 'assigned',
          assignmentState: 'assigned',
          uiActions: {},
        },
      ],
      total: 1,
      page: 1,
      pageSize: 100,
    },
    text: /protected task/,
    panel: () => <TicketProcessTasks ticketId={101} recordClass='generic' />,
  },
  {
    key: 'ticket',
    label: '工单详情',
    read: TicketApi.getTicket as jest.Mock,
    seed: { id: 101, title: 'protected ticket' },
    text: /protected ticket/,
    panel: () => <MainRead />,
  },
];
beforeEach(() => {
  jest.clearAllMocks();
  (serviceRequestAPI.listProvisioningTasks as jest.Mock).mockResolvedValue([]);
});
it.each(cases)('attributes denied $key while a slower sibling is pending', async testCase => {
  testCase.read.mockReset().mockResolvedValueOnce(testCase.seed);
  const count = jest.fn();
  let finish!: (result: DetailReadResult) => void;
  const slow = () =>
    new Promise<DetailReadResult>(resolve => {
      finish = resolve;
    });
  render(
    <App>
      <DetailRefreshProvider identity='101'>
        <Controls slow={slow} />
        {testCase.panel(count)}
      </DetailRefreshProvider>
    </App>
  );
  await screen.findByText(testCase.text);
  if (testCase.count) expect(count).toHaveBeenLastCalledWith(1);
  testCase.read.mockRejectedValueOnce(new ApiError('read forbidden', 403));
  fireEvent.click(screen.getByText('页面刷新'));
  await screen.findByText('read forbidden');
  expect(screen.queryByText(testCase.text)).not.toBeInTheDocument();
  if (testCase.count) expect(count).toHaveBeenLastCalledWith(undefined);
  await act(async () => finish({ status: 'success' }));
  const report = JSON.parse(screen.getByTestId('report').textContent!);
  expect(report.failed).toEqual([
    { key: testCase.key, label: testCase.label, message: 'read forbidden' },
  ]);
  expect(report.skipped).not.toContain(testCase.key);
});
it.each(['permission', 'unmount'])(
  'still skips an attachment participant after explicit %s removal',
  async reason => {
    attachments.list.mockReset().mockResolvedValue([attachment]);
    let finish!: (result: DetailReadResult) => void;
    const slow = () =>
      new Promise<DetailReadResult>(resolve => {
        finish = resolve;
      });
    const view = (available: boolean) => (
      <App>
        <DetailRefreshProvider identity='101'>
          <Controls slow={slow} />
          {(available || reason === 'permission') && (
            <AttachmentPanel
              targetType='ticket'
              targetId={101}
              adapter={attachments}
              permissions={{ ...access, canRead: available }}
            />
          )}
        </DetailRefreshProvider>
      </App>
    );
    const { rerender } = render(view(true));
    await screen.findByText(/protected attachment/);
    fireEvent.click(screen.getByText('页面刷新'));
    await waitFor(() => expect(attachments.list).toHaveBeenCalledTimes(2));
    rerender(view(false));
    expect(screen.queryByText(/protected attachment/)).not.toBeInTheDocument();
    await act(async () => finish({ status: 'success' }));
    const report = JSON.parse(screen.getByTestId('report').textContent!);
    expect(report.skipped).toContain('attachments');
    expect(report.failed).toEqual([]);
  }
);
