package intake

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricChannelOther       = "other"
	metricRecordClassUnknown = "unknown"
	metricResultError        = "error"
)

// Metrics owns the bounded-cardinality observability surface for Unified Intake.
// Tenant, actor, WorkItem, idempotency key and raw error values are deliberately
// excluded from every label set.
type Metrics struct {
	createTotal           *prometheus.CounterVec
	createDuration        *prometheus.HistogramVec
	workflowStartTotal    *prometheus.CounterVec
	identityExchangeTotal *prometheus.CounterVec
}

func NewMetrics(registerer prometheus.Registerer) *Metrics {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}
	metrics := &Metrics{
		createTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "itsm_intake_requests_total",
			Help: "Unified Intake create attempts by bounded outcome.",
		}, []string{"channel", "record_class", "result"}),
		createDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "itsm_intake_request_duration_seconds",
			Help:    "Unified Intake create latency by bounded outcome.",
			Buckets: prometheus.DefBuckets,
		}, []string{"channel", "record_class", "result"}),
		workflowStartTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "itsm_intake_workflow_start_total",
			Help: "Unified Intake workflow-start delivery state transitions.",
		}, []string{"channel", "record_class", "result"}),
		identityExchangeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "itsm_intake_identity_exchange_total",
			Help: "Unified Intake identity exchange outcomes.",
		}, []string{"channel", "result"}),
	}
	registerer.MustRegister(metrics.createTotal, metrics.createDuration, metrics.workflowStartTotal, metrics.identityExchangeTotal)
	return metrics
}

var defaultMetrics = NewMetrics(prometheus.DefaultRegisterer)

func DefaultMetrics() *Metrics { return defaultMetrics }

func (m *Metrics) ObserveCreate(channel, recordClass, result string, elapsed time.Duration) {
	if m == nil {
		return
	}
	channel = boundedMetricChannel(channel)
	recordClass = boundedMetricRecordClass(recordClass)
	result = boundedCreateResult(result)
	m.createTotal.WithLabelValues(channel, recordClass, result).Inc()
	m.createDuration.WithLabelValues(channel, recordClass, result).Observe(elapsed.Seconds())
}

// ObserveWorkflowStart satisfies service.WorkflowStartObserver without making
// the service layer depend upward on the Intake HTTP package.
func (m *Metrics) ObserveWorkflowStart(channel, recordClass, result string) {
	if m == nil {
		return
	}
	m.workflowStartTotal.WithLabelValues(
		boundedMetricChannel(channel), boundedMetricRecordClass(recordClass), boundedWorkflowResult(result),
	).Inc()
}

func (m *Metrics) ObserveIdentityDenial(channel, _ string) {
	if m == nil {
		return
	}
	m.identityExchangeTotal.WithLabelValues(boundedMetricChannel(channel), "denied").Inc()
}

func (m *Metrics) ObserveIdentitySuccess(channel string) {
	if m == nil {
		return
	}
	m.identityExchangeTotal.WithLabelValues(boundedMetricChannel(channel), "issued").Inc()
}

func createMetricResult(result *CreateWorkItemResult, err error) (recordClass, outcome string) {
	if err != nil {
		if errors.Is(err, ErrIdempotencyConflict) {
			return metricRecordClassUnknown, "conflict"
		}
		return metricRecordClassUnknown, metricResultError
	}
	if result == nil {
		return metricRecordClassUnknown, metricResultError
	}
	if result.Replayed {
		return result.RecordClass, "replayed"
	}
	return result.RecordClass, "created"
}

func boundedMetricChannel(value string) string {
	switch value {
	case "itsm_web", "itsm_api", "kaf_web", "teams", "feishu", "dingtalk", "wecom":
		return value
	default:
		return metricChannelOther
	}
}

func boundedMetricRecordClass(value string) string {
	switch value {
	case RecordClassIncident, RecordClassServiceRequestItem:
		return value
	default:
		return metricRecordClassUnknown
	}
}

func boundedCreateResult(value string) string {
	switch value {
	case "created", "replayed", "conflict":
		return value
	default:
		return metricResultError
	}
}

func boundedWorkflowResult(value string) string {
	switch value {
	case "pending", "published", "retry", "dead":
		return value
	default:
		return metricResultError
	}
}
