package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

func newParticipationTestClient(t *testing.T) (*ent.Client, int, int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:participation_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("part-1").SetDomain("part-1.example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	u, err := client.User.Create().
		SetUsername("candidate1").SetEmail("candidate1@example.com").SetName("Candidate One").
		SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	return client, tenant.ID, u.ID
}

func TestResolveCallerIdentity_PopulatesIDUsernameEmail(t *testing.T) {
	client, tenantID, userID := newParticipationTestClient(t)
	identity, err := ResolveCallerIdentity(context.Background(), client, NewGroupResolver(client), tenantID, userID)
	require.NoError(t, err)
	assert.Equal(t, "candidate1", identity.Username)
	assert.Equal(t, "candidate1@example.com", identity.Email)
	assert.NotEmpty(t, identity.IDStr)
}

func TestResolveCallerIdentity_UnknownUserErrors(t *testing.T) {
	client, tenantID, _ := newParticipationTestClient(t)
	_, err := ResolveCallerIdentity(context.Background(), client, NewGroupResolver(client), tenantID, 999999)
	assert.Error(t, err)
}

func TestIsTaskParticipant_MatchesAssigneeByIDUsernameOrEmail(t *testing.T) {
	identity := &CallerIdentity{IDStr: "42", Username: "candidate1", Email: "candidate1@example.com"}

	byID := &ent.ProcessTask{Assignee: "42"}
	assert.True(t, identity.IsTaskParticipant(byID))

	byUsername := &ent.ProcessTask{Assignee: "candidate1"}
	assert.True(t, identity.IsTaskParticipant(byUsername))

	byEmail := &ent.ProcessTask{Assignee: "candidate1@example.com"}
	assert.True(t, identity.IsTaskParticipant(byEmail))

	noMatch := &ent.ProcessTask{Assignee: "someone-else"}
	assert.False(t, identity.IsTaskParticipant(noMatch))
}

func TestIsTaskParticipant_MatchesCandidateUsersCSV(t *testing.T) {
	identity := &CallerIdentity{IDStr: "42", Username: "candidate1"}
	task := &ent.ProcessTask{CandidateUsers: "7, candidate1, 99"}
	assert.True(t, identity.IsTaskParticipant(task))
}

func TestIsTaskParticipant_MatchesCandidateGroupsByExactToken(t *testing.T) {
	identity := &CallerIdentity{IDStr: "42", GroupsCSV: "network_eng,dept_manager"}

	matches := &ent.ProcessTask{CandidateGroups: "network_eng"}
	assert.True(t, identity.IsTaskParticipant(matches))

	// A caller in "eng" must NOT match a task requiring "network_eng" — exact
	// token comparison, not substring, unlike the caller's group list being a
	// raw CSV.
	noPartialMatch := &ent.ProcessTask{CandidateGroups: "network_eng"}
	otherIdentity := &CallerIdentity{GroupsCSV: "eng"}
	assert.False(t, otherIdentity.IsTaskParticipant(noPartialMatch))

	noGroups := &ent.ProcessTask{CandidateGroups: ""}
	assert.False(t, identity.IsTaskParticipant(noGroups))
}

func TestIsTaskParticipant_EmptyTaskFieldsNeverMatch(t *testing.T) {
	identity := &CallerIdentity{IDStr: "42", Username: "candidate1", Email: "candidate1@example.com"}
	task := &ent.ProcessTask{}
	assert.False(t, identity.IsTaskParticipant(task))
}
