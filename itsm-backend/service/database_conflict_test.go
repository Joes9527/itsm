package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

func TestIsRetryableDatabaseConflict_ClassifiesSQLStateAndSQLiteLocks(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "postgres serialization", err: &pq.Error{Code: "40001"}, want: true},
		{name: "wrapped postgres deadlock", err: fmt.Errorf("save transaction: %w", &pq.Error{Code: "40P01"}), want: true},
		{name: "other postgres state", err: &pq.Error{Code: "23505"}, want: false},
		{name: "postgres wording without typed error", err: errors.New("could not serialize access due to concurrent update"), want: false},
		{name: "postgres deadlock wording without typed error", err: errors.New("deadlock detected"), want: false},
		{name: "sqlite locked", err: errors.New("database is locked"), want: true},
		{name: "sqlite table locked", err: fmt.Errorf("update: %w", errors.New("database table is locked: process_tasks")), want: true},
		{name: "unrelated", err: errors.New("connection refused"), want: false},
		{name: "nil", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isRetryableDatabaseConflict(tt.err))
		})
	}
}
