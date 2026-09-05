//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
)

// A real unique-index race, including a completed process, is distinct from an
// Application spy: all contenders execute the actual BPMN transaction.
func TestPostgresWorkflowStartConcurrentReplay(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	require.NotEmpty(t, dsn, "explicit disposable INTAKE_POSTGRES_TEST_DSN is required")
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "/sslvpn_test", parsed.Path, "this fixture is reserved for the isolated SSLVPN database")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	schema := fmt.Sprintf("a4_workflow_start_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() {
		_, err := db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	}()
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	client, err := ent.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.Schema.Create(ctx))
	tenant := client.Tenant.Create().SetName("workflow test").SetCode("workflow-test").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("actor").SetName("actor").SetEmail("actor@example.test").SetRole("super_admin").SetPasswordHash("test").SaveX(ctx)
	deployment := client.ProcessDeployment.Create().SetDeploymentID("test").SetDeploymentName("test").SetTenantID(tenant.ID).SaveX(ctx)
	definition := client.ProcessDefinition.Create().SetTenantID(tenant.ID).SetDeploymentID(deployment.ID).SetKey("start").SetName("start").SetIsActive(true).SetIsLatest(false).SetBpmnXML([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="p" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="flow" sourceRef="start" targetRef="end"/></bpmn:process></bpmn:definitions>`)).SaveX(ctx)
	ctx = service.WithTrustedBPMNTenantContext(ctx, tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actor.ID)
	engine := service.NewCustomProcessEngine(client, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
	type outcome struct {
		item *ent.ProcessInstance
		err  error
	}
	outcomes := make(chan outcome, 16)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(16)
	for range 16 {
		go func() {
			ready.Done()
			<-start
			item, err := engine.StartProcessByDefinitionID(ctx, service.FreezeProcessDefinition(definition), "ticket:91", "ticket", 91, nil, "workflow-start:91:1")
			outcomes <- outcome{item, err}
		}()
	}
	ready.Wait()
	close(start)
	first := 0
	for range 16 {
		out := <-outcomes
		require.NoError(t, out.err)
		require.NotNil(t, out.item)
		if first == 0 {
			first = out.item.ID
		}
		require.Equal(t, first, out.item.ID)
	}
	require.Equal(t, 1, client.ProcessInstance.Query().CountX(ctx))
	require.Equal(t, 1, client.ProcessAuditLog.Query().CountX(ctx))
	// Restarted engine sees the committed identity after acknowledgement loss.
	replay, err := service.NewCustomProcessEngine(client, zap.NewNop().Sugar()).(*service.CustomProcessEngine).StartProcessByDefinitionID(ctx, service.FreezeProcessDefinition(definition), "ticket:91", "ticket", 91, nil, "workflow-start:91:1")
	require.NoError(t, err)
	require.Equal(t, first, replay.ID)
}
