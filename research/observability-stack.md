# Observability Stack — Component Responsibilities

## Signal types

| Signal | What it answers | Backend |
|--------|----------------|---------|
| **Metrics** | "What is the system doing right now / over time?" — rates, counts, histograms | Thanos |
| **Traces** | "What happened inside a single request?" — spans, durations, attributes | Tempo |
| **Logs** | "What did the system say?" — structured per-request output from upstream services | Loki |

---

## APISIX

**Produces:** metrics + traces  
**Does NOT produce:** structured logs (stdout only, not shipped)

### Metrics (via `prometheus` plugin)
Scraped by OTEL Collector from `:9091/apisix/prometheus/metrics`. Full metric inventory, label definitions, and gap analysis in [apisix-metrics-reference.md](apisix-metrics-reference.md).

Key metrics used by the dashboards: `apisix_http_status_total` (per-route/consumer request counts), `apisix_http_latency_bucket` (latency with `type` label splitting gateway vs upstream overhead), `apisix_bandwidth_total` (bytes in/out), `apisix_nginx_http_current_connections` (connection pool), `apisix_upstream_status` (node health).

`prefer_name: true` on the prometheus plugin substitutes route `name` for raw ID in the `route` label.

### Traces (via `opentelemetry` plugin)
Exported as OTLP/HTTP to OTEL Collector `:4318`. One span per request containing:
- HTTP method, status code, route name
- `consumer.username` (from the authenticated consumer, set via `proxy-rewrite` headers)
- `X-Correlation-ID` (from the `request-id` plugin)

Tracing is entirely the gateway's responsibility. The mock API services are plain HTTP
servers with no OTel instrumentation — APISIX is the only trace producer in this stack.

Requires both `plugin_attr.opentelemetry` in `config.yaml` **and** a `plugin_metadata`
entry in `apisix.yaml` (see gotchas in `poc-findings.md`).

---

## Mock API services (`mock-apis/`)

**Produces:** structured logs (zerolog JSON to stdout)  
**Does NOT produce:** metrics, traces  
**Role:** simulate realistic upstream latency and response shapes

Three services:

| Service | Purpose | Auth |
|---------|---------|------|
| `traffic-events-service` | Traffic event CRUD, geo-bounded queries | `be-mobile-api-key` + consumer whitelist |
| `route-guidance-service` | Route calculation, alternatives, cache lookup | `be-mobile-api-key` + consumer whitelist |
| `echo-service` | Request inspection, configurable status codes | None (open) |

All three use a shared logging middleware pattern: a `statusWriter` wraps each request,
captures the response code, and emits one zerolog JSON line per request containing
`method`, `path`, `status`, `duration_ms`, `trace_id` (parsed from the APISIX-injected
`Traceparent` header), and `correlation_id` (from `X-Correlation-Id`). Level is derived
from status: 5xx → `error`, 4xx → `warn`, otherwise `info`.

The `trace_id` value in log lines is the 32-hex trace identifier from the `Traceparent`
header APISIX injects into upstream requests. It matches the trace visible in Tempo,
enabling log-to-trace correlation via the Loki derived field.

Metrics and traces for these services come from the APISIX layer, not from the services
themselves. Internal service spans are intentionally absent.

---

## OTEL Collector

**Role:** telemetry router — no storage, no query API  
**Receives:** metrics (prometheus scrape) + traces (OTLP/HTTP from APISIX)  
**Forwards:** metrics → Thanos Receive, traces → Tempo

```
APISIX :9091          ──scrape──►  prometheus receiver  ──remote_write──► Thanos Receive
APISIX :4318 (OTLP)  ──push──►    otlp receiver         ──otlp/grpc──►   Tempo
```

Side effects of the prometheus receiver:
- Appends `_total` suffix to counter metrics (OpenMetrics normalisation) — `apisix_http_status` becomes `apisix_http_status_total`
- Adds OTEL resource labels (`service_name`, `instance`, `job`) to every metric series

---

## Thanos (Receive + Query)

**Stores and serves:** metrics only  
**No traces, no logs**

| Component | Role |
|-----------|------|
| **Thanos Receive** | Accepts remote_write from OTEL Collector, writes to local TSDB |
| **Thanos Query** | Prometheus-compatible query API (`/api/v1/query`) — Grafana datasource |

In production Be-Mobile uses a full Thanos deployment with Compactor, Store Gateway,
and object storage for long-term retention. This POC runs Receive + Query only.

---

## Tempo

**Stores and serves:** traces only  
**No metrics, no logs**

Single container, local filesystem storage. Receives OTLP/gRPC from the OTEL Collector.

Grafana queries it via the Tempo datasource for:
- Trace search by `consumer.username`, `http.route`, duration, status
- Individual trace waterfall view (span timeline)
- Identifying which specific requests caused a latency spike visible in Thanos metrics

