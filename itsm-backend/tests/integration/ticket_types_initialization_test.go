package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/ent"
	_ "itsm-backend/ent/runtime"
	"itsm-backend/ent/tickettype"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/pkg/seeder"
	"itsm-backend/service"
)

type ticketTypesFixture struct {
	ctx    context.Context
	client *ent.Client
	db     *sql.DB
	owner  *seeder.Seeder
	tenant *ent.Tenant
	actor  *ent.User
	dsn    string
}

func ticketTypesJSON(t *testing.T, value interface{}) string {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return string(payload)
}

func ticketTypesPostgres(t *testing.T) ticketTypesFixture {
	t.Helper()
	dsn := os.Getenv("TICKET_TYPES_TEST_DSN")
	if dsn == "" {
		t.Skip("TICKET_TYPES_TEST_DSN is required for isolated PostgreSQL integration")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "/gb_ticket_types_test", u.Path, "fixture may only use its dedicated test database")
	ctx := context.Background()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "ttinit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		require.NoError(t, e)
		require.NoError(t, admin.Close())
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB("postgres", db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, client.Schema.Create(ctx))
	tenant := client.Tenant.Create().SetName("Initialization test").SetCode("test").SetStatus("active").SaveX(ctx)
	actor := client.User.Create().SetUsername("admin-fixture").SetEmail("admin@example.invalid").SetName("Fixture").SetPasswordHash("unused").SetTenantID(tenant.ID).SetRole("sysadmin").SetActive(true).SaveX(ctx)
	r := client.Role.Create().SetTenantID(tenant.ID).SetCode("sysadmin").SetName("Administrator").SetIsActive(true).SaveX(ctx)
	p := client.Permission.Create().SetTenantID(tenant.ID).SetCode("system_config:update").SetName("Configure product").SetResource("system_config").SetAction("update").SaveX(ctx)
	client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(r.ID).SetPermissionID(p.ID).SaveX(ctx)
	actor = client.User.GetX(ctx, actor.ID)
	tenant = client.Tenant.GetX(ctx, tenant.ID)
	return ticketTypesFixture{tenantctx.WithTenantID(ctx, tenant.ID), client, db, seeder.NewSeeder(client, zap.NewNop().Sugar(), &config.Config{}), tenant, actor, u.String()}
}

func TestTicketTypesInitializationPlanApplyAndCreation(t *testing.T) {
	f := ticketTypesPostgres(t)
	prepare := func(input *creation.GenericInput) error {
		tx, err := f.client.Tx(f.ctx)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = (&service.TicketService{}).Prepare(f.ctx, tx, creation.ResolvedIntake{
			Identity:    creation.Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, RequesterID: f.actor.ID, Role: f.actor.Role, Channel: "api"},
			RecordClass: creation.RecordClassGeneric,
			Command:     creation.CreateWorkItemCommand{Title: "Configured subtype", Generic: input},
		})
		return err
	}
	require.Error(t, prepare(&creation.GenericInput{Type: "general"}))
	plan, err := f.owner.InitializeTicketTypes(f.ctx, f.tenant.ID, f.actor.ID, false)
	require.NoError(t, err)
	require.False(t, plan.Applied)
	require.Len(t, plan.CreateCodes, 12)
	require.Zero(t, f.client.TicketType.Query().CountX(f.ctx))
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
	applied, err := f.owner.InitializeTicketTypes(f.ctx, f.tenant.ID, f.actor.ID, true)
	require.NoError(t, err)
	require.True(t, applied.Applied)
	require.Equal(t, plan.ManifestSHA256, applied.ManifestSHA256)
	require.Equal(t, 12, f.client.TicketType.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.AuditLog.Query().CountX(f.ctx))
	require.Zero(t, f.client.TicketCategory.Query().CountX(f.ctx))
	require.Zero(t, f.client.SLADefinition.Query().CountX(f.ctx))
	require.Zero(t, f.client.ProcessDefinition.Query().CountX(f.ctx))
	require.Zero(t, f.client.ServiceCatalog.Query().CountX(f.ctx))
	require.JSONEq(t, ticketTypesJSON(t, f.actor), ticketTypesJSON(t, f.client.User.GetX(f.ctx, f.actor.ID)), "identity remains unchanged")
	require.JSONEq(t, ticketTypesJSON(t, f.tenant), ticketTypesJSON(t, f.client.Tenant.GetX(f.ctx, f.tenant.ID)))
	require.NoError(t, prepare(&creation.GenericInput{Type: "general"}))
	general := f.client.TicketType.Query().Where(tickettype.CodeEQ("general")).OnlyX(f.ctx)
	require.NoError(t, prepare(&creation.GenericInput{TypeID: strconv.Itoa(general.ID)}))
	require.Error(t, prepare(&creation.GenericInput{TypeID: strconv.Itoa(general.ID), Type: "k8s_scale"}))
	replayed, err := f.owner.InitializeTicketTypes(f.ctx, f.tenant.ID, f.actor.ID, true)
	require.NoError(t, err)
	require.Empty(t, replayed.CreateCodes)
	require.Len(t, replayed.PreservedCodes, 12)
	require.Equal(t, 12, f.client.TicketType.Query().CountX(f.ctx))
}

