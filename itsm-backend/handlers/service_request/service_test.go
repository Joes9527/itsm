package service_request

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/cmdb"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestService_Create_PersistsFieldValues(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_field_values?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-field-values").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester").SetEmail("requester@test.com").SetName("Requester").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "environment", Label: "环境", FieldType: "text"}})
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	ticketSvc := service.NewTicketServiceForTest(client, zaptest.NewLogger(t).Sugar())
	svc := NewService(srRepo, scRepo, cmdbRepo, client, zaptest.NewLogger(t).Sugar(), ticketSvc)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":       "申请一台云主机",
			"reason":      "测试",
			"environment": "production",
		},
	})
	require.NoError(t, err)
	require.Greater(t, created.TicketID, 0, "Create 必须创建关联 Ticket 并回写 TicketID")

	values, err := service.NewFieldValueService(client).ListValues(ctx, tenant.ID, "ticket", created.TicketID)
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.Equal(t, "environment", values[0].Name)
	assert.Equal(t, "production", values[0].Value)
}

func TestService_Create_SystemFormDataFieldsNotCollectedAsCustomFields(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_system_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-system-fields").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester2").SetEmail("requester2@test.com").SetName("Requester2").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	// 故意不定义任何 field_definitions，只提交系统已知字段。
	catalog, err := scService.Create(ctx, "VPN权限", "网络", "desc", 1, tenant.ID, "enabled", 0, 0, nil)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	ticketSvc := service.NewTicketServiceForTest(client, zaptest.NewLogger(t).Sugar())
	svc := NewService(srRepo, scRepo, cmdbRepo, client, zaptest.NewLogger(t).Sugar(), ticketSvc)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData:           map[string]interface{}{"title": "VPN 权限申请", "reason": "测试", "cost_center": "CC-001"},
	})
	require.NoError(t, err)

	values, err := service.NewFieldValueService(client).ListValues(ctx, tenant.ID, "ticket", created.TicketID)
	require.NoError(t, err)
	assert.Empty(t, values, "没有对应 field_definitions 的系统字段不应该落进 field_values")
}

// TestService_Create_PersistsFieldValues_ArrayShapeSnakeCaseName 证明数组形状（[{name,value}]）
// 提交的下划线字段名能完整存活到 field_values——这是 http-client.ts 全局 camelCase 请求体
// 转换会破坏 map 形状 key 的那个缺陷的回归测试（最终整分支评审 Fix 1）。
func TestService_Create_PersistsFieldValues_ArrayShapeSnakeCaseName(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_field_values_array?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-field-values-array").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester3").SetEmail("requester3@test.com").SetName("Requester3").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "办公用品申请", "行政", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "office_location", Label: "办公地点", FieldType: "text"}})
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	ticketSvc := service.NewTicketServiceForTest(client, zaptest.NewLogger(t).Sugar())
	svc := NewService(srRepo, scRepo, cmdbRepo, client, zaptest.NewLogger(t).Sugar(), ticketSvc)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":  "申请办公用品",
			"reason": "测试",
			// 模拟前端 httpClient 对请求体做完全局 camelCase key 转换之后、
			// 仍然把字段名放在数组元素的 value 位置（而不是 map 的 key）的形状。
			"customFieldValues": []interface{}{
				map[string]interface{}{"name": "office_location", "value": "Beijing"},
			},
		},
	})
	require.NoError(t, err)

	values, err := service.NewFieldValueService(client).ListValues(ctx, tenant.ID, "ticket", created.TicketID)
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.Equal(t, "office_location", values[0].Name)
	assert.Equal(t, "Beijing", values[0].Value)
}

// TestService_Create_RequiredFieldMissing_Rejected 证明 catalog 上标记为 required 的动态字段
// 缺失或空值时，Create 在写入 ServiceRequest（以及创建关联 Ticket）之前就以 400 拒绝提交
// （最终整分支评审 Fix 3）。
func TestService_Create_RequiredFieldMissing_Rejected(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_required_field?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-required-field").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester4").SetEmail("requester4@test.com").SetName("Requester4").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "服务器扩容", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0,
		[]service.FieldDefinitionInput{{Name: "reason_code", Label: "原因代码", FieldType: "text", Required: true}})
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	ticketSvc := service.NewTicketServiceForTest(client, zaptest.NewLogger(t).Sugar())
	svc := NewService(srRepo, scRepo, cmdbRepo, client, zaptest.NewLogger(t).Sugar(), ticketSvc)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData:           map[string]interface{}{"title": "扩容申请", "reason": "测试"},
	})
	require.Error(t, err)
	assert.Nil(t, created)

	_, total, err := srRepo.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	assert.Equal(t, 0, total, "required 字段校验失败时不应该创建 ServiceRequest 记录")

	ticketCount, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, ticketCount, "required 字段校验失败时不应该先创建关联 Ticket")
}

