package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"itsm-backend/common/executionscope"
	"itsm-backend/config"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Manager 负责"已注册连接器" + "已配置实例" 的生命周期管理
// 多个租户、每个租户可挂多个同名连接器实例（例如：飞书A区机器人 + 飞书B区机器人）
type CapabilityGate interface {
	RequireConnectorDelivery(context.Context, executionscope.Ref, string) error
	RequireIntegrationManagement(context.Context, int) error
	RequireCapability(context.Context, int, string) error
	RequireStartupCapability(context.Context, string) error
	ConnectorStartupTargets(context.Context) ([]config.ConnectorTargetConfig, error)
}

type Manager struct {
	gate     CapabilityGate
	registry *Registry
	logger   *zap.SugaredLogger

	mu               sync.RWMutex
	nextGeneration   uint64
	instances        map[string]*instance // key = tenantID + "/" + connectorName + "/" + instanceID
	closed           bool
	startupAttempted bool
	initializing     sync.WaitGroup // Add under mu before closed can become true.
}

type instance struct {
	generation uint64
	cfg        Config
	conn       Connector
	health     json.RawMessage
	target     *targetAuthority
}

// NewManager 创建管理器
func NewManager(registry *Registry, logger *zap.SugaredLogger, gate CapabilityGate) *Manager {
	if registry == nil {
		registry = Default()
	}
	return &Manager{
		registry:  registry,
		gate:      gate,
		logger:    logger,
		instances: make(map[string]*instance),
	}
}

func instanceKey(c Config) string {
	return fmt.Sprintf("%d/%s/%s", c.TenantID, c.Name, c.Provider)
}

// RequireIntegrationManagement checks the deployment policy before request-side
// configuration changes. It does not replace endpoint RBAC.
func (m *Manager) RequireIntegrationManagement(ctx context.Context, tenantID int) error {
	if m == nil || m.gate == nil {
		return executionscope.ErrDenied
	}
	return m.gate.RequireIntegrationManagement(ctx, tenantID)
}

// Provision 根据配置创建/更新一个连接器实例
func (m *Manager) Provision(ctx context.Context, cfg Config) error {
	if err := m.RequireIntegrationManagement(ctx, cfg.TenantID); err != nil {
		return err
	}
	m.mu.Lock()
	unavailable := m.closed || m.startupAttempted
	if unavailable {
		m.mu.Unlock()
		return executionscope.ErrDenied
	}
	m.initializing.Add(1)
	m.mu.Unlock()
	defer m.initializing.Done()
	if !cfg.Enabled {
		return m.Revoke(ctx, cfg)
	}
	c, err := m.initializeConnector(ctx, cfg, nil, "")
	if err != nil {
		return err
	}
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return errors.Join(err, c.Close())
	}
	if m.closed || m.startupAttempted {
		m.mu.Unlock()
		return errors.Join(executionscope.ErrDenied, c.Close())
	}
	m.nextGeneration++
	m.instances[instanceKey(cfg)] = &instance{cfg: cfg, conn: c, generation: m.nextGeneration}
	m.mu.Unlock()
	if m.logger != nil {
		m.logger.Infow("connector provisioned",
			"tenant", cfg.TenantID, "name", cfg.Name, "provider", cfg.Provider)
	}
	return nil
}

