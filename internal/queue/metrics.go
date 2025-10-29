package queue

import "github.com/prometheus/client_golang/prometheus"

var (
	QueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "queue_depth",
			Help: "Approximate number of ready tasks per kind",
		},
		[]string{"kind"},
	)
	QueueProcessedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "queue_processed_total",
			Help: "Total tasks processed grouped by status",
		},
		[]string{"kind", "status"},
	)
	QueueDLQSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "queue_dlq_size",
			Help: "Number of tasks stored in DLQ",
		},
		[]string{"kind"},
	)
)

func init() {
	prometheus.MustRegister(QueueDepth, QueueProcessedTotal, QueueDLQSize)
}
