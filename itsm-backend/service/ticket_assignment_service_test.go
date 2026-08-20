package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestTicketAssignmentService_AssignTicket(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	ctx := context.Background()

	// 创建测试数据
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	testUser, err := client.User.Create().
		SetUsername("testuser").
		SetEmail("test@example.com").
		SetName("Test User").
		SetPasswordHash("hashedpassword").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 创建测试处理人
	assignee, err := client.User.Create().
		SetUsername("assignee").
		SetEmail("assignee@example.com").
		SetName("Assignee User").
		SetPasswordHash("hashedpassword").
		SetRole("agent").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 创建测试工单
	testTicket, err := client.Ticket.Create().
		SetTitle("测试工单").
		SetDescription("测试工单描述").
		SetPriority("medium").
		SetStatus("open").
		SetTicketNumber("TICKET-001").
		SetRequesterID(testUser.ID).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	tests := []struct {
		name          string
		request       *AssignmentRequest
		expectedError bool
		checkResult   func(*testing.T, *AssignmentResponse)
	}{
		{
			name: "手动分配给指定用户",
			request: &AssignmentRequest{
				TicketID:      testTicket.ID,
				TenantID:      testTenant.ID,
				Priority:      "medium",
				AutoAssign:    false,
				PreferredUser: &assignee.ID,
			},
			expectedError: false,
			checkResult: func(t *testing.T, resp *AssignmentResponse) {
				assert.Equal(t, "manual", resp.AssignmentType)
				assert.NotNil(t, resp.AssignedTo)
				assert.Equal(t, assignee.ID, *resp.AssignedTo)
			},
		},
		{
			name: "工单不存在",
			request: &AssignmentRequest{
				TicketID:   99999,
				TenantID:   testTenant.ID,
				Priority:   "medium",
				AutoAssign: false,
			},
			expectedError: true,
			checkResult:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := assignmentService.AssignTicket(ctx, tt.request)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, resp)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				if tt.checkResult != nil {
					tt.checkResult(t, resp)
				}
			}
		})
	}
}

