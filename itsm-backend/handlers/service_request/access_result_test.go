package service_request

import (
	"github.com/stretchr/testify/require"
	"itsm-backend/handlers/common/accessgrant"
	"math"
	"strings"
	"testing"
	"time"
)

func TestComputeAccessExpiryUsesVerificationTime(t *testing.T) {
	verified := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	expiry, err := ComputeAccessExpiry(verified, 30*24*60*60)
	require.NoError(t, err)
	require.Equal(t, verified.Add(30*24*time.Hour), expiry)
	for _, seconds := range []int64{0, -1, math.MaxInt64/int64(time.Second) + 1} {
		_, err = ComputeAccessExpiry(verified, seconds)
		require.Error(t, err)
	}
	_, err = ComputeAccessExpiry(time.Time{}, 3600)
	require.Error(t, err)
	expiry, err = ComputeAccessExpiry(verified.In(time.FixedZone("local", 8*3600)), 3600)
	require.NoError(t, err)
	require.Equal(t, time.UTC, expiry.Location())
}

func TestAccessResultStrictApprovedTargetAndBaseline(t *testing.T) {
	snapshot := accessgrant.ApprovalSnapshot{PolicyID: 1, PolicyVersion: 1, Provider: accessgrant.Graph, ExternalSystem: "directory", SubjectID: "subject", GroupID: "group", DurationKey: "month", DurationSeconds: 30 * 86400}
	raw := `{"outcome":"granted","provider":"graph","subjectId":"subject","groupId":"group","baseline":"not_member","verifiedAt":"2026-09-05T08:00:00Z","evidenceRef":"evidence"}`
	result, expiry, err := ValidateAccessResult([]byte(raw), snapshot)
	require.NoError(t, err)
	require.Equal(t, "granted", result.Outcome)
	require.Equal(t, result.VerifiedAt.Add(30*24*time.Hour), *expiry)
	for _, replacement := range []struct{ old, new string }{{`"granted"`, `"unknown"`}, {`"graph"`, `"ldap"`}, {`"subject"`, `"other"`}, {`"group"`, `"other"`}, {`"not_member"`, `"member"`}, {`"evidence"`, `""`}, {`"2026-09-05T08:00:00Z"`, `"0001-01-01T00:00:00Z"`}, {`"evidenceRef":"evidence"`, `"evidenceRef":"evidence","expiresAt":"2030-01-01T00:00:00Z"`}} {
		_, _, err = ValidateAccessResult([]byte(strings.Replace(raw, replacement.old, replacement.new, 1)), snapshot)
		require.Error(t, err)
	}
	present := strings.Replace(strings.Replace(raw, `"granted"`, `"already_present"`, 1), `"not_member"`, `"member"`, 1)
	result, expiry, err = ValidateAccessResult([]byte(present), snapshot)
	require.NoError(t, err)
	require.Nil(t, expiry)
	require.False(t, result.VerifiedAt.IsZero())
	_, _, err = ValidateAccessResult([]byte(raw+raw), snapshot)
	require.Error(t, err)
}
