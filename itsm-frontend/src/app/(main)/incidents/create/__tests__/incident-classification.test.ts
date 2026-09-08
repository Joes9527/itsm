import { classificationInput, classificationOptions } from '../incident-classification';
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
