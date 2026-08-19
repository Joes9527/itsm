package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type UserService struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

func NewUserService(client *ent.Client, logger *zap.SugaredLogger) *UserService {
	return &UserService{
		client: client,
		logger: logger,
	}
}

// CreateUser 创建用户
func (s *UserService) CreateUser(ctx context.Context, req *dto.CreateUserRequest, tenantID int) (*ent.User, error) {
	s.logger.Infof("创建用户: %s", req.Username)

	// 检查用户名是否已存在
	exists, err := s.client.User.Query().
		Where(user.UsernameEQ(req.Username)).
		Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("检查用户名失败: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("用户名已存在: %s", req.Username)
	}

	// 检查邮箱是否已存在
	exists, err = s.client.User.Query().
		Where(user.EmailEQ(req.Email)).
		Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("检查邮箱失败: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("邮箱已存在: %s", req.Email)
	}

	// 加密密码（如果不提供则生成随机密码）
	password := req.Password
	if password == "" {
		password = fmt.Sprintf("P@ssw0rd%08d", time.Now().UnixNano()%100000000)
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("密码加密失败: %w", err)
	}

	// 创建用户
	uc := s.client.User.Create().
		SetUsername(req.Username).
		SetEmail(req.Email).
		SetName(req.Name).
		SetDepartment(req.Department).
		SetPhone(req.Phone).
		SetPasswordHash(string(hashedPassword)).
		SetActive(true).
		SetTenantID(tenantID).
		SetIsLeader(req.IsLeader)
	if req.DepartmentID > 0 {
		uc = uc.SetDepartmentID(req.DepartmentID)
	}
	if strings.TrimSpace(req.Gender) != "" {
		uc = uc.SetGender(req.Gender)
	}
	if strings.TrimSpace(req.FunctionLine) != "" {
		uc = uc.SetFunctionLine(req.FunctionLine)
	}
	if req.ManagerID > 0 {
		uc = uc.SetManagerID(req.ManagerID)
	}
	// 如果请求中提供了角色，则设置角色；否则使用Schema默认值（end_user）
	if strings.TrimSpace(req.Role) != "" {
		role := strings.ToLower(strings.TrimSpace(req.Role))
		// 兼容前端传的"user"角色，自动转换为"end_user"
		if role == "user" {
			role = "end_user"
		}
		uc = uc.SetRole(role)

	}
	// 如果请求中提供了MSP角色，则设置MSP角色
	if strings.TrimSpace(req.MSPRole) != "" {
		uc = uc.SetMspRole(user.MspRole(strings.ToLower(strings.TrimSpace(req.MSPRole))))
	}
	userEntity, err := uc.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}

	s.logger.Infof("用户创建成功: ID=%d, Username=%s", userEntity.ID, userEntity.Username)
	return userEntity, nil
}

// ListUsers 获取用户列表
func (s *UserService) ListUsers(ctx context.Context, req *dto.ListUsersRequest, tenantID int) (*dto.PagedUsersResponse, error) {
	s.logger.Infof("获取用户列表: page=%d, pageSize=%d", req.Page, req.PageSize)

	query := s.client.User.Query().
		Where(user.TenantIDEQ(tenantID)).
		WithRoles()

	// 按状态过滤
	if req.Status != "" {
		active := req.Status == "active"
		query = query.Where(user.ActiveEQ(active))
	}

	// 按部门过滤（自由文本，模糊匹配 department 展示字段）
	if req.Department != "" {
		query = query.Where(user.DepartmentContainsFold(req.Department))
	}

	// 按部门ID精确过滤（组织树左侧选中节点联动，不含子部门——子部门是单独的节点，
	// 选中哪个节点就精确查那个节点自己的直属用户）
	if req.DepartmentID > 0 {
		query = query.Where(user.DepartmentIDEQ(req.DepartmentID))
	}

	// 按职能条线精确过滤——跟 departmentId 是两条独立的查询维度（同一条线的人可能
	// 分散在不同法人实体/正式部门下面），互不影响，可以同时传也可以只传一个。
	if req.FunctionLine != "" {
		query = query.Where(user.FunctionLineEQ(req.FunctionLine))
	}

	// 搜索过滤——functionLine 也纳入模糊搜索，这样用户不需要知道"SPT_资讯科技服务部"这种
	// 精确条线名，直接搜"资讯科技"就能按职能条线找到人（这条线跟正式部门树是两个维度，
	// 见 ent/schema/user.go FunctionLine 字段注释）。
	if req.Search != "" {
		search := strings.TrimSpace(req.Search)
		query = query.Where(
			user.Or(
				user.UsernameContainsFold(search),
				user.NameContainsFold(search),
				user.EmailContainsFold(search),
				user.FunctionLineContainsFold(search),
			),
		)
	}

	// 计算总数
	total, err := query.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("统计用户总数失败: %w", err)
	}

	// 分页查询
	users, err := query.
		Limit(req.PageSize).
		Offset((req.Page - 1) * req.PageSize).
		Order(ent.Desc(user.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询用户列表失败: %w", err)
	}

	// 转换为响应格式——统一走 dto.ToUserDetailResponse，不再在这里维护一份平行字段列表
	// （之前这里手写过一份字面量，漏了 MSPRole，新增 gender/isLeader/departmentId 时
	// 又得在两处同步改，是同一类"影子 mapper"问题，见 dto/mappers.go 的注释）。
	userResponses := dto.ToUserDetailResponseList(users)
	s.attachManagerNames(ctx, tenantID, userResponses)

	response := &dto.PagedUsersResponse{
		Users: userResponses,
		Pagination: dto.PaginationResponse{
			Page:      req.Page,
			PageSize:  req.PageSize,
			Total:     total,
			TotalPage: (total + req.PageSize - 1) / req.PageSize,
		},
	}

	s.logger.Infof("用户列表查询成功: total=%d, returned=%d", total, len(users))
	return response, nil
}

