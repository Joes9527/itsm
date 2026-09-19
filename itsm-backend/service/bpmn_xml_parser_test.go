package service

import (
	"fmt"
	"strings"
	"testing"
)

func TestBPMNParser_ParseXML(t *testing.T) {
	parser := NewBPMNParser()

	// 测试有效的BPMN XML
	validXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" 
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" 
                  xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
                  id="Definitions_1" 
                  targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_1" name="Sample Process" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
    <bpmn:userTask id="UserTask_1" name="Review Request" assignee="reviewer"/>
    <bpmn:exclusiveGateway id="Gateway_1" name="Decision" default="Flow_3"/>
    <bpmn:endEvent id="EndEvent_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="UserTask_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="UserTask_1" targetRef="Gateway_1"/>
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Gateway_1" targetRef="EndEvent_1">
      <bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">approved == true</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
  </bpmn:process>
</bpmn:definitions>`

	definitions, err := parser.ParseXML([]byte(validXML))
	if err != nil {
		t.Fatalf("解析有效BPMN XML失败: %v", err)
	}

	if len(definitions.Processes) != 1 {
		t.Errorf("期望1个流程，实际得到%d个", len(definitions.Processes))
	}

	process := definitions.Processes[0]
	if process.ID != "Process_1" {
		t.Errorf("期望流程ID为Process_1，实际为%s", process.ID)
	}

	if len(process.StartEvents) != 1 {
		t.Errorf("期望1个开始事件，实际得到%d个", len(process.StartEvents))
	}

	if len(process.UserTasks) != 1 {
		t.Errorf("期望1个用户任务，实际得到%d个", len(process.UserTasks))
	}

	if len(process.ExclusiveGateways) != 1 {
		t.Errorf("期望1个排他网关，实际得到%d个", len(process.ExclusiveGateways))
	}

	if len(process.EndEvents) != 1 {
		t.Errorf("期望1个结束事件，实际得到%d个", len(process.EndEvents))
	}

	if len(process.SequenceFlows) != 3 {
		t.Errorf("期望3个顺序流，实际得到%d个", len(process.SequenceFlows))
	}
}

func TestBPMNParser_ValidateBPMNXML(t *testing.T) {
	parser := NewBPMNParser()

	// 测试有效的BPMN XML
	validXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	err := parser.ValidateBPMNXML([]byte(validXML))
	if err != nil {
		t.Errorf("验证有效BPMN XML失败: %v", err)
	}

	// 测试无效的XML
	invalidXML := `<?xml version="1.0" encoding="UTF-8"?>
<invalid>This is not BPMN</invalid>`

	err = parser.ValidateBPMNXML([]byte(invalidXML))
	if err == nil {
		t.Error("期望验证失败，但验证通过了")
	}

	// 测试缺少BPMN命名空间的XML
	noNamespaceXML := `<?xml version="1.0" encoding="UTF-8"?>
<definitions>
  <process id="Process_1" isExecutable="true">
    <startEvent id="StartEvent_1"/>
    <endEvent id="EndEvent_1"/>
  </process>
</definitions>`

	err = parser.ValidateBPMNXML([]byte(noNamespaceXML))
	if err == nil {
		t.Error("期望验证失败（缺少BPMN命名空间），但验证通过了")
	}
}

func TestBPMNParser_ExtractProcessInfo(t *testing.T) {
	parser := NewBPMNParser()

	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" name="Test Process" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:userTask id="UserTask_1" assignee="user1"/>
    <bpmn:serviceTask id="ServiceTask_1" implementation="java"/>
    <bpmn:exclusiveGateway id="Gateway_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="UserTask_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="UserTask_1" targetRef="Gateway_1"/>
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Gateway_1" targetRef="ServiceTask_1"/>
    <bpmn:sequenceFlow id="Flow_4" sourceRef="ServiceTask_1" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	definitions, err := parser.ParseXML([]byte(xmlData))
	if err != nil {
		t.Fatalf("解析XML失败: %v", err)
	}

	info := parser.ExtractProcessInfo(definitions)
	if info == nil {
		t.Fatal("提取流程信息失败")
	}

	expectedElements := map[string]int{
		"startEvents":       1,
		"endEvents":         1,
		"userTasks":         1,
		"serviceTasks":      1,
		"exclusiveGateways": 1,
		"sequenceFlows":     4,
	}

	elements, ok := info["elements"].(map[string]interface{})
	if !ok {
		t.Fatal("无法获取elements信息")
	}

	for elementType, expectedCount := range expectedElements {
		if count, ok := elements[elementType].(int); !ok || count != expectedCount {
			t.Errorf("期望%s数量为%d，实际为%v", elementType, expectedCount, count)
		}
	}
}

func TestBPMNParser_ExtractUserTasks(t *testing.T) {
	parser := NewBPMNParser()

	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:userTask id="UserTask_1" name="Review" assignee="reviewer1" candidateUsers="user1,user2"/>
    <bpmn:userTask id="UserTask_2" name="Approve" assignee="approver" candidateGroups="managers"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="UserTask_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="UserTask_1" targetRef="UserTask_2"/>
    <bpmn:sequenceFlow id="Flow_3" sourceRef="UserTask_2" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	definitions, err := parser.ParseXML([]byte(xmlData))
	if err != nil {
		t.Fatalf("解析XML失败: %v", err)
	}

	tasks := parser.ExtractUserTasks(definitions)
	if len(tasks) != 2 {
		t.Errorf("期望2个用户任务，实际得到%d个", len(tasks))
	}

	// 检查第一个任务
	task1 := tasks[0]
	if task1["id"] != "UserTask_1" {
		t.Errorf("期望任务ID为UserTask_1，实际为%s", task1["id"])
	}
	if task1["assignee"] != "reviewer1" {
		t.Errorf("期望分配者为reviewer1，实际为%s", task1["assignee"])
	}
	if task1["candidateUsers"] != "user1,user2" {
		t.Errorf("期望候选用户为user1,user2，实际为%s", task1["candidateUsers"])
	}

	// 检查第二个任务
	task2 := tasks[1]
	if task2["id"] != "UserTask_2" {
		t.Errorf("期望任务ID为UserTask_2，实际为%s", task2["id"])
	}
	if task2["assignee"] != "approver" {
		t.Errorf("期望分配者为approver，实际为%s", task2["assignee"])
	}
	if task2["candidateGroups"] != "managers" {
		t.Errorf("期望候选组为managers，实际为%s", task2["candidateGroups"])
	}
}

func TestBPMNParser_ExtractGateways(t *testing.T) {
	parser := NewBPMNParser()

	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:exclusiveGateway id="Gateway_1" name="Decision" default="Flow_2"/>
    <bpmn:parallelGateway id="Gateway_2" name="Split"/>
    <bpmn:inclusiveGateway id="Gateway_3" name="Merge" default="Flow_5"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Gateway_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Gateway_1" targetRef="Gateway_2"/>
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Gateway_2" targetRef="Gateway_3"/>
    <bpmn:sequenceFlow id="Flow_4" sourceRef="Gateway_3" targetRef="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_5" sourceRef="Gateway_3" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	definitions, err := parser.ParseXML([]byte(xmlData))
	if err != nil {
		t.Fatalf("解析XML失败: %v", err)
	}

	gateways := parser.ExtractGateways(definitions)

	// 检查排他网关
	if len(gateways["exclusive"]) != 1 {
		t.Errorf("期望1个排他网关，实际得到%d个", len(gateways["exclusive"]))
	}
	exclusiveGateway := gateways["exclusive"][0]
	if exclusiveGateway["id"] != "Gateway_1" {
		t.Errorf("期望排他网关ID为Gateway_1，实际为%s", exclusiveGateway["id"])
	}
	if exclusiveGateway["defaultFlow"] != "Flow_2" {
		t.Errorf("期望默认流为Flow_2，实际为%s", exclusiveGateway["defaultFlow"])
	}

	// 检查并行网关
	if len(gateways["parallel"]) != 1 {
		t.Errorf("期望1个并行网关，实际得到%d个", len(gateways["parallel"]))
	}
	parallelGateway := gateways["parallel"][0]
	if parallelGateway["id"] != "Gateway_2" {
		t.Errorf("期望并行网关ID为Gateway_2，实际为%s", parallelGateway["id"])
	}

	// 检查包容网关
	if len(gateways["inclusive"]) != 1 {
		t.Errorf("期望1个包容网关，实际得到%d个", len(gateways["inclusive"]))
	}
	inclusiveGateway := gateways["inclusive"][0]
	if inclusiveGateway["id"] != "Gateway_3" {
		t.Errorf("期望包容网关ID为Gateway_3，实际为%s", inclusiveGateway["id"])
	}
}

func TestBPMNParser_ExtractSequenceFlows(t *testing.T) {
	parser := NewBPMNParser()

	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:userTask id="UserTask_1"/>
    <bpmn:exclusiveGateway id="Gateway_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="UserTask_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="UserTask_1" targetRef="Gateway_1"/>
    <bpmn:sequenceFlow id="Flow_3" sourceRef="Gateway_1" targetRef="EndEvent_1">
      <bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">amount > 1000</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
  </bpmn:process>
</bpmn:definitions>`

	definitions, err := parser.ParseXML([]byte(xmlData))
	if err != nil {
		t.Fatalf("解析XML失败: %v", err)
	}

	flows := parser.ExtractSequenceFlows(definitions)
	if len(flows) != 3 {
		t.Errorf("期望3个顺序流，实际得到%d个", len(flows))
	}

	// 检查带条件的顺序流
	conditionalFlow := flows[2]
	if conditionalFlow["id"] != "Flow_3" {
		t.Errorf("期望顺序流ID为Flow_3，实际为%s", conditionalFlow["id"])
	}
	if conditionalFlow["conditionExpression"] != "amount > 1000" {
		t.Errorf("期望条件表达式为'amount > 1000'，实际为%s", conditionalFlow["conditionExpression"])
	}
}

