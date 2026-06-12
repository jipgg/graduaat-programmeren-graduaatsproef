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

type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type RouteResult struct {
	ID           int    `json:"id"`
	DistanceM    int    `json:"distance_m"`
	DurationS    int    `json:"duration_s"`
	DelayS       int    `json:"delay_s"`
	HasIncidents bool   `json:"has_incidents"`
	Origin       LatLon `json:"origin"`
	Destination  LatLon `json:"destination"`
	PolylineLen  int    `json:"polyline_points"`
}

var cachedRoutes = []RouteResult{
	{1, 45200, 1820, 240, true, LatLon{50.8503, 4.3517}, LatLon{51.2194, 4.4025}, 312},
	{2, 12400, 540, 0, false, LatLon{50.8503, 4.3517}, LatLon{50.9367, 4.7205}, 98},
	{3, 87300, 3120, 600, true, LatLon{51.0543, 3.7174}, LatLon{50.8798, 4.7005}, 604},
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
	logger := zerolog.New(os.Stdout).With().Timestamp().Str("service", "route-guidance-service").Logger()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /route", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Origin      LatLon `json:"origin"`
			Destination LatLon `json:"destination"`
			Profile     string `json:"profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			logger.Warn().Err(err).Msg("failed to decode route request")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		if req.Profile == "" {
			req.Profile = "car"
		}

		time.Sleep(jitter(20, 40))  // graph load
		time.Sleep(jitter(60, 180)) // traffic layer
		time.Sleep(jitter(80, 250)) // solve

		if rand.IntN(100) < 4 {
			logger.Error().
				Float64("origin_lat", req.Origin.Lat).
				Float64("origin_lon", req.Origin.Lon).
				Msg("routing engine timeout — simulated solver failure")
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "routing engine timeout"})
			return
		}

		writeJSON(w, http.StatusOK, RouteResult{
			ID:           rand.IntN(9000) + 1000,
			DistanceM:    rand.IntN(100000) + 5000,
			DurationS:    rand.IntN(3600) + 300,
			DelayS:       rand.IntN(600),
			HasIncidents: rand.IntN(3) == 0,
			Origin:       req.Origin,
			Destination:  req.Destination,
			PolylineLen:  rand.IntN(800) + 50,
		})
	})
	mux.HandleFunc("GET /route/{id}/alternatives", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			logger.Warn().Str("raw_id", r.PathValue("id")).Msg("invalid route id")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		n := 3
		alts := make([]RouteResult, 0, n)
		for range n {
			time.Sleep(jitter(60, 140))
			alts = append(alts, RouteResult{
				ID:          rand.IntN(9000) + 1000,
				DistanceM:   rand.IntN(120000) + 8000,
				DurationS:   rand.IntN(4000) + 400,
				DelayS:      rand.IntN(800),
				PolylineLen: rand.IntN(900) + 60,
			})
		}
		logger.Info().Int("route_id", id).Int("alternatives", n).Msg("alternatives computed")
		writeJSON(w, http.StatusOK, alts)
	})
	mux.HandleFunc("GET /route/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			logger.Warn().Str("raw_id", r.PathValue("id")).Msg("invalid route id")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		time.Sleep(jitter(8, 30))
		for _, rt := range cachedRoutes {
			if rt.ID == id {
				writeJSON(w, http.StatusOK, rt)
				return
			}
		}
		logger.Warn().Int("route_id", id).Msg("route not found in cache")
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "route not found"})
	})
	mux.HandleFunc("POST /subscriptions", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(jitter(15, 45))
		writeJSON(w, http.StatusCreated, map[string]any{
			"subscription_id": rand.IntN(9000000) + 1000000,
			"ttl_seconds":     3600,
			"webhook_url":     "https://client.example.com/route-updates",
		})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "route-guidance-service"})
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
