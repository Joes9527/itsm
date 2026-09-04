package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKafOutboxMetricsUseOnlyAllowlistedLabels(t *testing.T) {
	metrics := NewKafOutboxMetrics()
	metrics.RecordAttempt()
	metrics.RecordTransition("blocked", "permanent_http")

	families, err := metrics.Registry().Gather()
	require.NoError(t, err)
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				require.Contains(t, []string{"event_type", "status", "error_class"}, label.GetName())
			}
		}
	}
}