// attachManagerNames 给这一页的响应补上直属上级的姓名（ManagerName 是展示用的冗余字段，
// dto.ToUserDetailResponse 只接触单条 ent.User，够不到"上级也是个 user，要另查一次"这件
// 事，所以放在 service 层批量补——只查当页里出现过的 manager_id，不是全表扫）。查不到就
// 留空，不当错误处理（比如上级本人被软删/换租户之类，不应该让整页列表查询失败）。
func (s *UserService) attachManagerNames(ctx context.Context, tenantID int, responses []*dto.UserDetailResponse) {
	managerIDs := make(map[int]struct{})
	for _, r := range responses {
		if r.ManagerID > 0 {
			managerIDs[r.ManagerID] = struct{}{}
		}
	}
	if len(managerIDs) == 0 {
		return
	}
	ids := make([]int, 0, len(managerIDs))
	for id := range managerIDs {
		ids = append(ids, id)
	}
	managers, err := s.client.User.Query().
		Where(user.IDIn(ids...), user.TenantIDEQ(tenantID)).
		All(ctx)
	if err != nil {
		s.logger.Warnw("批量查询直属上级姓名失败，展示时留空", "error", err)
		return
	}
	nameByID := make(map[int]string, len(managers))
	for _, m := range managers {
		nameByID[m.ID] = m.Name
	}
	for _, r := range responses {
		if name, ok := nameByID[r.ManagerID]; ok {
			r.ManagerName = name
		}
	}
}

// GetUserByID 根据ID获取用户
func (s *UserService) GetUserByID(ctx context.Context, id int, tenantID int) (*ent.User, error) {
	s.logger.Infof("获取用户详情: ID=%d", id)

	userEntity, err := s.client.User.Query().
		Where(
			user.IDEQ(id),
			user.TenantIDEQ(tenantID),
		).
		WithRoles().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("用户不存在: ID=%d", id)
		}
		return nil, fmt.Errorf("获取用户失败: %w", err)
	}

	return userEntity, nil
}

