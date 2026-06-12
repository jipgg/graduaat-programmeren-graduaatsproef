package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func traceID(r *http.Request) string {
	tp := r.Header.Get("Traceparent")
	// traceparent: 00-{traceId 32hex}-{spanId 16hex}-{flags}
	if len(tp) >= 35 && tp[2] == '-' {
		return tp[3:35]
	}
	return ""
}

func main() {
	logger := zerolog.New(os.Stdout).With().Timestamp().Str("service", "echo-service").Logger()

	mux := http.NewServeMux()
	echo := func(w http.ResponseWriter, r *http.Request) {
		headers := make(map[string]string, len(r.Header))
		for k, v := range r.Header {
			headers[k] = v[0]
		}
		query := make(map[string]string, len(r.URL.Query()))
		for k, v := range r.URL.Query() {
			query[k] = v[0]
		}
		respond(w, http.StatusOK, map[string]any{
			"method":  r.Method,
			"path":    r.URL.Path,
			"headers": headers,
			"query":   query,
		})
	}
	mux.HandleFunc("GET /echo", echo)
	mux.HandleFunc("POST /echo", echo)
	mux.HandleFunc("GET /status/{code}", func(w http.ResponseWriter, r *http.Request) {
		code, err := strconv.Atoi(r.PathValue("code"))
		if err != nil || code < 100 || code > 599 {
			respond(w, http.StatusBadRequest, map[string]string{"error": "invalid status code"})
			return
		}
		w.WriteHeader(code)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]string{"status": "ok", "service": "echo-service"})
	})

	addr := ":8080"
	logger.Info().Str("addr", addr).Msg("starting")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		mux.ServeHTTP(sw, r)
		evt := logger.Info()
		if sw.status >= 500 {
			evt = logger.Error()
		} else if sw.status >= 400 {
			evt = logger.Warn()
		}
		e := evt.
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", sw.status).
			Int64("duration_ms", time.Since(start).Milliseconds())
		if cid := r.Header.Get("X-Correlation-Id"); cid != "" {
			e = e.Str("correlation_id", cid)
		}
		if tid := traceID(r); tid != "" {
			e = e.Str("trace_id", tid)
		}
		e.Msg("request")
	})

	if err := http.ListenAndServe(addr, handler); err != nil {
		logger.Fatal().Err(err).Msg("server error")
	}
}