// TestService_Create_LinksTicketAndDelegatesFields 证明 Create 委托给 Ticket：
// 返回的 ServiceRequest.TicketID > 0，且能用它查到一条 title/description 对应申请内容的 Ticket。
func TestService_Create_LinksTicketAndDelegatesFields(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_links_ticket?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-links-ticket").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester5").SetEmail("requester5@test.com").SetName("Requester5").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-link", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":  "申请一台云主机-Link测试",
			"reason": "delegation test reason",
		},
	})
	require.NoError(t, err)
	require.Greater(t, created.TicketID, 0, "Create 必须创建关联 Ticket 并回写 TicketID")

	tkt, err := client.Ticket.Get(ctx, created.TicketID)
	require.NoError(t, err)
	assert.Equal(t, "申请一台云主机-Link测试", tkt.Title)
	assert.Equal(t, "delegation test reason", tkt.Description)
	assert.Equal(t, "service_request", tkt.Type)
	assert.Equal(t, "service_catalog", tkt.Source, "服务目录发起的申请必须标记 ticket.source=service_catalog（Task 2 前端据此判断是否渲染 SR 面板）")
	assert.Equal(t, tenant.ID, tkt.TenantID)
	assert.Equal(t, requester.ID, tkt.RequesterID)
}

// TestService_GetByTicketID_ReturnsLinkedServiceRequest 证明 GetByTicketID 能用 Create
// 返回的 TicketID 查回同一条 ServiceRequest（Task 2 前端渲染 ticket 详情页 SR 面板依赖此接口）。
func TestService_GetByTicketID_ReturnsLinkedServiceRequest(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_get_by_ticket?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-get-by-ticket").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester6").SetEmail("requester6@test.com").SetName("Requester6").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-getbyticket", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		CostCenter:         "CC-GETBYTICKET",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":  "申请一台云主机-GetByTicket",
			"reason": "get by ticket test",
		},
	})
	require.NoError(t, err)
	require.Greater(t, created.TicketID, 0)

	fetched, err := svc.GetByTicketID(ctx, created.TicketID, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, catalog.ID, fetched.CatalogID)
	assert.Equal(t, "CC-GETBYTICKET", fetched.CostCenter)
	assert.Equal(t, created.TicketID, fetched.TicketID)
}

// TestService_List_BatchLoadsLinkedTicketSummary 证明 List 给每条记录批量回填了关联 ticket
// 的 title/status（/my-requests 列表页据此展示，因为 ServiceRequest 表本身已经不存 status/title
// 了——委托给 Ticket）。批量加载：一次 Ticket.Query().Where(IDIn(...)) 而不是逐条查。
func TestService_List_BatchLoadsLinkedTicketSummary(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sr_list_ticket_summary?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("t").SetCode("sr-list-summary").SetDomain("d.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().
		SetUsername("requester7").SetEmail("requester7@test.com").SetName("Requester7").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	scRepo := service_catalog.NewEntRepository(client)
	scService := service_catalog.NewService(scRepo, client, zaptest.NewLogger(t).Sugar())
	catalog, err := scService.Create(ctx, "云主机申请-list", "云服务", "desc", 1, tenant.ID, "enabled", 0, 0, nil)
	require.NoError(t, err)

	srRepo := NewEntRepository(client)
	cmdbRepo := cmdb.NewEntRepository(client)
	logger := zaptest.NewLogger(t).Sugar()
	ticketSvc := service.NewTicketServiceForTest(client, logger)
	svc := NewService(srRepo, scRepo, cmdbRepo, client, logger, ticketSvc)

	created, err := svc.Create(ctx, tenant.ID, requester.ID, catalog.ID, &ServiceRequest{
		ComplianceAck:      true,
		DataClassification: "internal",
		ExpireAt:           ptrTime(time.Now().Add(24 * time.Hour)),
		FormData: map[string]interface{}{
			"title":  "申请一台云主机-List测试",
			"reason": "list batch load test",
		},
	})
	require.NoError(t, err)
	require.Greater(t, created.TicketID, 0)

	list, total, err := svc.List(ctx, tenant.ID, ListFilters{Page: 1, Size: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, list, 1)
	assert.Equal(t, "申请一台云主机-List测试", list[0].TicketTitle)
	assert.NotEmpty(t, list[0].TicketStatus)

	tkt, err := client.Ticket.Get(ctx, created.TicketID)
	require.NoError(t, err)
	assert.Equal(t, tkt.Status, list[0].TicketStatus)
}

func ptrTime(t time.Time) *time.Time { return &t }