func TestTicketTypesInitializationPartialAndConflicts(t *testing.T) {
	f := ticketTypesPostgres(t)
	_, err := f.owner.InitializeTicketTypes(f.ctx, f.tenant.ID, f.actor.ID, true)
	require.NoError(t, err)
	general := f.client.TicketType.Query().Where(tickettype.CodeEQ("general")).OnlyX(f.ctx)
	require.NoError(t, f.client.TicketType.DeleteOneID(general.ID).Exec(f.ctx))
	custom := f.client.TicketType.Create().SetCode("custom").SetName("Custom").SetDescription("keep").SetIcon("FileText").SetColor("#000000").SetTenantID(int64(f.tenant.ID)).SetCreatedBy(int64(f.actor.ID)).SetCreatedAt(time.Now()).SetUpdatedAt(time.Now()).SetAssignmentRules([]interface{}{}).SetNotificationConfig(map[string]interface{}{}).SetPermissionConfig(map[string]interface{}{}).SaveX(f.ctx)
	custom = f.client.TicketType.GetX(f.ctx, custom.ID)
	result, err := f.owner.InitializeTicketTypes(f.ctx, f.tenant.ID, f.actor.ID, true)
	require.NoError(t, err)
	require.Equal(t, []string{"general"}, result.CreateCodes)
	require.JSONEq(t, ticketTypesJSON(t, custom), ticketTypesJSON(t, f.client.TicketType.GetX(f.ctx, custom.ID)))
	_, err = f.client.TicketType.Delete().Where(tickettype.CodeEQ("general")).Exec(f.ctx)
	require.NoError(t, err)
	_, err = f.client.TicketType.Update().Where(tickettype.CodeEQ("k8s_scale")).SetName("Customized value").Save(f.ctx)
	require.NoError(t, err)
	audits := f.client.AuditLog.Query().CountX(f.ctx)
	_, err = f.owner.InitializeTicketTypes(f.ctx, f.tenant.ID, f.actor.ID, true)
	require.ErrorContains(t, err, "conflict")
	require.False(t, f.client.TicketType.Query().Where(tickettype.CodeEQ("general")).ExistX(f.ctx))
	require.Equal(t, audits, f.client.AuditLog.Query().CountX(f.ctx))
}

