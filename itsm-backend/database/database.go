package database

// database包：负责数据库连接和配置
// 使用Ent ORM框架与PostgreSQL数据库交互

import (
	"context"      // Go标准库，用于处理上下文（超时、取消等）
	"database/sql" // Go标准库，提供数据库操作的通用接口
	"fmt"          // Go标准库，用于格式化字符串
	"time"         // Go标准库，用于时间处理

	"itsm-backend/config"       // 自定义配置包
	"itsm-backend/database/rls" // RLS driver 装饰器
	"itsm-backend/ent"          // Ent ORM生成的代码包

	entsql "entgo.io/ent/dialect/sql" // Ent ORM的SQL方言包
	_ "github.com/lib/pq"             // PostgreSQL驱动，下划线表示只导入init函数
	"go.uber.org/zap"
)

var rawDB *sql.DB

// rlsDriver 保存最后一次初始化的 RLS 装饰器实例，供 /internal/rls-stats
// 等诊断端点读取运行时统计。为 nil 表示未启用 RLS 或未初始化。
var rlsDriver *rls.Driver

// GetRawDB returns the underlying *sql.DB for raw SQL operations (e.g., pgvector)
func GetRawDB() *sql.DB { return rawDB }

// SetRawDBForTest 仅供测试使用：直接注入一个已建立连接的 *sql.DB，绕过
// InitDatabase/InitDatabaseWithRLS 的完整初始化流程（连接池配置、RLS 装饰器、
// pgvector 兼容处理等）。生产代码路径不应调用此函数。
//
// 用途：service 包内直接调用 database.GetRawDB() 的函数（例如
// CloseChangeApprovalChains）需要在集成测试中注入真实 Postgres 连接才能验证
// 原始 SQL 行为；调用方应在测试结束时用 t.Cleanup 恢复之前的值。
func SetRawDBForTest(db *sql.DB) { rawDB = db }

// GetRLSDriver 返回当前进程使用的 RLS 装饰器（可能为 nil）。
// 用于运维/诊断端点导出 Stats()。
func GetRLSDriver() *rls.Driver { return rlsDriver }

// InitDB initializes a raw database connection without Ent-specific setup
// Used for migrations and other operations that don't need Ent ORM
func InitDB(cfg *config.DatabaseConfig) (*sql.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s password=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.DBName, cfg.SSLMode, cfg.Password)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed opening connection to postgres: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// InitDatabase 初始化数据库连接
// 参数：cfg - 数据库配置信息（主机、端口、用户名、密码等）
// 返回值：*ent.Client - Ent ORM客户端，用于数据库操作
//
//	error - 错误信息
func InitDatabase(cfg *config.DatabaseConfig) (*ent.Client, error) {
	db, err := InitDB(cfg)
	if err != nil {
		return nil, err
	}
	drv := entsql.OpenDB("postgres", db)
	rawDB = db
	client := ent.NewClient(ent.Driver(drv))
	RegisterSoftDeleteInterceptors(client)
	return client, nil
}

// PrepareBootstrapInfrastructure installs pre-schema infrastructure through
// the canonical bootstrap only. Each DDL failure is returned to the caller.
func PrepareBootstrapInfrastructure(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("bootstrap database is required")
	}
	statements := []struct{ name, sql string }{
		{"pgvector extension", `CREATE EXTENSION IF NOT EXISTS vector`},
		{"vectors table", `CREATE TABLE IF NOT EXISTS vectors (id BIGSERIAL PRIMARY KEY, tenant_id INT NOT NULL, object_type TEXT NOT NULL, object_id INT NOT NULL, embedding VECTOR(1536) NOT NULL, content TEXT, source TEXT, created_at TIMESTAMPTZ DEFAULT NOW())`},
		{"vectors unique index", `CREATE UNIQUE INDEX IF NOT EXISTS vectors_unique_tenant_obj ON vectors(tenant_id, object_type, object_id)`},
		{"vectors embedding index", `CREATE INDEX IF NOT EXISTS vectors_embedding_idx ON vectors USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)`},
		{"ai feedbacks table", `CREATE TABLE IF NOT EXISTS ai_feedbacks (id BIGSERIAL PRIMARY KEY, created_at TIMESTAMPTZ DEFAULT NOW(), tenant_id INT NOT NULL, user_id INT NOT NULL, request_id TEXT NOT NULL, kind TEXT NOT NULL, query TEXT, item_type TEXT, item_id INT, useful BOOLEAN NOT NULL, score INT, notes TEXT)`},
		{"ai feedback tenant index", `CREATE INDEX IF NOT EXISTS ai_feedbacks_tenant_idx ON ai_feedbacks(tenant_id)`},
		{"ai feedback created index", `CREATE INDEX IF NOT EXISTS ai_feedbacks_created_idx ON ai_feedbacks(created_at)`},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.sql); err != nil {
			return fmt.Errorf("prepare %s: %w", statement.name, err)
		}
	}
	return nil
}

