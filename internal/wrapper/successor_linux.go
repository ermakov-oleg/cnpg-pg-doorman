//go:build linux

package wrapper

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
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

// killStrays SIGKILLs every process running binaryPath except excludePid and
// the wrapper itself, and returns the pids it killed. It backs the "exactly one
// supervised pooler" invariant: SO_REUSEPORT lets a survivor of a failed
// handover keep serving traffic nobody supervises, which is worse than dropping
// its connections. SIGTERM is not enough — such a process may be mid-migration
// and take its whole drain, while a new pooler is about to start.
func killStrays(binaryPath string, excludePid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var killed []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == excludePid || pid == self {
			continue
		}
		if !runsBinary("/proc/"+e.Name(), binaryPath) {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err == nil {
			killed = append(killed, pid)
		}
	}
	return killed
}

// validateConfigShortFlag is the short form of ValidateConfigFlag; both mark a
// validation run of the same binary, which must never be adopted.
const validateConfigShortFlag = "-t"

// runsBinary reports whether the process described by procDir runs binaryPath as
// a pooler. Config validation runs the same binary, so it is excluded explicitly.
func runsBinary(procDir, binaryPath string) bool {
	args, haveArgs := readCmdline(procDir)
	// A serving pg_doorman always has a readable argv; an empty cmdline means a
	// zombie or a process inside execve, and adopting one would let a config
	// validator child pass the exe-link check below.
	if !haveArgs {
		return false
	}
	if slices.Contains(args, ValidateConfigFlag) || slices.Contains(args, validateConfigShortFlag) {
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
	return slices.Contains(args[:min(2, len(args))], binaryPath)
}

func readCmdline(procDir string) ([]string, bool) {
	data, err := os.ReadFile(procDir + "/cmdline") //nolint:gosec // procDir is a /proc entry we enumerated
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), true
}
