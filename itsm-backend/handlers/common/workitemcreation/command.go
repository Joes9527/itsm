package workitemcreation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

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
	AffectedUsers       int         `json:"affectedUsers,omitempty"`
	RevenueImpact       json.Number `json:"revenueImpact,omitempty"`
	ServiceAvailability json.Number `json:"serviceAvailability,omitempty"`
}
type TimeImpact struct {
	IsOverdue          bool   `json:"isOverdue,omitempty"`
	HoursSinceCreation int    `json:"hoursSinceCreation,omitempty"`
	ResponseDeadline   string `json:"responseDeadline,omitempty"`
	ResolutionDeadline string `json:"resolutionDeadline,omitempty"`
}

// EmailInput is accepted only from the verified MS Graph channel. SourceReference
// owns internet message/thread identity; these fields retain recoverable provider references.
type EmailInput struct {
	Mailbox        string `json:"mailbox"`
	GraphMessageID string `json:"graphMessageId"`
	SenderEmail    string `json:"senderEmail"`
	HasAttachments bool   `json:"hasAttachments"`
	TriageComment  string `json:"triageComment,omitempty"`
}

type FeishuTaskInput struct {
	TaskGUID      string `json:"taskGuid"`
	CreatorOpenID string `json:"creatorOpenId"`
	Status        string `json:"status,omitempty"`
	Completed     bool   `json:"completed,omitempty"`
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
	SourceIncidentID *int   `json:"sourceIncidentId,omitempty"`
	Category         string `json:"category,omitempty"`
	RootCause        string `json:"rootCause,omitempty"`
	Impact           string `json:"impact,omitempty"`
}
type ServiceRequestInput struct {
	Amount             json.Number `json:"amount,omitempty"`
	CloudResourceRefID *int        `json:"cloudResourceRefId,omitempty"`
	CostCenter         string      `json:"costCenter,omitempty"`
	DataClassification string      `json:"dataClassification,omitempty"`
	NeedsPublicIP      bool        `json:"needsPublicIp,omitempty"`
	SourceIPWhitelist  []string    `json:"sourceIpWhitelist,omitempty"`
	ExpireAt           string      `json:"expireAt,omitempty"`
	ComplianceAck      bool        `json:"complianceAck,omitempty"`
	ContactName        string      `json:"contactName,omitempty"`
	ContactEmail       string      `json:"contactEmail,omitempty"`
	Quantity           *int        `json:"quantity,omitempty"`
	ExpectedAt         string      `json:"expectedAt,omitempty"`
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
	FeishuTask        *FeishuTaskInput     `json:"feishuTask,omitempty"`
	Email             *EmailInput          `json:"email,omitempty"`
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
	DefinitionDigest  string
	DefinitionID      *int
	DefinitionKey     string
	DefinitionVersion string
	NoProcess         bool
}

type ResolvedCatalog struct {
	RequiresApproval        bool
	SLAResponseTime         int
	SLAResolutionTime       int
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
	CategoryName string
	CategoryID   *int
	TypeID       *int
	ItemID       *int
}

type ResolvedFieldDefinition struct {
	ID       int
	Key      string
	DataType string
	Required bool
	Options  []any
}

type FieldDefinitionScope struct {
	EntityType string
	EntityID   int
	Version    string
}

type ResolvedIntake struct {
	FieldScope         *FieldDefinitionScope
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
	CreatorEmail      string
	ExternalMessageID string
	ConversationID    string
	GenericSubtype    string
	TemplateID        *int
	ParentTicketID    *int
	TagIDs            []int
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
	// WorkflowVariables are prepared by the owning domain and frozen in the outbox.
	WorkflowVariables map[string]any
	// Routing facts are supplied by the domain after effective defaults/policy.
	// Priority is read from WorkItem; submitted Command remains digest evidence.
	BusinessSubtype   string
	RoutingValues     map[string]any
	RequiresWorkflow  bool
	Resolved          ResolvedIntake
	WorkItem          WorkItemDraft
	ProfessionalInput any
}

// DecodeCreateWorkItemCommand enforces the exact tagged wire shape before
// binding. encoding/json alone permits case folding, duplicate keys and nulls.
// Dynamic form/metadata contents retain their JSON values and domain-defined keys.
func DecodeCreateWorkItemCommand(reader io.Reader) (CreateWorkItemCommand, error) {
	decoder := json.NewDecoder(reader)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return CreateWorkItemCommand{}, wireError(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return CreateWorkItemCommand{}, wireError(err)
	}
	shape := json.NewDecoder(bytes.NewReader(raw))
	shape.UseNumber()
	if err := validateWireValue(shape, reflect.TypeOf(CreateWorkItemCommand{})); err != nil {
		return CreateWorkItemCommand{}, wireError(err)
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var command CreateWorkItemCommand
	if err := decoder.Decode(&command); err != nil {
		return CreateWorkItemCommand{}, wireError(err)
	}
	return command, nil
}
func wireError(cause error) error {
	return NewInvalidCommand("invalid intake command", FieldError{Field: "body", Message: "must be one JSON object with exact supported field names, no duplicate typed keys, and concrete typed values"}, cause)
}

func validateWireValue(decoder *json.Decoder, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("null is not a concrete %s value", typ)
	}
	if typ == reflect.TypeOf(json.Number("")) {
		if _, ok := token.(json.Number); !ok {
			return fmt.Errorf("expected a JSON number")
		}
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return fmt.Errorf("expected an object")
		}
		fields := make(map[string]reflect.Type, typ.NumField())
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if tag != "" && tag != "-" {
				fields[tag] = field.Type
			}
		}
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("expected a field name")
			}
			field, ok := fields[name]
			if !ok {
				return fmt.Errorf("unsupported field name")
			}
			if seen[name] {
				return fmt.Errorf("duplicate typed field")
			}
			seen[name] = true
			if err := validateWireValue(decoder, field); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case reflect.Map:
		if token != json.Delim('{') {
			return fmt.Errorf("expected a dynamic object")
		}
		for decoder.More() {
			if _, err = decoder.Token(); err != nil {
				return err
			}
			var value json.RawMessage
			if err = decoder.Decode(&value); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case reflect.Slice:
		if token != json.Delim('[') {
			return fmt.Errorf("expected an array")
		}
		for decoder.More() {
			if err := validateWireValue(decoder, typ.Elem()); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case reflect.String:
		if _, ok := token.(string); !ok {
			return fmt.Errorf("expected a string")
		}
	case reflect.Bool:
		if _, ok := token.(bool); !ok {
			return fmt.Errorf("expected a boolean")
		}
	case reflect.Int, reflect.Int64:
		if _, ok := token.(json.Number); !ok {
			return fmt.Errorf("expected an integer")
		}
	default:
		return fmt.Errorf("unsupported typed wire value")
	}
	return nil
}