func TestBPMNParser_GenerateBPMNSummary(t *testing.T) {
	parser := NewBPMNParser()

	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" name="Test Process" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:userTask id="UserTask_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="UserTask_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="UserTask_1" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	definitions, err := parser.ParseXML([]byte(xmlData))
	if err != nil {
		t.Fatalf("解析XML失败: %v", err)
	}

	summary := parser.GenerateBPMNSummary(definitions)
	if summary == nil {
		t.Fatal("生成摘要失败")
	}

	if summary["totalProcesses"] != 1 {
		t.Errorf("期望总流程数为1，实际为%d", summary["totalProcesses"])
	}

	processes := summary["processes"].([]map[string]interface{})
	if len(processes) != 1 {
		t.Errorf("期望1个流程摘要，实际得到%d个", len(processes))
	}

	process := processes[0]
	if process["id"] != "Process_1" {
		t.Errorf("期望流程ID为Process_1，实际为%s", process["id"])
	}

	elements := process["elements"].(map[string]interface{})
	if elements["startEvents"] != 1 {
		t.Errorf("期望开始事件数为1，实际为%d", elements["startEvents"])
	}
	if elements["userTasks"] != 1 {
		t.Errorf("期望用户任务数为1，实际为%d", elements["userTasks"])
	}
	if elements["endEvents"] != 1 {
		t.Errorf("期望结束事件数为1，实际为%d", elements["endEvents"])
	}
	if elements["sequenceFlows"] != 2 {
		t.Errorf("期望顺序流数为2，实际为%d", elements["sequenceFlows"])
	}
}

