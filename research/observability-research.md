# APISIX Observability — Research Notes

## What APISIX Exposes

APISIX covers the three observability pillars through plugins. All plugins are enabled
per-route or globally via `plugin_attr` in `config.yaml`.

### Metrics — `prometheus` plugin

The most mature and production-ready observability plugin. Since v3.0, metric collection
runs in a **dedicated NGINX worker process**, isolating it from the hot request path.

**Endpoint:** `http://<apisix>:9091/apisix/prometheus/metrics`

| Metric | Type | Key labels |
|---|---|---|
| `apisix_http_requests_total` | Counter | route, service, consumer |
| `apisix_http_status` | Counter | route, service, consumer, status |
| `apisix_http_latency_bucket` | Histogram | type (request/upstream/apisix), route, service |
| `apisix_bandwidth` | Counter | type (ingress/egress), route, service |
| `apisix_nginx_http_current_connections` | Gauge | state |
| `apisix_upstream_status` | Gauge | name, ip, port |
| `apisix_shared_dict_capacity_bytes` | Gauge | name |

The three `type` values on `apisix_http_latency` are the critical split for diagnosis:
- `type="apisix"` — pure gateway overhead (plugin execution, forward-auth, etc.)
- `type="upstream"` — backend response time only
- `type="request"` — end-to-end (sum of both)

A spike on `type="apisix"` with a flat `type="upstream"` points at the gateway
(plugin overhead, a slow auth service, rate-limiter overhead). The reverse points at
the backend. This distinction is not available in most other API gateways.

**Cardinality note:** `route`, `service`, and `consumer` labels can be high-cardinality
in large deployments. Use `prefer_name: true` so that names (stable) are used instead
of auto-generated IDs (reset on restart).

### Tracing — `opentelemetry` plugin (recommended)

Available since v2.13. Sends spans to an OTEL Collector via **OTLP/HTTP only** (no
gRPC). The default endpoint is `http://otel-collector:4318`.

Each request through APISIX generates a root span with:
- `http.method`, `http.scheme`, `http.target`, `http.status_code`
- `service.name` (configurable via `plugin_attr.opentelemetry.resource`)
- Custom attributes via `additional_attributes` (e.g. `route_id`, `apisix.service_id`)
- W3C `traceparent` propagation to upstreams

**Important limitation:** APISIX's OpenTelemetry plugin supports **traces only**. Metrics
and logs via OTLP are not implemented. There is no native OTLP metrics push from APISIX;
the `prometheus` plugin is the only metrics path today.

Sampling strategies available in `sampler.name`:
- `always_on` — trace every request (use for POC/dev)
- `always_off` — no tracing
- `trace_id_ratio` — probabilistic (0.0–1.0)
- `parent_base` — respect upstream sampling decision; configure with a fallback root
  sampler (recommended for production)

### Tracing — `zipkin` plugin (legacy path)

Sends Zipkin v1/v2 format spans. Compatible with Zipkin, Jaeger, and SkyWalking
backends (all accept Zipkin v1/v2). This is what the official tutorial uses.

For a modern OTEL-first stack, prefer the `opentelemetry` plugin — the Collector then
normalises traces to OTLP internally and can route to any backend (Tempo, Jaeger,
Zipkin, etc.) from a single collector config.

### Logging — `http-logger` and alternatives

APISIX can push access logs to HTTP endpoints. The tutorial uses `http-logger` against
Mockbin as a demo sink. Production-grade options:

| Plugin | Sink |
|---|---|
| `http-logger` | Any HTTP endpoint (Elasticsearch, Loki ingest, etc.) |
| `kafka-logger` | Apache Kafka |
| `clickhouse-logger` | ClickHouse |
| `skywalking-logger` | Apache SkyWalking OAP |
| `file-logger` | Local file (then shipped by a log agent) |

Logs are not covered by the dashboards in this directory — the focus is metrics
(Thanos) and traces (Tempo).

---

## Official "Observe Your API" Tutorial

