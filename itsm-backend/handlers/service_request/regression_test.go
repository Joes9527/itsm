package service_request

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/schema"
	"itsm-backend/handlers/cmdb"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// 本文件是 Wave0 ServiceRequest 域回归测试补齐任务的产出，覆盖任务包指定的 5 个场景：
//  1. TestService_Create_FullChain_TicketStatusReflectedAfterChange —— 目录提交到 Ticket 的全链路。
//  2. TestService_Create_FormDataFieldValuesConsistency_FieldLevel —— form_data/field_values 一致性（字段级）。
//  3. TestService_Create_ResolvesApprovalChainIntoFormData —— 审批链解析生成的流程变量正确性。
//  4. TestService_Create_IncidentCatalog_NoServiceRequestRowCreated —— itsm_type=Incident 分流。
//  5. TestService_CrossTenantIsolation_GetUpdateDelete —— 跨租户隔离。
//
// Wave0 版本这些测试只锁定现状行为，不修复任何已发现的缺陷。Wave2（ServiceRequest 层级规范化）
// 改动了其中两个场景对应的现状行为，测试跟着一起更新，不是新增测试：
//   - 场景 2：form_data/field_values 双写已收敛为 field_values 单一权威，断言从"两边相等"
//     改为"field_values 有值且 form_data 不再重复该键"（entity.go stripStructuredFieldKeys）。
//   - 场景 4：Incident 分流判断的依据从 itsm_type 改成 target_class（entity.go
//     isIncidentCatalog），测试 fixture 需要同时设置两个字段模拟"已跑过
//     cmd/backfill_servicecatalog_target_class 回填"的状态。

// TestService_Create_FullChain_TicketStatusReflectedAfterChange 覆盖场景 1：
// 服务目录提交表单 → Service.Create 生成 ServiceRequest → 委托生成关联 Ticket，
// 且 ServiceRequest 侧展示的状态（List/GetByTicketID）必须从 Ticket 实时读回，
// 不是创建时刻的快照——ServiceRequest 表本身不存 status 列（entity.go 的注释已经说明
// 这是委托设计，不是遗漏）。
func TestService_Create_FullChain_TicketStatusReflectedAfterChange(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_full_chain_status?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-full-chain").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("full-chain-requester").SetEmail("full-chain@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-全链路", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc, nil, nil)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":  "申请一台云主机-全链路",
			"reason": "全链路回归测试",
		},
	})
	require.NoError(t, err)
	require.Greater(t, created.TicketID, 0, "全链路必须委托创建关联 Ticket 并回写 TicketID")

	// 创建之后的初始状态：List 读回的 TicketStatus 应与新建 Ticket 的初始状态一致。
	list, total, err := svc.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, list, 1)
	initialTicket, err := client.Ticket.Get(ctx, created.TicketID)
	require.NoError(t, err)
	assert.Equal(t, initialTicket.Status, list[0].TicketStatus, "初始状态必须从关联 Ticket 读回")

	// 模拟工作流推进：Ticket 状态变化后，ServiceRequest 侧必须实时读回最新状态，
	// 而不是创建时刻缓存的快照——因为 ServiceRequest.List/GetByTicketID 每次都重新查询
	// 关联 Ticket（见 service.go attachTicketSummaries），不持久化 status 到 SR 自身。
	_, err = client.Ticket.UpdateOneID(created.TicketID).SetStatus("resolved").Save(ctx)
	require.NoError(t, err)

	listAfter, _, err := svc.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	require.Len(t, listAfter, 1)
	assert.Equal(t, "resolved", listAfter[0].TicketStatus,
		"Ticket 状态变化后，ServiceRequest 列表必须读回最新状态，不能停留在创建时的快照")

	fetchedByTicket, err := svc.GetByTicketID(ctx, created.TicketID, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, fetchedByTicket)
	assert.Equal(t, created.ID, fetchedByTicket.ID)
	assert.Equal(t, catalog.ID, fetchedByTicket.CatalogID)
	assert.Equal(t, tenant.ID, fetchedByTicket.TenantID)
}

