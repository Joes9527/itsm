package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/passwordresettoken"
	"itsm-backend/ent/tenant"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	client       *ent.Client
	jwtSecret    string
	logger       *zap.SugaredLogger
	emailService *EmailService
	baseURL      string // 前端基础URL，用于生成重置链接
}

func NewAuthService(client *ent.Client, jwtSecret string, logger *zap.SugaredLogger) *AuthService {
	return &AuthService{
		client:    client,
		jwtSecret: jwtSecret,
		logger:    logger,
		baseURL:   "http://localhost:3000", // 默认值，可在生产环境通过配置覆盖
	}
}

// SetEmailService 设置邮件服务
func (s *AuthService) SetEmailService(emailService *EmailService) {
	s.emailService = emailService
}

// SetBaseURL 设置前端基础URL
func (s *AuthService) SetBaseURL(baseURL string) {
	s.baseURL = baseURL
}

// getUserPermissions 获取用户的权限列表（优先数据库，fallback 硬编码兜底）
func (s *AuthService) getUserPermissions(userEntity *ent.User) []string {
	permissions := make([]string, 0)

	// 超级管理员拥有所有权限
	if userEntity.Role == "super_admin" {
		return []string{"*"}
	}

	roleCode := string(userEntity.Role)
	seen := make(map[string]bool)

	// 优先从数据库加载（与运行时权限校验一致）
	rolePerms := authorization.GetRolePermissions(s.client, roleCode, userEntity.TenantID)
	// 数据库为空时 fallback 硬编码（MSP 角色等尚未迁入数据库）
	if len(rolePerms) == 0 {
		rolePerms = authorization.RolePermissions[roleCode]
	}
	for _, p := range rolePerms {
		key := p.Resource + ":" + p.Action
		if !seen[key] {
			seen[key] = true
			permissions = append(permissions, key)
		}
	}

	// 对于 MSP 用户，也要合并 RBAC MSP 角色的权限
	if userEntity.MspRole != "" {
		if rbacRole := authorization.GetMSPRBACRole(string(userEntity.MspRole)); rbacRole != "" {
			mspPerms := authorization.GetRolePermissions(s.client, rbacRole, userEntity.TenantID)
			if len(mspPerms) == 0 {
				mspPerms = authorization.RolePermissions[rbacRole]
			}
			for _, p := range mspPerms {
				key := p.Resource + ":" + p.Action
				if !seen[key] {
					seen[key] = true
					permissions = append(permissions, key)
				}
			}
		}
	}

	return permissions
}

// GetUserTenants 获取用户可访问的租户列表
func (s *AuthService) GetUserTenants(ctx context.Context, userID int) (*dto.UserTenantsResponse, error) {
	// 查询用户的租户（这里简化为用户只属于一个租户）
	userEntity, err := s.client.User.Query().
		Where(user.IDEQ(userID)).
		First(ctx)
	if err != nil {
		s.logger.Errorw("Failed to get user", "user_id", userID, "error", err)
		return nil, fmt.Errorf("查询用户信息失败")
	}

	// 查询租户信息
	tenantEntity, err := s.client.Tenant.Get(ctx, userEntity.TenantID)
	if err != nil {
		s.logger.Errorw("Failed to get tenant", "tenant_id", userEntity.TenantID, "error", err)
		return nil, fmt.Errorf("查询租户信息失败")
	}

	tenants := []dto.TenantInfo{
		{
			ID:     tenantEntity.ID,
			Name:   tenantEntity.Name,
			Code:   tenantEntity.Code,
			Domain: tenantEntity.Domain,
			Type:   string(tenantEntity.Type),
			Status: tenantEntity.Status,
		},
	}

	return &dto.UserTenantsResponse{
		Tenants: tenants,
	}, nil
}

