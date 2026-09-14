package service_request

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// Values come from actual PostgreSQL timestamp input/output, including decimal
// half-microseconds whose binary representation lies above or below the tie.
func TestAccessReceiptTimestampMatchesPostgresFractionalParsing(t *testing.T) {
	for _, sample := range []struct{ nanos, micros int }{{1000500, 1001}, {8000500, 8001}, {251000500, 251001}, {500, 0}, {1500, 2}, {123456789, 123457}, {999999500, 1000000}} {
		for _, year := range []int{1800, 2026, 2500} {
			base := time.Date(year, 9, 13, 10, 0, 0, 0, time.UTC)
			require.Equal(t, base.Add(time.Duration(sample.micros)*time.Microsecond), accessReceiptTimestamp(base.Add(time.Duration(sample.nanos)*time.Nanosecond)))
		}
	}
}
