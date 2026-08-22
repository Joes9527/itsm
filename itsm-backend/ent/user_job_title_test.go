package ent_test

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestUser_JobTitle_PersistsAndDefaultsEmpty(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:user_job_title_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	tenant, err := client.Tenant.Create().
		SetName("JobTitle Tenant").
		SetCode("job-title").
		SetDomain("job-title.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	withoutTitle, err := client.User.Create().
		SetUsername("no_title_user").
		SetEmail("no-title@job-title.test").
		SetName("No Title").
		SetPasswordHash("hash").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	require.Empty(t, withoutTitle.JobTitle)

	withTitle, err := client.User.Create().
		SetUsername("gm_user").
		SetEmail("gm@job-title.test").
		SetName("GM User").
		SetPasswordHash("hash").
		SetTenantID(tenant.ID).
		SetJobTitle("财务管理总经理").
		Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "财务管理总经理", withTitle.JobTitle)
}
