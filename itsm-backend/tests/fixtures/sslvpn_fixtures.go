package fixtures

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/fielddefinition"
	"itsm-backend/ent/group"
	"itsm-backend/ent/role"
	"itsm-backend/ent/servicecatalog"
	"itsm-backend/ent/ticketcategory"
	"itsm-backend/ent/user"
	"itsm-backend/service"

	"golang.org/x/crypto/bcrypt"
)

// SSLVPNTestUsers 包含预置的三个测试角色账号信息
type SSLVPNTestUsers struct {
	EndUser    *ent.User // end_user_test / end_user
	Supervisor *ent.User // supervisor_test / dept_manager
	Lixin      *ent.User // lixin_test / network_eng
}

// SSLVPNFixtureResult 包含 SSL-VPN 场景所需的全部测试元数据
type SSLVPNFixtureResult struct {
	Category       *ent.TicketCategory
	CatalogItem    *ent.ServiceCatalog
	FieldDefs      []*ent.FieldDefinition
	Users          *SSLVPNTestUsers
	DeptManagerGrp *ent.Group
	NetworkEngGrp  *ent.Group
}

// EnsureSSLVPNMetadata 幂等确保 SSL-VPN 相关的服务分类、服务目录项（绑定流程 sslvpn_approval_flow）、
// 8项动态自定义字段、BPMN内置流程模板部署以及 3 个测试用户与审批组存在。
func EnsureSSLVPNMetadata(ctx context.Context, client *ent.Client, tenantID int) (*SSLVPNFixtureResult, error) {
	// 1. 部署 BPMN 模板 (sslvpn_approval_flow)
	templateSvc := service.NewBPMNTemplateService(client)
	if err := templateSvc.DeployTemplateByName(ctx, "sslvpn_approval_flow", tenantID); err != nil {
		return nil, fmt.Errorf("部署 sslvpn_approval_flow 模板失败: %w", err)
	}

	// 2. 确保服务分类：网络与远程访问服务 (network_and_remote_access)
	cat, err := client.TicketCategory.Query().
		Where(
			ticketcategory.CodeEQ("network_and_remote_access"),
			ticketcategory.TenantIDEQ(tenantID),
		).
		First(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			return nil, fmt.Errorf("查询服务分类失败: %w", err)
		}
		cat, err = client.TicketCategory.Create().
			SetName("网络与远程访问服务").
			SetCode("network_and_remote_access").
			SetDescription("提供网络接入、远程办公及 VPN 访问权限申请").
			SetItsmType("Request").
			SetDefaultPriority("P3").
			SetSLATier("标准服务").
			SetIsUserFacing(true).
			SetTenantID(tenantID).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("创建服务分类失败: %w", err)
		}
	}

	// 3. 确保服务目录项：SSL-VPN 远程办公访问权限申请 (sslvpn_access_request)
	catalog, err := client.ServiceCatalog.Query().
		Where(
			servicecatalog.NameEQ("SSL-VPN 远程办公访问权限申请"),
			servicecatalog.TenantIDEQ(tenantID),
		).
		First(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			return nil, fmt.Errorf("查询服务目录项失败: %w", err)
		}
		catalog, err = client.ServiceCatalog.Create().
			SetName("SSL-VPN 远程办公访问权限申请").
			SetDescription("为员工因业务需要申请 SSL-VPN 远程接入权限，需经部门领导初审与 L2 网络运维复审。").
			SetCategory("网络与远程访问服务").
			SetServiceType("security").
			SetTargetClass("service_request_item").
			SetRequiresApproval(true).
			SetApprovalLevel(2).
			SetProcessDefinitionKey("sslvpn_approval_flow").
			SetSLAResponseTime(15).
			SetSLAResolutionTime(120).
			SetStatus("active").
			SetIsActive(true).
			SetTenantID(tenantID).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("创建服务目录项失败: %w", err)
		}
	} else if catalog.ProcessDefinitionKey != "sslvpn_approval_flow" {
		catalog, err = catalog.Update().
			SetProcessDefinitionKey("sslvpn_approval_flow").
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("更新服务目录项流程绑定失败: %w", err)
		}
	}

	// 4. 确保 8 个动态自定义字段定义 (归属 entity_type="service_catalog", entity_id=catalog.ID)
	fieldsConfig := []struct {
		Name      string
		Label     string
		FieldType string
		Required  bool
		Options   []interface{}
		SortOrder int
	}{
		{
			Name:      "applicant_name",
			Label:     "申请人姓名",
			FieldType: "text",
			Required:  true,
			SortOrder: 1,
		},
		{
			Name:      "applicant_upn",
			Label:     "申请人域账号/UPN",
			FieldType: "text",
			Required:  true,
			SortOrder: 2,
		},
		{
			Name:      "employee_id",
			Label:     "员工工号",
			FieldType: "text",
			Required:  true,
			SortOrder: 3,
		},
		{
			Name:      "department",
			Label:     "所属部门",
			FieldType: "select",
			Required:  true,
			Options: []interface{}{
				map[string]interface{}{"label": "IT研发中心", "value": "IT研发中心"},
				map[string]interface{}{"label": "供应链运营", "value": "供应链运营"},
				map[string]interface{}{"label": "财务部", "value": "财务部"},
				map[string]interface{}{"label": "人力资源部", "value": "人力资源部"},
			},
			SortOrder: 4,
		},
		{
			Name:      "vpn_level",
			Label:     "申请权限级别与用户组",
			FieldType: "select",
			Required:  true,
			Options: []interface{}{
				map[string]interface{}{"label": "Level 1 - 基础办公组 (CNDL-OKTA-SSLVPN-Level1-Users)", "value": "Level 1 - 基础办公组 (CNDL-OKTA-SSLVPN-Level1-Users)"},
				map[string]interface{}{"label": "Level 2 - 业务系统组 (CNDL-OKTA-SSLVPN-Level2-Users)", "value": "Level 2 - 业务系统组 (CNDL-OKTA-SSLVPN-Level2-Users)"},
				map[string]interface{}{"label": "Level 3 - 高权/运维组 (CNDL-OKTA-SSLVPN-Level3-Users)", "value": "Level 3 - 高权/运维组 (CNDL-OKTA-SSLVPN-Level3-Users)"},
			},
			SortOrder: 5,
		},
		{
			Name:      "target_systems",
			Label:     "访问目标系统与网段",
			FieldType: "text",
			Required:  true,
			SortOrder: 6,
		},
		{
			Name:      "access_duration",
			Label:     "权限有效期",
			FieldType: "select",
			Required:  true,
			Options: []interface{}{
				map[string]interface{}{"label": "30天临时", "value": "30天临时"},
				map[string]interface{}{"label": "90天临时", "value": "90天临时"},
				map[string]interface{}{"label": "长期有效", "value": "长期有效"},
			},
			SortOrder: 7,
		},
		{
			Name:      "access_reason",
			Label:     "业务申请理由",
			FieldType: "textarea",
			Required:  true,
			SortOrder: 8,
		},
	}

	var fieldDefs []*ent.FieldDefinition
	for _, cfg := range fieldsConfig {
		fd, err := client.FieldDefinition.Query().
			Where(
				fielddefinition.TenantIDEQ(tenantID),
				fielddefinition.EntityTypeEQ("service_catalog"),
				fielddefinition.EntityIDEQ(catalog.ID),
				fielddefinition.NameEQ(cfg.Name),
			).
			First(ctx)
		if err != nil {
			if !ent.IsNotFound(err) {
				return nil, fmt.Errorf("查询字段定义 %s 失败: %w", cfg.Name, err)
			}
			opts := cfg.Options
			if opts == nil {
				opts = []interface{}{}
			}
			fd, err = client.FieldDefinition.Create().
				SetTenantID(tenantID).
				SetEntityType("service_catalog").
				SetEntityID(catalog.ID).
				SetName(cfg.Name).
				SetLabel(cfg.Label).
				SetFieldType(cfg.FieldType).
				SetRequired(cfg.Required).
				SetOptions(opts).
				SetSortOrder(cfg.SortOrder).
				SetIsActive(true).
				Save(ctx)
			if err != nil {
				return nil, fmt.Errorf("创建字段定义 %s 失败: %w", cfg.Name, err)
			}
		} else {
			// 更新确保配置一致
			opts := cfg.Options
			if opts == nil {
				opts = []interface{}{}
			}
			fd, err = fd.Update().
				SetLabel(cfg.Label).
				SetFieldType(cfg.FieldType).
				SetRequired(cfg.Required).
				SetOptions(opts).
				SetSortOrder(cfg.SortOrder).
				SetIsActive(true).
				Save(ctx)
			if err != nil {
				return nil, fmt.Errorf("更新字段定义 %s 失败: %w", cfg.Name, err)
			}
		}
		fieldDefs = append(fieldDefs, fd)
	}

	// 5. 确保角色和测试用户存在
	// 角色列表：end_user, dept_manager, network_eng
	rolesConfig := []struct {
		Code string
		Name string
	}{
		{"end_user", "普通用户"},
		{"dept_manager", "部门经理"},
		{"network_eng", "网络安全工程师"},
	}
	for _, r := range rolesConfig {
		exists, err := client.Role.Query().Where(role.CodeEQ(r.Code), role.TenantIDEQ(tenantID)).Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查角色 %s 失败: %w", r.Code, err)
		}
		if !exists {
			_, err = client.Role.Create().
				SetCode(r.Code).
				SetName(r.Name).
				SetDescription(r.Name).
				SetTenantID(tenantID).
				Save(ctx)
			if err != nil {
				return nil, fmt.Errorf("创建角色 %s 失败: %w", r.Code, err)
			}
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("生成密码哈希失败: %w", err)
	}

	// 用户 1: end_user_test
	endUser, err := ensureUser(ctx, client, tenantID, "end_user_test", "end_user_test@example.com", "申请人测试账号", "end_user", string(hash))
	if err != nil {
		return nil, err
	}

	// 用户 2: supervisor_test
	supervisor, err := ensureUser(ctx, client, tenantID, "supervisor_test", "supervisor_test@example.com", "主管初审测试账号", "dept_manager", string(hash))
	if err != nil {
		return nil, err
	}

	// 用户 3: lixin_test
	lixin, err := ensureUser(ctx, client, tenantID, "lixin_test", "lixin_test@example.com", "李昕/L2网络运维", "network_eng", string(hash))
	if err != nil {
		return nil, err
	}

	testUsers := &SSLVPNTestUsers{
		EndUser:    endUser,
		Supervisor: supervisor,
		Lixin:      lixin,
	}

	// 6. 确保 BPMN 审批组 (dept_manager, network_eng) 存在且包含对应的审批人
	// 这样 GroupResolver 展开 candidateGroups 时能够正确解析出对应用户
	deptManagerGrp, err := ensureGroup(ctx, client, tenantID, "dept_manager", "部门领导初审组", supervisor.ID)
	if err != nil {
		return nil, fmt.Errorf("确保 dept_manager 组失败: %w", err)
	}

	networkEngGrp, err := ensureGroup(ctx, client, tenantID, "network_eng", "L2网络运维复审组", lixin.ID)
	if err != nil {
		return nil, fmt.Errorf("确保 network_eng 组失败: %w", err)
	}

	return &SSLVPNFixtureResult{
		Category:       cat,
		CatalogItem:    catalog,
		FieldDefs:      fieldDefs,
		Users:          testUsers,
		DeptManagerGrp: deptManagerGrp,
		NetworkEngGrp:  networkEngGrp,
	}, nil
}

