/**
 * 退役页面不得提交保留的静态确认框。
 *
 * antd 的静态 Modal.confirm 渲染在 document.body 的独立 portal 上，不随页面卸载销毁。
 * 若页面卸载后用户仍点到残留的"确定"，必须不能发出任何写请求。
 */
import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@/lib/test-utils';
import IncidentsPage from '../page';
import {IncidentAPI} from '@/lib/api/incident-api';

jest.mock('next/navigation', () => ({useRouter:()=>({push:jest.fn()})}));
jest.mock('@/lib/i18n/useI18n', () => ({useI18n:()=>({t: mockTranslate})}));
const mockTranslate = (key:string) => key;
jest.mock('@/lib/api/incident-api', () => ({IncidentAPI:{
  listIncidents:jest.fn(),
  getIncidentMetrics:jest.fn(),
  closeIncident:jest.fn(),
  assignIncident:jest.fn(),
  resolveIncident:jest.fn(),
  deleteIncident:jest.fn(),
}}));
jest.mock('@/lib/api/user-api', () => ({UserApi:{getUsers:jest.fn().mockResolvedValue({users:[]})}}));
jest.mock('@/components/layout/BusinessPageTemplate', () => ({BusinessPageTemplate:({children}:React.PropsWithChildren)=> <>{children}</>}));
jest.mock('../components/IncidentList', () => ({IncidentList:({incidents,onSelectedRowKeysChange,selectedRowKeys}:any)=> <><button onClick={()=>onSelectedRowKeysChange(incidents.map((item:any)=>item.id))}>Select incidents</button><span>Selected: {selectedRowKeys.join(',')}</span></>}));
jest.mock('../components/IncidentFilters', () => ({IncidentFilters:()=>null}));
jest.mock('../components/IncidentStats', () => ({IncidentStats:()=>null}));
jest.mock('@/components/business/UnifiedKanbanBoard', () => ({UnifiedKanbanBoard:()=>null}));
jest.mock('@/components/business/BatchActionBar', () => ({BatchActionBar:({actions}:any)=><>{actions.map((action:any)=><button key={action.key} onClick={action.onClick}>{action.label}</button>)}</>}));

const rows = [{id:7,version:3,title:'Incident',status:'resolved'}];

beforeEach(() => {
  jest.clearAllMocks();
  (IncidentAPI.listIncidents as jest.Mock).mockReset().mockResolvedValue({incidents:rows,total:1});
  (IncidentAPI.getIncidentMetrics as jest.Mock).mockResolvedValue({});
  (IncidentAPI.closeIncident as jest.Mock).mockReset();
  (IncidentAPI.resolveIncident as jest.Mock).mockReset();
  Object.defineProperty(crypto,'randomUUID',{configurable:true,value:jest.fn().mockReturnValue('retire-op-1')});
});

/**
 * 打开批量操作确认框 → 填写说明 → 卸载页面 → 若确认框仍残留则点击"确定"。
 * 返回是否真的点到了残留的确认框，避免用例空跑。
 */
async function submitStaleConfirmation(actionLabel: string, fieldLabel: string, value: string) {
  const page = render(<IncidentsPage />);
  await waitFor(()=>expect(IncidentAPI.listIncidents).toHaveBeenCalledTimes(1));
  fireEvent.click(await screen.findByRole('button',{name:'Select incidents'}));
  fireEvent.click(await screen.findByRole('button',{name:actionLabel}));
  const dialog = await screen.findByRole('dialog');
  fireEvent.change(within(dialog).getByLabelText(fieldLabel),{target:{value}});

  page.unmount();

  const lingering = screen.queryByRole('dialog');
  const ok = lingering ? within(lingering).queryByRole('button',{name:/确|OK/}) : null;
  if (ok) fireEvent.click(ok);
  await new Promise(resolve=>setTimeout(resolve,80));
  return Boolean(ok);
}

test('does not submit a batch close after the page unmounts', async () => {
  const attempted = await submitStaleConfirmation('批量关闭','关闭说明','Old session confirmation');
  expect(attempted).toBe(true);
  expect(IncidentAPI.closeIncident).not.toHaveBeenCalled();
});

test('does not submit a batch resolve after the page unmounts', async () => {
  const attempted = await submitStaleConfirmation('批量解决','恢复验证说明','Old session resolution');
  expect(attempted).toBe(true);
  expect(IncidentAPI.resolveIncident).not.toHaveBeenCalled();
});
