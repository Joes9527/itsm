import { buildWorkItemAssigneePatch } from '../designer/work-item-assignment-policy';
import itsmModdleDescriptor from '../itsm-moddle-descriptor';

const BpmnModdle = require('../../../../node_modules/bpmn-moddle/dist/bpmn-moddle.umd.js');

it('removes delegated-handler metadata while preserving unknown extensions on export', async () => {
  const moddle = new BpmnModdle({ itsm: itsmModdleDescriptor });
  const xml = '<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:itsm="https://github.com/heidsoft/itsm/schema/bpmn" targetNamespace="test"><bpmn:process id="Process_1"><bpmn:userTask id="Task_1" taskPurpose="fulfillment" /></bpmn:process></bpmn:definitions>';
  const { rootElement } = await moddle.fromXML(xml);
  const task = rootElement.rootElements[0].flowElements[0];
  const metadata = (name: string, body: string) => moddle.createAny(
    'bpmn:metaData', 'http://www.omg.org/spec/BPMN/20100524/MODEL', { name, $body: body }
  );
  task.extensionElements = moddle.create('bpmn:ExtensionElements', { values: [
    metadata('service_task_type', 'ticket_task'), metadata('action', 'assign'),
    metadata('allowed_actions', 'complete'), metadata('callback_config_ref', 'connector-1'),
    metadata('callback_optional', 'true'), metadata('future_setting', 'keep-me'),
  ] });

  const patch = buildWorkItemAssigneePatch(task);
  Object.assign(task, patch);
  const result = await moddle.toXML(rootElement);

  expect(result.xml).toContain('assigneeSource="work_item_assignee"');
  expect(result.xml).toContain('name="future_setting"');
  expect(result.xml).toContain('keep-me');
  expect(result.xml).not.toContain('name="service_task_type"');
  expect(result.xml).not.toContain('name="action"');
  expect(result.xml).not.toContain('name="allowed_actions"');
  expect(result.xml).not.toContain('name="callback_config_ref"');
  expect(result.xml).not.toContain('name="callback_optional"');
});
