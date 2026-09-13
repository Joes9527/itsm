//go:build candidate_scope

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	_ "itsm-backend/ent/runtime"
	"itsm-backend/internal/bootstrap"
	"itsm-backend/migration"
)

// This child has its own globals, environment and configuration directory. It
// deliberately constructs the application without starting its runtime.
func TestCandidateConstructChild(t *testing.T) {
	if os.Getenv("CANDIDATE_CONSTRUCT_CHILD") != "1" {
		t.Skip("subprocess only")
	}
	app := bootstrap.NewApplication()
	require.NotNil(t, app.Router)
	time.Sleep(300 * time.Millisecond)
}

func TestCandidateConstructPreservesDatabaseAndStreams(t *testing.T) {
	socket, redisBinary := os.Getenv("CANDIDATE_SCOPE_TEST_SOCKET"), os.Getenv("CANDIDATE_TEST_REDIS_BINARY")
	minioBinary := os.Getenv("CANDIDATE_TEST_MINIO_BINARY")
	if socket == "" || redisBinary == "" || minioBinary == "" {
		t.Skip("requires private PostgreSQL marker and explicit Redis/MinIO binaries")
	}
	require.True(t, filepath.IsAbs(socket))
	require.True(t, filepath.IsAbs(redisBinary))
	require.True(t, filepath.IsAbs(minioBinary))
	marker, err := os.ReadFile(filepath.Join(socket, "candidate-test-instance"))
	require.NoError(t, err)
	require.Equal(t, "itsm-candidate-isolated-test\n", string(marker))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	id := "construct_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	dsn := func(db string) string {
		return fmt.Sprintf("host=%s port=25439 dbname=%s user=candidate_test_owner sslmode=disable", socket, db)
	}
	admin, err := sql.Open("postgres", dsn("postgres"))
	require.NoError(t, err)
	defer admin.Close()
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+id)
	require.NoError(t, err)
	runRole, systemRole := id+"_run", id+"_sys"
	allocatedRoles := []string{}
	defer func() {
		_, e := admin.Exec("DROP DATABASE " + id + " WITH (FORCE)")
		require.NoError(t, e)
		for _, role := range allocatedRoles {
			_, e = admin.Exec("DROP ROLE " + role)
			require.NoError(t, e)
		}
	}()
	_, err = admin.ExecContext(ctx, "CREATE ROLE "+runRole+" LOGIN")
	require.NoError(t, err)
	allocatedRoles = append(allocatedRoles, runRole)
	_, err = admin.ExecContext(ctx, "CREATE ROLE "+systemRole+" LOGIN BYPASSRLS NOINHERIT")
	require.NoError(t, err)
	allocatedRoles = append(allocatedRoles, systemRole)
	owner, err := sql.Open("postgres", dsn(id))
	require.NoError(t, err)
	defer owner.Close()
	client := ent.NewClient(ent.Driver(entsql.OpenDB("postgres", owner)))
	require.NoError(t, client.Schema.Create(ctx))
	tenant, err := client.Tenant.Create().SetName("History").SetCode("history").Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().SetUsername("history").SetName("History").SetEmail("history@example.invalid").SetPasswordHash("test-only-unusable").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.Ticket.Create().SetTitle("Protected history").SetTicketNumber("HISTORY-1").SetRequesterID(user.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	// Business DML is available so accidental constructor writes are observable;
	// the new execution-control tables remain read-only for the runtime role.
	_, err = owner.ExecContext(ctx, fmt.Sprintf("GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO %s; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO %s", runRole, runRole))
	require.NoError(t, err)
	_, err = owner.ExecContext(ctx, migration.GetMigrationSQL("039_candidate_execution_scope"))
	require.NoError(t, err)
	_, err = owner.ExecContext(ctx, migration.GetMigrationSQL(migration.ToolInvocationExecutionScopeVersion))
	require.NoError(t, err)
	_, err = owner.ExecContext(ctx, migration.GetMigrationSQL(migration.ToolExecutionAuthorityLockVersion))
	require.NoError(t, err)
	_, err = owner.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION public.lock_candidate_tool_authority(uuid,text,bigint,bigint) TO "+runRole)
	require.NoError(t, err)
	scope := uuid.NewString()
	_, err = owner.ExecContext(ctx, `INSERT INTO execution_scopes(id,deployment_id,tenant_id,status,created_by) VALUES($1,'construct-test',$2,'active',$3)`, scope, tenant.ID, user.ID)
	require.NoError(t, err)
	_, err = owner.ExecContext(ctx, `INSERT INTO execution_runtime_bindings VALUES($1,'construct-test','candidate')`, runRole)
	require.NoError(t, err)
	_, err = owner.ExecContext(ctx, fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s,%s;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s;
GRANT SELECT ON users,tenants,msp_allocations,process_callback_outboxes,external_identities,connector_configs TO %s;
GRANT SELECT,UPDATE ON outbox_events,ticket_notifications TO %s;
GRANT INSERT,SELECT(id) ON audit_logs TO %s;
GRANT USAGE ON SEQUENCE audit_logs_id_seq TO %s`, runRole, systemRole, runRole, systemRole, systemRole, systemRole, systemRole))
	require.NoError(t, err)
	// The loopback proxy has exactly one destination: this marked private socket.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				upstream, e := net.Dial("unix", filepath.Join(socket, ".s.PGSQL.25439"))
				if e != nil {
					return
				}
				defer upstream.Close()
				go func() { _, _ = io.Copy(upstream, c) }()
				_, _ = io.Copy(c, upstream)
			}()
		}
	}()
	redisDir := t.TempDir()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	redisPort := reservation.Addr().(*net.TCPAddr).Port
	require.NoError(t, reservation.Close())
	redisPassword := uuid.NewString()
	redisCommand := exec.CommandContext(ctx, redisBinary, "--requirepass", redisPassword, "--bind", "127.0.0.1", "--port", fmt.Sprint(redisPort), "--save", "", "--appendonly", "no", "--dir", redisDir)
	redisLog, err := os.Create(filepath.Join(redisDir, "redis.log"))
	require.NoError(t, err)
	defer redisLog.Close()
	redisCommand.Stdout = redisLog
	redisCommand.Stderr = redisLog
	require.NoError(t, redisCommand.Start())
	redisExited := make(chan struct{})
	go func() { _ = redisCommand.Wait(); close(redisExited) }()
	defer func() { _ = redisCommand.Process.Kill(); <-redisExited }()
	rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", redisPort), Password: redisPassword})
	defer rdb.Close()
	require.Eventually(t, func() bool {
		select {
		case <-redisExited:
			return false
		default:
		}
		info, err := rdb.Info(ctx, "server").Result()
		return err == nil && strings.Contains(info, fmt.Sprintf("process_id:%d\r\n", redisCommand.Process.Pid))
	}, 5*time.Second, 20*time.Millisecond, "must authenticate to this test's Redis PID before fixture writes")
	for _, topic := range []string{"sla.breached", "ai.triage.completed"} {
		require.NoError(t, rdb.XAdd(ctx, &redis.XAddArgs{Stream: topic, Values: map[string]interface{}{"history": "unchanged"}}).Err())
	}
	require.NoError(t, rdb.Set(ctx, "consumed-refresh-history", "protected", 0).Err())
	minioAddress, minioClient, minioAccess, minioSecret := startCandidateMinio(t, ctx, minioBinary)
	require.NoError(t, minioClient.MakeBucket(ctx, "protected-history", minio.MakeBucketOptions{}))
	_, err = minioClient.PutObject(ctx, "protected-history", "old-attachment.txt", strings.NewReader("protected fixture"), int64(len("protected fixture")), minio.PutObjectOptions{})
	require.NoError(t, err)
	beforeMinio := snapshotCandidateMinio(t, ctx, minioClient)
	beforeDB := snapshotCandidateTables(t, ctx, owner)
	beforeRedis := snapshotCandidateRedis(t, ctx, rdb)
	workDir := t.TempDir()
	cfg := fmt.Sprintf(`database:
  host: 127.0.0.1
  port: %d
  dbname: %s
  user: %s
  system_role_user: %s
  sslmode: disable
  schema: public
rls:
  mode: enforce
  tenant_var_name: app.current_tenant
execution:
  mode: candidate
  deployment_id: construct-test
  scopes:
    - tenant_id: %d
      scope_id: %s
  capabilities:
    event_audit: scoped
    webhook: scoped
    tool_queue: scoped
server:
  mode: release
jwt:
  secret: private-constructor-test-secret-not-for-use
log:
  level: error
  path: ./logs
redis:
  host: 127.0.0.1
  port: %d
  password: %s
`, listener.Addr().(*net.TCPAddr).Port, id, runRole, systemRole, tenant.ID, scope, redisPort, redisPassword)
	cfg += fmt.Sprintf("\nminio:\n  endpoint: %s\n  access_key: %s\n  secret_key: %s\n  bucket: protected-history\n  use_ssl: false\n", minioAddress, minioAccess, minioSecret)
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "config.yaml"), []byte(cfg), 0600))
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCandidateConstructChild$", "-test.v")
	child.Dir = workDir
	child.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + workDir, "TMPDIR=" + workDir, "ENV=test", "CANDIDATE_CONSTRUCT_CHILD=1"}
	output, err := child.CombinedOutput()
	require.NoError(t, err, "constructor subprocess: %s", output)
	require.Equal(t, beforeDB, snapshotCandidateTables(t, ctx, owner), "constructor changed table contents or inventory")
	require.Equal(t, beforeRedis, snapshotCandidateRedis(t, ctx, rdb), "constructor changed keys, streams or consumer groups")
	require.Equal(t, beforeMinio, snapshotCandidateMinio(t, ctx, minioClient), "constructor changed buckets or object contents")
	t.Log("full application construction preserved PostgreSQL fixture, real Redis and private MinIO; no runtime started")
}

func snapshotCandidateTables(t *testing.T, ctx context.Context, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`)
	require.NoError(t, err)
	var tables []string
	for rows.Next() {
		var table string
		require.NoError(t, rows.Scan(&table))
		tables = append(tables, table)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	result := map[string]string{}
	for _, table := range tables {
		var contents string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT to_jsonb(t) x FROM public.`+pq.QuoteIdentifier(table)+` t) q`).Scan(&contents))
		result[table] = contents
	}
	// Sequences and schema definitions must also remain unchanged during construction.
	for name, query := range map[string]string{
		"_schema_columns":     `SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY table_name,ordinal_position)::text,'[]') FROM information_schema.columns c WHERE table_schema='public'`,
		"_schema_indexes":     `SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY indexname)::text,'[]') FROM pg_indexes i WHERE schemaname='public'`,
		"_schema_extensions":  `SELECT COALESCE(jsonb_agg(to_jsonb(e) ORDER BY extname)::text,'[]') FROM pg_extension e`,
		"_sequences":          `SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY sequencename)::text,'[]') FROM pg_sequences s WHERE schemaname='public'`,
		"_constraints":        `SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY c.oid)::text,'[]') FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname='public'`,
		"_triggers":           `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.oid)::text,'[]') FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'`,
		"_policies":           `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY tablename,policyname)::text,'[]') FROM pg_policies p WHERE schemaname='public'`,
		"_table_permissions":  `SELECT COALESCE(jsonb_agg(jsonb_build_object('name',c.relname,'owner',c.relowner,'acl',c.relacl,'rls',c.relrowsecurity,'force',c.relforcerowsecurity) ORDER BY c.relname)::text,'[]') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'`,
		"_functions":          `SELECT COALESCE(jsonb_agg(jsonb_build_object('definition',pg_get_functiondef(p.oid),'acl',p.proacl,'owner',p.proowner) ORDER BY p.oid)::text,'[]') FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.prokind <> 'a'`,
		"_schema_permissions": `SELECT to_jsonb(n)::text FROM pg_namespace n WHERE nspname='public'`,
	} {
		var value string
		require.NoError(t, db.QueryRowContext(ctx, query).Scan(&value))
		result[name] = value
	}
	return result
}
func snapshotCandidateRedis(t *testing.T, ctx context.Context, db *redis.Client) map[string]string {
	t.Helper()
	keys, err := db.Keys(ctx, "*").Result()
	require.NoError(t, err)
	result := map[string]string{}
	for _, key := range keys {
		dump, err := db.Dump(ctx, key).Result()
		require.NoError(t, err)
		result[key] = dump
		typ, err := db.Type(ctx, key).Result()
		require.NoError(t, err)
		if typ == "stream" {
			groups, err := db.XInfoGroups(ctx, key).Result()
			require.NoError(t, err)
			b, err := json.Marshal(groups)
			require.NoError(t, err)
			result[key+":groups"] = string(b)
		}
	}
	return result
}

