// Package slacontract holds the frozen, applied SLA value contract.
package slacontract

import "time"

type Policy struct {
	SchemaVersion     int                    `json:"schemaVersion"`
	DefinitionID      int                    `json:"definitionId"`
	DefinitionVersion time.Time              `json:"definitionVersion"`
	Name              string                 `json:"name"`
	ServiceType       string                 `json:"serviceType"`
	ResponseMinutes   int                    `json:"responseMinutes"`
	ResolutionMinutes int                    `json:"resolutionMinutes"`
	BusinessHours     map[string]interface{} `json:"businessHours"`
}