Traces contain only the APISIX gateway span — no child spans from upstream services.
The gateway span captures full round-trip latency, upstream response time
(`type="upstream"`), consumer identity, and correlation ID.

**Production swap:** replace the single-node Tempo container with whatever trace backend
is in use (Jaeger, Elasticsearch/APM, managed Tempo). Only the OTEL Collector exporter
address changes.

---

## Loki

**Stores and serves:** logs only  
**No metrics, no traces**

Single container, local filesystem storage, 72h retention. Receives log streams pushed
by Alloy via the Loki push API (`/loki/api/v1/push`).

Streams are indexed by labels: `service`, `container`, `level`. The `level` label is
extracted from the `level` key in zerolog JSON output — containers that don't emit JSON
(APISIX, Tempo, etc.) have no `level` label and are excluded from level-filtered queries.

Requires `user: root` in the Docker Compose service definition because the named volume
is created root-owned and Loki's process needs to create subdirectories in it.

---

## Alloy

**Role:** log collector/shipper — no storage  
**Discovers:** containers via Docker socket (`/var/run/docker.sock`)  
**Filters:** only containers in the `apisix-dashboards` compose project  
**Forwards:** log streams → Loki push API

Pipeline per log line:
1. `stage.json`: extract `level` field from the log body
2. `stage.labels`: promote `level` to a Loki stream label
3. Push to Loki with labels `service` (from compose service name) and `container`

Alloy replaces Promtail, which was deprecated by Grafana in 2024. See [log-collector-comparison.md](log-collector-comparison.md).

---

## Grafana

**Queries:** Thanos (metrics) + Tempo (traces) + Loki (logs)  
**Does NOT store** anything — pure query/visualisation layer

| Datasource | UID | What it powers |
|------------|-----|---------------|
| Thanos | `thanos` | All metric panels (dashboards 1–3) |
| Tempo | `tempo` | Trace search + waterfall; `trace_id` link target from Loki |
| Loki | `loki` | Log panels (dashboard 4); `trace_id` derived field → Tempo link |

Tempo datasource is configured with `tracesToLogsV2` pointing at Loki (filter by trace ID), so a trace in Tempo can jump to its associated log lines. The reverse direction (Loki log line → Tempo trace) is handled by the Loki datasource's `derivedFields` regex on `trace_id`.

---

## Dashboards

Datasource UIDs are defined in `config/grafana/provisioning/datasources/datasources.yaml`. File-provisioned dashboards require literal UID strings — `${DS_THANOS}`-style import placeholders only work for manual UI imports.

| UID | Type | Backed by |
|-----|------|-----------|
| `thanos` | prometheus | Thanos Query `:10902` |
| `tempo` | tempo | Tempo `:3200` |
| `loki` | loki | Loki `:3100` |

### Gateway Health (`apisix-gateway-health`)

**Audience:** ops / on-call · **Datasource:** `thanos`

Variables: `interval` (1m / 5m / 15m rate window)

| Panel | Type | Key metric |
|---|---|---|
| Request Rate | timeseries | `apisix_http_requests_total` |
| HTTP Error Rates | timeseries | `apisix_http_status_total{code=~"4..\|5.."}` |
| Latency P50/P95/P99 | timeseries | `apisix_http_latency_bucket` × three `type` values |
| Active Connections | stat | `apisix_nginx_http_current_connections{state="active"}` |
| Gateway P95 Latency | stat | `apisix_http_latency_bucket{type="apisix"}` |
| Upstream P95 Latency | stat | `apisix_http_latency_bucket{type="upstream"}` |
| Upstream Node Health | table | `apisix_upstream_status` — colour-coded 1/0 |
| Ingress / Egress Bandwidth | timeseries | `apisix_bandwidth_total` split by `type` |
| NGINX Connection States | timeseries | all states |

### API & Consumer Usage (`apisix-consumer-usage`)

**Audience:** product / API management · **Datasource:** `thanos`

Variables: `interval`, `route` (`label_values(apisix_http_status_total, route)`), `consumer` (same pattern) — both auto-populate from live metric labels.

| Panel | Type | Key metric |
|---|---|---|
| Top 10 Routes by Volume | barchart | instant ranked query |
| Top Consumers by Traffic | barchart | by consumer, instant |
| Request Rate by Route | timeseries | filtered to `$route` |
| P95 Latency by Route | timeseries | `type="request"`, filtered |
| Error Rate by Route | table | 4xx+5xx breakdown |
| Consumer Error Rate (5xx) | table | consumer × route matrix |

Requires authenticated requests so APISIX can set the `consumer` label. Unauthenticated requests appear with `consumer=""`.

### Route Cluster Overview (`apisix-route-cluster`)

**Audience:** platform ops · **Datasource:** `thanos`

Same variables and data requirements as Consumer Usage. Repeating row layout — one panel row per selected `$route` showing request rate, latency, error rates, and gateway vs upstream split.

