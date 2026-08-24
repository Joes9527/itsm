package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// provisioningTestFixture creates a fresh tenant, requester, linked ticket, and ServiceRequest
// row. label just needs to be unique per fixture within a test (used for unique username/email/
// ticket-number/tenant-code values) — CreateTaskFromServiceRequest was changed (earlier in the
// ServiceRequest-to-Ticket delegation branch) to gate provisioning on a ProcessApprovalDecision
// row for the linked ticket instead of the old sr.Status == "security_approved" check, but had
// zero test coverage (final review fix wave, Fix 6).
func provisioningTestFixture(t *testing.T, client *ent.Client, label string) (sr *ent.ServiceRequest, ticket *ent.Ticket) {
	t.Helper()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Tenant " + label).
		SetCode("prov-tenant-" + label).
		SetDomain("prov-" + label + ".test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("prov-requester-" + label).
		SetEmail("prov-requester-" + label + "@test.com").
		SetName("Prov Requester").
		SetPasswordHash("hash").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	tkt, err := client.Ticket.Create().
		SetTicketNumber("TKT-PROV-" + label).
		SetTitle("Provisioning Test Ticket").
		SetDescription("desc").
		SetPriority("medium").
		SetStatus("open").
		SetRequesterID(requester.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	req, err := client.ServiceRequest.Create().
		SetTenantID(tenant.ID).
		SetTicketID(tkt.ID).
		SetCatalogID(1).
		SetRequesterID(requester.ID).
		SetComplianceAck(true).
		SetDataClassification("internal").
		Save(ctx)
	require.NoError(t, err)

	// CanProvision 需要 actor 持有 service_request:provision，且不是申请人本人——
	// provisioningTestActorID 是测试里固定使用的履约人 ID，跟 requester.ID 保证不撞。
	seedRolePermission(t, client, tenant.ID, provisioningTestRole, "service_request", "provision")

	return req, tkt
}

// provisioningTestRole/provisioningTestActorID：测试里固定的"履约人"身份，
// 用来跟 provisioningTestFixture 创建的 requester 区分开，验证职责分离不会误伤正常路径。
const provisioningTestRole = "l1_support"

var provisioningTestActorID = 999999

// TestCreateTaskFromServiceRequest_RejectsWithoutApprovalDecision proves provisioning refuses to
// start when no process_approval_decision row exists for the linked ticket — this is the "not
// yet approved" baseline the ProcessApprovalDecision.Exist check replaced sr.Status ==
// "security_approved" with.
func TestCreateTaskFromServiceRequest_RejectsWithoutApprovalDecision(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:prov_no_decision?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	middleware.InvalidateAllPermissionCaches() // 避免不同测试的租户ID复用造成缓存串号
	ctx := context.Background()

	sr, _ := provisioningTestFixture(t, client, "no-decision")

	svc := NewProvisioningService(client, zaptest.NewLogger(t).Sugar())
	task, err := svc.CreateTaskFromServiceRequest(ctx, sr.ID, sr.TenantID, provisioningTestActorID, provisioningTestRole)
	require.Error(t, err)
	assert.Nil(t, task)

	count, err := client.ProvisioningTask.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "no ProvisioningTask should be created when the approval precondition is not met")
}

// TestCreateTaskFromServiceRequest_SucceedsWithApprovalDecision proves provisioning starts once a
// matching process_approval_decision row exists (business_type=ticket, business_id=<ticket ID>,
// decision=approved, same tenant) — the success path this precondition change had never been
// exercised by an automated test for (only the rejection path had manual verification).
func TestCreateTaskFromServiceRequest_SucceedsWithApprovalDecision(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:prov_decision_approved?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	middleware.InvalidateAllPermissionCaches() // 避免不同测试的租户ID复用造成缓存串号
	ctx := context.Background()

	sr, ticket := provisioningTestFixture(t, client, "approved")

	_, err := client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).
		SetProcessTaskID(1).
		SetProcessInstanceKey("PI-1").
		SetTaskID("TASK-1").
		SetProcessDefinitionKey("ticket_general_flow").
		SetNodeKey("approval").
		SetBusinessType("ticket").
		SetBusinessID(strconv.Itoa(ticket.ID)).
		SetActorID(1).
		SetAction("approve").
		SetDecision("approved").
		SetTenantID(sr.TenantID).
		Save(ctx)
	require.NoError(t, err)

	svc := NewProvisioningService(client, zaptest.NewLogger(t).Sugar())
	task, err := svc.CreateTaskFromServiceRequest(ctx, sr.ID, sr.TenantID, provisioningTestActorID, provisioningTestRole)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Equal(t, sr.ID, task.ServiceRequestID)
	assert.Equal(t, sr.TenantID, task.TenantID)

	count, err := client.ProvisioningTask.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestCreateTaskFromServiceRequest_CrossTenantApprovalDoesNotUnlock is the tenant-isolation case
// CLAUDE.md requires a test for: a process_approval_decision row for the SAME ticket ID but a
// DIFFERENT tenant must not unlock provisioning. Without the tenant_id filter in the Exist query,
// a decision recorded for one tenant's (coincidentally same-numbered) ticket could leak
// authorization across tenants.
func TestCreateTaskFromServiceRequest_CrossTenantApprovalDoesNotUnlock(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:prov_cross_tenant?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	middleware.InvalidateAllPermissionCaches() // 避免不同测试的租户ID复用造成缓存串号
	ctx := context.Background()

	srA, ticketA := provisioningTestFixture(t, client, "tenant-a")
	srB, _ := provisioningTestFixture(t, client, "tenant-b")

	// The strongest form of the isolation check: a decision with tenant A's own ticket ID as
	// business_id, but filed under tenant B. If the Exist query ever dropped its tenant_id
	// filter, this row alone would be enough to wrongly unlock tenant A's provisioning.
	_, err := client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).
		SetProcessTaskID(1).
		SetProcessInstanceKey("PI-B").
		SetTaskID("TASK-B").
		SetProcessDefinitionKey("ticket_general_flow").
		SetNodeKey("approval").
		SetBusinessType("ticket").
		SetBusinessID(strconv.Itoa(ticketA.ID)).
		SetActorID(1).
		SetAction("approve").
		SetDecision("approved").
		SetTenantID(srB.TenantID).
		Save(ctx)
	require.NoError(t, err)

	svc := NewProvisioningService(client, zaptest.NewLogger(t).Sugar())
	task, err := svc.CreateTaskFromServiceRequest(ctx, srA.ID, srA.TenantID, provisioningTestActorID, provisioningTestRole)
	require.Error(t, err, "a same-business_id approval decision filed under a different tenant must not unlock provisioning")
	assert.Nil(t, task)

	count, err := client.ProvisioningTask.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
