//go:build linux

package wrapper

import (
	"os"
	"slices"
	"strconv"
	"strings"
)

// findSuccessorPid scans /proc for the re-exec'd pg_doorman spawned by the
// upstream binary upgrade. The container has its own PID namespace, so any
// process running the runtime binary that is not the old one is ours.
func findSuccessorPid(binaryPath string, excludePid int) (int, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == excludePid || pid == self {
			continue
		}
		if runsBinary("/proc/"+e.Name(), binaryPath) {
			return pid, true
		}
	}
	return 0, false
}

// runsBinary reports whether the process described by procDir runs binaryPath as
// a pooler. Config validation runs the same binary, so it is excluded explicitly.
func runsBinary(procDir, binaryPath string) bool {
	args, haveArgs := readCmdline(procDir)
	if haveArgs && slices.Contains(args, ValidateConfigFlag) {
		return false
	}
	// A binary replaced on disk keeps the " (deleted)" suffix on its exe link.
	if exe, err := os.Readlink(procDir + "/exe"); err == nil {
		if strings.TrimSuffix(exe, " (deleted)") == binaryPath {
			return true
		}
	}
	// The exe of a shebang script is its interpreter, and the kernel moves the
	// script path to argv[1]; only the test doubles are scripts.
	return haveArgs && slices.Contains(args[:min(2, len(args))], binaryPath)
}

func readCmdline(procDir string) ([]string, bool) {
	data, err := os.ReadFile(procDir + "/cmdline") //nolint:gosec // procDir is a /proc entry we enumerated
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), true
}
