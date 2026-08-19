package specs

import "testing"

func TestGetRBACName(t *testing.T) {
	if got := GetRBACName("foo"); got != "foo-pg-doorman" {
		t.Errorf("expected foo-pg-doorman, got %q", got)
	}
}
