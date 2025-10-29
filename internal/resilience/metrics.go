package resilience

import "github.com/prometheus/client_golang/prometheus"

var (
	BreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "breaker_state",
			Help: "Current breaker state: 0=closed,1=open,2=half-open",
		},
		[]string{"target"},
	)
	BreakerTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "breaker_transition_total",
			Help: "Count of breaker state transitions",
		},
		[]string{"target", "from", "to"},
	)
	BreakerOpenedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "breaker_open_total",
			Help: "Number of times a breaker transitioned into open state",
		},
		[]string{"target"},
	)
)

func init() {
	prometheus.MustRegister(BreakerState, BreakerTransitions, BreakerOpenedTotal)
}
