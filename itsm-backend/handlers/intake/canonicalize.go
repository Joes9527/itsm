package intake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const CanonicalDigestVersion = "intake-v2"

type canonicalIncidentInputV1 struct {
	Type             string                 `json:"type,omitempty"`
	Severity         string                 `json:"severity,omitempty"`
	ExplicitPriority string                 `json:"priority,omitempty"`
	Impact           string                 `json:"impact,omitempty"`
	Urgency          string                 `json:"urgency,omitempty"`
	DetectedAt       string                 `json:"detectedAt,omitempty"`
	Category         string                 `json:"category,omitempty"`
	Subcategory      string                 `json:"subcategory,omitempty"`
	AssigneeID       *int                   `json:"assigneeId,omitempty"`
	ImpactAnalysis   map[string]interface{} `json:"impactAnalysis,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
	Source           string                 `json:"source,omitempty"`
}

type canonicalChangeInputV1 struct {
	Justification      string   `json:"justification,omitempty"`
	Type               string   `json:"type,omitempty"`
	ImpactScope        string   `json:"impactScope,omitempty"`
	RiskLevel          string   `json:"riskLevel,omitempty"`
	PlannedStartDate   string   `json:"plannedStartDate,omitempty"`
	PlannedEndDate     string   `json:"plannedEndDate,omitempty"`
	ImplementationPlan string   `json:"implementationPlan,omitempty"`
	RollbackPlan       string   `json:"rollbackPlan,omitempty"`
	AffectedCIs        []string `json:"affectedCis,omitempty"`
}

type canonicalCommandV1 struct {
	IntakeKind      string                    `json:"intakeKind"`
	Title           string                    `json:"title"`
	Description     string                    `json:"description,omitempty"`
	CatalogItemID   *int                      `json:"catalogItemId,omitempty"`
	CTI             *CTIInput                 `json:"cti,omitempty"`
	CIIDs           []int                     `json:"ciIds,omitempty"`
	FormValues      map[string]any            `json:"formValues,omitempty"`
	SourceReference *SourceReference          `json:"sourceReference,omitempty"`
	Incident        *canonicalIncidentInputV1 `json:"incident,omitempty"`
	Change          *canonicalChangeInputV1   `json:"change,omitempty"`
}

func CanonicalizeCommand(command CreateWorkItemCommand) (CreateWorkItemCommand, string, error) {
	normalized := command
	normalized.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	normalized.IntakeKind = strings.TrimSpace(command.IntakeKind)
	normalized.Title = strings.TrimSpace(command.Title)
	normalized.Description = strings.TrimSpace(command.Description)

	if err := validateBaseCommand(normalized); err != nil {
		return CreateWorkItemCommand{}, "", err
	}

	normalizedCIIDs, err := normalizeCIIDs(command.CIIDs)
	if err != nil {
		return CreateWorkItemCommand{}, "", err
	}
	normalized.CIIDs = normalizedCIIDs
	if command.CTI != nil {
		normalized.CTI = &CTIInput{
			CategoryID: copyInt(command.CTI.CategoryID),
			TypeID:     copyInt(command.CTI.TypeID),
			ItemID:     copyInt(command.CTI.ItemID),
		}
		if err := validateCTI(*normalized.CTI); err != nil {
			return CreateWorkItemCommand{}, "", err
		}
	}
	if command.CatalogItemID != nil {
		normalized.CatalogItemID = copyInt(command.CatalogItemID)
		if *normalized.CatalogItemID <= 0 {
			return CreateWorkItemCommand{}, "", NewInvalidCommand("invalid intake command", FieldError{Field: "catalogItemId", Message: "must be positive"}, nil)
		}
	}
	if command.SourceReference != nil {
		normalized.SourceReference = &SourceReference{
			Provider:       strings.TrimSpace(command.SourceReference.Provider),
			EventID:        strings.TrimSpace(command.SourceReference.EventID),
			ConversationID: strings.TrimSpace(command.SourceReference.ConversationID),
		}
		if normalized.SourceReference.Provider == "" || normalized.SourceReference.EventID == "" {
			return CreateWorkItemCommand{}, "", NewInvalidCommand("invalid intake command", FieldError{Field: "sourceReference", Message: "provider and eventId are required"}, nil)
		}
	}
	if command.Incident != nil {
		normalized.Incident = &IncidentInput{
			Type:             strings.TrimSpace(command.Incident.Type),
			Severity:         strings.TrimSpace(command.Incident.Severity),
			ExplicitPriority: strings.TrimSpace(command.Incident.ExplicitPriority),
			Impact:           strings.TrimSpace(command.Incident.Impact),
			Urgency:          strings.TrimSpace(command.Incident.Urgency),
			Category:         strings.TrimSpace(command.Incident.Category),
			Subcategory:      strings.TrimSpace(command.Incident.Subcategory),
			DetectedAt:       strings.TrimSpace(command.Incident.DetectedAt),
			AssigneeID:       copyInt(command.Incident.AssigneeID),
			ImpactAnalysis:   command.Incident.ImpactAnalysis,
			Metadata:         command.Incident.Metadata,
			Source:           strings.TrimSpace(command.Incident.Source),
		}
		if err := normalizeIncident(normalized.Incident); err != nil {
			return CreateWorkItemCommand{}, "", err
		}
	}
	if command.Change != nil {
		affectedCIs := append([]string(nil), command.Change.AffectedCIs...)
		sort.Strings(affectedCIs)
		normalized.Change = &ChangeInput{
			Justification:      strings.TrimSpace(command.Change.Justification),
			Type:               strings.TrimSpace(command.Change.Type),
			ImpactScope:        strings.TrimSpace(command.Change.ImpactScope),
			RiskLevel:          strings.TrimSpace(command.Change.RiskLevel),
			PlannedStartDate:   strings.TrimSpace(command.Change.PlannedStartDate),
			PlannedEndDate:     strings.TrimSpace(command.Change.PlannedEndDate),
			ImplementationPlan: strings.TrimSpace(command.Change.ImplementationPlan),
			RollbackPlan:       strings.TrimSpace(command.Change.RollbackPlan),
			AffectedCIs:        affectedCIs,
		}
	}

	formValues, err := cloneFormValues(command.FormValues)
	if err != nil {
		return CreateWorkItemCommand{}, "", NewInvalidCommand("invalid intake command", FieldError{Field: "formValues", Message: "must contain JSON-compatible values"}, err)
	}
	normalized.FormValues = formValues

	var canonicalIncident *canonicalIncidentInputV1
	if normalized.Incident != nil {
		canonicalIncident = &canonicalIncidentInputV1{
			Type:             normalized.Incident.Type,
			Severity:         normalized.Incident.Severity,
			ExplicitPriority: normalized.Incident.ExplicitPriority,
			Impact:           normalized.Incident.Impact,
			Urgency:          normalized.Incident.Urgency,
			DetectedAt:       normalized.Incident.DetectedAt,
			Category:         normalized.Incident.Category,
			Subcategory:      normalized.Incident.Subcategory,
			AssigneeID:       normalized.Incident.AssigneeID,
			ImpactAnalysis:   normalized.Incident.ImpactAnalysis,
			Metadata:         normalized.Incident.Metadata,
			Source:           normalized.Incident.Source,
		}
	}
	var canonicalChange *canonicalChangeInputV1
	if normalized.Change != nil {
		canonicalChange = &canonicalChangeInputV1{
			Justification:      normalized.Change.Justification,
			Type:               normalized.Change.Type,
			ImpactScope:        normalized.Change.ImpactScope,
			RiskLevel:          normalized.Change.RiskLevel,
			PlannedStartDate:   normalized.Change.PlannedStartDate,
			PlannedEndDate:     normalized.Change.PlannedEndDate,
			ImplementationPlan: normalized.Change.ImplementationPlan,
			RollbackPlan:       normalized.Change.RollbackPlan,
			AffectedCIs:        normalized.Change.AffectedCIs,
		}
	}
	canonical := canonicalCommandV1{
		IntakeKind:      normalized.IntakeKind,
		Title:           normalized.Title,
		Description:     normalized.Description,
		CatalogItemID:   normalized.CatalogItemID,
		CTI:             normalized.CTI,
		CIIDs:           normalized.CIIDs,
		FormValues:      normalized.FormValues,
		SourceReference: normalized.SourceReference,
		Incident:        canonicalIncident,
		Change:          canonicalChange,
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return CreateWorkItemCommand{}, "", NewInvalidCommand("invalid intake command", FieldError{Field: "body", Message: "could not be canonicalized"}, err)
	}
	digest := sha256.Sum256(payload)
	return normalized, hex.EncodeToString(digest[:]), nil
}

func validateBaseCommand(command CreateWorkItemCommand) error {
	if command.IdempotencyKey == "" || len(command.IdempotencyKey) > 200 {
		return NewInvalidCommand("invalid intake command", FieldError{Field: "idempotencyKey", Message: "must contain 1 to 200 characters"}, nil)
	}
	if command.IntakeKind != IntakeKindCatalogItem && command.IntakeKind != IntakeKindIncident {
		return NewInvalidCommand("invalid intake command", FieldError{Field: "intakeKind", Message: "must be catalog_item or incident"}, nil)
	}
	if command.Title == "" || len(command.Title) > 500 {
		return NewInvalidCommand("invalid intake command", FieldError{Field: "title", Message: "must contain 1 to 500 characters"}, nil)
	}
	if len(command.Description) > 20_000 {
		return NewInvalidCommand("invalid intake command", FieldError{Field: "description", Message: "must not exceed 20000 characters"}, nil)
	}
	if command.IntakeKind == IntakeKindCatalogItem && command.CatalogItemID == nil {
		return NewInvalidCommand("invalid intake command", FieldError{Field: "catalogItemId", Message: "is required for catalog_item"}, nil)
	}
	return nil
}

func validateCTI(cti CTIInput) error {
	for field, value := range map[string]*int{"cti.categoryId": cti.CategoryID, "cti.typeId": cti.TypeID, "cti.itemId": cti.ItemID} {
		if value != nil && *value <= 0 {
			return NewInvalidCommand("invalid intake command", FieldError{Field: field, Message: "must be positive"}, nil)
		}
	}
	return nil
}

func normalizeIncident(incident *IncidentInput) error {
	for field, value := range map[string]string{
		"incident.severity": incident.Severity,
		"incident.impact":   incident.Impact,
		"incident.urgency":  incident.Urgency,
	} {
		if value != "" && value != "low" && value != "medium" && value != "high" && value != "critical" {
			return NewInvalidCommand("invalid intake command", FieldError{Field: field, Message: "must be low, medium, high, or critical"}, nil)
		}
	}
	if incident.DetectedAt == "" {
		return nil
	}
	detectedAt, err := time.Parse(time.RFC3339, incident.DetectedAt)
	if err != nil {
		return NewInvalidCommand("invalid intake command", FieldError{Field: "incident.detectedAt", Message: "must be an RFC3339 timestamp"}, err)
	}
	incident.DetectedAt = detectedAt.UTC().Format(time.RFC3339Nano)
	return nil
}

func normalizeCIIDs(ids []int) ([]int, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	unique := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, NewInvalidCommand("invalid intake command", FieldError{Field: "ciIds", Message: "must contain only positive IDs"}, nil)
		}
		unique[id] = struct{}{}
	}
	normalized := make([]int, 0, len(unique))
	for id := range unique {
		normalized = append(normalized, id)
	}
	sort.Ints(normalized)
	return normalized, nil
}

func cloneFormValues(values map[string]any) (map[string]any, error) {
	if values == nil {
		return nil, nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		copyValue, err := cloneJSONValue(value)
		if err != nil {
			return nil, fmt.Errorf("form value %q: %w", key, err)
		}
		cloned[key] = copyValue
	}
	return cloned, nil
}

func cloneJSONValue(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return cloneFormValues(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			copyItem, err := cloneJSONValue(item)
			if err != nil {
				return nil, err
			}
			cloned[index] = copyItem
		}
		return cloned, nil
	default:
		if _, err := json.Marshal(value); err != nil {
			return nil, err
		}
		return value, nil
	}
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
