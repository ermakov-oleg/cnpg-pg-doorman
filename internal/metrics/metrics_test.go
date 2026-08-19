package metrics

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObserveCountsErrors(t *testing.T) {
	before := testutil.ToFloat64(hookErrors.WithLabelValues("test-hook"))

	_, err := Observe(context.Background(), "test-hook", func() (int, error) {
		return 0, errors.New("boom")
	})
	if err == nil {
		t.Fatal("error must be passed through")
	}
	if got := testutil.ToFloat64(hookErrors.WithLabelValues("test-hook")); got != before+1 {
		t.Errorf("hook_errors_total = %v, want %v", got, before+1)
	}

	v, err := Observe(context.Background(), "test-hook", func() (int, error) { return 42, nil })
	if err != nil || v != 42 {
		t.Errorf("Observe must pass through result, got %v, %v", v, err)
	}
	if got := testutil.ToFloat64(hookErrors.WithLabelValues("test-hook")); got != before+1 {
		t.Errorf("success must not increment errors, got %v", got)
	}
}
