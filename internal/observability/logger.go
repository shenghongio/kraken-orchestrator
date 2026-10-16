package observability

import (
	"log/slog"
	"os"
	"strings"
)

var sensitiveKeys = []string{
	"password", "secret", "token", "api_key", "apikey",
	"credential", "dsn", "private_key",
}

func InitLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{
		Level: lvl,
	}
	var handler slog.Handler
	switch format {
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
func WithTask(logger *slog.Logger, taskID string, attempt int) *slog.Logger {
	return logger.With(
		slog.String("task_id", taskID),
		slog.Int("attempt", attempt),
	)
}
func WithAgent(logger *slog.Logger, agentID string) *slog.Logger {
	return logger.With(slog.String("agent_id", agentID))
}
func WithRequestID(logger *slog.Logger, requestID string) *slog.Logger {
	return logger.With(slog.String("request_id", requestID))
}

// Redact 这个 Redact 是手动调用的工具函数。在 ReplaceAttr 回调里也可以自动脱敏，但自动脱敏容易误伤，建议在记录含密钥字段时显式调用 Redact。
func Redact(key string, value string) string {
	lower := strings.ToLower(key)
	for _, sk := range sensitiveKeys {
		if strings.Contains(lower, sk) {
			return "*******"
		}
	}
	return value
}
