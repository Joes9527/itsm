package migration

import (
	"fmt"
	"reflect"
	"strings"
)

type MigrationStage string

const (
	StageOrdinary MigrationStage = "ordinary"
	StagePrepare  MigrationStage = "workitem_prepare"
	StageRetire   MigrationStage = "workitem_retire"
)

type MigrationOperation string

const (
	OpUp      MigrationOperation = "up"
	OpPrepare MigrationOperation = "prepare"
	OpRetire  MigrationOperation = "retire"
	OpDown    MigrationOperation = "down"
	OpReset   MigrationOperation = "reset"
)

type MigrationDefinition struct {
	Migration Migration
	Stage     MigrationStage
	Requires  []string
}
type MigrationPlan struct {
	Executable    []Migration
	PendingManual []Migration
}

// ControlledMigrationCatalog defines the sole active migration stage order.
// Requires describes a fixed chain, not a general-purpose workflow engine.
func ControlledMigrationCatalog() []MigrationDefinition {
	known := allKnownMigrations()
	var catalog []MigrationDefinition
	previous := ""
	afterPreparation := false
	add := func(m Migration, stage MigrationStage) {
		d := MigrationDefinition{Migration: m, Stage: stage}
		if previous != "" {
			d.Requires = []string{previous}
		}
		if afterPreparation && previous != WorkItemPrepareVersion {
			d.Requires = append(d.Requires, WorkItemPrepareVersion)
		}
		catalog = append(catalog, d)
		if stage == StagePrepare {
			afterPreparation = true
		}
		previous = m.Version
	}
	for _, version := range frozenMigrationVersions() {
		if version == "022_drop_professional_extension_shared_fields" || version == "027_work_item_identity_field_retirement" {
			continue
		}
		add(known[version], StageOrdinary)
		if version == "021_add_callback_optional_declared" {
			add(Migration{Version: WorkItemPrepareVersion, Description: "Prepare WorkItem structure with controlled evidence"}, StagePrepare)
		}
	}
	add(known[CandidateExecutionScopeVersion], StageOrdinary)
	add(known[SLAAlertNotificationVersion], StageOrdinary)
	add(known[ToolInvocationExecutionScopeVersion], StageOrdinary)
	add(known[ToolExecutionAuthorityLockVersion], StageOrdinary)
	add(known[ToolExecutionAuthorizationLockVersion], StageOrdinary)
	add(known[NotificationConnectorTargetVersion], StageOrdinary)
	// Candidate infrastructure does not change the immutable retirement contract:
	// old valid R receipts must remain upgradeable without a future 039 receipt.
	catalog = append(catalog, MigrationDefinition{
		Migration: Migration{Version: WorkItemRetireVersion, Description: "Retire WorkItem legacy structures with controlled evidence"},
		Stage:     StageRetire,
		Requires:  []string{"036_intake_frozen_workflow_context", WorkItemPrepareVersion},
	})
	return catalog
}

// PlanMigrations is pure: it validates the entire ledger and operation before
// returning any work. It never substitutes caller or ledger rollback SQL.
func PlanMigrations(catalog []MigrationDefinition, applied []Migration, operation MigrationOperation, requestedVersions []string) (MigrationPlan, error) {
	empty := MigrationPlan{}
	for _, d := range catalog {
		switch d.Stage {
		case StageOrdinary, StagePrepare, StageRetire:
		default:
			return empty, fmt.Errorf("unknown stage %q", d.Stage)
		}
	}
	if !reflect.DeepEqual(catalog, ControlledMigrationCatalog()) {
		return empty, fmt.Errorf("migration catalog does not match fixed controlled catalog")
	}
	switch operation {
	case OpUp, OpPrepare, OpRetire, OpDown, OpReset:
	default:
		return empty, fmt.Errorf("unknown migration operation %q", operation)
	}
	if operation != OpDown && len(requestedVersions) > 0 {
		return empty, fmt.Errorf("operation %s does not accept requested versions", operation)
	}
	seen, err := validateControlledLedger(catalog, applied)
	if err != nil {
		return empty, err
	}
	if operation == OpDown || operation == OpReset {
		return planControlledDown(catalog, applied, seen, operation, requestedVersions)
	}
	plan := MigrationPlan{}
	for _, d := range catalog {
		if seen[d.Migration.Version] {
			continue
		}
		switch d.Stage {
		case StageOrdinary:
			if operation == OpUp {
				ready := true
				for _, dep := range d.Requires {
					if !seen[dep] {
						ready = false
					}
				}
				if ready {
					plan.Executable = append(plan.Executable, d.Migration)
					seen[d.Migration.Version] = true
				}
			}
		case StagePrepare, StageRetire:
			selected := operation == OpPrepare && d.Stage == StagePrepare || operation == OpRetire && d.Stage == StageRetire
			if !selected {
				plan.PendingManual = append(plan.PendingManual, d.Migration)
				continue
			}
			for _, dep := range d.Requires {
				if !seen[dep] {
					return empty, fmt.Errorf("stage %s requires %s", d.Stage, dep)
				}
			}
			plan.Executable = append(plan.Executable, d.Migration)
		}
	}
	return plan, nil
}