### Service Logs (`service-logs`)

**Audience:** developers / ops · **Datasource:** `loki`

Variables: `service` (Loki label values — JSON query `{ type: 1, label: "service" }`, **not** Prometheus `label_values()` syntax), `level` (static: error/warn/info), `search` (free-text regex).

| Panel | LogQL |
|---|---|
| Log Rate by Service | `sum by (service) (rate({service=~"$service", level=~"$level"}[$__interval]))` |
| Log Rate by Level | same, grouped by level — error=red, warn=orange, info=green |
| Log Stream | `{service=~"$service", level=~"$level"} \|~ "(?i)$search"` |

`trace_id` in log lines is a Loki derived field — clicking it navigates to the matching Tempo trace. The value comes from the `Traceparent` header APISIX injects into upstream requests, parsed and logged by the mock service middleware.

### Gateway Internals (`apisix-gateway-internals`)

**Audience:** platform ops · **Datasource:** `thanos`

Covers metrics not exposed by the health or usage dashboards: shared memory pressure, NGINX worker internals, collector health, and per-upstream-node traffic distribution.

Variables: `interval` (1m / 5m / 15m)

| Panel | Type | Key metric |
|---|---|---|
| Node Info | table | `apisix_node_info` — hostname, version labels |
| Metric Collection Errors | stat | `apisix_nginx_metric_errors_total` — red if non-zero |
| Batch Processor Queue | stat | `apisix_batch_process_entries` — buffered log entries |
| Active Connections | stat | `apisix_nginx_http_current_connections{state="active"}` |
| Shared Dict Utilization | bargauge | `1 - free/capacity` per dict, thresholds 70%/90% |
| Shared Dict Capacity vs Free | table | `apisix_shared_dict_capacity_bytes` + `apisix_shared_dict_free_space_bytes` |
| NGINX Connection States | timeseries | active / waiting / reading / writing |
| Request Rate by Upstream Node | timeseries | `node` label — detects uneven load distribution |
| P95 Upstream Latency by Node | timeseries | per-node histogram_quantile — detects degraded instances |
| Bandwidth by Upstream Node | timeseries | ingress + egress bytes/s per node |
| Upstream Node Health | table | `apisix_upstream_status` — colour-coded healthy/unhealthy |

### SLA & Errors (`apisix-sla-errors`)

**Audience:** ops / SRE · **Datasource:** `thanos`

SLO compliance stats, error breakdown at route/consumer/URI granularity, P99 latency per consumer, and bandwidth efficiency ratio.

Variables: `interval` (1m / 5m / 15m)

| Panel | Type | Key metric / query |
|---|---|---|
| Overall Success Rate | stat | `rate(apisix_http_status_total{code=~"2.."}) / rate(apisix_http_status_total)` |
| SLO — Requests < 200 ms | stat | bucket ratio `le="200"` / `le="+Inf"` on `type="request"` |
| SLO — Requests < 500 ms | stat | same, `le="500"` |
| SLO — Requests < 1 s | stat | same, `le="1000"` |
| 4xx Rate by Route | timeseries | `apisix_http_status_total{code=~"4.."}` by route + code |
| 5xx Rate by Route | timeseries | `apisix_http_status_total{code=~"5.."}` by route |
| P99 Latency by Consumer | table | `histogram_quantile(0.99, …)` by consumer |
| Consumer 5xx Error Rate | table | 5xx req/s per consumer × route |
| Error Rate by URI | timeseries | `matched_uri` label — endpoint-level error visibility |
| Avg Bytes/Request by Consumer | timeseries | egress bandwidth / request count per consumer |
| Top URIs by P99 Latency | barchart | `topk(10, histogram_quantile(0.99, …))` by `matched_uri` |
| Top URIs by Error Rate | barchart | `topk(10, rate(…{code=~"[45].."}) )` by `matched_uri` |

### Cross-dashboard data flow

```
APISIX prometheus plugin (:9091)
  └─► OTEL Collector → Thanos Receive → Thanos Query
        └─► dashboards 1, 2, 3, 5, 6

APISIX opentelemetry plugin → OTEL Collector → Tempo
  └─► Grafana Explore + trace_id links from dashboard 4

mock-api stdout (zerolog JSON) → Docker → Alloy → Loki
  └─► dashboard 4
```

---

## What is NOT covered by this stack

| Gap | Production solution |
|-----|-------------------|
| Service-internal span breakdown | OTel SDK in upstream services (child spans per DB call, etc.) |
| Trace-to-metric exemplars | OTel SDK exemplar API |
| Distributed rate limiting state | Redis (`limit-count` is in-memory in this POC) |
| Long-term metric retention | Thanos Compactor + Store Gateway + object storage |
| Tail-based trace sampling | OTEL Collector `tail_sampling` processor (POC uses `always_on`) |
