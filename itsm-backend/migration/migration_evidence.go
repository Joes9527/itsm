package migration

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type MigrationTarget struct {
	DeploymentID string
	Database     string
	Schema       string
}
type MigrationEvidence struct {
	Retirement              *RetirementEvidence `json:",omitempty"`
	Target                  MigrationTarget
	CatalogRevision         string
	LedgerDigest            string
	ApplicationDigest       string
	InventoryDigest         string
	BackupDigest            string
	RestoreReportDigest     string
	JourneyReportDigest     string
	ObservationReportDigest string
	Operator                string
	ChangeRecord            string
}

// PreparationInventory is a read-only snapshot, not approval or a receipt.
type PreparationInventory struct {
	Target          MigrationTarget
	LedgerDigest    string
	InventoryDigest string
}

func evidenceDigest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return checksumSQL(string(b)), nil
}
func (m *Migrator) preparationTarget(ctx context.Context, q migrationQuery) (MigrationTarget, error) {
	if strings.TrimSpace(m.controlConfig.DeploymentID) == "" {
		return MigrationTarget{}, fmt.Errorf("trusted migration deployment identity is not configured")
	}
	schema, err := migrationTargetSchema(ctx, q)
	if err != nil {
		return MigrationTarget{}, err
	}
	target := MigrationTarget{DeploymentID: m.controlConfig.DeploymentID, Schema: schema}
	err = q.QueryRowContext(ctx, `SELECT current_database()`).Scan(&target.Database)
	return target, err
}
func validatePreparationEvidence(e MigrationEvidence, i PreparationInventory) error {
	if e.Target != i.Target {
		return fmt.Errorf("migration evidence target mismatch")
	}
	if e.CatalogRevision != ControlledCatalogRevision {
		return fmt.Errorf("migration evidence catalog revision mismatch")
	}
	if e.LedgerDigest != i.LedgerDigest || e.InventoryDigest != i.InventoryDigest {
		return fmt.Errorf("migration evidence ledger or inventory digest changed")
	}
	// P precedes current journeys and observation; those reports belong to the
	// later retirement evidence gate and may truthfully be absent here.
	for _, v := range []string{e.ApplicationDigest, e.BackupDigest, e.RestoreReportDigest, e.Operator, e.ChangeRecord, e.LedgerDigest, e.InventoryDigest} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("migration evidence contains an empty required field")
		}
	}
	return nil
}

// MigrationControlConfig is trusted operator configuration, separate from submitted
// evidence. Every nonowner table privilege must be explicitly reviewed.
type MigrationControlConfig struct {
	// Supplied independently by trusted deployment/operator configuration.
	Operator                       string
	RetirementPublicKeys           map[string]ed25519.PublicKey
	HistoricalRetirementPublicKeys map[string]ed25519.PublicKey
	DeploymentID                   string
	ReviewedGrants                 []MigrationRoleGrant
}
type MigrationRoleGrant struct {
	Role       string
	Table      string
	Privileges []string
}

// RetirementObject is an exact object identity. Dependencies are listed explicitly
// in execution order; discovering an object never authorizes its deletion.
type RetirementObject struct{ Kind, Schema, Table, Name string }
type RetirementReport struct {
	Result                                                              string
	Kind                                                                string
	Target                                                              MigrationTarget
	ApplicationDigest, LedgerDigest, InventoryDigest, PreparationDigest string
	DataDigest                                                          string
	RecordedAt                                                          time.Time
	Content                                                             []byte
	Digest                                                              string
}
type RetirementAuthorization struct {
	KeyID, Action, EvidenceDigest string
	Target                        MigrationTarget
	Operator, ChangeRecord        string
	ExpiresAt                     time.Time
	Signature                     []byte
}
type RetirementEvidence struct {
	PreparationDigest                                                       string
	Objects                                                                 []RetirementObject
	EmptyInventory                                                          bool
	Reports                                                                 []RetirementReport
	ObservationStartedAt, ObservationEndedAt, PausedAt, FinalRestorePointAt time.Time
	DataDigest                                                              string
	Authorization                                                           *RetirementAuthorization `json:",omitempty"`
}
type RetirementInventory struct {
	PreparationInventory
	PreparationDigest string
	DataDigest        string
	Objects           []RetirementObject
}

func validDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}

// RetirementEvidenceDigest is the canonical binding signed by an existing
// operational authority. The signature envelope is excluded to avoid recursion.
func RetirementEvidenceDigest(e MigrationEvidence) (string, error) {
	if e.Retirement != nil {
		r := *e.Retirement
		r.Authorization = nil
		e.Retirement = &r
	}
	return evidenceDigest(e)
}
func RetirementAuthorizationPayload(a RetirementAuthorization) ([]byte, error) {
	a.Signature = nil
	return json.Marshal(a)
}