func TestBPMNParser_ValidateProcess(t *testing.T) {
	parser := NewBPMNParser()

	// 测试缺少开始事件的流程
	invalidXML1 := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" isExecutable="true">
    <bpmn:userTask id="UserTask_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="UserTask_1" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err := parser.ParseXML([]byte(invalidXML1))
	if err == nil {
		t.Error("期望验证失败（缺少开始事件），但验证通过了")
	}

	// 测试缺少结束事件的流程
	invalidXML2 := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:userTask id="UserTask_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="UserTask_1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err = parser.ParseXML([]byte(invalidXML2))
	if err == nil {
		t.Error("期望验证失败（缺少结束事件），但验证通过了")
	}

	// 测试无效的顺序流引用
	invalidXML3 := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="NonExistentElement" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err = parser.ParseXML([]byte(invalidXML3))
	if err == nil {
		t.Error("期望验证失败（无效的顺序流引用），但验证通过了")
	}
}

func TestBPMNParser_ParseXMLFromReader(t *testing.T) {
	parser := NewBPMNParser()

	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Process_1" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"/>
    <bpmn:endEvent id="EndEvent_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`

	reader := strings.NewReader(xmlData)
	definitions, err := parser.ParseXMLFromReader(reader)
	if err != nil {
		t.Fatalf("从Reader解析XML失败: %v", err)
	}

	if len(definitions.Processes) != 1 {
		t.Errorf("期望1个流程，实际得到%d个", len(definitions.Processes))
	}

	process := definitions.Processes[0]
	if process.ID != "Process_1" {
		t.Errorf("期望流程ID为Process_1，实际为%s", process.ID)
	}
}

