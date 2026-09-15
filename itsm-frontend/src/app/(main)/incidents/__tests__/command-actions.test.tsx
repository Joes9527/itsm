import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@/lib/test-utils';
import IncidentsPage from '../page';
import {IncidentAPI} from '@/lib/api/incident-api';

jest.mock('next/navigation', () => ({useRouter:()=>({push:jest.fn()})}));
jest.mock('@/lib/i18n/useI18n', () => ({useI18n:()=>({t: mockTranslate})}));
const mockTranslate = (key:string) => key;
jest.mock('@/lib/api/incident-api', () => ({IncidentAPI:{listIncidents:jest.fn(),getIncidentMetrics:jest.fn(),closeIncident:jest.fn()}}));
jest.mock('@/lib/api/user-api', () => ({UserApi:{getUsers:jest.fn().mockResolvedValue({users:[]})}}));
jest.mock('@/components/layout/BusinessPageTemplate', () => ({BusinessPageTemplate:({children}:React.PropsWithChildren)=> <>{children}</>}));
jest.mock('../components/IncidentList', () => ({IncidentList:({incidents,onSelectedRowKeysChange,selectedRowKeys}:any)=> <><button onClick={()=>onSelectedRowKeysChange(incidents.map((item:any)=>item.id))}>Select incidents</button><span>Selected: {selectedRowKeys.join(',')}</span></>}));
jest.mock('../components/IncidentFilters', () => ({IncidentFilters:()=>null}));
jest.mock('../components/IncidentStats', () => ({IncidentStats:()=>null}));
jest.mock('@/components/business/UnifiedKanbanBoard', () => ({UnifiedKanbanBoard:()=>null}));
jest.mock('@/components/business/BatchActionBar', () => ({BatchActionBar:({actions,loading}:any)=><>{actions.map((action:any)=><button key={action.key} disabled={loading} onClick={action.onClick}>{action.label}</button>)}</>}));
const rows = [{id:7,version:3,title:'Incident',status:'resolved'}];
beforeEach(() => {
  jest.clearAllMocks();
  (IncidentAPI.listIncidents as jest.Mock).mockReset().mockResolvedValue({incidents:rows,total:1});
  (IncidentAPI.getIncidentMetrics as jest.Mock).mockResolvedValue({});
  (IncidentAPI.closeIncident as jest.Mock).mockReset();
  Object.defineProperty(crypto,'randomUUID',{configurable:true,value:jest.fn().mockReturnValueOnce('first-close').mockReturnValue('new-close')});
});
async function confirmClose() {
  fireEvent.click(await screen.findByRole('button',{name:'批量关闭'}));
  const dialog = await screen.findByRole('dialog');
  fireEvent.change(within(dialog).getByLabelText('关闭说明'),{target:{value:'Requester verified'}});
  fireEvent.click(within(dialog).getByRole('button',{name:/确|OK/}));
}
test('list closure reuses the original version after an uncertain response and refreshes after the receipt', async () => {
  (IncidentAPI.closeIncident as jest.Mock).mockRejectedValueOnce(new Error('connection lost')).mockResolvedValueOnce({workItemId:101,version:4,status:'closed',replayed:true});
  (IncidentAPI.listIncidents as jest.Mock).mockResolvedValueOnce({incidents:rows,total:1}).mockResolvedValue({incidents:[{...rows[0],version:4}],total:1});
  render(<IncidentsPage />);
  await waitFor(()=>expect(IncidentAPI.listIncidents).toHaveBeenCalledTimes(1));
  await screen.findByRole('button',{name:'批量关闭'});
  fireEvent.click(await screen.findByRole('button',{name:'Select incidents'}));
  await confirmClose();
  await waitFor(()=>expect(IncidentAPI.listIncidents).toHaveBeenCalledTimes(2));
  await waitFor(()=>expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  expect(screen.getByText('Selected: 7')).toBeInTheDocument();
  await confirmClose();
  await waitFor(()=>expect(IncidentAPI.closeIncident).toHaveBeenCalledTimes(2));
  const calls=(IncidentAPI.closeIncident as jest.Mock).mock.calls;
  expect(calls[0]).toEqual([7,{version:3,operationId:'first-close',reason:'Requester verified'}]);
  expect(calls[1]).toEqual(calls[0]);
  await waitFor(()=>expect(IncidentAPI.listIncidents).toHaveBeenCalledTimes(3));
});
