package wrapper

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry collects wrapper metrics, served on the health port: in-process
// pg_doorman restarts and rejected reloads are invisible to Kubernetes
// (restartCount stays 0), so this is the only signal.
var Registry = prometheus.NewRegistry()

var (
	// ReloadsTotal counts applied and failed config changes.
	ReloadsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pg_doorman_wrapper_reloads_total",
		Help: "Config change applications by result.",
	}, []string{"result"})

	// ProcessRestartsTotal counts unexpected pg_doorman exits.
	ProcessRestartsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pg_doorman_wrapper_process_restarts_total",
		Help: "Unexpected pg_doorman exits followed by an in-process restart.",
	})

	// ConfigStale is 1 while the mounted rendered config differs from the
	// applied one (e.g. it keeps failing validation).
	ConfigStale = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "pg_doorman_wrapper_config_stale",
		Help: "1 when the mounted config differs from the applied one.",
	})
)

func init() {
	Registry.MustRegister(ReloadsTotal, ProcessRestartsTotal, ConfigStale)
}

// MetricsHandler serves the wrapper metrics.
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
}