// UpdateUser 更新用户信息
func (s *UserService) UpdateUser(ctx context.Context, id int, req *dto.UpdateUserRequest, tenantID int) (*ent.User, error) {
	s.logger.Infof("更新用户: ID=%d", id)

	// 验证用户属于当前租户，防止跨租户访问
	existingUser, err := s.client.User.Query().
		Where(user.IDEQ(id), user.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("用户不存在: ID=%d", id)
		}
		return nil, fmt.Errorf("获取用户失败: %w", err)
	}

	update := s.client.User.UpdateOneID(id).Where(user.TenantIDEQ(tenantID))

	// 检查用户名是否已被其他用户使用
	if req.Username != "" && req.Username != existingUser.Username {
		exists, err := s.client.User.Query().
			Where(
				user.And(
					user.UsernameEQ(req.Username),
					user.IDNEQ(id),
				),
			).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查用户名失败: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("用户名已存在: %s", req.Username)
		}
		update = update.SetUsername(req.Username)
	}

	// 检查邮箱是否已被其他用户使用
	if req.Email != "" && req.Email != existingUser.Email {
		exists, err := s.client.User.Query().
			Where(
				user.And(
					user.EmailEQ(req.Email),
					user.IDNEQ(id),
				),
			).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查邮箱失败: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("邮箱已存在: %s", req.Email)
		}
		update = update.SetEmail(req.Email)
	}

	// 更新其他字段
	if req.Name != "" {
		update = update.SetName(req.Name)
	}
	if req.Department != "" {
		update = update.SetDepartment(req.Department)
	}
	if req.DepartmentID != nil {
		update = update.SetDepartmentID(*req.DepartmentID)
	}
	if req.Phone != "" {
		update = update.SetPhone(req.Phone)
	}
	if strings.TrimSpace(req.Gender) != "" {
		update = update.SetGender(req.Gender)
	}
	if req.IsLeader != nil {
		update = update.SetIsLeader(*req.IsLeader)
	}
	if strings.TrimSpace(req.FunctionLine) != "" {
		update = update.SetFunctionLine(req.FunctionLine)
	}
	if req.ManagerID != nil {
		update = update.SetManagerID(*req.ManagerID)
	}
	// 角色更新（仅在提供时设置），管理员权限由RBAC控制
	if strings.TrimSpace(req.Role) != "" {
		role := strings.ToLower(strings.TrimSpace(req.Role))
		// 兼容前端传的"user"角色，自动转换为"end_user"
		if role == "user" {
			role = "end_user"
		}
		update = update.SetRole(role)

	}

	// 附加角色（仅影响 BPMN 按角色路由的候选资格，不影响 RBAC 权限判定，见 dto.UpdateUserRequest
	// 里 AdditionalRoleIds 的注释）。nil 表示不修改；非 nil 时用传入的列表整体替换现有附加角色——
	// 先清空再整体重设，语义等同于"提交的列表就是完整的附加角色集合"，避免增量 add/remove 的状态漂移。
	if req.AdditionalRoleIds != nil {
		update = update.ClearRoles()
		if len(*req.AdditionalRoleIds) > 0 {
			update = update.AddRoleIDs(*req.AdditionalRoleIds...)
		}
	}

	userEntity, err := update.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("更新用户失败: %w", err)
	}

	// update.Save 返回的实体不带预加载的边，这里补一次查询把最新的附加角色状态
	// 挂到 Edges.Roles 上，供 controller 层 dto.ToUserDetailResponse 使用。查询失败
	// 不影响主流程——Edges.Roles 保持 nil 即可。
	if roles, rolesErr := userEntity.QueryRoles().All(ctx); rolesErr == nil {
		userEntity.Edges.Roles = roles
	}

	s.logger.Infof("用户更新成功: ID=%d", id)
	return userEntity, nil
}

// DeleteUser 删除用户
func (s *UserService) DeleteUser(ctx context.Context, id int, tenantID int) error {
	s.logger.Infof("删除用户: ID=%d", id)

	// 检查用户是否存在
	_, err := s.GetUserByID(ctx, id, tenantID)
	if err != nil {
		return err
	}

	// 软删除 - 设置为非激活状态
	err = s.client.User.UpdateOneID(id).
		Where(user.TenantIDEQ(tenantID)).
		SetActive(false).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("删除用户失败: %w", err)
	}

	s.logger.Infof("用户删除成功: ID=%d", id)
	return nil
}

// ChangeUserStatus 更改用户状态
// currentUserID 是当前操作的用户ID，用于防止用户停用自己
func (s *UserService) ChangeUserStatus(ctx context.Context, id int, active bool, currentUserID int, tenantID int) error {
	s.logger.Infof("更改用户状态: ID=%d, active=%t, currentUserID=%d", id, active, currentUserID)

	// 检查用户是否存在
	_, err := s.GetUserByID(ctx, id, tenantID)
	if err != nil {
		return err
	}

	// 防止用户停用自己
	if id == currentUserID && !active {
		return fmt.Errorf("不能停用当前登录用户")
	}

	err = s.client.User.UpdateOneID(id).
		Where(user.TenantIDEQ(tenantID)).
		SetActive(active).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("更改用户状态失败: %w", err)
	}

	s.logger.Infof("用户状态更改成功: ID=%d, active=%t", id, active)
	return nil
}

// ResetPassword 重置用户密码
func (s *UserService) ResetPassword(ctx context.Context, id int, newPassword string, tenantID int) error {
	s.logger.Infof("重置用户密码: ID=%d", id)

	// 检查用户是否存在
	_, err := s.GetUserByID(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("密码加密失败: %w", err)
	}

	err = s.client.User.UpdateOneID(id).
		Where(user.TenantIDEQ(tenantID)).
		SetPasswordHash(string(hashedPassword)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("重置密码失败: %w", err)
	}

	s.logger.Infof("用户密码重置成功: ID=%d", id)
	return nil
}

