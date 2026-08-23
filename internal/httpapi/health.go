package httpapi

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"
)

func Health(ready func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok := ready()
		status := http.StatusOK
		if !ok {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok})
	}
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// Hijack allows WebSocket upgrades through the logging middleware.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func RequestLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		next.ServeHTTP(sw, r)
		// After a successful WebSocket hijack, further writes may be invalid; still log.
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.code,
			"remote", r.RemoteAddr,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if up := r.Header.Get("Upgrade"); up != "" {
			attrs = append(attrs, "upgrade", up)
		}
		// Unauthenticated live WS probes / expired tabs retry often; keep INFO clean.
		if sw.code == http.StatusUnauthorized && r.URL.Path == "/admin/ws" {
			log.Debug("http", attrs...)
			return
		}
		log.Info("http", attrs...)
	})
}
