package intake

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func validIncidentCommand(key string, ciIDs []int) CreateWorkItemCommand {
	return CreateWorkItemCommand{
		IdempotencyKey: key,
		IntakeKind:     "incident",
		Title:          "Database unavailable",
		Description:    "Production database is not responding",
		CIIDs:          ciIDs,
		FormValues: map[string]any{
			"nested": map[string]any{"z": "last", "a": "first"},
			"steps":  []any{"observe", "restart"},
		},
		Incident: &IncidentInput{Severity: "high", DetectedAt: "2026-08-31T18:30:00+08:00"},
	}
}

func TestCanonicalizeCommandProducesSameDigestForEquivalentCISets(t *testing.T) {
	a := validIncidentCommand("key-1", []int{9, 3, 9})
	b := validIncidentCommand("key-1", []int{3, 9})

	normalizedA, digestA, err := CanonicalizeCommand(a)
	require.NoError(t, err)
	_, digestB, err := CanonicalizeCommand(b)
	require.NoError(t, err)

	require.Equal(t, []int{3, 9}, normalizedA.CIIDs)
	require.Equal(t, digestA, digestB)
	require.Len(t, digestA, 64)
}

func TestCanonicalizeCommandNormalizesKnownStringsAndTimestampWithoutMutatingCaller(t *testing.T) {
	command := validIncidentCommand(" key-1 ", []int{9, 3, 9})
	command.IntakeKind = " incident "
	command.Title = " Database unavailable "
	command.Description = " Production database is not responding "
	command.Incident.Severity = " high "
	originalNested := command.FormValues["nested"].(map[string]any)

	normalized, digestA, err := CanonicalizeCommand(command)
	require.NoError(t, err)
	equivalent := validIncidentCommand("another-key", []int{3, 9})
	_, digestB, err := CanonicalizeCommand(equivalent)
	require.NoError(t, err)

	require.Equal(t, "key-1", normalized.IdempotencyKey)
	require.Equal(t, "incident", normalized.IntakeKind)
	require.Equal(t, "Database unavailable", normalized.Title)
	require.Equal(t, "2026-08-31T10:30:00Z", normalized.Incident.DetectedAt)
	require.Equal(t, digestA, digestB, "idempotency key must not affect the semantic request digest")
	require.Equal(t, []int{9, 3, 9}, command.CIIDs)
	require.Equal(t, " high ", command.Incident.Severity)
	normalized.FormValues["nested"].(map[string]any)["z"] = "changed"
	require.Equal(t, "last", originalNested["z"])
}

func TestCanonicalizeCommandRejectsNonPositiveCIReference(t *testing.T) {
	command := validIncidentCommand("key-1", []int{3, 0})

	_, _, err := CanonicalizeCommand(command)
	require.ErrorIs(t, err, ErrInvalidCommand)
}

func TestCanonicalizeCommandIgnoresObjectInsertionOrderButPreservesArrayOrder(t *testing.T) {
	a := validIncidentCommand("key-1", nil)
	b := validIncidentCommand("key-1", nil)
	a.FormValues = map[string]any{"b": 2, "a": map[string]any{"y": true, "x": false}, "steps": []any{"a", "b"}}
	b.FormValues = map[string]any{"steps": []any{"a", "b"}, "a": map[string]any{"x": false, "y": true}, "b": 2}

	_, digestA, err := CanonicalizeCommand(a)
	require.NoError(t, err)
	_, digestB, err := CanonicalizeCommand(b)
	require.NoError(t, err)
	require.Equal(t, digestA, digestB)

	b.FormValues["steps"] = []any{"b", "a"}
	_, digestReordered, err := CanonicalizeCommand(b)
	require.NoError(t, err)
	require.NotEqual(t, digestA, digestReordered)
}

func TestCanonicalizeCommandRejectsInvalidDetectedTimestamp(t *testing.T) {
	command := validIncidentCommand("key-1", nil)
	command.Incident.DetectedAt = "yesterday"

	_, _, err := CanonicalizeCommand(command)
	require.ErrorIs(t, err, ErrInvalidCommand)
}

func TestCanonicalDigestVersionIsStable(t *testing.T) {
	require.Equal(t, "intake-v1", CanonicalDigestVersion)
	_, digest, err := CanonicalizeCommand(validIncidentCommand("key-1", []int{9, 3, 9}))
	require.NoError(t, err)
	require.Equal(t, "8cd8a8c0c6c6db017300f2cc0b9e5da27ff798ed7e88137a0778522da5b5a11a", digest)
}
