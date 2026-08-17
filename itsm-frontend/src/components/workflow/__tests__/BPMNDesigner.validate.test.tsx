// Mock bpmn-js before importing the component
jest.mock('bpmn-js/lib/Modeler', () => {
  return jest.fn().mockImplementation(() => ({
    createDiagram: jest.fn().mockResolvedValue({}),
    importXML: jest.fn().mockResolvedValue({}),
    saveXML: jest.fn().mockResolvedValue({ xml: '<?xml version="1.0"?><bpmn:definitions/>' }),
    saveSVG: jest.fn().mockResolvedValue({ svg: '<svg/>' }),
    on: jest.fn(),
    destroy: jest.fn(),
    get: jest.fn().mockImplementation((name: string) => {
      if (name === 'canvas') {
        return { zoom: jest.fn() };
      }
      if (name === 'selection') {
        return { get: jest.fn().mockReturnValue([]) };
      }
      if (name === 'modeling') {
        return { removeElements: jest.fn() };
      }
      return {};
    }),
  }));
});

jest.mock('diagram-js/lib/features/grid-snapping', () => ({}));

import { checkUnsupportedElements } from '../BPMNDesigner';

describe('checkUnsupportedElements — 检测引擎不支持真正执行的元素类型', () => {
  it('并行网关触发警告，说明引擎会退化成单分支执行', () => {
    const elements = [
      { id: 'Gateway_1', type: 'bpmn:ParallelGateway', businessObject: { name: '并行网关' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues).toHaveLength(1);
    expect(issues[0].type).toBe('warning');
    expect(issues[0].message).toContain('并行网关');
    expect(issues[0].message).toContain('不支持');
  });

  it('包容网关同样触发警告', () => {
    const elements = [
      { id: 'Gateway_2', type: 'bpmn:InclusiveGateway', businessObject: { name: '包容网关' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues).toHaveLength(1);
    expect(issues[0].message).toContain('包容网关');
  });

  it('子流程触发警告', () => {
    const elements = [
      { id: 'Sub_1', type: 'bpmn:SubProcess', businessObject: { name: '子流程' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues).toHaveLength(1);
    expect(issues[0].message).toContain('子流程');
  });

  it('排他网关、用户任务等受支持的元素不触发这条规则', () => {
    const elements = [
      { id: 'Gateway_3', type: 'bpmn:ExclusiveGateway', businessObject: { name: '排他网关' } },
      { id: 'Task_1', type: 'bpmn:UserTask', businessObject: { name: '用户任务' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues).toHaveLength(0);
  });

  it('每个不支持的元素都带上 elementId，方便定位', () => {
    const elements = [
      { id: 'Gateway_4', type: 'bpmn:ParallelGateway', businessObject: { name: '并行网关4' } },
    ];
    const issues = checkUnsupportedElements(elements);
    expect(issues[0].elementId).toBe('Gateway_4');
    expect(issues[0].elementType).toBe('bpmn:ParallelGateway');
    expect(issues[0].elementName).toBe('并行网关4');
  });
});
