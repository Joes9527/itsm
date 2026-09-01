package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"golang.org/x/crypto/bcrypt"
)

func TestAuthService_GetUserTenants(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	authService := &AuthService{client: client, jwtSecret: "test-secret-key", logger: zaptest.NewLogger(t).Sugar()}
	ctx := context.Background()
	tenant1, err := client.Tenant.Create().
		SetName("Tenant 1").SetCode("tenant1").SetDomain("tenant1.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	_, err = client.Tenant.Create().
		SetName("Tenant 2").SetCode("tenant2").SetDomain("tenant2.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)
	testUser, err := client.User.Create().
		SetUsername("testuser").SetEmail("test@example.com").SetName("Test User").
		SetPasswordHash(string(hashedPassword)).SetRole("end_user").SetActive(true).SetTenantID(tenant1.ID).Save(ctx)
	require.NoError(t, err)

	t.Run("获取用户租户列表", func(t *testing.T) {
		response, err := authService.GetUserTenants(ctx, testUser.ID)
		require.NoError(t, err)
		require.Len(t, response.Tenants, 1)
		assert.Equal(t, tenant1.ID, response.Tenants[0].ID)
		assert.Equal(t, "Tenant 1", response.Tenants[0].Name)
	})
	t.Run("不存在的用户", func(t *testing.T) {
		response, err := authService.GetUserTenants(ctx, 99999)
		require.Error(t, err)
		assert.Nil(t, response)
	})
}

func TestAuthService_GetUserInfo(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	authService := &AuthService{client: client, jwtSecret: "test-secret-key", logger: zaptest.NewLogger(t).Sugar()}
	ctx := context.Background()
	testTenant, err := client.Tenant.Create().
		SetName("Test Tenant").SetCode("test").SetDomain("test.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	require.NoError(t, err)
	testUser, err := client.User.Create().
		SetUsername("testuser").SetEmail("test@example.com").SetPasswordHash(string(hashedPassword)).
		SetRole("admin").SetActive(true).SetTenantID(testTenant.ID).SetName("Test User").
		SetPhone("13800138000").Save(ctx)
	require.NoError(t, err)

	t.Run("获取用户信息成功", func(t *testing.T) {
		response, err := authService.GetUserInfo(ctx, testUser.ID)
		require.NoError(t, err)
		assert.Equal(t, testUser.ID, response.ID)
		assert.Equal(t, "testuser", response.Username)
		assert.Equal(t, "test@example.com", response.Email)
		assert.Equal(t, "admin", response.Role)
		assert.Equal(t, "Test User", response.Name)
	})
	t.Run("用户不存在", func(t *testing.T) {
		response, err := authService.GetUserInfo(ctx, 99999)
		require.Error(t, err)
		assert.Nil(t, response)
	})
}
