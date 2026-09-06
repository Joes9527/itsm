package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// testDSN 为每个测试返回唯一的 SQLite 内存数据库 DSN，避免测试间数据库残留导致
// 唯一约束冲突（同一失败类别在 service/enttest_dsn_test.go 中已有先例）。
var testDBCounter int64

func testDSN() string {
	return fmt.Sprintf("file:check_work_item_integrity_test_%d?mode=memory&cache=shared&_fk=1", atomic.AddInt64(&testDBCounter, 1))
}

func TestFindMismatches_MissingExtension(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test-tenant-1").
		SetDomain("test1.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetUsername("reporter1").
		SetEmail("reporter1@example.com").
		SetName("Reporter One").
		SetPasswordHash("hash").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 一条 record_class="incident" 的 ticket，但没有对应的 incidents 行。
	tk, err := client.Ticket.Create().
		SetTitle("磁盘空间不足").
		SetDescription("磁盘空间不足，需要处理").
		SetPriority("high").
		SetStatus("open").
		SetTicketNumber("TICKET-INTEGRITY-001").
		SetRecordClass("incident").
		SetRequesterID(user.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	t.Run("missing_incident_extension", func(t *testing.T) {
		mismatches, err := findMismatches(ctx, client, tenant.ID)
		require.NoError(t, err)
		require.Len(t, mismatches, 1, "expected exactly one mismatch before the incident row exists")
		m := mismatches[0]
		require.Equal(t, "missing_extension", m.kind)
		require.Equal(t, tk.ID, m.ticketID)
		require.Equal(t, tenant.ID, m.tenantID)
		require.Equal(t, "incident", m.recordClass)
		t.Logf("Found mismatch: kind=%s, ticket_id=%d, record_class=%s", m.kind, m.ticketID, m.recordClass)
	})

	t.Run("after_creating_matching_extension", func(t *testing.T) {
		_, err := client.Incident.Create().
			SetWorkItemID(tk.ID).
			Save(ctx)
		require.NoError(t, err)

		mismatches, err := findMismatches(ctx, client, tenant.ID)
		require.NoError(t, err)
		require.Empty(t, mismatches, "no mismatch expected once the matching incident row exists")
	})

	t.Run("record_class_mismatch_via_backref", func(t *testing.T) {
		// 新建一条 record_class="problem" 的 ticket，然后建一条 incident 记录，
		// 让它的 work_item_id 错误地指向这条 problem ticket——checkBackref 应该
		// 报出 record_class_mismatch（incident 期望，但实际是 problem）。
		wrongClassTicket, err := client.Ticket.Create().
			SetTitle("错误分类的工单").
			SetDescription("record_class 与 incident 扩展记录不匹配").
			SetPriority("medium").
			SetStatus("open").
			SetTicketNumber("TICKET-INTEGRITY-002").
			SetRecordClass("problem").
			SetRequesterID(user.ID).
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, err)

		_, err = client.Incident.Create().
			SetWorkItemID(wrongClassTicket.ID).
			Save(ctx)
		require.NoError(t, err)

		mismatches, err := findMismatches(ctx, client, tenant.ID)
		require.NoError(t, err)

		// wrongClassTicket 自身的 record_class="problem" 但没有 problems 行，
		// 所以还会额外产生一条 missing_extension；我们只关心是否存在
		// record_class_mismatch。
		var found *mismatch
		for i := range mismatches {
			if mismatches[i].kind == "record_class_mismatch" {
				found = &mismatches[i]
			}
		}
		require.NotNil(t, found, "expected a record_class_mismatch to be reported, got: %+v", mismatches)
		require.Equal(t, wrongClassTicket.ID, found.ticketID)
		require.Equal(t, "problem", found.recordClass, "detail should report the ticket's actual record_class")
		t.Logf("SUCCESS: record_class_mismatch correctly detected for ticket_id=%d (actual=%s)", found.ticketID, found.recordClass)
	})

	t.Run("tenant_isolation", func(t *testing.T) {
		otherTenant, err := client.Tenant.Create().
			SetName("Other Tenant").
			SetCode("test-tenant-2").
			SetDomain("test2.example.com").
			SetStatus("active").
			Save(ctx)
		require.NoError(t, err)

		otherUser, err := client.User.Create().
			SetUsername("reporter2").
			SetEmail("reporter2@example.com").
			SetName("Reporter Two").
			SetPasswordHash("hash").
			SetRole("end_user").
			SetActive(true).
			SetTenantID(otherTenant.ID).
			Save(ctx)
		require.NoError(t, err)

		// 干净数据：otherTenant 下没有任何不一致。
		otherTicket, err := client.Ticket.Create().
			SetTitle("Other tenant generic ticket").
			SetDescription("no mismatch here").
			SetPriority("low").
			SetStatus("open").
			SetTicketNumber("TICKET-OTHER-TENANT-001").
			SetRequesterID(otherUser.ID).
			SetTenantID(otherTenant.ID).
			Save(ctx)
		require.NoError(t, err)
		_ = otherTicket

		// tenant(第一个租户) 此时仍有 wrongClassTicket 产生的 mismatch（上一个子测试遗留），
		// 查询 otherTenant 时不应该看到它们。
		otherMismatches, err := findMismatches(ctx, client, otherTenant.ID)
		require.NoError(t, err)
		require.Empty(t, otherMismatches, "querying tenant B must not surface tenant A's mismatches")

		tenantAMismatches, err := findMismatches(ctx, client, tenant.ID)
		require.NoError(t, err)
		require.NotEmpty(t, tenantAMismatches, "tenant A should still show its own mismatches")
		for _, m := range tenantAMismatches {
			require.Equal(t, tenant.ID, m.tenantID, "tenant A's mismatch list must not leak tenant B rows")
		}
		t.Logf("SUCCESS: tenant isolation verified — tenant A has %d mismatch(es), tenant B has 0", len(tenantAMismatches))
	})
}

func TestProfessionalExtensionRejectsDanglingWorkItemID(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Dangling Tenant").
		SetCode("test-tenant-dangling").
		SetDomain("dangling.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.User.Create().
		SetUsername("reporter-dangling").
		SetEmail("reporter-dangling@example.com").
		SetName("Reporter Dangling").
		SetPasswordHash("hash").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// work_item_id 指向一个不存在的 ticket id。
	const danglingWorkItemID = 999999
	_, err = client.Incident.Create().
		SetWorkItemID(danglingWorkItemID).
		Save(ctx)
	require.Error(t, err, "professional extension FK must reject a dangling WorkItem reference")
}

// TestFindMismatches_UnknownRecordClass 锁定 record_class 分派 switch 的 default 分支：
// 一个本工具不认识的 record_class（拼错、或某个新域忘了在这里登记）以前会跟
// service_request_item/catalog_task 一样被静默 continue 掉，等于完整性检查对这条记录
// 完全失效。现在它必须被报成 unknown_record_class。
func TestFindMismatches_UnknownRecordClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Unknown RecordClass Tenant").
		SetCode("unknown-record-class").
		SetDomain("unknown-record-class.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetUsername("reporter-unknown").
		SetEmail("reporter-unknown@example.com").
		SetName("Reporter Unknown").
		SetPasswordHash("hash").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 拼错的 record_class（正确值是 change_request）。
	bad, err := client.Ticket.Create().
		SetTitle("record_class 拼错的工单").
		SetDescription("unknown record_class 检查").
		SetPriority("medium").
		SetStatus("open").
		SetTicketNumber("TICKET-UNKNOWN-CLASS-001").
		SetRecordClass("change").
		SetRequesterID(user.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// Wave 1 已知的两个"暂不检查"取值仍必须静默跳过，不能被这次改动误报。
	for i, deferredClass := range []string{"service_request_item", "catalog_task"} {
		_, err = client.Ticket.Create().
			SetTitle("Wave 2 才检查的类别").
			SetDescription("暂不检查").
			SetPriority("low").
			SetStatus("open").
			SetTicketNumber(fmt.Sprintf("TICKET-DEFERRED-CLASS-%03d", i+1)).
			SetRecordClass(deferredClass).
			SetRequesterID(user.ID).
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	mismatches, err := findMismatches(ctx, client, tenant.ID)
	require.NoError(t, err)
	require.Len(t, mismatches, 1, "expected exactly one unknown_record_class, got: %+v", mismatches)
	require.Equal(t, "unknown_record_class", mismatches[0].kind)
	require.Equal(t, bad.ID, mismatches[0].ticketID)
	require.Equal(t, tenant.ID, mismatches[0].tenantID)
	require.Equal(t, "change", mismatches[0].recordClass)
}
