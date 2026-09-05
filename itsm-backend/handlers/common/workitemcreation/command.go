package workitemcreation

import (
	"encoding/json"
	"fmt"
	"io"

	"itsm-backend/ent"
)

const (
	IntakeKindCatalogItem   = "catalog_item"
	IntakeKindIncident      = "incident"
	IntakeKindGeneric       = "generic"
	IntakeKindProblem       = "problem"
	IntakeKindChangeRequest = "change_request"
	RecordClassGeneric      = "generic"
	RecordClassProblem      = "problem"

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

// ImpactAnalysis preserves the complete professional creation projection.
type ImpactAnalysis struct {
	BusinessImpact  *BusinessImpact `json:"businessImpact,omitempty"`
	TechnicalImpact string          `json:"technicalImpact,omitempty"`
	AffectedUsers   int             `json:"affectedUsers,omitempty"`
	TimeImpact      *TimeImpact     `json:"timeImpact,omitempty"`
}
type BusinessImpact struct {
	AffectedUsers       int     `json:"affectedUsers,omitempty"`
	RevenueImpact       float64 `json:"revenueImpact,omitempty"`
	ServiceAvailability float64 `json:"serviceAvailability,omitempty"`
}
type TimeImpact struct {
	IsOverdue          bool   `json:"isOverdue,omitempty"`
	HoursSinceCreation int    `json:"hoursSinceCreation,omitempty"`
	ResponseDeadline   string `json:"responseDeadline,omitempty"`
	ResolutionDeadline string `json:"resolutionDeadline,omitempty"`
}
type GenericInput struct {
	Type                  string `json:"type,omitempty"`
	TypeID                string `json:"typeId,omitempty"`
	Source                string `json:"source,omitempty"`
	Category              string `json:"category,omitempty"`
	TemplateID            *int   `json:"templateId,omitempty"`
	ParentTicketID        *int   `json:"parentTicketId,omitempty"`
	TagIDs                []int  `json:"tagIds,omitempty"`
	WorkflowDefinitionKey string `json:"workflowDefinitionKey,omitempty"`
}
type ProblemInput struct {
	Category  string `json:"category,omitempty"`
	RootCause string `json:"rootCause,omitempty"`
	Impact    string `json:"impact,omitempty"`
}
type ServiceRequestInput struct {
	CostCenter         string   `json:"costCenter,omitempty"`
	DataClassification string   `json:"dataClassification,omitempty"`
	NeedsPublicIP      bool     `json:"needsPublicIp,omitempty"`
	SourceIPWhitelist  []string `json:"sourceIpWhitelist,omitempty"`
	ExpireAt           string   `json:"expireAt,omitempty"`
	ComplianceAck      bool     `json:"complianceAck,omitempty"`
	ContactName        string   `json:"contactName,omitempty"`
	ContactEmail       string   `json:"contactEmail,omitempty"`
	Quantity           *int     `json:"quantity,omitempty"`
	ExpectedAt         string   `json:"expectedAt,omitempty"`
}
type IncidentInput struct {
	Type           string                 `json:"type,omitempty"`
	Severity       string                 `json:"severity,omitempty"`
	Impact         string                 `json:"impact,omitempty"`
	Urgency        string                 `json:"urgency,omitempty"`
	Category       string                 `json:"category,omitempty"`
	Subcategory    string                 `json:"subcategory,omitempty"`
	DetectedAt     string                 `json:"detectedAt,omitempty"`
	ImpactAnalysis *ImpactAnalysis        `json:"impactAnalysis,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	Source         string                 `json:"source,omitempty"`
}

type ChangeInput struct {
	Justification      string   `json:"justification,omitempty"`
	Type               string   `json:"type,omitempty"`
	ImpactScope        string   `json:"impactScope,omitempty"`
	RiskLevel          string   `json:"riskLevel,omitempty"`
	PlannedStartDate   string   `json:"plannedStartDate,omitempty"`
	PlannedEndDate     string   `json:"plannedEndDate,omitempty"`
	ImplementationPlan string   `json:"implementationPlan,omitempty"`
	RollbackPlan       string   `json:"rollbackPlan,omitempty"`
	AffectedCIs        []string `json:"affectedCis,omitempty"`
	RelatedTickets     []int    `json:"relatedTickets,omitempty"`
}

// Identity is supplied separately by trusted adapters, never by command JSON.
// FormValues owns dynamic fields; professional fields have typed inputs.
type CreateWorkItemCommand struct {
	RecordClass       string               `json:"recordClass"`
	Confirmation      string               `json:"confirmation"`
	CatalogVersion    string               `json:"catalogVersion,omitempty"`
	FormSchemaVersion string               `json:"formSchemaVersion,omitempty"`
	Priority          string               `json:"priority,omitempty"`
	AssigneeID        *int                 `json:"assigneeId,omitempty"`
	AssignmentGroupID *int                 `json:"assignmentGroupId,omitempty"`
	Generic           *GenericInput        `json:"generic,omitempty"`
	Problem           *ProblemInput        `json:"problem,omitempty"`
	ServiceRequest    *ServiceRequestInput `json:"serviceRequest,omitempty"`
	IdempotencyKey    string               `json:"idempotencyKey"`
	IntakeKind        string               `json:"intakeKind"`
	Title             string               `json:"title"`
	Description       string               `json:"description,omitempty"`
	CatalogItemID     *int                 `json:"catalogItemId,omitempty"`
	CTI               *CTIInput            `json:"cti,omitempty"`
	CIIDs             []int                `json:"ciIds,omitempty"`
	FormValues        map[string]any       `json:"formValues,omitempty"`
	SourceReference   *SourceReference     `json:"sourceReference,omitempty"`
	Incident          *IncidentInput       `json:"incident,omitempty"`
	Change            *ChangeInput         `json:"change,omitempty"`
}

// Generic creation has no extension: its reference is the zero value {type:"", id:0}.
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
	TenantID          int
	ActorID           int
	RequesterID       int
	RecordClass       string
	Title             string
	Description       string
	Status            string
	Priority          string
	Source            string
	TicketNumber      string
	AssigneeID        *int
	AssignmentGroupID *int
	CategoryID        *int
	SLADefinitionID   *int
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
	decoder.UseNumber()

	var command *CreateWorkItemCommand
	if err := decoder.Decode(&command); err != nil {
		return CreateWorkItemCommand{}, NewInvalidCommand("invalid intake command", FieldError{Field: "body", Message: "must be valid JSON with only supported fields"}, err)
	}
	if command == nil {
		return CreateWorkItemCommand{}, NewInvalidCommand("invalid intake command", FieldError{Field: "body", Message: "must be a JSON object"}, nil)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return CreateWorkItemCommand{}, NewInvalidCommand("invalid intake command", FieldError{Field: "body", Message: "must contain exactly one JSON object"}, err)
	}
	return *command, nil
}
