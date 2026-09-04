package service

import "github.com/prometheus/client_golang/prometheus"

// KafOutboxMetrics contains the Worker-local, low-cardinality KAF delivery
// signals. Labels deliberately exclude tenant, task, event, endpoint and
// payload identifiers.
type KafOutboxMetrics struct {
	registry         *prometheus.Registry
	deliveryAttempts *prometheus.CounterVec
	deliveryFinal    *prometheus.CounterVec
}

func NewKafOutboxMetrics() *KafOutboxMetrics {
	registry := prometheus.NewRegistry()
	metrics := &KafOutboxMetrics{
		registry: registry,
		deliveryAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "itsm_kaf_outbox_delivery_attempts_total",
			Help: "KAF Outbox delivery attempts after their durable attempt marker is committed.",
		}, []string{"event_type"}),
		deliveryFinal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "itsm_kaf_outbox_delivery_final_total",
			Help: "Final or retryable KAF Outbox delivery transitions after persistence succeeds.",
		}, []string{"event_type", "status", "error_class"}),
	}
	registry.MustRegister(metrics.deliveryAttempts, metrics.deliveryFinal)
	return metrics
}

func (m *KafOutboxMetrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

func (m *KafOutboxMetrics) RecordAttempt() {
	if m != nil {
		m.deliveryAttempts.WithLabelValues(KafDelegateRequestedEventType).Inc()
	}
}

func (m *KafOutboxMetrics) RecordTransition(status, errorClass string) {
	if m != nil {
		m.deliveryFinal.WithLabelValues(KafDelegateRequestedEventType, status, errorClass).Inc()
	}
}
