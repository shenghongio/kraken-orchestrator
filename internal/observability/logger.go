package observability

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"runtime"
	"strings"
)

var sensitiveKeys = []string{
	"password", "secret", "token", "api_key", "apikey",
	"credential", "dsn", "private_key", "authorization",
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
		Level:       lvl,
		AddSource:   true,
		ReplaceAttr: replaceAttr,
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

// WithLeaseEpoch 附加 lease_epoch 字段，用于追踪 fencing token。
func WithLeaseEpoch(logger *slog.Logger, leaseEpoch int64) *slog.Logger {
	return logger.With(slog.Int64("lease_epoch", leaseEpoch))
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

// replaceAttr 在日志输出前自动脱敏，命中敏感词的字符串替换为 ************
// 同时将 source 中的绝对路径裁剪为相对项目的路径（cmd/kraken-scheduler/main.go:61）
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.SourceKey {
		source := a.Value.Any().(*slog.Source)
		source.File = trimSourcePath(source.File)
		// 返回 file:line 格式的字符串，更紧凑
		return slog.String(slog.SourceKey, fmt.Sprintf("%s:%d", source.File, source.Line))
	}
	if a.Value.Kind() == slog.KindString {
		key := strings.ToLower(a.Key)
		// dsn 只对密码生效，保留 host/port/dbname 等
		if strings.Contains(key, "dsn") {
			return slog.String(key, maskDSN(a.Value.String()))
		}
		for _, sk := range sensitiveKeys {
			if strings.Contains(key, sk) {
				return slog.String(a.Key, "************")
			}
		}
	}
	return a
}

// Stack 返回当前调用栈的文本表示，跳过 observability 包自身的帧
// 用于在debug/error 日志中附加堆栈，方便定位问题
//func Stack() string {
//	const maxFrames = 32
//	pcs := make([]uintptr, maxFrames)
//	n := runtime.Callers(3, pcs)
//	frames := runtime.CallersFrames(pcs[:n])
//	var b strings.Builder
//	for {
//		frame, more := frames.Next()
//		fmt.Fprintf(&b, "\n%s:%d %s", frame.File, frame.Line, frame.Function)
//		if !more {
//			break
//		}
//	}
//	return b.String()
//}

// ErrAttr 返回一个带堆栈的error日志属性
// 统一错误日志格式: message +stack 都在error group 下
func ErrAttr(err error) slog.Attr {
	if err == nil {
		return slog.Any("error", nil)
	}
	return slog.Group("error", slog.String("message", err.Error()), slog.Any("stack", stackFrames()))
}

// stackFrames 返回调用栈的字符串数组，每个元素一帧
func stackFrames() []string {
	const maxFrames = 32
	pcs := make([]uintptr, maxFrames)
	n := runtime.Callers(4, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var out []string
	for {
		frame, more := frames.Next()
		out = append(out, fmt.Sprintf("%s:%d %s", trimSourcePath(frame.File), frame.Line, frame.Function))
		if !more {
			break
		}
	}
	return out
}

// RedactMap 对 map中的敏感字段做脱敏，返回新的map（不修改原,map）
// 用于在 debug日志中安全地输出请求参数
func RedactMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		lower := strings.ToLower(k)
		sensitive := false
		for _, sk := range sensitiveKeys {
			if strings.Contains(lower, sk) {
				sensitive = true
				break
			}
		}
		if sensitive {
			out[k] = "************"
		} else if inner, ok := v.(map[string]any); ok {
			out[k] = RedactMap(inner)
		} else {
			out[k] = v
		}
	}
	return out
}

// trimSourcePath 吧绝对路径精简为项目内的相对路径.md/kraken-scheduler/main.go
func trimSourcePath(file string) string {
	// 优先取 "kraken-orchestrator/" 之后的部分
	if idx := strings.LastIndex(file, "kraken-orchestrator/"); idx >= 0 {
		return file[idx+len("kraken-orchestrator/"):]
	}
	// 否则只取最后两段(目录/文件)
	parts := strings.Split(file, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return file
}

// maskDSN 只掩 DSN 中的密码，保留 host/port/dbname 等可用于排查的信息
// 支持 URL 格式和 key=value 格式
func maskDSN(dsn string) string {
	// URL 格式: postgres://user:password@host:port/dbname
	if strings.Contains(dsn, "://") {
		if parsed, err := url.Parse(dsn); err == nil {
			if parsed.User != nil {
				if _, hasPwd := parsed.User.Password(); hasPwd {
					parsed.User = url.UserPassword(parsed.User.Username(), "***")
				}
			}
			return parsed.String()
		}
	}
	// key=value 格式: user=xxx password=yyy host=...
	parts := strings.Fields(dsn)
	for i, part := range parts {
		if strings.HasPrefix(part, "password=") {
			parts[i] = "password=******"
		}
	}
	return strings.Join(parts, " ")
}