// TestService_Create_FormDataFieldValuesConsistency_FieldLevel 覆盖场景 2：field_values
// 是结构化自定义字段的唯一权威来源（design doc §8.3），form_data 不再重复存储同一批字段
// （entity.go stripStructuredFieldKeys，Wave2 收敛掉了此前 field_values/form_data 的双写）。
// 这条测试按任务包要求做精确到字段级的比对：field_values（entity_type="ticket"）里能查到
// 每个提交的自定义字段且值正确；form_data JSON 里这些同名键必须不存在——不能只断言
// "form_data 有值"，要断言"不重复"。系统上下文键（title/reason）不受影响，仍然留在
// form_data 里。
func TestService_Create_FormDataFieldValuesConsistency_FieldLevel(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_form_data_consistency?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-consistency").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("consistency-requester").SetEmail("consistency@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	// 定义三个自定义字段，全部非必填——测试关心的是一致性，不是必填校验。
	catalog, err := scService.Create(ctx, "云主机申请-一致性", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{
			{Name: "environment", Label: "环境", FieldType: "text"},
			{Name: "budget_code", Label: "预算代码", FieldType: "text"},
			{Name: "contact_note", Label: "联系备注", FieldType: "text"},
		}, "", "")
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc, nil, nil)

	submittedCustomFields := map[string]interface{}{
		"environment":  "production",
		"budget_code":  "BC-100",
		"contact_note": "urgent, please expedite",
	}
	formData := map[string]interface{}{
		"title":  "申请一台云主机-一致性",
		"reason": "一致性回归测试",
	}
	for k, v := range submittedCustomFields {
		formData[k] = v
	}

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData:           formData,
	})
	require.NoError(t, err)
	require.Greater(t, created.TicketID, 0)

	// 一侧：field_values 表，entity_type="ticket"，entity_id=createdTicket.ID
	// （service.go:212 明确把 entity_type 归到 ticket，而不是 service_request）。
	fieldValues, err := service.NewFieldValueService(client).ListValues(ctx, tenant.ID, "ticket", created.TicketID)
	require.NoError(t, err)
	require.Len(t, fieldValues, len(submittedCustomFields), "field_values 里应该能查到全部提交的自定义字段")
	fieldValueByName := make(map[string]interface{}, len(fieldValues))
	for _, fv := range fieldValues {
		fieldValueByName[fv.Name] = fv.Value
	}

	// 另一侧：form_data JSON 列，从 repo 重新查询（走真实的 ent JSON 序列化/反序列化往返，
	// 不是直接读内存里创建前的 map）。
	fetched, err := srRepo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched.FormData)

	// field_values 侧：每个提交的自定义字段值必须正确、完整。
	for name, submittedValue := range submittedCustomFields {
		fvValue, ok := fieldValueByName[name]
		require.True(t, ok, "field_values 表里缺少字段 %q", name)
		assert.Equal(t, submittedValue, fvValue, "field_values[%q] 必须等于提交时的原始值", name)
	}

	// form_data 侧：结构化字段已经收敛到 field_values 单一权威，不能在 form_data 里重复出现——
	// 明确断言"键不存在"，不是弱化成"值可能为空"。
	for name := range submittedCustomFields {
		_, stillPresent := fetched.FormData[name]
		assert.False(t, stillPresent, "字段 %q 已经写入 field_values，不应该在 form_data 里重复出现（停止双写）", name)
	}

	// 系统上下文键不受影响：title/reason 既不经过 extractServiceRequestFieldValues 提取，
	// 也不应该被误删。
	assert.Equal(t, "申请一台云主机-一致性", fetched.FormData["title"])
	assert.Equal(t, "一致性回归测试", fetched.FormData["reason"])
}