func validateControlledLedger(catalog []MigrationDefinition, applied []Migration) (map[string]bool, error) {
	known := allKnownMigrations()
	definitions := make(map[string]MigrationDefinition, len(catalog))
	for _, d := range catalog {
		known[d.Migration.Version] = d.Migration
		definitions[d.Migration.Version] = d
	}
	seen := make(map[string]bool, len(applied))
	for _, m := range applied {
		if _, ok := known[m.Version]; !ok {
			return nil, fmt.Errorf("migration ledger contains unknown version %q", m.Version)
		}
		if seen[m.Version] {
			return nil, fmt.Errorf("migration ledger contains duplicate version %q", m.Version)
		}
		if m.Checksum != checksumSQL(GetMigrationSQL(m.Version)) {
			return nil, fmt.Errorf("migration checksum mismatch for %s", m.Version)
		}
		if d, ok := definitions[m.Version]; ok && d.Stage != StageOrdinary {
			if m.CatalogRevision == nil || *m.CatalogRevision != ControlledCatalogRevision {
				return nil, fmt.Errorf("stage %s has missing or unknown catalog revision", d.Stage)
			}
			if m.EvidenceDigest == nil || strings.TrimSpace(*m.EvidenceDigest) == "" {
				return nil, fmt.Errorf("stage %s has missing evidence digest", d.Stage)
			}
		}
		seen[m.Version] = true
	}
	if !seen[WorkItemPrepareVersion] {
		if seen[CandidateExecutionScopeVersion] || seen[SLAAlertNotificationVersion] || seen[ToolInvocationExecutionScopeVersion] || seen[ToolExecutionAuthorityLockVersion] || seen[ToolExecutionAuthorizationLockVersion] || seen[NotificationConnectorTargetVersion] {
			return nil, fmt.Errorf("candidate execution scope requires preparation")
		}
		// Never derive this order from the new active/legacy classification: removing
		// 022/027 must not legalize an invalid historical ledger.
		if err := requireMigrationPrefix(frozenMigrationVersions(), seen); err != nil {
			return nil, err
		}
		if seen[WorkItemRetireVersion] {
			return nil, fmt.Errorf("retirement requires preparation")
		}
		return seen, nil
	}
	var ordinary []string
	for _, d := range catalog {
		if d.Stage == StageOrdinary {
			ordinary = append(ordinary, d.Migration.Version)
		}
		if seen[d.Migration.Version] {
			for _, dep := range d.Requires {
				if !seen[dep] {
					return nil, fmt.Errorf("migration %s requires %s", d.Migration.Version, dep)
				}
			}
		}
	}
	if err := requireMigrationPrefix(ordinary, seen); err != nil {
		return nil, err
	}
	return seen, nil
}

func requireMigrationPrefix(versions []string, seen map[string]bool) error {
	missing := false
	for _, v := range versions {
		if !seen[v] {
			missing = true
			continue
		}
		if missing {
			return fmt.Errorf("migration ledger is not a continuous prefix: %s follows an earlier gap", v)
		}
	}
	return nil
}

func planControlledDown(catalog []MigrationDefinition, applied []Migration, seen map[string]bool, operation MigrationOperation, requested []string) (MigrationPlan, error) {
	empty := MigrationPlan{}
	targets := make(map[string]bool)
	if operation == OpReset {
		for _, m := range applied {
			targets[m.Version] = true
		}
	} else {
		if len(requested) == 0 {
			return empty, fmt.Errorf("down requires explicit target versions")
		}
		for _, v := range requested {
			if !seen[v] {
				return empty, fmt.Errorf("down target %s is not applied", v)
			}
			if targets[v] {
				return empty, fmt.Errorf("duplicate down target %s", v)
			}
			targets[v] = true
		}
	}
	// Include historical entries in reverse planning. Irreversible stages and
	// historical migrations cause the complete reset request to fail, never skip.
	known := allKnownMigrations()
	for _, d := range catalog {
		known[d.Migration.Version] = d.Migration
	}
	for v := range targets {
		if strings.TrimSpace(known[v].RollbackSQL) == "" {
			return empty, fmt.Errorf("migration %s is irreversible", v)
		}
	}
	remaining := make([]Migration, 0, len(applied))
	for _, m := range applied {
		if !targets[m.Version] {
			remaining = append(remaining, m)
		}
	}
	if _, err := validateControlledLedger(catalog, remaining); err != nil {
		return empty, fmt.Errorf("down violates reverse dependencies: %w", err)
	}
	// A selected predecessor cannot leave its applied successor behind. This also
	// prevents removing P while keeping any post-P ordinary migration.
	for _, d := range catalog {
		if !seen[d.Migration.Version] || targets[d.Migration.Version] {
			continue
		}
		for _, dep := range d.Requires {
			if targets[dep] {
				return empty, fmt.Errorf("down target %s is required by %s", dep, d.Migration.Version)
			}
		}
	}
	order := frozenMigrationVersions()
	if seen[WorkItemPrepareVersion] {
		order = nil
		for _, d := range catalog {
			order = append(order, d.Migration.Version)
		}
	}
	plan := MigrationPlan{}
	for i := len(order) - 1; i >= 0; i-- {
		if targets[order[i]] {
			plan.Executable = append(plan.Executable, known[order[i]])
			delete(targets, order[i])
		}
	}
	if len(targets) > 0 {
		return empty, fmt.Errorf("historical-only migrations cannot be rolled back through controlled catalog")
	}
	return plan, nil
}