func ensureUser(ctx context.Context, client *ent.Client, tenantID int, username, email, name, roleCode, passwordHash string) (*ent.User, error) {
	u, err := client.User.Query().
		Where(user.UsernameEQ(username), user.TenantIDEQ(tenantID)).
		First(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			return nil, fmt.Errorf("查询用户 %s 失败: %w", username, err)
		}
		u, err = client.User.Create().
			SetUsername(username).
			SetEmail(email).
			SetName(name).
			SetRole(roleCode).
			SetPasswordHash(passwordHash).
			SetActive(true).
			SetTenantID(tenantID).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("创建用户 %s 失败: %w", username, err)
		}
	} else {
		u, err = u.Update().
			SetName(name).
			SetRole(roleCode).
			SetActive(true).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("更新用户 %s 失败: %w", username, err)
		}
	}
	return u, nil
}

func ensureGroup(ctx context.Context, client *ent.Client, tenantID int, groupName, desc string, memberUserIDs ...int) (*ent.Group, error) {
	g, err := client.Group.Query().
		Where(group.NameEQ(groupName), group.TenantIDEQ(tenantID)).
		WithMembers().
		First(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			return nil, fmt.Errorf("查询组 %s 失败: %w", groupName, err)
		}
		g, err = client.Group.Create().
			SetName(groupName).
			SetDescription(desc).
			SetTenantID(tenantID).
			AddMemberIDs(memberUserIDs...).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("创建组 %s 失败: %w", groupName, err)
		}
		return g, nil
	}

	// 确保成员已添加
	existingMemberIDs := make(map[int]bool)
	for _, m := range g.Edges.Members {
		existingMemberIDs[m.ID] = true
	}
	var toAdd []int
	for _, id := range memberUserIDs {
		if !existingMemberIDs[id] {
			toAdd = append(toAdd, id)
		}
	}
	if len(toAdd) > 0 {
		g, err = g.Update().
			AddMemberIDs(toAdd...).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("向组 %s 添加成员失败: %w", groupName, err)
		}
	}

	return g, nil
}
