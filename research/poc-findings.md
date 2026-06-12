# POC Findings — APISIX Observability Stack

Graduate thesis proof-of-concept for Be-Mobile CVP gateway.  
Stack validated: 2026-04-30. Working directory: `graduate-thesis/`.

---

## Stack Topology

```
                        ┌────────────────────────────────────────────────────┐
                        │               docker-compose network                │
                        │                                                     │
  curl / browser  ──►  APISIX :9080  ──►  traffic-events-service  (zerolog) │
  (be-mobile-api-key)   │  (standalone,    route-guidance-service  (zerolog) │
                        │   no etcd)       echo-service            (zerolog) │
                        │                           │                         │
                        │   :9091 ──► OTEL Collector├──► Thanos ─────────────┤──► Grafana
                        │              │ prometheus  │    Receive/             │    :3000
                        │              │ receiver    │    Query                │
                        │              │             │                         │
                        │              └──► Tempo ◄──┘      Alloy ◄──────────┤── Docker sock
                        │                   (OTLP/gRPC)      │                │
                        │   :4318 ────► otlp receiver         └──► Loki ──────┤──► Grafana
                        └────────────────────────────────────────────────────┘     Loki DS
```

**Services:** apisix · traffic-events-service · route-guidance-service · echo-service · otel-collector · thanos-receive · thanos-query · tempo · loki · alloy · grafana  
**Eliminated:** etcd (APISIX standalone mode), httpbin (replaced by echo-service)

All three observability pillars are covered:
- **Metrics** — APISIX prometheus plugin → OTEL Collector → Thanos
- **Traces** — APISIX opentelemetry plugin → OTEL Collector → Tempo
- **Logs** — mock API services (zerolog) → Docker → Alloy → Loki

---

## Validated Capabilities

### 1. Consumer Attribution in Metrics

APISIX injects the `consumer` label on every metric when a request is authenticated.
Live metrics example from `make metrics`:

```
apisix_http_status{code="200",route="traffic-events-nl-generic",
  service="",consumer="Flitsmeister_Client",...} 14
apisix_http_status{code="403",route="route-guidance-generic",
  service="",consumer="TomTom_Integration",...} 12
apisix_http_status{code="401",route="traffic-events-nl-generic",
  service="",consumer="",...} 3
```

Key observations:
- `consumer=""` only on 401s (auth failed before consumer was identified)
- 403s from `consumer-restriction` carry the consumer name — you can see exactly which consumer was denied and on which route
- `prefer_name: true` on the `prometheus` plugin makes `route` show the route `name` field instead of the raw ID

### 2. Latency Decomposition

`apisix_http_latency_bucket` carries a `type` label with three values:

| type | meaning |
|------|---------|
| `request` | full end-to-end (client ↔ APISIX ↔ upstream) |
| `apisix` | gateway-only overhead (auth, plugins, rewrite) |
| `upstream` | backend response time only |

This directly answers the thesis question about separating gateway overhead from service
latency. Observed values in POC:
- `type="apisix"` P95 ≈ 1–3 ms (plugin chain adds negligible overhead)
- `type="upstream"` P95 ≈ 50–300 ms (artificial jitter in mock services)

### 3. OTEL Collector as Metrics Pipeline

The collector scrapes APISIX's Prometheus endpoint and remote-writes to Thanos Receive.  
This mirrors the likely production path (Prometheus scrape → remote_write → Thanos)
without deploying a full Prometheus instance per gateway node.

Confirmed working: metrics appear in Grafana with <30s lag from request time.

### 4. Distributed Traces (Tempo)

APISIX exports OTLP/HTTP traces to the collector on `:4318` via the `opentelemetry`
plugin. The collector forwards via `otlp/grpc` to Tempo.

Each trace contains a single APISIX gateway span with:
- HTTP method, URL, status code
- Route name (`route-guidance-generic`, `traffic-events-nl-generic`, etc.)
- `consumer.username` attribute (set from `X-Consumer-Username` header injected by `proxy-rewrite`)
- `X-Correlation-ID` for cross-referencing with client-side logging
- Full round-trip latency including the `type="upstream"` split

No child spans from upstream services — mock APIs are uninstrumented. Gateway-level
trace granularity is sufficient for validating the APISIX → OTEL Collector → Tempo →
Grafana pipeline.

