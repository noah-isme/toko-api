package jobs

import "github.com/prometheus/client_golang/prometheus"

var (
	jobRunsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "worker_job_runs_total",
			Help: "Periodic worker job runs grouped by outcome (success, error, skipped)",
		},
		[]string{"job", "status"},
	)
	jobDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "worker_job_duration_seconds",
			Help:    "Duration of periodic worker job runs that held the job lock",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		},
		[]string{"job"},
	)
	jobLastSuccess = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "worker_job_last_success_timestamp_seconds",
			Help: "Unix time of the last successful run of a periodic worker job",
		},
		[]string{"job"},
	)
	jobItemsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "worker_job_items_total",
			Help: "Items handled by periodic worker jobs grouped by action",
		},
		[]string{"job", "action"},
	)
)

func init() {
	prometheus.MustRegister(jobRunsTotal, jobDuration, jobLastSuccess, jobItemsTotal)
}