// InitDatabaseWithRLS 与 InitDatabase 行为完全一致，但在返回 Ent Client 之前
// 用 RLS 装饰器包裹 SQL Driver。
//
// 三档行为（由 rlsCfg.Mode 决定）：
//   - off      (默认)：装饰器为 no-op，零开销、零风险，行为等同 InitDatabase
//   - shadow   ：审计每次查询，warn 缺少 tenant 的调用点；不改变 SQL 语义
//   - enforce  ：审计计数 + 依赖 middleware / AcquireConn 在 conn 上 SET 变量
//
// 说明：本函数**不**主动执行 SET LOCAL/SESSION。变量注入由 middleware 与
// 显式 rls.AcquireConn 负责，装饰器只是插入观测点。这么设计的原因：
//  1. 一次 request 可能触发多次 Ent 查询，共享同一 *sql.Conn。若在装饰器
//     内每查询前后 SET+RESET，连接来回换值成本高且易出错。
//  2. SET LOCAL 只在事务内生效；Ent 大多数查询是 autocommit，事务边界由
//     业务逻辑控制，装饰器无法感知。
//  3. 装饰器保持无副作用，可以在 R2A 阶段以 shadow 模式安全上线。
func InitDatabaseWithRLS(cfg *config.DatabaseConfig, rlsCfg *config.RLSConfig, logger *zap.SugaredLogger) (*ent.Client, error) {
	// 复用 InitDatabase 连接与 Ent client 初始化。
	client, err := InitDatabase(cfg)
	if err != nil {
		return nil, err
	}
	if rlsCfg == nil || rlsCfg.Mode == "" || rlsCfg.Mode == string(rls.ModeOff) {
		if logger != nil {
			logger.Infow("rls: driver mode=off (default, pass-through)")
		}
		rlsDriver = nil
		return client, nil
	}

	// 装饰：把底层 entsql driver 换成 RLS 装饰器
	innerDrv := entsql.OpenDB("postgres", rawDB)
	deco := rls.From(innerDrv, rlsCfg.Mode, logger)
	rlsDriver = deco
	if logger != nil {
		logger.Infow(
			"rls: driver installed",
			"mode", string(deco.Mode()),
			"tenant_var", rlsCfg.TenantVarName,
		)
	}
	rlsClient := ent.NewClient(ent.Driver(deco))
	RegisterSoftDeleteInterceptors(rlsClient)
	return rlsClient, nil
}

// 数据库连接池说明：
// - MaxOpenConns: 控制最大并发连接数，避免数据库过载
// - MaxIdleConns: 保持一定数量的空闲连接，减少连接建立的开销
// - ConnMaxLifetime: 定期关闭旧连接，避免连接泄漏
//
// PostgreSQL特点：
// - ACID事务：原子性、一致性、隔离性、持久性
// - 复杂查询：支持子查询、窗口函数、JSON操作等
// - 扩展性强：支持自定义函数、数据类型、索引等
// - 高性能：MVCC并发控制、WAL日志、查询优化器等
//
// Ent ORM特点：
// - 类型安全：编译时检查SQL查询
// - 代码生成：根据schema自动生成CRUD代码
// - 图查询：支持复杂的关联查询
// - 迁移管理：自动处理数据库schema变更
