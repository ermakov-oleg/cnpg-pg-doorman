// Package metrics instruments the plugin gRPC hooks. cnpg-i-machinery
// hardcodes its interceptor chain, so instrumentation lives at the
// implementation level instead of a gRPC interceptor.
package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	hookDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cnpg_pg_doorman_hook_duration_seconds",
		Help:    "Duration of CNPG plugin hook calls.",
		Buckets: prometheus.DefBuckets,
	}, []string{"hook"})

	hookErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cnpg_pg_doorman_hook_errors_total",
		Help: "Total number of failed CNPG plugin hook calls.",
	}, []string{"hook"})
)

func init() {
	ctrlmetrics.Registry.MustRegister(hookDuration, hookErrors)
}

// Observe wraps one hook invocation with duration and error metrics.
func Observe[T any](_ context.Context, hook string, fn func() (T, error)) (T, error) {
	start := time.Now()
	result, err := fn()
	hookDuration.WithLabelValues(hook).Observe(time.Since(start).Seconds())
	if err != nil {
		hookErrors.WithLabelValues(hook).Inc()
	}
	return result, err
}
