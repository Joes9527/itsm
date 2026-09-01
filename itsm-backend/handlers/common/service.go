package common

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	enttenant "itsm-backend/ent/tenant"
	entuser "itsm-backend/ent/user"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	repo          Repository
	jwtSecret     string
	logger        *zap.SugaredLogger
	client        *ent.Client // Authentication audit and tenant queries.
	refreshTokens *authentication.RefreshTokenConsumer
}

func NewService(repo Repository, jwtSecret string, logger *zap.SugaredLogger, client *ent.Client, refreshTokens *authentication.RefreshTokenConsumer) *Service {
	return &Service{
		repo:          repo,
		jwtSecret:     jwtSecret,
		logger:        logger,
		client:        client,
		refreshTokens: refreshTokens,
	}
}

// Auth

// getUserPermissions 获取用户的权限列表
func (s *Service) getUserPermissions(role string) []string {
	permissions := make([]string, 0)

	// 超级管理员拥有所有权限
	if role == "super_admin" {
		return []string{"*"}
	}

	// 从应用授权策略获取角色权限。
	rolePerms, ok := authorization.RolePermissions[role]
	if !ok {
		return permissions
	}

	seen := make(map[string]bool)
	for _, p := range rolePerms {
		key := p.Resource + ":" + p.Action
		if !seen[key] {
			seen[key] = true
			permissions = append(permissions, key)
		}
	}

	return permissions
}

func (s *Service) Login(ctx context.Context, username, password string, tenantID int, tenantCode string) (*AuthResult, error) {
	// Resolve tenant
	if tenantID == 0 && tenantCode != "" {
		t, err := s.client.Tenant.Query().Where(enttenant.CodeEQ(tenantCode)).First(ctx)
		if err == nil {
			tenantID = t.ID
		}
	}
	// When no tenant is specified, find user by username alone (matches across tenants)
	var u *User
	var entUser *ent.User
	var err error
	if tenantID == 0 {
		// Look for user by username without tenant filter
		entUser, err = s.client.User.Query().Where(entuser.UsernameEQ(username)).Only(ctx)
		if err != nil {
			authentication.RecordLoginAudit(ctx, s.client, 0, tenantID, username, "LOGIN_FAILED", "用户不存在")
			return nil, fmt.Errorf("invalid credentials")
		}
		u = toUserDomain(entUser)
	} else {
		entUser, err = s.client.User.Query().
			Where(entuser.UsernameEQ(username), entuser.TenantID(tenantID)).
			Only(ctx)
		if err != nil {
			authentication.RecordLoginAudit(ctx, s.client, 0, tenantID, username, "LOGIN_FAILED", "用户不存在")
			return nil, fmt.Errorf("invalid credentials")
		}
		u = toUserDomain(entUser)
	}

	// Set msp_role from ent user
	mspRoleStr := string(entUser.MspRole)
	if mspRoleStr != "" {
		u.MSPRole = &mspRoleStr
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(entUser.PasswordHash), []byte(password)); err != nil {
		authentication.RecordLoginAudit(ctx, s.client, 0, entUser.TenantID, username, "LOGIN_FAILED", "密码错误")
		return nil, fmt.Errorf("invalid credentials")
	}

	if !u.Active {
		authentication.RecordLoginAudit(ctx, s.client, 0, entUser.TenantID, username, "LOGIN_FAILED", "账户锁定")
		return nil, fmt.Errorf("user account is inactive")
	}

	// 对于 MSP 用户，需要将 MSP 角色转换为 RBAC 角色
	// u.Role 是数据库中存储的 RBAC 角色（MSP 用户的 Role 是 admin）
	// 如果用户有 MSP 角色，则从 MSP 角色映射到正确的 RBAC 角色
	if mspRoleStr != "" {
		if mappedRole := authorization.GetMSPRBACRole(mspRoleStr); mappedRole != "" {
			u.Role = mappedRole
		}
	}

	// Generate tokens
	tokens, err := authentication.IssueSessionTokens(authentication.SessionIdentity{
		UserID: u.ID, Username: u.Username, Role: u.Role, TenantID: u.TenantID,
	}, s.jwtSecret)
	if err != nil {
		return nil, err
	}

	// 获取用户权限
	u.Permissions = s.getUserPermissions(u.Role)
	authentication.RecordLoginAudit(ctx, s.client, entUser.ID, entUser.TenantID, username, "LOGIN_SUCCESS", "")

	return &AuthResult{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		User:         u,
	}, nil
}

