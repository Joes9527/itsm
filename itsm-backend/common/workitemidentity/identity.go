// Package workitemidentity 是 WorkItem 规范身份的唯一定义处：recordClass 词表与
// "{recordClass}:{workItemId}" 业务键。
//
// 它必须保持为叶子包（只依赖标准库），因为身份既要被 dto、authorization 使用，也要被
// handlers/shared/workitemmutation 这类低层包使用，而 dto 与 common 之间存在依赖，低层包
// 无法直接引用 dto。任何第二份词表或键格式都会重新引入双解释，必须避免。
package workitemidentity

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Canonical WorkItem record classes. The legacy wire vocabulary ("ticket", "change",
// "service_request") is a presentation projection and must never be persisted as a
// process identity.
const (
	RecordClassGeneric            = "generic"
	RecordClassServiceRequestItem = "service_request_item"
	RecordClassIncident           = "incident"
	RecordClassProblem            = "problem"
	RecordClassChangeRequest      = "change_request"
	RecordClassCatalogTask        = "catalog_task"
)

// ReleaseRecordClass keeps its existing explicit legacy identity. Release is not a
// WorkItem, so it is neither a convergence target nor a blocker.
const ReleaseRecordClass = "release"

var recordClasses = []string{
	RecordClassGeneric,
	RecordClassServiceRequestItem,
	RecordClassIncident,
	RecordClassProblem,
	RecordClassChangeRequest,
	RecordClassCatalogTask,
}

// RecordClasses returns the canonical classes in stable order.
func RecordClasses() []string {
	return append([]string(nil), recordClasses...)
}

// IsRecordClass reports whether a value is a canonical WorkItem record class.
func IsRecordClass(value string) bool {
	for _, class := range recordClasses {
		if value == class {
			return true
		}
	}
	return false
}

// IsKnownProcessIdentity accepts the canonical WorkItem classes plus the explicit
// Release identity, and rejects the retired Wave-1 vocabulary.
func IsKnownProcessIdentity(value string) bool {
	return IsRecordClass(value) || value == ReleaseRecordClass
}

// BusinessKey is the single authority for process business keys.
//
// It validates identity only; it does not decide whether a class can be created. A
// catalog_task therefore has a legitimate key while creation stays gated elsewhere.
func BusinessKey(recordClass string, id int) (string, error) {
	if id <= 0 {
		return "", errors.New("positive work item ID required")
	}
	if !IsRecordClass(recordClass) {
		return "", fmt.Errorf("unsupported work item class %q", recordClass)
	}
	return fmt.Sprintf("%s:%d", recordClass, id), nil
}

// ParseBusinessKey reads a canonical key and fails closed: a legacy "ticket:1", an
// unknown class or a malformed ID is never re-interpreted, so the new runtime can
// never adopt an old instance identity.
func ParseBusinessKey(key string) (string, int, error) {
	class, rawID, found := strings.Cut(key, ":")
	if !found || rawID == "" {
		return "", 0, fmt.Errorf("malformed work item business key %q", key)
	}
	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 {
		return "", 0, fmt.Errorf("malformed work item business key %q", key)
	}
	if _, err := BusinessKey(class, id); err != nil {
		return "", 0, err
	}
	return class, id, nil
}
