package intake

import (
	"context"
	"net/http"
	"strings"
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
	fixture := newResolverFixture(t)
	service := newServiceUnderTest(t, fixture)
	service.metrics = metrics
	command := fixture.catalogCommand(fixture.serviceCatalog.ID)

	created, err := service.Create(context.Background(), fixture.identity(), command)
	require.NoError(t, err)
	require.False(t, created.Replayed)

	replayed, err := service.Create(context.Background(), fixture.identity(), command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)

	conflict := command
	conflict.Title = "Different command with the same key"
	_, err = service.Create(context.Background(), fixture.identity(), conflict)
	require.ErrorIs(t, err, ErrIdempotencyConflict)

	channel := "itsm_web"
	recordClass := RecordClassServiceRequestItem
	require.Equal(t, float64(1), counterValue(t, metrics.createTotal, channel, recordClass, "created"))
	require.Equal(t, float64(1), counterValue(t, metrics.createTotal, channel, recordClass, "replayed"))
	require.Equal(t, float64(1), counterValue(t, metrics.createTotal, channel, "unknown", "conflict"))
	require.Equal(t, float64(1), counterValue(t, metrics.workflowStartTotal, channel, recordClass, "pending"))
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

func TestIdentityExchangeHandlerRecordsDenialWithoutUnboundedCodeLabel(t *testing.T) {
	metrics := NewMetrics(prometheus.NewRegistry())
	fixture := newIdentityExchangeFixture(t, "super_admin")
	fixture.handler.metrics = metrics
	assertion := fixture.request
	assertion.Signature = strings.Repeat("0", 64)

	response := fixture.exchange(assertion)

	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Equal(t, float64(1), counterValue(t, metrics.identityExchangeTotal, "teams", "denied"))
}
