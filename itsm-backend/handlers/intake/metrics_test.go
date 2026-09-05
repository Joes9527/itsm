package intake

import (
	"context"
	"itsm-backend/handlers/common/workitemcreation"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func counterValue(t *testing.T, counter *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	metric := &dto.Metric{}
	require.NoError(t, counter.WithLabelValues(labels...).Write(metric))
	return metric.GetCounter().GetValue()
}

func histogramCount(t *testing.T, histogram *prometheus.HistogramVec, labels ...string) uint64 {
	t.Helper()
	metric := &dto.Metric{}
	collector, ok := histogram.WithLabelValues(labels...).(prometheus.Metric)
	require.True(t, ok)
	require.NoError(t, collector.Write(metric))
	return metric.GetHistogram().GetSampleCount()
}

func TestMetricsRecordCreateReplayConflictLatencyAndWorkflowStates(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)
	_, service, identity, command, _, _ := intakeFixture(t)
	service.metrics = metrics

	created, err := service.Create(context.Background(), identity, command)
	require.NoError(t, err)
	require.False(t, created.Replayed)

	replayed, err := service.Create(context.Background(), identity, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)

	conflict := command
	conflict.Title = "Different command with the same key"
	_, err = service.Create(context.Background(), identity, conflict)
	require.ErrorIs(t, err, workitemcreation.ErrIdempotencyConflict)

	channel := "itsm_web"
	recordClass := workitemcreation.RecordClassGeneric
	require.Equal(t, float64(1), counterValue(t, metrics.createTotal, channel, recordClass, "created"))
	require.Equal(t, float64(1), counterValue(t, metrics.createTotal, channel, recordClass, "replayed"))
	require.Equal(t, float64(1), counterValue(t, metrics.createTotal, channel, "unknown", "conflict"))
	require.Equal(t, float64(0), counterValue(t, metrics.workflowStartTotal, channel, recordClass, "pending"))
	require.Equal(t, uint64(1), histogramCount(t, metrics.createDuration, channel, recordClass, "created"))
	require.Equal(t, uint64(1), histogramCount(t, metrics.createDuration, channel, recordClass, "replayed"))
	require.Equal(t, uint64(1), histogramCount(t, metrics.createDuration, channel, "unknown", "conflict"))
}

func TestMetricsBoundUntrustedLabelsAndCountIdentityDenials(t *testing.T) {
	metrics := NewMetrics(prometheus.NewRegistry())

	metrics.ObserveWorkflowStart("tenant-controlled-channel", "custom_class", "exception text")
	metrics.ObserveIdentityDenial("tenant-controlled-channel", "raw database error")

	require.Equal(t, float64(1), counterValue(t, metrics.workflowStartTotal, "other", "unknown", "error"))
	require.Equal(t, float64(1), counterValue(t, metrics.identityExchangeTotal, "other", "denied"))
}

func TestBoundedMetricRecordClassRecognizesChangeRequest(t *testing.T) {
	require.Equal(t, workitemcreation.RecordClassIncident, boundedMetricRecordClass(workitemcreation.RecordClassIncident))
	require.Equal(t, workitemcreation.RecordClassServiceRequestItem, boundedMetricRecordClass(workitemcreation.RecordClassServiceRequestItem))
	require.Equal(t, workitemcreation.RecordClassChangeRequest, boundedMetricRecordClass(workitemcreation.RecordClassChangeRequest))
	require.Equal(t, metricRecordClassUnknown, boundedMetricRecordClass("custom_class"))
}
