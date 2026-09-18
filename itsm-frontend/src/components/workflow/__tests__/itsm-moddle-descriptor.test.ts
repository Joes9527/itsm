import itsmModdleDescriptor from '../itsm-moddle-descriptor';
// Jest resolves the package's ESM entry, so exercise its published CommonJS build.
const BpmnModdle = require('../../../../node_modules/bpmn-moddle/dist/bpmn-moddle.umd.js');

describe('ITSM BPMN moddle descriptor', () => {
  it('persists assignment, approval and countersign attributes', () => {
    const properties = itsmModdleDescriptor.types[0].properties.map(property => property.name);
    expect(properties).toEqual(expect.arrayContaining([
      'assignee', 'assigneeRole', 'assigneeDeptId', 'assigneeGmChain', 'candidateUsers', 'candidateGroups', 'taskPurpose',
      // 引擎支持但过去未声明的属性：未声明会在设计器保存时被丢掉。
      'assigneeTeamId', 'assigneeProjectId', 'assigneeTempTeamId',
      'assigneeDirectManager', 'assigneeManagerLevel',
      'approvalMode', 'approvalThreshold', 'rejectStrategy', 'timeoutAction',
      'allowDelegate', 'allowAddApprover', 'commentRequiredOnReject',
    ]));
  });

  it('round-trips the WorkItem assignment source through BPMN XML', async () => {
    const moddle = new BpmnModdle({ itsm: itsmModdleDescriptor });
    const xml = '<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:itsm="https://github.com/heidsoft/itsm/schema/bpmn" targetNamespace="test"><bpmn:process id="Process_1"><bpmn:userTask id="Task_1" itsm:assigneeSource="work_item_assignee" /></bpmn:process></bpmn:definitions>';

    const { rootElement } = await moddle.fromXML(xml);
    const result = await moddle.toXML(rootElement);

    expect(result.xml).toContain('itsm:assigneeSource="work_item_assignee"');
  });
});