// Revoke 关闭并移除一个实例
func (m *Manager) Revoke(ctx context.Context, cfg Config) error {
	if err := m.RequireIntegrationManagement(ctx, cfg.TenantID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.startupAttempted {
		return executionscope.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	k := instanceKey(cfg)
	inst, ok := m.instances[k]
	if !ok {
		return nil
	}
	if err := inst.conn.Close(); err != nil {
		return fmt.Errorf("connector close failed: %w", err)
	}
	delete(m.instances, k)
	if m.logger != nil {
		m.logger.Infow("connector revoked", "tenant", cfg.TenantID, "name", cfg.Name)
	}
	return nil
}

// Get 根据租户+名称取出连接器
func (m *Manager) Get(tenantID int, name string) (Connector, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.instances {
		if inst.cfg.TenantID == tenantID && inst.cfg.Name == name && inst.cfg.Enabled {
			return inst.conn, true
		}
	}
	return nil, false
}

// GetInstance resolves one exact instance. The returned connector is bound to
// that provisioned object; callers must not look it up again after validation.
func (m *Manager) GetInstance(tenantID int, name, provider string) (Connector, uint64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[instanceKey(Config{TenantID: tenantID, Name: name, Provider: provider})]
	if !ok || !inst.cfg.Enabled {
		return nil, 0, false
	}
	return inst.conn, inst.generation, true
}

// GetByCallbackInstanceID resolves a public webhook without exposing an enumerable tenant ID.
func (m *Manager) GetByCallbackInstanceID(name, callbackInstanceID string) (Connector, int, bool) {
	if callbackInstanceID == "" {
		return nil, 0, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var found Connector
	var tenantID int
	matched := false
	for _, inst := range m.instances {
		id, _ := inst.cfg.Settings["callbackInstanceId"].(string)
		if inst.cfg.Name == name && inst.cfg.Enabled && id == callbackInstanceID {
			if matched {
				return nil, 0, false
			}
			found, tenantID, matched = inst.conn, inst.cfg.TenantID, true
		}
	}
	return found, tenantID, matched
}

// ListByTenant 列出某租户所有运行中的连接器
func (m *Manager) ListByTenant(tenantID int) []Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Config, 0)
	for _, inst := range m.instances {
		if inst.cfg.TenantID == tenantID {
			out = append(out, inst.cfg)
		}
	}
	return out
}

// Send 通过指定连接器发送消息
func (m *Manager) Send(ctx context.Context, tenantID int, name string, msg *Message) error {
	c, ok := m.Get(tenantID, name)
	if !ok {
		return fmt.Errorf("connector %q not provisioned for tenant %d", name, tenantID)
	}
	return c.Send(ctx, msg)
}

// HealthSnapshot reads only observed results for one tenant. A newly provisioned
// instance has no result; replacing an instance discards the previous generation.
func (m *Manager) HealthSnapshot(tenantID int) (map[string]HealthStatus, error) {
	out := make(map[string]HealthStatus)
	if tenantID <= 0 {
		return nil, executionscope.ErrDenied
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for key, ins := range m.instances {
		if ins.cfg.TenantID != tenantID || len(ins.health) == 0 {
			continue
		}
		var status HealthStatus
		if err := json.Unmarshal(ins.health, &status); err != nil {
			return nil, fmt.Errorf("invalid cached connector health: %w", err)
		}
		out[key] = status
	}
	return out, nil
}

// RefreshHealth is the only Manager diagnostic operation. Deployment permission
// never substitutes for the HTTP/application owner's connector write permission.
func (m *Manager) RefreshHealth(ctx context.Context, tenantID int) error {
	if m.gate == nil {
		return executionscope.ErrDenied
	}
	if err := m.gate.RequireCapability(ctx, tenantID, "connector_diagnostics"); err != nil {
		return err
	}
	m.mu.RLock()
	insts := make([]*instance, 0, len(m.instances))
	for _, ins := range m.instances {
		if ins.cfg.TenantID == tenantID {
			insts = append(insts, ins)
		}
	}
	m.mu.RUnlock()
	for _, ins := range insts {
		if err := ctx.Err(); err != nil {
			return err
		}
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		status := ins.conn.HealthCheck(checkCtx)
		checkErr := checkCtx.Err()
		cancel()
		if checkErr != nil {
			return checkErr
		}
		status.CheckedAt = time.Now().UTC()
		raw, err := json.Marshal(status)
		if err != nil {
			return fmt.Errorf("invalid connector health result: %w", err)
		}
		m.mu.Lock()
		current, ok := m.instances[instanceKey(ins.cfg)]
		if ok && current.generation == ins.generation {
			current.health = raw
		}
		m.mu.Unlock()
	}
	return ctx.Err()
}

// CloseAll 关闭所有连接器（用于优雅停机）
func (m *Manager) CloseAll() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	// No new initializer can Add after closed is set under the same mutex.
	// Wait without holding mu so pending batches can reject publication and clean up.
	m.initializing.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, inst := range m.instances {
		_ = inst.conn.Close()
		delete(m.instances, k)
	}
}

// RequireRestore guards the legacy all-tenant restore-and-poll lifecycle. It does
// not authorize candidate delivery targets or ordinary request provisioning.
func (m *Manager) RequireRestore(ctx context.Context) error {
	if m == nil || m.gate == nil {
		return executionscope.ErrDenied
	}
	return m.gate.RequireStartupCapability(ctx, "connector_poll")
}
