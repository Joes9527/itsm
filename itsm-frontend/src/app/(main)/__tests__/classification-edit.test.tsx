import React from 'react';
import { randomUUID } from 'node:crypto';

// jsdom omits this browser API; preserve real operation IDs in the edit contract.
Object.defineProperty(crypto, 'randomUUID', { configurable: true, value: randomUUID });
import { render, screen, fireEvent, waitFor } from '@/lib/test-utils';
import userEvent from '@testing-library/user-event';
import IncidentEdit from '../incidents/[id]/edit/page';
import ProblemEdit from '../problems/[id]/edit/page';
import { IncidentAPI } from '@/lib/api/incident-api';
import { ProblemApi } from '@/lib/api/problem-api';
import { useAuthStore } from '@/lib/store/auth-store';
jest.setTimeout(30000);
let mockId = '7';
const mockRouter = {push:jest.fn(),back:jest.fn()};
jest.mock('next/navigation', () => ({ useParams: () => ({id:mockId}), useRouter: () => mockRouter }));
jest.mock('@/lib/i18n', () => ({useI18n:()=>({t:(key:string)=>key})}));
jest.mock('@/lib/api/ticket-category-api', () => ({TicketCategoryApi:{getCategoryTree:jest.fn().mockResolvedValue([{id:81,name:'原分类',isActive:true,parentId:null},{id:82,name:'新分类',isActive:true,parentId:null}])}}));
jest.mock('@/lib/api/incident-api', () => ({IncidentAPI:{getIncident:jest.fn(),updateIncident:jest.fn().mockResolvedValue({})}}));
jest.mock('@/lib/api/problem-api', () => ({ProblemApi:{getProblem:jest.fn(),updateProblem:jest.fn().mockResolvedValue({})}}));
const record={id:7,version:5,title:'现有记录标题',description:'现有记录的详细说明内容',categoryId:81,category:'重复显示名称',priority:'medium',severity:'medium',status:'new'};
beforeEach(()=>{
 jest.clearAllMocks(); mockId = '7';
 useAuthStore.setState({isAuthenticated:true,user:{id:1,tenantId:2} as never,currentTenant:{id:2} as never});
 jest.mocked(IncidentAPI.getIncident).mockResolvedValue(record as never);
 jest.mocked(ProblemApi.getProblem).mockResolvedValue({...record,status:'open'} as never);
});
for(const [name,Page,save] of [['incident',IncidentEdit,IncidentAPI.updateIncident],['problem',ProblemEdit,ProblemApi.updateProblem]] as const){
 it(`${name} preserves untouched classification`,async()=>{
  render(<Page/>);
  await screen.findByDisplayValue('现有记录标题');
  fireEvent.click(screen.getByRole('button',{name:/保\s*存/}));
  await waitFor(()=>expect(save).toHaveBeenCalledTimes(1));
  const payload=jest.mocked(save).mock.calls[0][1];
  expect(payload).not.toHaveProperty('categoryId');
  expect(payload).not.toHaveProperty('category');
  expect(payload).not.toHaveProperty('classification');
  if (name === 'problem') {
    expect(payload).toMatchObject({ version: 5, operationId: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i) });
  }
 });
 it(`${name} sends selected ID rather than display name`,async()=>{
  render(<Page/>);
  await screen.findByDisplayValue('现有记录标题');
  await waitFor(()=>expect(screen.getByLabelText('分类')).toBeEnabled());
  await userEvent.click(screen.getByLabelText('分类'));
  await userEvent.click(await screen.findByText('新分类'));
  // 分类变化必须说明原因（前后端同一契约）。
  await userEvent.type(screen.getByLabelText('分类调整原因'),'首次分流选错了类型');
  fireEvent.click(screen.getByRole('button',{name:/保\s*存/}));
  await waitFor(()=>expect(save).toHaveBeenCalledTimes(1));
  expect(jest.mocked(save).mock.calls[0][1]).toMatchObject({categoryId:82,classificationReason:'首次分流选错了类型'});
 });
 it(`${name} explicitly clears the classification`,async()=>{
  render(<Page/>);
  await screen.findByDisplayValue('现有记录标题');
  const clear = await screen.findByRole('img',{name:'close-circle'});
  await userEvent.click(clear);
  await userEvent.type(screen.getByLabelText('分类调整原因'),'确认与任何分类无关');
  fireEvent.click(screen.getByRole('button',{name:/保\s*存/}));
  await waitFor(()=>expect(save).toHaveBeenCalledTimes(1));
  expect(jest.mocked(save).mock.calls[0][1]).toMatchObject({categoryId:0,classificationReason:'确认与任何分类无关'});
 });
 it(`${name} blocks a classification change without a reason`,async()=>{
  render(<Page/>);
  await screen.findByDisplayValue('现有记录标题');
  await waitFor(()=>expect(screen.getByLabelText('分类')).toBeEnabled());
  await userEvent.click(screen.getByLabelText('分类'));
  await userEvent.click(await screen.findByText('新分类'));
  fireEvent.click(screen.getByRole('button',{name:/保\s*存/}));
  // 客户端先行阻断，避免"界面成功、后端 400"。
  await waitFor(()=>expect(screen.getByText('调整分类时必须填写原因')).toBeInTheDocument());
  expect(save).not.toHaveBeenCalled();
 });
 it(`${name} drops cancelled selection when navigating to another record`,async()=>{
  const view=render(<Page/>);
  await screen.findByDisplayValue('现有记录标题');
  await waitFor(()=>expect(screen.getByLabelText('分类')).toBeEnabled());
  await userEvent.click(screen.getByLabelText('分类'));
  await userEvent.click(await screen.findByText('新分类'));
  const next={...record,id:8,title:'另一条记录标题',categoryId:81};
  jest.mocked(IncidentAPI.getIncident).mockResolvedValue(next as never);
  jest.mocked(ProblemApi.getProblem).mockResolvedValue({...next,status:'open'} as never);
  mockId='8'; view.rerender(<Page/>);
  await screen.findByDisplayValue('另一条记录标题');
  fireEvent.click(screen.getByRole('button',{name:/保\s*存/}));
  await waitFor(()=>expect(save).toHaveBeenCalledTimes(1));
  expect(jest.mocked(save).mock.calls[0][0]).toBe(8);
  expect(jest.mocked(save).mock.calls[0][1]).not.toHaveProperty('categoryId');
 });
}