// TestService_Create_ResolvesApprovalChainIntoFormData 覆盖场景 3：
// ApprovalChainResolver.ResolveForServiceRequest 解析出的审批步骤会被注入
// ServiceRequest.FormData["_approval_chain"]，供 BPMN 流程 / 前端引用（entity.go
// injectApprovalChain 的注释）。这条测试验证接入 Service.Create 之后，注入的内容
// 精确等于按金额阈值和 group_controlled 规则过滤后的步骤——不是"存在即可"。
func TestService_Create_ResolvesApprovalChainIntoFormData(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_approval_chain_formdata?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-chain-formdata").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("chain-requester").SetEmail("chain-requester@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	allSteps := []schema.ApprovalChainStep{
		{Level: 1, Name: "部门主管审批", Role: "manager", ApprovalType: "serial", IsRequired: true},
		{Level: 2, Name: "IT审批", Role: "it_admin", ApprovalType: "serial", IsRequired: true, AmountThreshold: 50000},
		{Level: 3, Name: "安全审批", Role: "security_admin", ApprovalType: "parallel", IsRequired: true, GroupControlled: true},
	}
	_, err = client.ApprovalChain.Create().
		SetName("服务请求审批链").
		SetEntityType("service_request").
		SetTenantID(tenant.ID).
		SetChain(allSteps).
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-审批链", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	chainResolver := service.NewApprovalChainResolver(client, logger)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc, chainResolver, nil)

	// 金额低于 IT 审批阈值（50000）：应该只保留 level 1（普通步骤）和 level 3
	// （group_controlled，始终保留），level 2 被过滤掉。
	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":  "申请一台云主机-审批链",
			"reason": "审批链回归测试",
			"amount": float64(10000),
		},
	})
	require.NoError(t, err)

	expectedFiltered := []schema.ApprovalChainStep{allSteps[0], allSteps[2]}
	assertApprovalChainStepsEqual(t, expectedFiltered, created.FormData["_approval_chain"])

	// 从 repo 重新查询，确认注入的流程变量确实持久化进了 form_data JSON 列，
	// 不只是内存里的临时值。
	fetched, err := srRepo.Get(ctx, created.ID, tenant.ID)
	require.NoError(t, err)
	assertApprovalChainStepsEqual(t, expectedFiltered, fetched.FormData["_approval_chain"])

	// 金额达到 IT 审批阈值：三个步骤全部保留。
	createdHighAmount, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":  "申请一台云主机-审批链-高金额",
			"reason": "审批链回归测试-高金额",
			"amount": float64(80000),
		},
	})
	require.NoError(t, err)
	assertApprovalChainStepsEqual(t, allSteps, createdHighAmount.FormData["_approval_chain"])
}

// TestService_Create_NoApprovalChainConfigured_FormDataHasNoApprovalChainKey 是场景 3 的
// 补充：租户未配置 service_request 类型的审批链时，resolvedSteps 为 nil，
// hasApprovalChainSteps 判 false，FormData 不应该出现 "_approval_chain" 这个键
// （entity.go injectApprovalChain：steps==nil 时原样返回 formData，不写空键）。
func TestService_Create_NoApprovalChainConfigured_FormDataHasNoApprovalChainKey(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_no_approval_chain?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-no-chain").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("no-chain-requester").SetEmail("no-chain@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-无审批链", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	chainResolver := service.NewApprovalChainResolver(client, logger) // 有效但租户下无配置
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc, chainResolver, nil)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":  "申请一台云主机-无审批链",
			"reason": "无审批链回归测试",
		},
	})
	require.NoError(t, err)
	_, hasKey := created.FormData["_approval_chain"]
	assert.False(t, hasKey, "租户未配置审批链时，form_data 不应该出现 _approval_chain 键")
}

// assertApprovalChainStepsEqual 用 JSON 结构比较而不是 reflect.DeepEqual——resolvedSteps
// 在 Service.Create 内部是 []schema.ApprovalChainStep，但从 repo 重新查询之后，ent 的 JSON
// 字段会把它还原成 []interface{} / map[string]interface{}，两种类型语义相同但 Go 类型不同，
// 结构化 JSON 比较能屏蔽这个无关差异，同时仍然精确到每个字段。
func assertApprovalChainStepsEqual(t *testing.T, expected []schema.ApprovalChainStep, actual interface{}) {
	t.Helper()
	expectedJSON, err := json.Marshal(expected)
	require.NoError(t, err)
	actualJSON, err := json.Marshal(actual)
	require.NoError(t, err)
	assert.JSONEq(t, string(expectedJSON), string(actualJSON))
}

// fakeIncidentCreator 是 IncidentCreator 接口的测试替身。IncidentCreator 是
// handlers/service_request 包自己定义的最小接口（entity.go 顶部注释所述"避免直接依赖具体
// 实现"），生产环境由 internal/bootstrap/app.go 的 srIncidentBridge 适配真正的
// IncidentService；测试这里只关心 Service.Create 在 isIncidentCatalog 分流时是否正确
// 委托、以及委托之后 ServiceRequest 表是否真的没有落地行，不需要拉起完整的 Incident 域。
type fakeIncidentCreator struct {
	incidentID int
	called     bool
	gotTenant  int
	gotCatalog int
	gotTitle   string
}

func (f *fakeIncidentCreator) CreateIncident(ctx context.Context, tenantID, requesterID int, title, description string, catalogID int) (int, error) {
	f.called = true
	f.gotTenant = tenantID
	f.gotCatalog = catalogID
	f.gotTitle = title
	return f.incidentID, nil
}

