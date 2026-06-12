package main

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

type TrafficEvent struct {
	ID          int     `json:"id"`
	Type        string  `json:"type"`
	Severity    string  `json:"severity"`
	Description string  `json:"description"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Active      bool    `json:"active"`
}

var events = []TrafficEvent{
	{1, "accident", "high", "Multi-vehicle collision on E40", 50.8503, 4.3517, true},
	{2, "roadwork", "medium", "Lane closure on R0 ring", 50.8229, 4.3980, true},
	{3, "congestion", "low", "Slow traffic near Leuven interchange", 50.8798, 4.7005, true},
	{4, "incident", "high", "Wrong-way driver on E17", 51.0543, 3.7174, false},
	{5, "roadwork", "medium", "Night works on E313", 51.2093, 4.4226, false},
	{6, "congestion", "high", "Rush-hour gridlock Brussels inner ring", 50.8467, 4.3525, true},
	{7, "accident", "medium", "Fender bender on A12, right shoulder", 51.1784, 4.3625, true},
	{8, "roadwork", "low", "Resurfacing works N25", 50.7264, 4.8821, false},
}

func jitter(baseMs, spreadMs int) time.Duration {
	return time.Duration(baseMs+rand.IntN(spreadMs)) * time.Millisecond
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
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
	logger := zerolog.New(os.Stdout).With().Timestamp().Str("service", "traffic-events-service").Logger()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /events/active", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(jitter(20, 60))
		active := make([]TrafficEvent, 0, len(events))
		for _, e := range events {
			if e.Active {
				active = append(active, e)
			}
		}
		writeJSON(w, http.StatusOK, active)
	})
	mux.HandleFunc("GET /events/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			logger.Warn().Str("raw_id", r.PathValue("id")).Msg("invalid event id")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		time.Sleep(jitter(5, 20))
		for _, e := range events {
			if e.ID == id {
				writeJSON(w, http.StatusOK, e)
				return
			}
		}
		logger.Warn().Int("event_id", id).Msg("event not found")
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "event not found"})
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(jitter(15, 50))
		writeJSON(w, http.StatusOK, events)
	})
	mux.HandleFunc("POST /events/query", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			BBoxMinLat float64 `json:"bbox_min_lat"`
			BBoxMaxLat float64 `json:"bbox_max_lat"`
			BBoxMinLon float64 `json:"bbox_min_lon"`
			BBoxMaxLon float64 `json:"bbox_max_lon"`
			Severity   string  `json:"severity"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Warn().Err(err).Msg("failed to decode query request")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		time.Sleep(jitter(30, 80))
		if rand.IntN(100) < 3 {
			logger.Error().Msg("data source timeout — simulated upstream failure")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "data source timeout"})
			return
		}
		result := make([]TrafficEvent, 0)
		for _, e := range events {
			inBBox := e.Lat >= req.BBoxMinLat && e.Lat <= req.BBoxMaxLat &&
				e.Lon >= req.BBoxMinLon && e.Lon <= req.BBoxMaxLon
			if (req.BBoxMinLat == 0 || inBBox) &&
				(req.Severity == "" || e.Severity == req.Severity) {
				result = append(result, e)
			}
		}
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "traffic-events-service"})
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
