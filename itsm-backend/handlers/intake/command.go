package intake

import (
	"encoding/json"
	"fmt"
	"io"

	"itsm-backend/ent"
)

const (
	IntakeKindCatalogItem = "catalog_item"
	IntakeKindIncident    = "incident"

	RecordClassServiceRequestItem = "service_request_item"
	RecordClassIncident           = "incident"
	RecordClassChangeRequest      = "change_request"
)

type CTIInput struct {
	CategoryID *int `json:"categoryId,omitempty"`
	TypeID     *int `json:"typeId,omitempty"`
	ItemID     *int `json:"itemId,omitempty"`
}

type SourceReference struct {
	Provider       string `json:"provider"`
	EventID        string `json:"eventId"`
	ConversationID string `json:"conversationId,omitempty"`
}

type IncidentInput struct {
	Type        string `json:"type,omitempty"`
	Severity    string `json:"severity,omitempty"`
	Impact      string `json:"impact,omitempty"`
	Urgency     string `json:"urgency,omitempty"`
	Category    string `json:"category,omitempty"`
	Subcategory string `json:"subcategory,omitempty"`
	DetectedAt  string `json:"detectedAt,omitempty"`
}

type CreateWorkItemCommand struct {
	IdempotencyKey  string           `json:"idempotencyKey"`
	IntakeKind      string           `json:"intakeKind"`
	Title           string           `json:"title"`
	Description     string           `json:"description,omitempty"`
	CatalogItemID   *int             `json:"catalogItemId,omitempty"`
	CTI             *CTIInput        `json:"cti,omitempty"`
	CIIDs           []int            `json:"ciIds,omitempty"`
	FormValues      map[string]any   `json:"formValues,omitempty"`
	SourceReference *SourceReference `json:"sourceReference,omitempty"`
	Incident        *IncidentInput   `json:"incident,omitempty"`
}

type ProfessionalReference struct {
	Type string `json:"type"`
	ID   int    `json:"id"`
}

type CreateWorkItemResult struct {
	WorkItemID            int                   `json:"workItemId"`
	Number                string                `json:"number"`
	RecordClass           string                `json:"recordClass"`
	ProfessionalReference ProfessionalReference `json:"professionalReference"`
	WorkflowStartStatus   string                `json:"workflowStartStatus"`
	Replayed              bool                  `json:"replayed"`
}

type ResolvedWorkflowBinding struct {
	DefinitionID      *int
	DefinitionKey     string
	DefinitionVersion string
	NoProcess         bool
}

type ResolvedCatalog struct {
	ID                      int
	Version                 string
	TargetClass             string
	ServiceType             string
	DeliveryTime            int
	FormSchemaVersion       string
	ProcessDefinitionKey    string
	SLADefinitionID         *int
	ConfigurationItemTypeID *int
	CloudServiceID          *int
}

type ResolvedCTI struct {
	CategoryID *int
	TypeID     *int
	ItemID     *int
}

type ResolvedFieldDefinition struct {
	ID       int
	Key      string
	DataType string
	Required bool
	Options  []any
}

type ResolvedIntake struct {
	Identity           Identity
	Command            CreateWorkItemCommand
	RecordClass        string
	Catalog            *ResolvedCatalog
	CTI                ResolvedCTI
	CIIDs              []int
	ConfigurationItems []*ent.ConfigurationItem
	FieldDefinitions   []ResolvedFieldDefinition
	Workflow           ResolvedWorkflowBinding
	SLADefinitionID    *int
	ResolverVersion    string
}

type WorkItemDraft struct {
	TenantID        int
	ActorID         int
	RequesterID     int
	RecordClass     string
	Title           string
	Description     string
	Status          string
	Priority        string
	Source          string
	TicketNumber    string
	CategoryID      *int
	SLADefinitionID *int
}

type CreationPlan struct {
	Resolved          ResolvedIntake
	WorkItem          WorkItemDraft
	ProfessionalInput any
}

// DecodeCreateWorkItemCommand is the single strict JSON decoder used by Intake
// HTTP adapters. Identity and authorization fields are deliberately absent from
// CreateWorkItemCommand, so DisallowUnknownFields rejects attempts to smuggle
// them in the payload.
func DecodeCreateWorkItemCommand(reader io.Reader) (CreateWorkItemCommand, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var command CreateWorkItemCommand
	if err := decoder.Decode(&command); err != nil {
		return CreateWorkItemCommand{}, NewInvalidCommand("invalid intake command", FieldError{Field: "body", Message: "must be valid JSON with only supported fields"}, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return CreateWorkItemCommand{}, NewInvalidCommand("invalid intake command", FieldError{Field: "body", Message: "must contain exactly one JSON object"}, err)
	}
	return command, nil
}