// TestService_Create_IncidentCatalog_NoServiceRequestRowCreated 覆盖场景 4：itsm_type=Incident
// 的服务目录项在 Service.Create 里直接分流给 IncidentCreator（service.go:85-87
// isIncidentCatalog + createIncidentFromCatalog），跳过 ServiceRequest/Ticket 的正常委托路径。
// 按任务包要求，这里只断言"没有产生 ServiceRequest 行"这一半——Incident 侧的完整行为
// （事件本身的字段/状态是否正确）由 Incident 任务包覆盖。
func TestService_Create_IncidentCatalog_NoServiceRequestRowCreated(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_incident_diversion?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-incident-diversion").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("incident-requester").SetEmail("incident-requester@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "系统故障上报", "运维", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)
	// Service.Create 没有暴露设置 itsm_type 的参数（默认 Request），直接用 ent 改成 Incident，
	// 模拟目录项被配置为"事件类"目录。同时手动设置 target_class——路由判断（entity.go
	// isIncidentCatalog）自 target_class 收敛改造后读的是 target_class 不是 itsm_type，
	// 这里手动补上等价于该行已经跑过 cmd/backfill_servicecatalog_target_class 回填。
	_, err = client.ServiceCatalog.UpdateOneID(catalog.ID).
		SetItsmType("Incident").
		SetTargetClass(service_catalog.TargetClassIncident).
		Save(ctx)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	fakeIncident := &fakeIncidentCreator{incidentID: 4242}
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc, nil, fakeIncident)

	result, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		FormData: map[string]interface{}{"title": "生产环境服务器宕机", "reason": "紧急"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, fakeIncident.called, "Incident 类型目录项必须委托给 IncidentCreator")
	assert.Equal(t, tenant.ID, fakeIncident.gotTenant)
	assert.Equal(t, catalog.ID, fakeIncident.gotCatalog)
	assert.Equal(t, "生产环境服务器宕机", fakeIncident.gotTitle)
	assert.Equal(t, 4242, result.ID, "返回的 stub ServiceRequest 借用 ID 字段传递 incidentID（createIncidentFromCatalog 的注释）")

	_, total, err := srRepo.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "itsm_type=Incident 分流不应该产生任何 ServiceRequest 行")

	ticketCount, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, ticketCount, "Incident 分流跳过 SR→Ticket 委托路径，不应该创建 Ticket")
}

// TestService_Update_ForbiddenForNonOwnerWithoutPermission 和
// TestService_Update_AllowedForNonOwnerWithPermission 不属于任务包 5 个必测场景，是补充覆盖：
// canManageServiceRequest（Update/Delete 共用的权限判定）此前完全没有测试命中（0% 覆盖），
// 补上之后能把整体覆盖率稳定推过 70% 的验收线，而不是卡在四舍五入的边界上。
func TestService_Update_ForbiddenForNonOwnerWithoutPermission(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_update_forbidden?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-update-forbidden").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("owner").SetEmail("owner@test.com").SetName("Owner").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	otherUser, err := client.User.Create().
		SetUsername("other").SetEmail("other@test.com").SetName("Other").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-权限", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc, nil, nil)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData:           map[string]interface{}{"title": "申请一台云主机-权限", "reason": "权限回归测试"},
	})
	require.NoError(t, err)

	// otherUser 既不是申请人，也没有配置 service_request:write 权限（一个干净的
	// enttest 库没有种任何 permissions 行）——canManageServiceRequest 必须判 false。
	_, err = svc.Update(ctx, created.ID, tenant.ID, otherUser.ID, "end_user", &ServiceRequest{CostCenter: "CC-HIJACK"})
	require.Error(t, err)
	appErr, ok := common.AsAppError(err)
	require.True(t, ok)
	assert.Equal(t, common.ErrCodeForbidden, appErr.Code)

	err = svc.Delete(ctx, created.ID, tenant.ID, otherUser.ID, "end_user")
	require.Error(t, err)
	appErr, ok = common.AsAppError(err)
	require.True(t, ok)
	assert.Equal(t, common.ErrCodeForbidden, appErr.Code)
}

