package service

import (
	"errors"
	"strings"

	"github.com/lib/pq"
)

// isRetryableDatabaseConflict recognizes transaction conflicts without
// depending on PostgreSQL's localized error text. SQLite string matching is
// retained solely because deterministic in-process concurrency tests use it.
func isRetryableDatabaseConflict(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "40001" || pqErr.Code == "40P01"
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked")
}
