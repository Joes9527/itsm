package seeder

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/workitemidentity"
	"itsm-backend/pkg/tenantmode"
)

func TestLoadedSeedUsesCanonicalWorkItemIdentity(t *testing.T) {
	t.Setenv("ITSM_SEED_CONFIG", "")
	t.Chdir("../..")
	cfg := loadSeedConfig(zap.NewNop().Sugar())
	for _, catalog := range cfg.ServiceCatalog {
		require.True(t, workitemidentity.IsRecordClass(catalog.TargetClass), "loaded catalog %s target %q", catalog.Name, catalog.TargetClass)
	}
	found := map[string]bool{}
	for _, binding := range cfg.ProcessBindings {
		require.True(t, workitemidentity.IsKnownProcessIdentity(binding.BusinessType), "loaded default identity %s", binding.BusinessType)
		found[binding.BusinessType] = true
	}
	for _, class := range []string{"generic", "incident", "problem", "change_request", "service_request_item", "release"} {
		require.True(t, found[class], class)
	}
}

func TestSeedRejectsRetiredWorkItemBindingBeforeWrites(t *testing.T) {
	for _, class := range []string{"ticket", "change", "service_request", "unknown"} {
		t.Run(class, func(t *testing.T) {
			s, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
			s.seedDefaultTenant(ctx)
			s.config.ProcessBindings = []ProcessBindingSeed{{BusinessType: class, ProcessDefinitionKey: "legacy", IsDefault: true}}
			require.Error(t, s.seedProcessBindings(ctx))
			require.Zero(t, s.client.ProcessBinding.Query().CountX(ctx))
		})
	}
}

func TestLoadedSeedPersistsCanonicalWorkItemBindings(t *testing.T) {
	t.Setenv("ITSM_SEED_CONFIG", "")
	t.Chdir("../..")
	s, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
	s.seedDefaultTenant(ctx)
	s.seedBPMNWorkflows(ctx)
	require.NoError(t, s.seedProcessBindings(ctx))
	bindings := s.client.ProcessBinding.Query().AllX(ctx)
	require.Len(t, bindings, len(s.config.ProcessBindings))
	for _, binding := range bindings {
		require.True(t, workitemidentity.IsKnownProcessIdentity(binding.BusinessType))
		require.Positive(t, binding.ProcessVersion)
	}
}

func TestProductionInitializersRejectRetiredWorkItemBinding(t *testing.T) {
	s, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
	s.config.ProcessBindings = []ProcessBindingSeed{{BusinessType: "ticket", ProcessDefinitionKey: "old"}}
	_, err := ProductionInitializers(s)
	require.Error(t, err)
	require.Error(t, s.SeedAll(ctx))
	require.Zero(t, s.client.Tenant.Query().CountX(ctx))
	require.Zero(t, s.client.ProcessBinding.Query().CountX(ctx))
}