// purposeDeclarationXML 生成一个最小可解析流程：单个用户任务，带上给定的任务属性和
// extensionElements 内容，用来验证 ParseXML 从流程定义解析出什么任务用途。
func purposeDeclarationXML(t *testing.T, taskAttrs, extensionElements string) []byte {
	t.Helper()
	return fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  id="Definitions_1"
                  targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Approval" name="Approval Process" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
    <bpmn:userTask id="Activity_ManagerApproval" name="主管审批"%s>
      %s
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_ManagerApproval"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Activity_ManagerApproval" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`, taskAttrs, extensionElements)
}

// parseSinglePurpose 解析一个用户任务，返回解析出的任务用途。
func parseSinglePurpose(t *testing.T, taskAttrs, extensionElements string) string {
	t.Helper()
	definitions, err := NewBPMNParser().ParseXML(purposeDeclarationXML(t, taskAttrs, extensionElements))
	if err != nil {
		t.Fatalf("解析BPMN XML失败: %v", err)
	}
	if len(definitions.Processes) != 1 || len(definitions.Processes[0].UserTasks) != 1 {
		t.Fatalf("期望1个流程和1个用户任务，实际得到%d个流程", len(definitions.Processes))
	}
	return definitions.Processes[0].UserTasks[0].TaskPurpose
}

// incident_emergency_flow / problem_management_flow_cn 里的审批节点用节点级
// approval_required metaData 声明审批，而不是 taskPurpose 属性。引擎只认属性时，
// 这些审批节点会退化成普通任务：待办中心看不到它们，审批人解析和"申请人不能审批
// 自己的任务"这条授权也不生效。
func TestBPMNParser_ApprovalRequiredMetaDataDeclaresApprovalTask(t *testing.T) {
	purpose := parseSinglePurpose(t, "", `<bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">incident_task</bpmn:metaData>
        <bpmn:metaData name="action">manager_approval</bpmn:metaData>
        <bpmn:metaData name="approval_required">true</bpmn:metaData>
      </bpmn:extensionElements>`)

	if purpose != "approval" {
		t.Errorf("期望节点级 approval_required=true 解析出 taskPurpose=approval，实际为 %q", purpose)
	}
}

func TestBPMNParser_ExplicitTaskPurposeWinsOverApprovalRequiredMetaData(t *testing.T) {
	purpose := parseSinglePurpose(t, ` taskPurpose="fulfillment"`, `<bpmn:extensionElements>
        <bpmn:metaData name="approval_required">true</bpmn:metaData>
      </bpmn:extensionElements>`)

	if purpose != "fulfillment" {
		t.Errorf("显式 taskPurpose 属性应优先于 approval_required 声明，实际为 %q", purpose)
	}
}

func TestBPMNParser_OnlyExplicitApprovalRequiredTrueDeclaresApproval(t *testing.T) {
	cases := map[string]string{
		"未声明":                 `<bpmn:extensionElements><bpmn:metaData name="action">manager_approval</bpmn:metaData></bpmn:extensionElements>`,
		"显式 false":            `<bpmn:extensionElements><bpmn:metaData name="approval_required">false</bpmn:metaData></bpmn:extensionElements>`,
		"无 extensionElements": ``,
	}

	for name, extensionElements := range cases {
		t.Run(name, func(t *testing.T) {
			purpose := parseSinglePurpose(t, "", extensionElements)
			if purpose != "" {
				t.Errorf("只有显式 approval_required=true 才算审批任务，实际解析出 %q", purpose)
			}
		})
	}
}

// approval_required 同时还是流程变量的名字（ticket_general_flow 等用它做网关条件），
// 所以审批声明只认节点自身 extensionElements 里的那份，流程层的同名 metaData 不算。
func TestBPMNParser_ProcessLevelApprovalRequiredDoesNotDeclareNodeApproval(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  id="Definitions_1"
                  targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_Approval" name="Approval Process" isExecutable="true">
    <bpmn:extensionElements>
      <bpmn:metaData name="approval_required">true</bpmn:metaData>
    </bpmn:extensionElements>
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
    <bpmn:userTask id="Activity_ManagerApproval" name="主管审批"/>
    <bpmn:endEvent id="EndEvent_1" name="End"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_ManagerApproval"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Activity_ManagerApproval" targetRef="EndEvent_1"/>
  </bpmn:process>
</bpmn:definitions>`)

	definitions, err := NewBPMNParser().ParseXML(xmlData)
	if err != nil {
		t.Fatalf("解析BPMN XML失败: %v", err)
	}
	if purpose := definitions.Processes[0].UserTasks[0].TaskPurpose; purpose != "" {
		t.Errorf("流程层 approval_required 不应把节点标成审批任务，实际解析出 %q", purpose)
	}
}

