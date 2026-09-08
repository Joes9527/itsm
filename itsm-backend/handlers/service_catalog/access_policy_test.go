package service_catalog

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/common/accessgrant"
	"itsm-backend/service"
	"math"
	"testing"
	"time"
)

func accessPolicyFixture() (*accessgrant.Policy, []service.FieldDefinitionInput) {
	return &accessgrant.Policy{Provider: accessgrant.Graph, ExternalSystem: "directory-a", GroupID: "group-a", DurationField: "requested_duration", DurationOptions: []accessgrant.DurationOption{{Key: "month", Label: "一个月", Seconds: 30 * 86400}}}, []service.FieldDefinitionInput{{Name: "requested_duration", Label: "申请有效期", FieldType: "select", Required: true, Options: []any{map[string]any{"value": "month", "label": "一个月"}}}}
}
func TestAccessPolicyValidatesFiniteNamedOptions(t *testing.T) {
	p, fields := accessPolicyFixture()
	require.NoError(t, ValidateAccessPolicy(p, fields))
	for _, kind := range []string{"provider", "system", "group", "field", "optional", "type", "missing", "zero", "infinite", "duplicate", "label", "overflow"} {
		t.Run(kind, func(t *testing.T) {
			p, f := accessPolicyFixture()
			switch kind {
			case "provider":
				p.Provider = "ldap"
			case "system":
				p.ExternalSystem = ""
			case "group":
				p.GroupID = ""
			case "field":
				p.DurationField = "other"
			case "optional":
				f[0].Required = false
			case "type":
				f[0].FieldType = "text"
			case "missing":
				p.DurationOptions = nil
			case "zero":
				p.DurationOptions[0].Seconds = 0
			case "infinite":
				p.DurationOptions[0].Seconds = -1
			case "duplicate":
				p.DurationOptions = append(p.DurationOptions, p.DurationOptions[0])
			case "label":
				p.DurationOptions[0].Label = "different"
			case "overflow":
				p.DurationOptions[0].Seconds = math.MaxInt64/int64(time.Second) + 1
			}
			require.Error(t, ValidateAccessPolicy(p, f))
		})
	}
}

func TestAccessPolicyPublicationAndRevision(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	owner := newCatalogPublisher(NewEntRepository(client), client, zap.NewNop().Sugar(), nil)
	p, fields := accessPolicyFixture()
	input := dto.CreateServiceCatalogRequest{Name: "Access", Category: "IT", TargetClass: "service_request_item", AccessPolicy: p, Fields: catalogTestFields(fields)}
	draft, err := owner.Create(ctx, 1, input)
	require.NoError(t, err)
	require.NotNil(t, draft.AccessPolicy)
	_, err = owner.Update(ctx, 1, draft.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: draft.CatalogVersion, Status: scPtr("enabled")})
	require.Error(t, err, "access policy without declared grant workflow cannot publish")
	p.GroupID = "different-target"
	changed, err := owner.Update(ctx, 1, draft.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: draft.CatalogVersion, AccessPolicy: p})
	require.NoError(t, err)
	require.NotEqual(t, draft.CatalogVersion, changed.CatalogVersion)
	require.Equal(t, 2, changed.AccessPolicy.Version)
	_, err = owner.Update(ctx, 1, draft.ID, dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: draft.CatalogVersion, AccessPolicy: p})
	require.Error(t, err)
	foreign, err := ReadAccessPolicy(ctx, client, 2, draft.ID)
	require.NoError(t, err)
	require.Nil(t, foreign)
}