Source: https://apisix.apache.org/docs/apisix/tutorials/observe-your-api/

The tutorial walks through all three pillars with the simplest possible setup:

| Pillar | Plugin | Backend |
|---|---|---|
| Logs | `http-logger` | Mockbin (HTTP echo) |
| Metrics | `prometheus` | Prometheus → Grafana |
| Traces | `zipkin` | Zipkin server (port 9411) |

**Stack difference from this POC:**

| Tutorial | This repo |
|---|---|
| Standalone Prometheus instance | OTEL Collector (prometheus receiver) → Thanos Receive |
| Zipkin for traces | `opentelemetry` plugin → OTEL Collector → Tempo |
| Direct Grafana → Prometheus | Grafana → Thanos Query |
| No OTEL Collector | OTEL Collector as central ingestion hub |
| No long-term storage | Thanos for retention beyond single Prometheus TSDB |

The tutorial's Prometheus plugin config (`"prometheus": {}`) is identical to what this
stack uses — the only difference is where the scrape happens (standalone Prometheus vs.
OTEL Collector). The Grafana dashboard import (official ID 11719) works against both.

The tutorial also mentions **Elasticsearch** as an optional sink for aggregating daily
or weekly log frequency data from the `http-logger` plugin.

---

## OTEL Collector — Why It's the Right Hub

The OTEL Collector sits between APISIX and both backends (Thanos, Tempo). Two pipelines:

```
APISIX opentelemetry plugin (OTLP/HTTP :4318)
  └─ → [otlp receiver] → [batch] → [otlp/grpc exporter] → Tempo

APISIX prometheus plugin (:9091)
  └─ scrape ← [prometheus receiver] → [batch] → [prometheusremotewrite exporter] → Thanos Receive
```

Mock API services (`mock-apis/`) are uninstrumented plain HTTP servers — they produce
no traces or metrics. All telemetry originates at the APISIX layer.

**Why not just scrape with Prometheus directly?**

The Collector handles both pipelines with one deployed component, and its
`prometheusremotewrite` exporter speaks exactly what Thanos Receive expects.
Running a standalone Prometheus just to remote-write to Thanos adds an extra hop and
an extra container with no benefit in a single-node setup.

**Why not push OTLP metrics from APISIX directly?**

APISIX has no OTLP metrics plugin today (traces only). The Prometheus plugin is the
stable path. The Collector's `prometheus` receiver converts the scrape into OTEL
internal representation before forwarding — functionally identical to native push.

**The pull-scrape overhead is real but acceptable.** The OTEL Collector's prometheus
receiver uses roughly 1.5× the CPU of a pure OTLP push path (measured against a
native Prometheus scraper). For a thesis POC this is irrelevant. For production, the
tradeoff is: slightly higher Collector CPU vs. zero custom code and zero additional
components.

---

## Log Collection — Alloy over Promtail

This stack uses **Grafana Alloy** rather than Promtail. Grafana deprecated Promtail in 2024; Alloy is its designated successor with all `loki.*` functionality carried over. Promtail is now maintenance-only.

For this POC the functional difference is zero — same Docker socket discovery, same label extraction, same Loki push target. Alloy's config is more explicit (each step is a named, wired component) and adds a built-in debug UI at `:12345` that shows live component state.

| | Promtail | Grafana Alloy |
|---|---|---|
| Status | Deprecated | Actively developed |
| Config language | YAML (`scrape_configs`) | Alloy/River (component graph) |
| Signals | Logs only | Logs, metrics, traces, profiles |
| Output targets | Loki only | Loki, OTLP, Kafka, S3, … |
| Debug UI | None | Built-in at `:12345` |
| Positions tracking | `positions.yaml` file | Managed internally |

Migration is mechanical: `grafana/promtail` image → `grafana/alloy`, YAML config → Alloy component syntax, mount path updated. Grafana provides a `alloy convert --source-format=promtail` CLI tool for automated conversion.

For dashboard panel details and datasource dependencies, see the dashboard section in [observability-stack.md](observability-stack.md).