// GetUserStats 获取用户统计信息
func (s *UserService) GetUserStats(ctx context.Context, tenantID int) (*dto.UserStatsResponse, error) {
	s.logger.Infof("获取用户统计: tenantID=%d", tenantID)

	if tenantID <= 0 {
		return nil, fmt.Errorf("租户信息无效")
	}
	query := s.client.User.Query().Where(user.TenantIDEQ(tenantID))

	// 总用户数
	total, err := query.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("统计总用户数失败: %w", err)
	}

	// 活跃用户数
	active, err := query.Clone().Where(user.ActiveEQ(true)).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("统计活跃用户数失败: %w", err)
	}

	monthStart := time.Now().In(time.Local)
	monthStart = time.Date(monthStart.Year(), monthStart.Month(), 1, 0, 0, 0, 0, monthStart.Location())
	newThisMonth, err := query.Clone().Where(user.CreatedAtGTE(monthStart)).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("统计本月新增用户失败: %w", err)
	}

	response := &dto.UserStatsResponse{
		Total:  total,
		Active: active,
		// 当前模型没有登录会话/心跳数据，返回0，避免把“启用”误报为“在线”。
		Online:       0,
		ByRole:       map[string]int{},
		ByDepartment: map[string]int{},
		NewThisMonth: newThisMonth,
	}

	s.logger.Infow("用户统计获取成功", "total", total, "active", active, "online", 0, "new_this_month", newThisMonth)
	return response, nil
}

// BatchUpdateUsers 批量更新用户
func (s *UserService) BatchUpdateUsers(ctx context.Context, req *dto.BatchUpdateUsersRequest, tenantID int) error {
	s.logger.Infof("批量更新用户: count=%d", len(req.UserIDs))

	if len(req.UserIDs) == 0 {
		return fmt.Errorf("用户ID列表不能为空")
	}

	ids := uniquePositiveIDs(req.UserIDs)
	if len(ids) == 0 {
		return fmt.Errorf("用户ID列表不能为空")
	}
	if req.Action == "deactivate" && req.OperatorID > 0 {
		for _, id := range ids {
			if id == req.OperatorID {
				return fmt.Errorf("批量停用不能包含当前登录用户")
			}
		}
	}
	matched, err := s.client.User.Query().Where(user.IDIn(ids...), user.TenantIDEQ(tenantID)).Count(ctx)
	if err != nil {
		return fmt.Errorf("校验批量用户失败: %w", err)
	}
	if matched != len(ids) {
		return fmt.Errorf("部分用户不存在或不属于当前租户，请刷新列表后重试")
	}

	update := s.client.User.Update().
		Where(
			user.IDIn(ids...),
			user.TenantIDEQ(tenantID),
		)

	// 根据操作类型更新
	switch req.Action {
	case "activate":
		update = update.SetActive(true)
	case "deactivate":
		update = update.SetActive(false)
	case "department":
		if req.Department == "" {
			return fmt.Errorf("部门不能为空")
		}
		update = update.SetDepartment(req.Department)
	default:
		return fmt.Errorf("不支持的操作类型: %s", req.Action)
	}

	count, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("批量更新用户失败: %w", err)
	}

	s.logger.Infof("批量更新用户成功: updated=%d", count)
	return nil
}

func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 128 {
		return fmt.Errorf("密码长度必须为12到128位")
	}
	var upper, lower, digit, special bool
	for _, r := range password {
		switch {
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			special = true
		}
	}
	if !upper || !lower || !digit || !special {
		return fmt.Errorf("密码必须同时包含大写字母、小写字母、数字和特殊字符")
	}
	return nil
}

// SearchUsers 搜索用户
func (s *UserService) SearchUsers(ctx context.Context, req *dto.SearchUsersRequest, tenantID int) ([]*dto.UserDetailResponse, error) {
	s.logger.Infof("搜索用户: keyword=%s", req.Keyword)

	if req.Keyword == "" {
		return []*dto.UserDetailResponse{}, nil
	}

	query := s.client.User.Query().
		Where(
			user.Or(
				user.UsernameContainsFold(req.Keyword),
				user.NameContainsFold(req.Keyword),
				user.EmailContainsFold(req.Keyword),
			),
		)

	query = query.Where(user.TenantIDEQ(tenantID))

	// 只返回活跃用户
	query = query.Where(user.ActiveEQ(true))

	users, err := query.
		Limit(req.Limit).
		Order(ent.Asc(user.FieldName)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("搜索用户失败: %w", err)
	}

	// 转换为响应格式
	userResponses := dto.ToUserDetailResponseList(users)

	s.logger.Infof("用户搜索成功: found=%d", len(users))
	return userResponses, nil
}