// SwitchTenant 切换租户
func (s *AuthService) SwitchTenant(ctx context.Context, userID, tenantID int) (*dto.LoginResponse, error) {
	// Session selection needs the authenticated actor and MSP relation before
	// authorizing a target. This scope never escapes the authentication owner.
	ctx = tenantctx.SystemContext(ctx, "auth:switch_tenant", "authorize the actor against the requested tenant")
	userEntity, err := s.client.User.Get(ctx, userID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("用户不存在")
		}
		s.logger.Errorw("Failed to load user for tenant switch", "user_id", userID, "error", err)
		return nil, fmt.Errorf("无权限访问该租户")
	}
	if !userEntity.Active {
		return nil, fmt.Errorf("用户账号已被禁用")
	}
	tenantEntity, err := authorization.AuthorizeTenantSession(ctx, s.client, userEntity, tenantID, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, authorization.ErrTenantInactive):
			return nil, fmt.Errorf("租户已被暂停")
		case errors.Is(err, authorization.ErrTenantExpired):
			return nil, fmt.Errorf("租户已过期")
		default:
			s.logger.Warnw("Switch tenant denied", "user_id", userID, "tenant_id", tenantID, "error", err)
			return nil, fmt.Errorf("无权限访问该租户")
		}
	}
	role := string(userEntity.Role)
	if userEntity.MspRole != "" {
		if mappedRole := authorization.GetMSPRBACRole(string(userEntity.MspRole)); mappedRole != "" {
			role = mappedRole
		}
	}

	tokens, err := authentication.IssueSessionTokens(authentication.SessionIdentity{
		UserID: userEntity.ID, Username: userEntity.Username, Role: role, TenantID: tenantID,
	}, s.jwtSecret)
	if err != nil {
		s.logger.Errorw("Failed to generate access token for tenant switch", "user_id", userEntity.ID, "tenant_id", tenantID, "error", err)
		return nil, fmt.Errorf("生成token失败")
	}

	s.logger.Infow("Tenant switched successfully", "user_id", userEntity.ID, "tenant_id", tenantID)

	// 获取用户权限列表
	permissions := s.getUserPermissions(userEntity)

	return &dto.LoginResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		User: &dto.LoginUserResponse{
			ID:       userEntity.ID,
			Username: userEntity.Username,
			Email:    userEntity.Email,
			Name:     userEntity.Name,
			Role:     role,
			MSPRole: func() *string {
				s := string(userEntity.MspRole)
				if s == "" {
					return nil
				}
				return &s
			}(),
			Department:   userEntity.Department,
			DepartmentID: userEntity.DepartmentID,
			Phone:        userEntity.Phone,
			Active:       userEntity.Active,
			TenantID:     tenantEntity.ID,
			CreatedAt:    userEntity.CreatedAt,
			UpdatedAt:    userEntity.UpdatedAt,
			Permissions:  permissions,
		},
		Tenant: tenantEntity,
	}, nil
}

// ValidateUser 验证用户是否存在且激活
func (s *AuthService) ValidateUser(ctx context.Context, userID int) (*ent.User, error) {
	userEntity, err := s.client.User.Get(ctx, userID)
	if err != nil {
		s.logger.Warnw("User not found", "user_id", userID, "error", err)
		return nil, fmt.Errorf("用户不存在")
	}

	if !userEntity.Active {
		s.logger.Warnw("User is inactive", "user_id", userID)
		return nil, fmt.Errorf("用户账号已被禁用")
	}

	return userEntity, nil
}

// GetUserInfo 获取用户信息
func (s *AuthService) GetUserInfo(ctx context.Context, userID int) (*dto.UserInfo, error) {
	userEntity, err := s.ValidateUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	// 使用数据库中的角色
	role := userEntity.Role

	return &dto.UserInfo{
		ID:         userEntity.ID,
		Username:   userEntity.Username,
		Role:       string(role),
		Email:      userEntity.Email,
		Name:       userEntity.Name,
		Department: userEntity.Department,
		TenantID:   userEntity.TenantID,
	}, nil
}