### 5. Echo Service (no-auth route)

Replaces httpbin. A minimal Go HTTP server (`mock-apis/echo-service`) with two endpoints:
- `GET /echo` — reflects method, headers, query params as JSON
- `GET /status/{code}` — returns the requested HTTP status code

Useful for verifying APISIX header injection (`X-Consumer-Username`, `X-Correlation-ID`,
`Traceparent`) without needing an API key. The `Traceparent` header visible in echo
responses confirms APISIX is injecting W3C trace context into upstream requests.

### 6. Structured Logs (Loki + Alloy)

All three mock API services emit zerolog JSON to stdout. Alloy discovers them via
the Docker socket and ships logs to Loki with stream labels `service`, `container`,
and `level` (extracted from the zerolog `level` field).

Each log line includes:
- `method`, `path`, `status`, `duration_ms` — per-request summary
- `trace_id` — 32-hex trace ID parsed from the `Traceparent` header APISIX injects
- `correlation_id` — from the `X-Correlation-Id` header, when present
- Level derived from status: 5xx → `error`, 4xx → `warn`, otherwise `info`

The `trace_id` field is a derived field in the Loki datasource config — clicking it in
the Grafana log stream navigates to the matching trace in Tempo. This closes the loop
between the log pillar (Loki) and the trace pillar (Tempo).

---

## Configuration Gotchas Discovered

### 1. `status: 1` is invalid in upstream definitions (standalone YAML)

APISIX standalone YAML allows `status: 1` on routes and consumers but rejects it on upstreams with:
```
additional properties forbidden, found status
```
Remove `status:` from all `upstreams:` entries. Routes/consumers: keep it.

### 2. `consumer-restriction` must be explicitly listed in `config.yaml`

The `plugins:` array in `config.yaml` is an allowlist. If `consumer-restriction` is omitted, APISIX logs `unknown plugin` and silently skips it — whitelists are not enforced. Verify it appears in the list:
```
plugins:
  - consumer-restriction
```

### 3. `plugin_attr.opentelemetry` alone is not sufficient (v3.15 standalone)

Setting `plugin_attr.opentelemetry` in `config.yaml` sets global defaults but does not
activate the plugin. APISIX v3.15 standalone also requires a `plugin_metadata` section
in `apisix.yaml`:

`PUT /apisix/admin/plugin_metadata/opentelemetry`
```json
{
  "trace_id_source": "x-request-id",
  "resource": {
    "service.name": "apisix"
  },
  "collector": {
    "address": "otel-collector:4318",
    "request_timeout": 3
  },
  "batch_span_processor": {
    "max_queue_size": 1024,
    "batch_timeout": 2,
    "max_export_batch_size": 16
  }
}
```

Without this, routes with `opentelemetry: { sampler: { name: always_on } }` produce no traces.

### 4. File-provisioned Grafana dashboards reject `${DS_THANOS}` datasource UID

`${DS_THANOS}` is the Grafana UI import placeholder. It works when a user imports a
JSON manually through the UI (Grafana resolves it interactively). It does NOT work for
dashboards loaded via `provisioning/dashboards/`.

File-provisioned dashboards require the literal datasource UID:
```json
"datasource": { "type": "prometheus", "uid": "thanos" }
```
The `uid` must match the `uid` field in the datasource provisioning YAML (`provisioning/datasources/`).

### 5. `$interval` vs `${interval}` in instant Prometheus queries

Custom template variables named `interval` shadow Grafana's internal `$__interval` in
some query contexts. In `instant: true` bar chart panels, `[$interval]` resolves to
`[]` (empty), causing a PromQL parse error:
```
bad_data: 1:75: parse error: unexpected "]"
```
Fix: use `[${interval}]` — the curly brace form forces explicit template variable lookup.

### 6. Loki container requires `user: root`

The `loki_data` named volume is created root-owned by Docker. Loki tries to create
`/var/loki/rules` inside it and fails with `permission denied` when running as its
default non-root user, causing a crash loop:
```
mkdir /var/loki/rules: permission denied
error initialising module: ruler-storage
```
Fix: add `user: root` to the loki service in `docker-compose.yml`. Thanos Receive has
the same issue and the same fix.

### 7. Loki variable query format differs from Prometheus