// TestTicketAssignmentService_AutoAssign_QueryCountIsBounded 复现真实环境（嘉里物流 eHR
// 导入后租户内 7826 个 active 用户）暴露出的性能 bug：autoAssignTicket 对每个候选人串行跑
// 3-5 条 SQL（getUserWorkload 的 2 个 count + 1 个 .All，加上评分阶段的 category/performance
// count），导致候选人越多、SQL 往返次数越多，线性放大。真实环境里这变成约 3 万次串行往返，
// 单次工单创建耗时 30+ 秒（看起来像卡死）。这里用 40 个候选人验证优化后的批量查询让总 SQL
// 次数保持在候选人数量之下的一个小常数，而不是随候选人数线性增长。
func TestTicketAssignmentService_AutoAssign_QueryCountIsBounded(t *testing.T) {
	var queryCount int
	countingLog := func(args ...any) {
		queryCount++
	}

	client := enttest.Open(t, "sqlite3", testDSN(), enttest.WithOptions(ent.Log(countingLog), ent.Debug()))
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	ctx := context.Background()

	testTenant, err := client.Tenant.Create().
		SetName("Perf Tenant").SetCode("perf").SetDomain("perf.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	// Active(false)：请求人本身不是候选处理人，这样它不会以"零负载"跟真正
	// 零负载的候选人打平分——分数打平时 sort.Slice 不保证稳定，会让断言变得不确定。
	requester, err := client.User.Create().
		SetUsername("perf_requester").SetEmail("perf_requester@example.com").SetName("Requester").
		SetPasswordHash("x").SetRole("end_user").SetActive(false).SetTenantID(testTenant.ID).Save(ctx)
	require.NoError(t, err)

	const numCandidates = 40
	var leastBusyID int
	for i := 0; i < numCandidates; i++ {
		u, err := client.User.Create().
			SetUsername(fmt.Sprintf("perf_agent_%d", i)).
			SetEmail(fmt.Sprintf("perf_agent_%d@example.com", i)).
			SetName("Agent").SetPasswordHash("x").SetRole("agent").
			SetActive(true).SetTenantID(testTenant.ID).Save(ctx)
		require.NoError(t, err)

		// 除最后一个候选人外，其余每人挂 1 个 open 工单，制造工作负载差异——
		// 最后一个候选人（零负载）应该在评分里胜出，用来同时验证批量查询
		// 没有破坏原有的"挑最闲的人"选人语义。
		if i < numCandidates-1 {
			_, err = client.Ticket.Create().
				SetTitle("existing load").SetDescription("x").SetPriority("medium").
				SetStatus("open").SetTicketNumber(fmt.Sprintf("PERF-%d", i)).
				SetAssigneeID(u.ID).SetRequesterID(requester.ID).SetTenantID(testTenant.ID).Save(ctx)
			require.NoError(t, err)
		} else {
			leastBusyID = u.ID
		}
	}

	ticketToAssign, err := client.Ticket.Create().
		SetTitle("待分配").SetDescription("x").SetPriority("medium").SetStatus("open").
		SetTicketNumber("PERF-TARGET").SetRequesterID(requester.ID).SetTenantID(testTenant.ID).Save(ctx)
	require.NoError(t, err)

	queryCount = 0 // 只统计 AssignTicket 调用期间的查询，不含前面建测试数据的部分
	resp, err := assignmentService.AssignTicket(ctx, &AssignmentRequest{
		TicketID:   ticketToAssign.ID,
		TenantID:   testTenant.ID,
		Priority:   "medium",
		AutoAssign: true,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.AssignedTo)

	assert.Equal(t, leastBusyID, *resp.AssignedTo, "零负载的候选人应该胜出，批量查询不能改变选人结果")
	// 优化前：至少 1(列用户) + N*3(getUserWorkload 的 2 个 count + 1 个 resolved .All) +
	// N*1(calculatePerformanceScore) = 4*40+1 = 161 条查询，随候选人数线性增长。
	// 优化后：应该是与候选人数无关的一个小常数（列用户 + 几条批量聚合查询）。
	assert.Less(t, queryCount, numCandidates,
		"SQL 查询次数应该保持在候选人数量之下的常数级别，不应该随候选人数量线性增长（当前 %d 次，%d 个候选人）",
		queryCount, numCandidates)
}

func TestTicketAssignmentService_GetUserWorkload(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	ctx := context.Background()

	// 创建测试数据
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	testUser, err := client.User.Create().
		SetUsername("testuser").
		SetEmail("test@example.com").
		SetName("Test User").
		SetPasswordHash("hashedpassword").
		SetRole("agent").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 创建一些测试工单
	for i := 0; i < 3; i++ {
		_, err := client.Ticket.Create().
			SetTitle("测试工单").
			SetDescription("测试描述").
			SetPriority("medium").
			SetStatus("open").
			SetTicketNumber("TICKET-WL-" + string(rune('0'+i))).
			SetAssigneeID(testUser.ID).
			SetRequesterID(testUser.ID).
			SetTenantID(testTenant.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	// 创建一些已解决的工单
	for i := 0; i < 2; i++ {
		_, err := client.Ticket.Create().
			SetTitle("已解决工单").
			SetDescription("测试描述").
			SetPriority("medium").
			SetStatus("resolved").
			SetTicketNumber("RESOLVED-WL-" + string(rune('0'+i))).
			SetAssigneeID(testUser.ID).
			SetRequesterID(testUser.ID).
			SetTenantID(testTenant.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	tests := []struct {
		name          string
		userID        int
		expectedError bool
		checkResult   func(*testing.T, *UserWorkload)
	}{
		{
			name:          "获取用户工作负载",
			userID:        testUser.ID,
			expectedError: false,
			checkResult: func(t *testing.T, workload *UserWorkload) {
				assert.Equal(t, testUser.ID, workload.UserID)
				assert.Equal(t, 3, workload.ActiveTickets)
				assert.Equal(t, 5, workload.TotalTickets)
			},
		},
		{
			name:          "用户不存在",
			userID:        99999,
			expectedError: false,
			checkResult: func(t *testing.T, workload *UserWorkload) {
				// 不存在的用户会返回默认值
				assert.Equal(t, 99999, workload.UserID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workload, err := assignmentService.GetUserWorkload(ctx, tt.userID)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, workload)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, workload)
				if tt.checkResult != nil {
					tt.checkResult(t, workload)
				}
			}
		})
	}
}

func TestTicketAssignmentService_GetTeamWorkload(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	ctx := context.Background()

	// 创建测试数据
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	// 创建多个活跃用户
	for i := 0; i < 3; i++ {
		_, err := client.User.Create().
			SetUsername("agent" + string(rune('a'+i))).
			SetEmail("agent" + string(rune('a'+i)) + "@example.com").
			SetName("Agent User").
			SetPasswordHash("hashedpassword").
			SetRole("agent").
			SetActive(true).
			SetTenantID(testTenant.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	workloads, err := assignmentService.GetTeamWorkload(ctx, testTenant.ID)

	assert.NoError(t, err)
	assert.NotNil(t, workloads)
	assert.Len(t, workloads, 3)
}

func TestTicketAssignmentService_ReassignTicket(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	ctx := context.Background()

	// 创建测试数据
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	oldAssignee, err := client.User.Create().
		SetUsername("old_assignee").
		SetEmail("old@example.com").
		SetName("Old Assignee").
		SetPasswordHash("hashedpassword").
		SetRole("agent").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	newAssignee, err := client.User.Create().
		SetUsername("new_assignee").
		SetEmail("new@example.com").
		SetName("New Assignee").
		SetPasswordHash("hashedpassword").
		SetRole("agent").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 创建测试工单
	testTicket, err := client.Ticket.Create().
		SetTitle("测试工单").
		SetDescription("测试工单描述").
		SetPriority("medium").
		SetStatus("open").
		SetTicketNumber("TICKET-001").
		SetAssigneeID(oldAssignee.ID).
		SetRequesterID(oldAssignee.ID).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	err = assignmentService.ReassignTicket(ctx, testTicket.ID, newAssignee.ID, "重新分配测试")

	assert.NoError(t, err)

	// 验证工单已被重新分配
	updatedTicket, err := client.Ticket.Get(ctx, testTicket.ID)
	assert.NoError(t, err)
	assert.Equal(t, newAssignee.ID, updatedTicket.AssigneeID)
}

func TestTicketAssignmentService_GetTicketsByAssignee(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	ctx := context.Background()

	// 创建测试数据
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	testUser, err := client.User.Create().
		SetUsername("testuser").
		SetEmail("test@example.com").
		SetName("Test User").
		SetPasswordHash("hashedpassword").
		SetRole("agent").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 创建测试工单
	for i := 0; i < 3; i++ {
		_, err := client.Ticket.Create().
			SetTitle("测试工单 " + string(rune('0'+i))).
			SetDescription("测试描述").
			SetPriority("medium").
			SetStatus("open").
			SetTicketNumber("TICKET-00" + string(rune('1'+i))).
			SetAssigneeID(testUser.ID).
			SetRequesterID(testUser.ID).
			SetTenantID(testTenant.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	// 创建一个已关闭的工单
	_, err = client.Ticket.Create().
		SetTitle("已关闭工单").
		SetDescription("测试描述").
		SetPriority("medium").
		SetStatus("closed").
		SetTicketNumber("CLOSED-001").
		SetAssigneeID(testUser.ID).
		SetRequesterID(testUser.ID).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	tickets, err := assignmentService.GetTicketsByAssignee(ctx, testUser.ID, testTenant.ID)

	assert.NoError(t, err)
	assert.NotNil(t, tickets)
	assert.Len(t, tickets, 3) // 不包含已关闭的工单
}

func TestTicketAssignmentService_CalculateSkillScore(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	tests := []struct {
		name           string
		userSkills     []string
		requiredSkills []string
		expectedScore  float64
	}{
		{
			name:           "完全匹配",
			userSkills:     []string{"network", "hardware", "software"},
			requiredSkills: []string{"network", "hardware", "software"},
			expectedScore:  1.0,
		},
		{
			name:           "部分匹配",
			userSkills:     []string{"network", "hardware"},
			requiredSkills: []string{"network", "hardware", "software"},
			expectedScore:  0.6666666666666666,
		},
		{
			name:           "无匹配",
			userSkills:     []string{"network"},
			requiredSkills: []string{"software", "database"},
			expectedScore:  0.0,
		},
		{
			name:           "无必需技能",
			userSkills:     []string{"network", "hardware"},
			requiredSkills: []string{},
			expectedScore:  1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := assignmentService.calculateSkillScore(tt.userSkills, tt.requiredSkills)
			assert.InDelta(t, tt.expectedScore, score, 0.01)
		})
	}
}

func TestTicketAssignmentService_CalculateWorkloadScore(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	tests := []struct {
		name          string
		activeTickets int
		avgResolution time.Duration
		minExpected   float64
		maxExpected   float64
	}{
		{
			name:          "低负载用户",
			activeTickets: 1,
			avgResolution: 1 * time.Hour,
			minExpected:   0.7,
			maxExpected:   1.0,
		},
		{
			name:          "高负载用户",
			activeTickets: 9,
			avgResolution: 24 * time.Hour,
			minExpected:   0.0,
			maxExpected:   0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := assignmentService.calculateWorkloadScore(tt.activeTickets, tt.avgResolution)
			assert.GreaterOrEqual(t, score, tt.minExpected)
			assert.LessOrEqual(t, score, tt.maxExpected)
		})
	}
}

func TestTicketAssignmentService_GetMaxActiveTickets(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	tests := []struct {
		priority    string
		expectedMax int
	}{
		{"critical", 3},
		{"high", 5},
		{"medium", 8},
		{"low", 12},
		{"unknown", 8},
	}

	for _, tt := range tests {
		t.Run(tt.priority, func(t *testing.T) {
			maxTickets := assignmentService.getMaxActiveTickets(1, tt.priority)
			assert.Equal(t, tt.expectedMax, maxTickets)
		})
	}
}

func TestTicketAssignmentService_AssignTickets(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	assignmentService := NewTicketAssignmentService(client, logger)

	ctx := context.Background()

	// 创建测试数据
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").
		SetCode("test").
		SetDomain("test.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	testUser, err := client.User.Create().
		SetUsername("testuser").
		SetEmail("test@example.com").
		SetName("Test User").
		SetPasswordHash("hashedpassword").
		SetRole("agent").
		SetActive(true).
		SetTenantID(testTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 创建多个测试工单
	ticketIDs := []int{}
	for i := 0; i < 3; i++ {
		ticket, err := client.Ticket.Create().
			SetTitle("批量测试工单 " + string(rune('0'+i))).
			SetDescription("测试描述").
			SetPriority("medium").
			SetStatus("open").
			SetTicketNumber("BATCH-00" + string(rune('1'+i))).
			SetRequesterID(testUser.ID).
			SetTenantID(testTenant.ID).
			Save(ctx)
		require.NoError(t, err)
		ticketIDs = append(ticketIDs, ticket.ID)
	}

	err = assignmentService.AssignTickets(ctx, testTenant.ID, ticketIDs, testUser.ID)

	assert.NoError(t, err)

	// 验证所有工单已被分配
	for _, ticketID := range ticketIDs {
		ticket, err := client.Ticket.Get(ctx, ticketID)
		assert.NoError(t, err)
		assert.Equal(t, testUser.ID, ticket.AssigneeID)
	}
}