// Register 用户注册
func (s *AuthService) Register(ctx context.Context, req *dto.RegisterRequest) (*dto.RegisterResponse, error) {
	// 检查用户名是否已存在
	exists, err := s.client.User.Query().
		Where(user.UsernameEQ(req.Username)).
		Exist(ctx)
	if err != nil {
		s.logger.Errorw("Failed to check username existence", "username", req.Username, "error", err)
		return nil, fmt.Errorf("检查用户名失败")
	}
	if exists {
		return nil, fmt.Errorf("用户名已被注册")
	}

	// 检查邮箱是否已存在
	exists, err = s.client.User.Query().
		Where(user.EmailEQ(req.Email)).
		Exist(ctx)
	if err != nil {
		s.logger.Errorw("Failed to check email existence", "email", req.Email, "error", err)
		return nil, fmt.Errorf("检查邮箱失败")
	}
	if exists {
		return nil, fmt.Errorf("邮箱已被注册")
	}

	// 获取租户ID
	tenantID := 1 // 默认租户
	if req.TenantCode != "" {
		tenantEntity, err := s.client.Tenant.Query().
			Where(tenant.CodeEQ(req.TenantCode)).
			First(ctx)
		if err != nil {
			s.logger.Warnw("Tenant not found", "tenant_code", req.TenantCode, "error", err)
			return nil, fmt.Errorf("租户不存在")
		}
		tenantID = tenantEntity.ID
	}

	// 加密密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		s.logger.Errorw("Failed to hash password", "error", err)
		return nil, fmt.Errorf("密码加密失败")
	}

	// 确定角色
	role := req.Role
	if role == "" {
		role = "end_user"
	}

	// 创建用户
	userEntity, err := s.client.User.Create().
		SetUsername(req.Username).
		SetEmail(req.Email).
		SetName(req.FullName).
		SetPasswordHash(string(hashedPassword)).
		SetPhone(req.Phone).
		SetDepartment(req.Company).
		SetRole(role).
		SetTenantID(tenantID).
		SetActive(true).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create user", "username", req.Username, "error", err)
		return nil, fmt.Errorf("创建用户失败")
	}

	s.logger.Infow("User registered successfully", "user_id", userEntity.ID, "username", req.Username)

	return &dto.RegisterResponse{
		ID:       userEntity.ID,
		Username: userEntity.Username,
		Email:    userEntity.Email,
		Message:  "注册成功",
	}, nil
}

// ForgotPassword 发送密码重置邮件
// 安全考虑：为了避免泄露邮箱/租户的存在性，无论"租户不存在"、"用户不存在"
// 还是"用户存在"，都返回相同的通用成功消息。只有 token 生成/邮件发送错误才返回 error。
func (s *AuthService) ForgotPassword(ctx context.Context, req *dto.ForgotPasswordRequest) (*dto.ForgotPasswordResponse, error) {
	genericOK := &dto.ForgotPasswordResponse{
		Message: "如果该邮箱已注册，我们将发送密码重置链接",
	}

	// 查找用户
	userQuery := s.client.User.Query().Where(user.EmailEQ(req.Email))

	// 如果提供了租户代码，按租户过滤
	if req.TenantCode != "" {
		tenantEntity, err := s.client.Tenant.Query().
			Where(tenant.CodeEQ(req.TenantCode)).
			First(ctx)
		if err != nil {
			// 安全：不区分"租户不存在"，返回通用成功避免泄露存在性
			s.logger.Warnw("Tenant not found during password reset",
				"tenant_code", req.TenantCode, "error", err)
			return genericOK, nil
		}
		userQuery = userQuery.Where(user.TenantIDEQ(tenantEntity.ID))
	}

	userEntity, err := userQuery.First(ctx)
	if err != nil {
		s.logger.Warnw("User not found for password reset", "email", req.Email, "error", err)
		// 为了安全，不提示用户不存在
		return genericOK, nil
	}

	// 生成重置令牌
	token, err := generateResetToken()
	if err != nil {
		s.logger.Errorw("Failed to generate password reset token", "user_id", userEntity.ID, "error", err)
		return nil, fmt.Errorf("生成重置令牌失败: %w", err)
	}
	expiresAt := time.Now().Add(1 * time.Hour) // 1小时后过期

	// 保存重置令牌
	_, err = s.client.PasswordResetToken.Create().
		SetUserID(userEntity.ID).
		SetEmail(req.Email).
		SetToken(token).
		SetExpiresAt(expiresAt).
		Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create password reset token", "user_id", userEntity.ID, "error", err)
		return nil, fmt.Errorf("生成重置令牌失败")
	}

	// 发送重置邮件
	if s.emailService != nil {
		err = s.emailService.SendPasswordResetEmail(ctx, []string{req.Email}, token, s.baseURL)
		if err != nil {
			s.logger.Errorw("Failed to send password reset email", "user_id", userEntity.ID, "email", req.Email, "error", err)
			// 邮件发送失败不影响流程，只记录日志
		} else {
			s.logger.Infow("Password reset email sent", "user_id", userEntity.ID, "email", req.Email)
		}
	} else {
		s.logger.Warnw("Email service not configured, skipping email send", "user_id", userEntity.ID, "email", req.Email)
	}

	return &dto.ForgotPasswordResponse{
		Message: "如果该邮箱已注册，我们将发送密码重置链接",
	}, nil
}