func TestTicketTypesInitializationAuthorization(t *testing.T) {
	for _, scenario := range []string{"inactive actor", "inactive tenant", "expired tenant", "missing tenant", "nonadmin", "inactive role", "permission revoked", "wrong tenant", "effective role restricted"} {
		t.Run(scenario, func(t *testing.T) {
			f := ticketTypesPostgres(t)
			tenantID := f.tenant.ID
			switch scenario {
			case "inactive actor":
				f.client.User.UpdateOneID(f.actor.ID).SetActive(false).ExecX(f.ctx)
			case "inactive tenant":
				f.client.Tenant.UpdateOneID(tenantID).SetStatus("inactive").ExecX(f.ctx)
			case "expired tenant":
				f.client.Tenant.UpdateOneID(tenantID).SetExpiresAt(time.Now().Add(-time.Hour)).ExecX(f.ctx)
			case "missing tenant":
				tenantID += 100
			case "nonadmin":
				f.client.User.UpdateOneID(f.actor.ID).SetRole("end_user").ExecX(f.ctx)
			case "effective role restricted":
				f.client.User.UpdateOneID(f.actor.ID).SetRole("super_admin").SetMspRole("customer_user").ExecX(f.ctx)
			case "permission revoked":
				f.client.RolePermission.Delete().ExecX(f.ctx)
			case "inactive role":
				f.client.Role.Update().SetIsActive(false).ExecX(f.ctx)
			case "wrong tenant":
				tenantID = f.client.Tenant.Create().SetName("Other").SetCode("other").SaveX(f.ctx).ID
			}
			_, err := f.owner.InitializeTicketTypes(tenantctx.WithTenantID(context.Background(), tenantID), tenantID, f.actor.ID, true)
			require.Error(t, err)
			require.Zero(t, f.client.TicketType.Query().CountX(f.ctx))
			require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
		})
	}
}

func TestTicketTypesInitializationAuditFailureRollsBack(t *testing.T) {
	f := ticketTypesPostgres(t)
	_, err := f.db.ExecContext(f.ctx, `ALTER TABLE audit_logs ADD CONSTRAINT reject_test_audit CHECK (action <> 'ticket_types.initialize')`)
	require.NoError(t, err)
	_, err = f.owner.InitializeTicketTypes(f.ctx, f.tenant.ID, f.actor.ID, true)
	require.Error(t, err)
	require.Zero(t, f.client.TicketType.Query().CountX(f.ctx), "all default inserts must roll back when audit cannot commit")
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
}

func TestTicketTypesInitializationInsertFailureRollsBack(t *testing.T) {
	f := ticketTypesPostgres(t)
	_, err := f.db.ExecContext(f.ctx, fmt.Sprintf(`ALTER TABLE ticket_types ADD CONSTRAINT reject_test_type CHECK (code <> '%s')`, "general"))
	require.NoError(t, err)
	_, err = f.owner.InitializeTicketTypes(f.ctx, f.tenant.ID, f.actor.ID, true)
	require.Error(t, err)
	require.Zero(t, f.client.TicketType.Query().CountX(f.ctx))
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
}

func TestTicketTypesInitializationRealCLIPlanAndApply(t *testing.T) {
	f := ticketTypesPostgres(t)
	module, err := filepath.Abs("../..")
	require.NoError(t, err)
	dir := t.TempDir()
	binary := filepath.Join(dir, "initialize_ticket_types")
	build := exec.Command("go", "build", "-o", binary, "./cmd/initialize_ticket_types")
	build.Dir = module
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	u, err := url.Parse(f.dsn)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	password, _ := u.User.Password()
	payload, err := json.Marshal(map[string]interface{}{"database": map[string]interface{}{
		"host": u.Hostname(), "port": port, "user": u.User.Username(), "dbname": "gb_ticket_types_test",
		"sslmode": "disable", "schema": u.Query().Get("search_path"),
	}, "deployment": map[string]interface{}{"mode": "private", "auto_migrate": false, "auto_seed": false}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), payload, 0o600))
	run := func(apply bool) seeder.TicketTypeInitializationResult {
		args := []string{"--tenant-id", strconv.Itoa(f.tenant.ID), "--actor-id", strconv.Itoa(f.actor.ID)}
		if apply {
			args = append(args, "--apply")
		}
		cmd := exec.Command(binary, args...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "DB_PASSWORD=" + password, "RLS_MODE=off"}
		out, err := cmd.Output()
		require.NoError(t, err)
		var response struct {
			Database string                                `json:"database"`
			Result   seeder.TicketTypeInitializationResult `json:"result"`
		}
		require.NoError(t, json.Unmarshal(out, &response))
		require.Equal(t, "gb_ticket_types_test", response.Database)
		return response.Result
	}
	require.False(t, run(false).Applied)
	require.Zero(t, f.client.TicketType.Query().CountX(f.ctx))
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
	require.True(t, run(true).Applied)
	require.Equal(t, 12, f.client.TicketType.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.AuditLog.Query().CountX(f.ctx))
}
