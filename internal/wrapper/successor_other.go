//go:build !linux

package wrapper

// findSuccessorPid has no non-Linux implementation: the binary upgrade handover
// relies on /proc and PR_SET_CHILD_SUBREAPER, so it never adopts a successor here.
func findSuccessorPid(_ string, _ int) (int, bool) { return 0, false }
