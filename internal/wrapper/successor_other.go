//go:build !linux

package wrapper

// findSuccessorPid has no non-Linux implementation: the binary upgrade handover
// relies on /proc and PR_SET_CHILD_SUBREAPER, so it never adopts a successor here.
func findSuccessorPid(_ string, _ int) (int, bool) { return 0, false }

// killStrays has no non-Linux implementation: without /proc there is no way to
// enumerate the processes running the runtime binary.
func killStrays(_ string, _ int) []int { return nil }
