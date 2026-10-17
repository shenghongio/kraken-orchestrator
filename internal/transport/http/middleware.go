package http

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// 为每个http请求添加一个唯一的请求ID
// 通过 crypto/rand 生成 16 字节的随机数，再用 hex.EncodeToString 转成 32 字符的十六进制字符串作为 Request ID。
// 同时写入请求头 X-Request-Id（供下游 handler 读取）和响应头（供客户端/日志关联）。
// 使用 crypto/rand 而非 math/rand，保证 ID 的唯一性和不可预测性。

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		reqID := hex.EncodeToString(b)
		r.Header.Set("X-Request-Id", reqID)
		w.Header().Set("X-Request-Id", reqID)
		next.ServeHTTP(w, r)
	})
}

// 捕获下游 handler 中发生的panic,防止服务器奔溃，并返回500的错误
//采用 defer + recover() 的标准模式。
//当捕获到 panic 时，用结构化日志 slog.Logger 记录：
//method、path、ip：请求基本信息
//trace：debug.Stack() 打印的调用栈，便于定位 panic 位置
//status：500 对应的状态文本
//panic：panic 的具体值（用 slog.Any 支持任意类型）
//然后调用 writeError 向客户端返回 500 Internal Server Error

func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.String("ip", r.RemoteAddr),
						slog.String("trace", string(debug.Stack())),
						slog.String("status", http.StatusText(http.StatusInternalServerError)),
						slog.Any("panic", rec),
					)
					writeError(w, http.StatusInternalServerError, "internal server error", "internal")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog 记录每个请求的访问日志，包括好事和响应状态码
//start := time.Now() 记录请求开始时间。
//用 statusRecorder（见下文）包装原始 ResponseWriter，默认状态码设为 200（因为部分 handler 不调用 WriteHeader 直接写 body，此时状态码隐式为 200）。
//调用 next.ServeHTTP(rw, r) 执行业务逻辑。
//请求处理完成后，用 slog.Info 输出日志，字段包括：method、path、ip、duration（耗时）、request_id（从请求头取，依赖 RequestID 中间件先执行）、trace、status。

func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{
				ResponseWriter: w,
				status:         http.StatusOK,
			}
			next.ServeHTTP(rw, r)
			logger.Info("request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("ip", r.RemoteAddr),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", r.Header.Get("X-Request-Id")),
				slog.Int("status", rw.status),
			)

		})
	}
}

// 包装器，Decorator模式，用于捕获响应的http状态码
//通过嵌入 http.ResponseWriter，自动继承其 Write、Header 等方法。
//只重写 WriteHeader，在调用底层方法前先保存状态码到 r.status。
//这样 AccessLog 中间件就能在请求结束后读取 rw.status 获取实际响应状态。

type statusRecorder struct {
	http.ResponseWriter // 嵌套原始 ResponseWriter
	status              int
}

// WriteHeader 提供http writeheader方法
func (r *statusRecorder) WriteHeader(code int) {
	r.status = code                    // 记录状态码
	r.ResponseWriter.WriteHeader(code) //透传给底层Writer
}
