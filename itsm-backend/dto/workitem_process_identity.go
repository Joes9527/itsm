package dto

import "itsm-backend/common/workitemidentity"

// WorkItem 规范身份的 dto 侧入口。词表与业务键格式的唯一实现位于
// common/workitemidentity（叶子包），这里只是同一实现的再导出，供 HTTP/DTO 边界使用；
// 不存在第二份取值或兼容别名。
const (
	RecordClassGeneric            = workitemidentity.RecordClassGeneric
	RecordClassServiceRequestItem = workitemidentity.RecordClassServiceRequestItem
	RecordClassIncident           = workitemidentity.RecordClassIncident
	RecordClassProblem            = workitemidentity.RecordClassProblem
	RecordClassChangeRequest      = workitemidentity.RecordClassChangeRequest
	RecordClassCatalogTask        = workitemidentity.RecordClassCatalogTask
)

// WorkItemRecordClasses returns the canonical classes in stable order.
func WorkItemRecordClasses() []string {
	return workitemidentity.RecordClasses()
}

// WorkItemBusinessKey is the single authority for process business keys.
//
// It validates identity only; it does not decide whether a class can be created. A
// catalog_task therefore has a legitimate key while creation stays gated by the
// registry. Release is deliberately absent: it keeps its existing explicit legacy
// identity and must not be smuggled into WorkItem identity through this function.
func WorkItemBusinessKey(recordClass string, id int) (string, error) {
	return workitemidentity.BusinessKey(recordClass, id)
}

// ParseWorkItemBusinessKey reads a canonical key and fails closed: a legacy
// "ticket:1", an unknown class or a malformed ID is never re-interpreted, so the new
// runtime can never adopt an old instance identity.
func ParseWorkItemBusinessKey(key string) (string, int, error) {
	return workitemidentity.ParseBusinessKey(key)
}
