import { classificationInput, classificationOptions, classificationPath, classificationUpdate } from '@/components/work-item/classification';
import type { TicketCategory } from '@/lib/api/ticket-category-api';

it('uses IDs for duplicate category names and preserves the selected hierarchy', () => {
  expect(classificationInput([1, 12, 123])).toEqual({ categoryId: 1, typeId: 12, itemId: 123 });
  expect(classificationInput([2, 22])).toEqual({ categoryId: 2, typeId: 22 });
  expect(classificationInput([])).toBeUndefined();
  expect(() => classificationInput([0])).toThrow();
  expect(() => classificationInput([1, 2, 3, 4])).toThrow();
});
it('uses tenant API names and excludes inactive branches', () => {
  const nodes = [{ id: 1, name: '企业应用', isActive: true, children: [
    { id: 2, name: '无法访问', isActive: true },
    { id: 3, name: '已停用', isActive: false },
  ] }, { id: 4, name: '已停用根', isActive: false, children: [{id:5,isActive:true}] }] as TicketCategory[];
  expect(classificationOptions(nodes)).toEqual([{value:1,label:'企业应用',children:[{value:2,label:'无法访问'}]}]);
});

it('reconstructs edit selection by ID and distinguishes omission from clearing', () => {
  const nodes = [{id:1,name:'same',isActive:true,parentId:null,children:[{id:2,name:'same',isActive:true,parentId:1}]}] as TicketCategory[];
  expect(classificationPath(2,nodes)).toEqual([1,2]);
  expect(classificationPath(99,nodes)).toBeUndefined();
  // 未触碰分类时不携带任何分类字段（既不省略也不清空语义混淆）。
  expect(classificationUpdate(undefined,false)).toEqual({});
  // 触碰后必须同时携带最深节点与必填原因（B1 治理契约）。
  expect(classificationUpdate([],true,'明确清空分类')).toEqual({categoryId:0,classificationReason:'明确清空分类'});
  expect(classificationUpdate([1,2],true,'  选错了类型  ')).toEqual({categoryId:2,classificationReason:'选错了类型'});
  expect(classificationUpdate([1,2],true)).toEqual({categoryId:2,classificationReason:''});
});