// ValidateRetirementEvidence checks content and binding, never approval truth.
func ValidateRetirementEvidence(e MigrationEvidence) error {
	return validateRetirementEvidenceAt(e, time.Now())
}
func validateRetirementEvidenceAt(e MigrationEvidence, at time.Time) error {
	if e.Retirement == nil {
		return fmt.Errorf("retirement evidence is required")
	}
	r := e.Retirement
	if e.Target.DeploymentID == "" || e.Target.Database == "" || e.Target.Schema == "" || e.Operator == "" || e.ChangeRecord == "" || e.CatalogRevision != ControlledCatalogRevision {
		return fmt.Errorf("retirement identity/catalog is incomplete")
	}
	for _, s := range []string{e.LedgerDigest, e.InventoryDigest, e.ApplicationDigest, e.BackupDigest, e.RestoreReportDigest, e.JourneyReportDigest, e.ObservationReportDigest, r.PreparationDigest, r.DataDigest} {
		if !validDigest(s) {
			return fmt.Errorf("retirement requires SHA256 content digests")
		}
	}
	if r.ObservationStartedAt.IsZero() || !r.ObservationEndedAt.After(r.ObservationStartedAt) || r.PausedAt.Before(r.ObservationEndedAt) || r.FinalRestorePointAt.Before(r.PausedAt) || r.FinalRestorePointAt.After(at) {
		return fmt.Errorf("invalid observation/pause/final restore point chronology")
	}
	if (len(r.Objects) == 0) != r.EmptyInventory {
		return fmt.Errorf("explicit empty inventory must match object list")
	}
	seen := map[RetirementObject]bool{}
	for _, o := range r.Objects {
		if o.Schema != e.Target.Schema || o.Table == "" || o.Name == "" || seen[o] {
			return fmt.Errorf("invalid/duplicate or cross-schema retirement object")
		}
		seen[o] = true
		switch o.Kind {
		case "table", "column", "constraint", "index", "view", "trigger", "policy", "sequence":
		default:
			return fmt.Errorf("unsupported retirement object kind")
		}
	}
	required := map[string]string{"backup": e.BackupDigest, "restore": e.RestoreReportDigest, "journey": e.JourneyReportDigest, "observation": e.ObservationReportDigest}
	for _, report := range r.Reports {
		if report.Result != "passed" {
			return fmt.Errorf("retirement report must record passed verification")
		}
		expected, ok := required[report.Kind]
		if !ok {
			return fmt.Errorf("unknown or duplicate retirement report")
		}
		delete(required, report.Kind)
		if !json.Valid(report.Content) || checksumSQL(string(report.Content)) != report.Digest || report.Digest != expected {
			return fmt.Errorf("retirement report content digest mismatch")
		}
		if report.Target != e.Target || report.ApplicationDigest != e.ApplicationDigest || report.LedgerDigest != e.LedgerDigest || report.InventoryDigest != e.InventoryDigest || report.PreparationDigest != r.PreparationDigest {
			return fmt.Errorf("retirement report target/baseline binding mismatch")
		}
		if report.RecordedAt.IsZero() || report.RecordedAt.After(at) {
			return fmt.Errorf("invalid report timestamp")
		}
		if report.Kind == "backup" || report.Kind == "restore" {
			if report.DataDigest != r.DataDigest || report.RecordedAt.Before(r.FinalRestorePointAt) {
				return fmt.Errorf("backup/restore must cover final recovery point after observation writes")
			}
		}
		if report.Kind == "observation" && report.RecordedAt.Before(r.ObservationEndedAt) {
			return fmt.Errorf("observation report precedes observation completion")
		}
	}
	if len(required) != 0 {
		return fmt.Errorf("missing retirement reports")
	}
	return nil
}
func (m *Migrator) authorizeRetirement(e MigrationEvidence) error {
	if err := ValidateRetirementEvidence(e); err != nil {
		return err
	}
	if m.controlConfig.Operator == "" || e.Operator != m.controlConfig.Operator {
		return fmt.Errorf("independent trusted operator authorization is required")
	}
	return verifyRetirementAuthorization(e, m.controlConfig.RetirementPublicKeys, time.Now())
}
func verifyRetirementAuthorization(e MigrationEvidence, keys map[string]ed25519.PublicKey, at time.Time) error {
	if e.Retirement == nil || e.Retirement.Authorization == nil {
		return fmt.Errorf("environment authorization envelope is required")
	}
	a := e.Retirement.Authorization
	key := keys[a.KeyID]
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("trusted environment authorization key is not configured")
	}
	digest, err := RetirementEvidenceDigest(e)
	if err != nil {
		return err
	}
	if a.Action != string(OpRetire) || a.Target != e.Target || a.Operator != e.Operator || a.ChangeRecord != e.ChangeRecord || a.EvidenceDigest != digest || !a.ExpiresAt.After(at) {
		return fmt.Errorf("environment authorization action/target/evidence/expiry mismatch")
	}
	payload, err := RetirementAuthorizationPayload(*a)
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, payload, a.Signature) {
		return fmt.Errorf("environment authorization signature is invalid")
	}
	return nil
}
func historicalRetirementKeys(c MigrationControlConfig) map[string]ed25519.PublicKey {
	keys := map[string]ed25519.PublicKey{}
	for k, v := range c.HistoricalRetirementPublicKeys {
		keys[k] = v
	}
	for k, v := range c.RetirementPublicKeys {
		keys[k] = v
	}
	return keys
}
