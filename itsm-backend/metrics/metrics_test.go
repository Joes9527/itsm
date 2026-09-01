package metrics

import (
	"testing"

	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestRecordBPMNCallbackEffectUsesOnlyDeclaredEffectValues(t *testing.T) {
	metric, err := BPMNCallbackEffectsTotal.GetMetricWithLabelValues("callback_test_handler", "notify", "blocked")
	require.NoError(t, err)
	before := prometheusCounterValue(t, metric)

	require.True(t, RecordBPMNCallbackEffect("callback_test_handler", "notify", "blocked"))
	require.Equal(t, before+1, prometheusCounterValue(t, metric))
	require.False(t, RecordBPMNCallbackEffect("callback_test_handler", "notify", "tenant-7-secret"))
}

func prometheusCounterValue(t *testing.T, metric interface {
	Write(*io_prometheus_client.Metric) error
}) float64 {
	t.Helper()
	payload := &io_prometheus_client.Metric{}
	require.NoError(t, metric.Write(payload))
	return payload.GetCounter().GetValue()
}
