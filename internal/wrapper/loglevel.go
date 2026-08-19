package wrapper

import "log/slog"

// ParseLogLevel maps the LOG_LEVEL env value (shared with pg_doorman, which
// reads the same variable natively) to a slog level. Unknown values mean info.
func ParseLogLevel(s string) slog.Level {
	switch s {
	case "error":
		return slog.LevelError
	case "warn":
		return slog.LevelWarn
	case "debug", "trace":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}
