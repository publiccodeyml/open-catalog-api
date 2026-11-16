package webhooks

import "github.com/prometheus/client_golang/prometheus"

// Failure reasons for deliveryFailures. The labels carry no webhook ID or
// URL, so the metric reveals nothing about subscribers.
const (
	failureTransport = "transport"
	failureStatus    = "status"
)

//nolint:gochecknoglobals // one counter per process, shared by every dispatch
var deliveryFailures = newDeliveryFailures()

func newDeliveryFailures() *prometheus.CounterVec {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "webhook_delivery_failures_total",
		Help: "Webhook deliveries that failed, by reason: transport for " +
			"requests that got no response, status for non-2xx responses.",
	}, []string{"reason"})

	// Both series exist from startup, so rate() and absence alerts work
	// before the first failure.
	counter.WithLabelValues(failureTransport)
	counter.WithLabelValues(failureStatus)

	return counter
}

// RegisterMetrics adds the webhook delivery metrics to the registry that
// serves /metrics.
func RegisterMetrics(registerer prometheus.Registerer) {
	registerer.MustRegister(deliveryFailures)
}
