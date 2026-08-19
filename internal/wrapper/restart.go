package wrapper

// NeedsProcessRestart reports whether the config change touches fields
// pg_doorman fixes at process start: worker_threads sizes the tokio runtime,
// max_connections is captured by the accept loop, host/port bind the
// listener. SIGHUP silently ignores them, so the process must be restarted.
func NeedsProcessRestart(oldCfg, newCfg *DoormanConfig) bool {
	return !intPtrEqual(oldCfg.General.WorkerThreads, newCfg.General.WorkerThreads) ||
		!intPtrEqual(oldCfg.General.MaxConnections, newCfg.General.MaxConnections) ||
		oldCfg.General.Host != newCfg.General.Host ||
		oldCfg.General.Port != newCfg.General.Port
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