// ResetPassword 重置密码
func (s *AuthService) ResetPassword(ctx context.Context, req *dto.PasswordResetRequest) (*dto.PasswordResetResponse, error) {
	// 检查两次密码是否一致
	if req.Password != req.PasswordConfirm {
		return nil, fmt.Errorf("两次输入的密码不一致")
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		s.logger.Errorw("Failed to hash new password", "error", err)
		return nil, fmt.Errorf("密码加密失败")
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("启动密码重置事务失败")
	}
	defer func() { _ = tx.Rollback() }()

	// 单条条件 UPDATE 原子消费令牌；并发请求中只允许一个事务影响一行。
	affected, err := tx.PasswordResetToken.Update().
		Where(
			passwordresettoken.TokenEQ(req.Token),
			passwordresettoken.EmailEQ(req.Email),
			passwordresettoken.Used(false),
			passwordresettoken.ExpiresAtGT(time.Now()),
		).
		SetUsed(true).
		Save(ctx)
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("令牌无效或已使用")
	}

	tokenEntity, err := tx.PasswordResetToken.Query().
		Where(passwordresettoken.TokenEQ(req.Token), passwordresettoken.EmailEQ(req.Email)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("令牌无效或已使用")
	}
	if _, err = tx.User.UpdateOneID(tokenEntity.UserID).
		SetPasswordHash(string(hashedPassword)).
		Save(ctx); err != nil {
		s.logger.Errorw("Failed to update password", "user_id", tokenEntity.UserID, "error", err)
		return nil, fmt.Errorf("更新密码失败")
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交密码重置失败")
	}

	s.logger.Infow("Password reset successfully", "user_id", tokenEntity.UserID)

	return &dto.PasswordResetResponse{
		Message: "密码重置成功，请使用新密码登录",
	}, nil
}

// ValidateResetToken 验证重置令牌是否有效
func (s *AuthService) ValidateResetToken(ctx context.Context, req *dto.ValidateResetTokenRequest) (*dto.ValidateResetTokenResponse, error) {
	tokenEntity, err := s.client.PasswordResetToken.Query().
		Where(
			passwordresettoken.TokenEQ(req.Token),
			passwordresettoken.EmailEQ(req.Email),
			passwordresettoken.Used(false),
		).
		First(ctx)
	if err != nil {
		return &dto.ValidateResetTokenResponse{
			Valid: false,
			Email: req.Email,
		}, nil
	}

	// 检查是否过期
	if time.Now().After(tokenEntity.ExpiresAt) {
		return &dto.ValidateResetTokenResponse{
			Valid: false,
			Email: req.Email,
		}, nil
	}

	return &dto.ValidateResetTokenResponse{
		Valid: true,
		Email: req.Email,
	}, nil
}

// generateResetToken generates a cryptographically secure password reset token
func generateResetToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("read cryptographic random bytes: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// CleanupExpiredTokens 清理过期的重置令牌
func (s *AuthService) CleanupExpiredTokens(ctx context.Context) error {
	_, err := s.client.PasswordResetToken.Delete().
		Where(passwordresettoken.ExpiresAtLT(time.Now())).
		Exec(ctx)
	if err != nil {
		s.logger.Errorw("Failed to cleanup expired tokens", "error", err)
		return fmt.Errorf("cleanup expired password reset tokens: %w", err)
	}
	return nil
}