func startCandidateMinio(t *testing.T, ctx context.Context, binary string) (string, *minio.Client, string, string) {
	t.Helper()
	allocate := func() string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := l.Addr().String()
		require.NoError(t, l.Close())
		return addr
	}
	address, console := allocate(), allocate()
	directory := t.TempDir()
	log, err := os.Create(filepath.Join(directory, "minio.log"))
	require.NoError(t, err)
	command := exec.CommandContext(ctx, binary, "server", "--address", address, "--console-address", console, filepath.Join(directory, "data"))
	access, secret := strings.ReplaceAll(uuid.NewString(), "-", "")[:20], uuid.NewString()
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + directory, "MINIO_ROOT_USER=" + access, "MINIO_ROOT_PASSWORD=" + secret, "MINIO_BROWSER=off", "MINIO_UPDATE=off", "MINIO_CALLHOME_ENABLE=off"}
	command.Stdout = log
	command.Stderr = log
	require.NoError(t, command.Start())
	exited := make(chan struct{})
	go func() { _ = command.Wait(); close(exited) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-exited; _ = log.Close() })
	client, err := minio.New(address, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: false})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		select {
		case <-exited:
			return false
		default:
		}
		_, err := client.ListBuckets(ctx)
		return err == nil
	}, 15*time.Second, 50*time.Millisecond)
	return address, client, access, secret
}
func snapshotCandidateMinio(t *testing.T, ctx context.Context, client *minio.Client) map[string]string {
	t.Helper()
	buckets, err := client.ListBuckets(ctx)
	require.NoError(t, err)
	result := map[string]string{}
	for _, bucket := range buckets {
		result[bucket.Name] = bucket.CreationDate.String()
		for object := range client.ListObjects(ctx, bucket.Name, minio.ListObjectsOptions{Recursive: true}) {
			require.NoError(t, object.Err)
			reader, err := client.GetObject(ctx, bucket.Name, object.Key, minio.GetObjectOptions{})
			require.NoError(t, err)
			contents, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			metadata, err := json.Marshal(object)
			require.NoError(t, err)
			result[bucket.Name+"/"+object.Key] = string(metadata) + "\n" + string(contents)
		}
	}
	return result
}