func TestBPMNParser_MalformedApprovalRequiredFailsClosed(t *testing.T) {
	cases := map[string]string{
		"非布尔取值": `<bpmn:extensionElements><bpmn:metaData name="approval_required">yes</bpmn:metaData></bpmn:extensionElements>`,
		"重复声明": `<bpmn:extensionElements>
        <bpmn:metaData name="approval_required">true</bpmn:metaData>
        <bpmn:metaData name="approval_required">false</bpmn:metaData>
      </bpmn:extensionElements>`,
	}

	for name, extensionElements := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewBPMNParser().ParseXML(purposeDeclarationXML(t, "", extensionElements))
			if err == nil {
				t.Fatal("畸形的 approval_required 声明必须让解析失败关闭，实际解析成功")
			}
			if !strings.Contains(err.Error(), "approval_required") {
				t.Errorf("错误信息应指出出问题的是 approval_required 声明，实际为 %v", err)
			}
		})
	}
}

// 显式 taskPurpose 属性只决定优先级，不能成为免检通道：定义里同时出现属性和畸形声明
// （取值非布尔、重复声明）属于自相矛盾，必须和只有声明时一样失败关闭。
func TestBPMNParser_MalformedApprovalRequiredFailsClosedWithExplicitTaskPurpose(t *testing.T) {
	cases := map[string]string{
		"非布尔取值": `<bpmn:extensionElements><bpmn:metaData name="approval_required">yes</bpmn:metaData></bpmn:extensionElements>`,
		"重复声明": `<bpmn:extensionElements>
        <bpmn:metaData name="approval_required">true</bpmn:metaData>
        <bpmn:metaData name="approval_required">false</bpmn:metaData>
      </bpmn:extensionElements>`,
	}

	for name, extensionElements := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewBPMNParser().ParseXML(
				purposeDeclarationXML(t, ` taskPurpose="fulfillment"`, extensionElements))
			if err == nil {
				t.Fatal("带显式 taskPurpose 的节点上出现畸形 approval_required 声明也必须失败关闭，实际解析成功")
			}
			if !strings.Contains(err.Error(), "approval_required") {
				t.Errorf("错误信息应指出出问题的是 approval_required 声明，实际为 %v", err)
			}
		})
	}
}
