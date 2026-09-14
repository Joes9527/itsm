package migration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func retirementUnitEvidence(t *testing.T) (MigrationEvidence, MigrationControlConfig) {
	now := time.Now().UTC().Add(-time.Hour)
	h := strings.Repeat("a", 64)
	e := MigrationEvidence{Target: MigrationTarget{"deployment", "db", "schema"}, CatalogRevision: ControlledCatalogRevision, LedgerDigest: h, InventoryDigest: h, ApplicationDigest: h, Operator: "operator", ChangeRecord: "change"}
	r := &RetirementEvidence{PreparationDigest: h, DataDigest: h, EmptyInventory: true, ObservationStartedAt: now, ObservationEndedAt: now.Add(time.Minute), PausedAt: now.Add(2 * time.Minute), FinalRestorePointAt: now.Add(3 * time.Minute)}
	e.Retirement = r
	for _, kind := range []string{"backup", "restore", "journey", "observation"} {
		b := json.RawMessage(`{"result":"verified"}`)
		d := checksumSQL(string(b))
		r.Reports = append(r.Reports, RetirementReport{Result: "passed", Kind: kind, Target: e.Target, ApplicationDigest: h, LedgerDigest: h, InventoryDigest: h, PreparationDigest: h, DataDigest: h, RecordedAt: now.Add(4 * time.Minute), Content: b, Digest: d})
		switch kind {
		case "backup":
			e.BackupDigest = d
		case "restore":
			e.RestoreReportDigest = d
		case "journey":
			e.JourneyReportDigest = d
		case "observation":
			e.ObservationReportDigest = d
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	digest, err := RetirementEvidenceDigest(e)
	require.NoError(t, err)
	a := &RetirementAuthorization{KeyID: "fixture", Action: string(OpRetire), EvidenceDigest: digest, Target: e.Target, Operator: e.Operator, ChangeRecord: e.ChangeRecord, ExpiresAt: time.Now().Add(time.Hour)}
	payload, err := RetirementAuthorizationPayload(*a)
	require.NoError(t, err)
	a.Signature = ed25519.Sign(priv, payload)
	r.Authorization = a
	return e, MigrationControlConfig{DeploymentID: "deployment", Operator: "operator", RetirementPublicKeys: map[string]ed25519.PublicKey{"fixture": pub}}
}

func TestRetirementEvidenceRejectsEmpty(t *testing.T) {
	require.Error(t, ValidateRetirementEvidence(MigrationEvidence{}))
}

func TestRetirementEvidenceAuthorizationAndReports(t *testing.T) {
	e, c := retirementUnitEvidence(t)
	m := NewMigrator(nil, nil, c)
	require.NoError(t, m.authorizeRetirement(e))
	cases := map[string]func(*MigrationEvidence, *MigrationControlConfig){
		"missing reports": func(e *MigrationEvidence, c *MigrationControlConfig) { e.Retirement.Reports = nil },
		"content changed": func(e *MigrationEvidence, c *MigrationControlConfig) { e.Retirement.Reports[0].Content = []byte(`{}`) },
		"wrong target":    func(e *MigrationEvidence, c *MigrationControlConfig) { e.Target.Database = "other" },
		"old backup": func(e *MigrationEvidence, c *MigrationControlConfig) {
			e.Retirement.Reports[0].RecordedAt = e.Retirement.ObservationStartedAt
		},
		"missing trust": func(e *MigrationEvidence, c *MigrationControlConfig) { c.RetirementPublicKeys = nil },
		"wrong key": func(e *MigrationEvidence, c *MigrationControlConfig) {
			pub, _, _ := ed25519.GenerateKey(rand.Reader)
			c.RetirementPublicKeys["fixture"] = pub
		},
		"wrong operator": func(e *MigrationEvidence, c *MigrationControlConfig) { c.Operator = "other" },
		"wrong action":   func(e *MigrationEvidence, c *MigrationControlConfig) { e.Retirement.Authorization.Action = "prepare" },
		"expired": func(e *MigrationEvidence, c *MigrationControlConfig) {
			e.Retirement.Authorization.ExpiresAt = time.Now().Add(-time.Hour)
		},
		"signature":    func(e *MigrationEvidence, c *MigrationControlConfig) { e.Retirement.Authorization.Signature[0] ^= 1 },
		"missing auth": func(e *MigrationEvidence, c *MigrationControlConfig) { e.Retirement.Authorization = nil },
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			e, c := retirementUnitEvidence(t)
			f(&e, &c)
			m := NewMigrator(nil, nil, c)
			require.Error(t, m.authorizeRetirement(e))
		})
	}
}

func TestRetirementEvidenceFailedReport(t *testing.T) {
	e, c := retirementUnitEvidence(t)
	e.Retirement.Reports[0].Result = "failed"
	require.Error(t, NewMigrator(nil, nil, c).authorizeRetirement(e))
}
