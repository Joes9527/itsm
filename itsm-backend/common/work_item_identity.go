package common

// WorkItemLegacyType projects the legacy wire field from canonical identity.
func WorkItemLegacyType(recordClass, genericSubtype string) string {
	switch recordClass {
	case "generic":
		return genericSubtype
	case "change_request":
		return "change"
	case "service_request_item":
		return "service_request"
	default:
		return recordClass
	}
}

// WorkItemIdentityFilter interprets the public legacy filter without storing it.
func WorkItemIdentityFilter(value string) (recordClass, genericSubtype string) {
	switch value {
	case "incident", "problem", "catalog_task":
		return value, ""
	case "change", "change_request":
		return "change_request", ""
	case "service_request", "service_request_item":
		return "service_request_item", ""
	default:
		return "generic", value
	}
}