`label_values(service)` is Prometheus/Thanos syntax. Using it as the query string for a
template variable backed by the Loki datasource fails silently — the variable populates
with no values and all panels that depend on it show "error in this plugin".

Fix: use a Loki-specific JSON query object:
```json
"query": {
  "type": 1,
  "label": "service",
  "stream": "",
  "refId": "LokiVariableQueryEditor-VariableQuery"
}
```
`type: 1` = LabelValues (vs `type: 0` = LabelNames).

---

## Tempo Assessment

### Keep for this POC

Tempo adds one container and gives visibility that metrics alone cannot provide:
- Identify which specific requests are slow (not just aggregate P95)
- See exactly where gateway latency is spent inside a request
- Correlate a 5xx alert with a specific trace ID (via `X-Correlation-ID` returned to clients)
- Validate that `X-Consumer-Username` and `traceparent` propagate correctly to upstream services

For the thesis assignment ("assess trace visibility for gateway/upstream latency"),
Tempo provides evidence that distributed tracing works end-to-end through the stack,
even without service-level instrumentation.

### Production swap

In production, the only change is the Tempo endpoint in the OTEL Collector config.
Be-Mobile's existing trace backend replaces the single-node Tempo container. No changes
to APISIX configuration.

### What Tempo does NOT replace

Tempo has no metrics storage. It cannot answer "what was P95 latency over the last 24h"
— that stays in Thanos. The two backends are complementary, not competing.

---

## Architectural Recommendation

### Preferred Integration Model

```
APISIX (prometheus plugin)
  └─► OTEL Collector (prometheus receiver)
        └─► remote_write ─► Thanos Receive
                                └─► Thanos Query ─► Grafana

APISIX (opentelemetry plugin, OTLP/HTTP)
  └─► OTEL Collector (otlp receiver)
        └─► existing trace backend (Tempo / Jaeger / Elasticsearch)

Upstream services (zerolog or equivalent structured logger)
  └─► stdout ─► log shipper (Alloy / Fluent Bit / Vector)
        └─► Loki / Elasticsearch ─► Grafana
```

All three observability pillars are covered with no OTel SDK required in upstream services. Metrics and traces originate at the APISIX gateway layer. Logs originate in the upstream services themselves but require no instrumentation beyond structured logging to stdout.

### Trade-offs

| | OTEL Collector approach | Direct Prometheus scrape |
|--|--|--|
| Backend independence | swap exporters, zero APISIX change | Prometheus-specific |
| Sampling / filtering | built-in processors | not possible |
| Operational complexity | one extra container | simpler for small deployments |
| Metric fidelity | identical (scrape → remote_write) | identical |
| Trace support | OTLP native | none |

### Production Considerations

1. **`prefer_name: true` stability** — route `name` fields must be stable across deploys. Renaming a route changes the `route` label, breaking Grafana queries and alert rules. Use versioned names (`traffic-events-v2-nl-generic`) and treat them as stable identifiers.

2. **Rate limiting** — `limit-count` with `redis` policy for distributed rate limiting across gateway replicas. The in-memory policy used in this POC does not share state between APISIX pods.

3. **Tail-based sampling** — the POC uses `always_on` sampling (100%). Production should use the OTEL Collector's tail sampling processor to keep error traces and slow traces while dropping routine traffic.

4. **Exemplars** — APISIX 3.x does not emit Prometheus exemplars natively. If trace-to-metric correlation (clicking a data point to jump to a trace) is needed, it requires service-level OTel SDK instrumentation.

5. **Thanos compaction and retention** — this POC runs only Thanos Receive + Query (no Compactor, no Store Gateway). Production needs the Compactor for downsampling and the Store Gateway for long-term object storage queries.

6. **`plugin_metadata` in Admin API mode** — in production (etcd-backed), `plugin_metadata` is set via `PUT /apisix/admin/plugin_metadata/opentelemetry`. The standalone YAML `plugin_metadata:` section used in this POC is the direct equivalent.

7. **Log shipper at scale** — Alloy works well for single-node Docker Compose. In a Kubernetes deployment, the standard approach is a DaemonSet-based log shipper (Alloy, Fluent Bit) reading from node-level log files. The zerolog-to-Loki pipeline is the same; only the collection mechanism changes.