func TestService_Update_AllowedForNonOwnerWithSuperAdminRole(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_update_super_admin?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-update-admin").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("owner2").SetEmail("owner2@test.com").SetName("Owner2").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	admin, err := client.User.Create().
		SetUsername("admin").SetEmail("admin@test.com").SetName("Admin").
		SetPasswordHash("hash").SetRole("super_admin").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-管理员", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc, nil, nil)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData:           map[string]interface{}{"title": "申请一台云主机-管理员", "reason": "权限回归测试-管理员"},
	})
	require.NoError(t, err)

	updated, err := svc.Update(ctx, created.ID, tenant.ID, admin.ID, "super_admin", &ServiceRequest{CostCenter: "CC-ADMIN-EDIT"})
	require.NoError(t, err, "super_admin 即使不是申请人也应该能编辑他人的服务请求")
	assert.Equal(t, "CC-ADMIN-EDIT", updated.CostCenter)

	err = svc.Delete(ctx, created.ID, tenant.ID, admin.ID, "super_admin")
	require.NoError(t, err, "super_admin 即使不是申请人也应该能删除他人的服务请求")
}

// TestService_CrossTenantIsolation_GetUpdateDelete 覆盖场景 5：租户 A 创建的 ServiceRequest，
// 租户 B 不能通过 Get/Update/Delete 读取或修改——即使租户 B 的调用方精确知道租户 A 那条记录的
// ID。仓储层已经有 GetByTicketID 的跨租户隔离测试（repository_impl_test.go），这里补齐
// Service 层 Get/Update/Delete 三个直接暴露给 handler 的入口，理由同 CLAUDE.md 对新增/改动的
// tenant-scoped 查询的强制测试要求。
func TestService_CrossTenantIsolation_GetUpdateDelete(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_cross_tenant_gud?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	tenantA, err := client.Tenant.Create().SetName("Tenant A").SetCode("sr-cross-a").SetDomain("cross-a.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	tenantB, err := client.Tenant.Create().SetName("Tenant B").SetCode("sr-cross-b").SetDomain("cross-b.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	requesterA, err := client.User.Create().
		SetUsername("cross-requester-a").SetEmail("cross-requester-a@test.com").SetName("Requester A").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenantA.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalogA, err := scService.Create(ctx, "云主机申请-跨租户", "云服务", "desc", 1, tenantA.ID, "enabled", 0, 0, nil, "", "")
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc, nil, nil)

	created, err := svc.Create(ctx, tenantA.ID, requesterA.ID, catalogA.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		CostCenter:         "CC-TENANT-A",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":  "申请一台云主机-跨租户隔离基线",
			"reason": "跨租户隔离回归测试",
		},
	})
	require.NoError(t, err)

	// 同租户能正常读到——先证明这不是配置错误导致的假阳性。
	sameTenant, err := svc.Get(ctx, created.ID, tenantA.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, sameTenant.ID)

	t.Run("Get", func(t *testing.T) {
		_, err := svc.Get(ctx, created.ID, tenantB.ID)
		require.Error(t, err)
		assert.True(t, ent.IsNotFound(err), "租户 B 不能用租户 A 的 ServiceRequest ID 读取到数据，got: %v", err)
	})

	t.Run("Update", func(t *testing.T) {
		_, err := svc.Update(ctx, created.ID, tenantB.ID, 0, "manager", &ServiceRequest{
			CostCenter: "CC-HIJACKED-BY-TENANT-B",
		})
		require.Error(t, err)
		appErr, ok := common.AsAppError(err)
		require.True(t, ok, "跨租户 Update 必须返回结构化 AppError，got: %v", err)
		assert.Equal(t, common.ErrCodeNotFound, appErr.Code)

		// 确认数据真的没有被改动。
		unchanged, err := svc.Get(ctx, created.ID, tenantA.ID)
		require.NoError(t, err)
		assert.Equal(t, "CC-TENANT-A", unchanged.CostCenter, "跨租户 Update 必须完全不生效")
	})

	t.Run("Delete", func(t *testing.T) {
		err := svc.Delete(ctx, created.ID, tenantB.ID, 0, "manager")
		require.Error(t, err)
		appErr, ok := common.AsAppError(err)
		require.True(t, ok, "跨租户 Delete 必须返回结构化 AppError，got: %v", err)
		assert.Equal(t, common.ErrCodeNotFound, appErr.Code)

		// 确认记录真的没有被软删除。
		stillThere, err := svc.Get(ctx, created.ID, tenantA.ID)
		require.NoError(t, err)
		assert.Equal(t, created.ID, stillThere.ID, "跨租户 Delete 必须完全不生效，记录对租户 A 仍然可见")
	})
}