func (s *Service) RefreshToken(ctx context.Context, refreshToken string) (*AuthResult, error) {
	validated, err := s.refreshTokens.Validate(refreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh token rejected: %w", err)
	}
	identity := validated.Identity()
	if s.client == nil {
		return nil, fmt.Errorf("refresh authentication context unavailable")
	}
	userEntity, err := s.client.User.Get(ctx, identity.UserID)
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}
	if !userEntity.Active {
		return nil, fmt.Errorf("user account is inactive")
	}
	role := string(userEntity.Role)
	if userEntity.MspRole != "" {
		if mappedRole := authorization.GetMSPRBACRole(string(userEntity.MspRole)); mappedRole != "" {
			role = mappedRole
		}
	}
	if identity.Username != userEntity.Username || identity.Role != role {
		return nil, fmt.Errorf("refresh token actor context is stale")
	}
	tenantEntity, err := authorization.AuthorizeTenantSession(ctx, s.client, userEntity, identity.TenantID, time.Now())
	if err != nil {
		return nil, fmt.Errorf("refresh tenant rejected: %w", err)
	}
	if err := s.refreshTokens.Consume(ctx, validated); err != nil {
		return nil, fmt.Errorf("refresh token rejected: %w", err)
	}
	user := toUserDomain(userEntity)
	user.Role = role
	user.TenantID = tenantEntity.ID
	user.Permissions = s.getUserPermissions(role)

	// regenerate tokens
	tokens, err := authentication.IssueSessionTokens(authentication.SessionIdentity{
		UserID: user.ID, Username: user.Username, Role: role, TenantID: tenantEntity.ID,
	}, s.jwtSecret)
	if err != nil {
		return nil, err
	}

	return &AuthResult{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		User:         user,
	}, nil
}

// User Management

func (s *Service) GetUser(ctx context.Context, id int) (*User, error) {
	return s.repo.GetUserByID(ctx, id)
}

func (s *Service) ListUsers(ctx context.Context, tenantID int) ([]*User, error) {
	return s.repo.ListUsers(ctx, tenantID)
}

// Organization Management

func (s *Service) GetDepartment(ctx context.Context, id int, tenantID int) (*Department, error) {
	return s.repo.GetDepartment(ctx, id, tenantID)
}

func (s *Service) GetDepartmentTree(ctx context.Context, tenantID int) ([]*Department, error) {
	return s.repo.GetDepartmentTree(ctx, tenantID)
}

func (s *Service) ListDepartments(ctx context.Context, tenantID int) ([]*Department, error) {
	return s.repo.ListDepartments(ctx, tenantID)
}

func (s *Service) CreateDepartment(ctx context.Context, d *Department) (*Department, error) {
	return s.repo.CreateDepartment(ctx, d)
}

func (s *Service) UpdateDepartment(ctx context.Context, d *Department) (*Department, error) {
	return s.repo.UpdateDepartment(ctx, d)
}

func (s *Service) DeleteDepartment(ctx context.Context, id int, tenantID int) error {
	return s.repo.DeleteDepartment(ctx, id, tenantID)
}

func (s *Service) ListTeams(ctx context.Context, tenantID int) ([]*Team, error) {
	return s.repo.ListTeams(ctx, tenantID)
}

func (s *Service) GetTeam(ctx context.Context, id int, tenantID int) (*Team, error) {
	return s.repo.GetTeam(ctx, id, tenantID)
}

func (s *Service) CreateTeam(ctx context.Context, t *Team) (*Team, error) {
	return s.repo.CreateTeam(ctx, t)
}

func (s *Service) UpdateTeam(ctx context.Context, t *Team) (*Team, error) {
	return s.repo.UpdateTeam(ctx, t)
}

func (s *Service) DeleteTeam(ctx context.Context, id int, tenantID int) error {
	return s.repo.DeleteTeam(ctx, id, tenantID)
}

func (s *Service) AddTeamMember(ctx context.Context, teamID int, userID int) error {
	return s.repo.AddTeamMember(ctx, teamID, userID)
}

// Tags

func (s *Service) ListTags(ctx context.Context, tenantID int) ([]*Tag, error) {
	return s.repo.ListTags(ctx, tenantID)
}

func (s *Service) CreateTag(ctx context.Context, t *Tag) (*Tag, error) {
	return s.repo.CreateTag(ctx, t)
}

// Auditing

func (s *Service) LogActivity(ctx context.Context, log *AuditLog) error {
	return s.repo.CreateAuditLog(ctx, log)
}

func (s *Service) GetAuditLogs(ctx context.Context, tenantID int, userID int) ([]*AuditLog, error) {
	return s.repo.ListAuditLogs(ctx, tenantID, userID, 100)
}

// GetUserTenants 获取用户所属的租户列表
func (s *Service) GetUserTenants(ctx context.Context, userID int) ([]interface{}, error) {
	// 直接使用 ent client 查询用户关联的租户
	user, err := s.client.User.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	// 通过 tenant_id 直接查询租户
	tenant, err := s.client.Tenant.Get(ctx, user.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant: %w", err)
	}

	if tenant == nil {
		return []interface{}{}, nil
	}

	return []interface{}{
		map[string]interface{}{
			"id":     tenant.ID,
			"name":   tenant.Name,
			"code":   tenant.Code,
			"type":   tenant.Type,
			"status": tenant.Status,
		},
	}, nil
}
