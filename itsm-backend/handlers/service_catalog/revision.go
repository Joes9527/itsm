package service_catalog

import (
	"encoding/json"
	"sort"

	"itsm-backend/ent"
	"itsm-backend/handlers/common/accessgrant"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// Explicit public contract: storage additions, actors, clocks and secrets do not
// silently join a user's confirmation. Process references are projected by their owner.
type publicCatalogDefinition struct {
	AccessPolicy         *accessgrant.Policy     `json:"accessPolicy"`
	TargetClass          string                  `json:"targetClass"`
	ServiceType          string                  `json:"serviceType"`
	Name                 string                  `json:"name"`
	Description          string                  `json:"description"`
	Category             string                  `json:"category"`
	DeliveryTime         int                     `json:"deliveryTime"`
	RequiresApproval     bool                    `json:"requiresApproval"`
	SLAResponseTime      int                     `json:"slaResponseTime"`
	SLAResolutionTime    int                     `json:"slaResolutionTime"`
	CITypeID             int                     `json:"ciTypeId"`
	CloudServiceID       int                     `json:"cloudServiceId"`
	ProcessDefinitionKey string                  `json:"processDefinitionKey"`
	Status               string                  `json:"status"`
	IsActive             bool                    `json:"isActive"`
	Fields               []publicFieldDefinition `json:"fields"`
	RoutingRevision      string                  `json:"routingRevision"`
	// DefaultTicketCategoryID 与派生路径属于公开确认契约：改默认分类、改名、改路径或
	// 停用节点都会改变 CatalogVersion，从而让旧确认失效，而不是按新目录悄悄创建。
	DefaultTicketCategoryID *int                `json:"defaultTicketCategoryId"`
	DefaultCTIPath          []publicCTIPathNode `json:"defaultCTIPath"`
}

// publicCTIPathNode 是默认分类路径的公开指纹（只含展示与状态，不含写入权威）。
type publicCTIPathNode struct {
	ID       int    `json:"id"`
	ParentID int    `json:"parentId"`
	Level    int    `json:"level"`
	Name     string `json:"name"`
	Code     string `json:"code"`
	IsActive bool   `json:"isActive"`
}
type publicFieldDefinition struct {
	ID        int               `json:"id"`
	Name      string            `json:"name"`
	Label     string            `json:"label"`
	Type      string            `json:"type"`
	Required  bool              `json:"required"`
	SortOrder int               `json:"sortOrder"`
	Options   []json.RawMessage `json:"options"`
}

func publicFields(defs []*ent.FieldDefinition) ([]publicFieldDefinition, error) {
	result := make([]publicFieldDefinition, 0, len(defs))
	for _, d := range defs {
		options := make([]json.RawMessage, 0, len(d.Options))
		for _, option := range d.Options {
			raw, err := json.Marshal(option)
			if err != nil {
				return nil, creation.NewDomainValidationFailed("invalid field option", err)
			}
			options = append(options, raw)
		}
		sort.Slice(options, func(i, j int) bool { return string(options[i]) < string(options[j]) })
		result = append(result, publicFieldDefinition{ID: d.ID, Name: d.Name, Label: d.Label, Type: d.FieldType, Required: d.Required, SortOrder: d.SortOrder, Options: options})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
