# APISIX Metrics Reference

Complete reference for what the APISIX `prometheus` plugin exposes, what it doesn't, and how it compares to alternative gateways.

---

## Full metric inventory

All metrics use the `apisix_` prefix. The OTEL Collector's prometheus receiver applies OpenMetrics normalisation — counters gain a `_total` suffix in Thanos/Grafana queries (`apisix_http_status` → `apisix_http_status_total`).

### HTTP traffic

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `apisix_http_requests_total` | Gauge | _(none)_ | Unlabeled running total. Not useful for per-route breakdowns — use `apisix_http_status` instead |
| `apisix_http_status` | Counter | `code`, `route`, `matched_uri`, `matched_host`, `service`, `consumer`, `node` | Primary request counter. Consumer label is populated only when the request is authenticated |
| `apisix_http_latency` | Histogram | `type`, `route`, `service`, `consumer`, `node` | Three values for `type`: `request` (end-to-end), `apisix` (gateway-only), `upstream` (backend-only) |
| `apisix_bandwidth` | Counter | `type`, `route`, `service`, `consumer`, `node` | `type` is `ingress` or `egress`. Counts bytes, not requests |

### NGINX internals

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `apisix_nginx_http_current_connections` | Gauge | `state` | States: `active`, `reading`, `writing`, `waiting` |
| `apisix_nginx_metric_errors_total` | Counter | _(none)_ | Internal lua-prometheus library errors. Should stay at zero |

### Shared memory (NGINX dictionaries)

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `apisix_shared_dict_capacity_bytes` | Gauge | `name` | Total allocated size per shared dict |
| `apisix_shared_dict_free_space_bytes` | Gauge | `name` | Remaining free space |

Useful for capacity planning — if a shared dict fills up, plugin state (rate limiter counters, JWT blacklists) starts being evicted.

### Upstream health

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `apisix_upstream_status` | Gauge | `name`, `ip`, `port` | 1 = healthy, 0 = unhealthy. Populated by active health checks |

### Node metadata

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `apisix_node_info` | Gauge | `hostname`, `version` | Always 1. Labels carry node identity — useful for multi-node cardinality |

### etcd (etcd mode only — not relevant in standalone)

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `apisix_etcd_reachable` | Gauge | _(none)_ | 1 = reachable, 0 = unreachable |
| `apisix_etcd_modify_indexes` | Gauge | `key` | Tracks config change index per etcd key |

Not emitted in standalone (`config_provider: yaml`) mode. These would appear in a production etcd-backed deployment.

### Batch processor

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `apisix_batch_process_entries` | Gauge | _(none)_ | Remaining buffered entries when using batch plugins (http-logger, kafka-logger, etc.) |

Not visible in this POC — no batch logging plugins are active.

### Stream routes (TCP/UDP)

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `apisix_stream_connection_total` | Counter | `route` | Only populated when stream routes are defined. HTTP-only stack: zero |

### LLM / AI Gateway (APISIX 3.11+)

| Metric | Type | Labels |
|---|---|---|
| `apisix_llm_latency` | Histogram | `route_id`, `service_id`, `consumer`, `node`, `llm_model` |
| `apisix_llm_active_connections` | Gauge | `route`, `service`, `consumer`, `llm_model` |
| `apisix_llm_completion_tokens` | Counter | `route_id`, `service_id`, `consumer`, `node`, `llm_model` |
| `apisix_llm_prompt_tokens` | Counter | `route_id`, `service_id`, `consumer`, `node`, `llm_model` |

Added for the `ai-proxy` plugin. Not relevant unless APISIX is used as an LLM gateway.

---

## What this POC uses

| Metric | Used in dashboard | Notes |
|---|---|---|
| `apisix_http_status_total` | Gateway Health, Consumer Usage | Primary request counter — consumer and route attribution |
| `apisix_http_latency_bucket` | Gateway Health, Consumer Usage | All three `type` values queried |
| `apisix_bandwidth_total` | Consumer Usage | Both ingress and egress |
| `apisix_nginx_http_current_connections` | Gateway Health | All states |
| `apisix_upstream_status` | Gateway Health | Table panel, colour-coded |
| `apisix_http_requests_total` | Gateway Health (rate panel only) | Used only for the unlabeled total rate |

Not used: etcd metrics (standalone mode), LLM metrics, stream metrics, batch_process_entries, shared_dict metrics, node_info.

---

## Gaps — what APISIX metrics don't cover

| What's missing | Workaround |
|---|---|
| **Prometheus exemplars** | APISIX does not emit exemplars on histogram buckets, so clicking a metric data point cannot jump to a trace. Requires OTel SDK in upstream services |
| **Per-request body size** | Only total bytes (`apisix_bandwidth`) — no histogram of request/response body sizes |
| **Rate limiter state** | Current window counts from `limit-count` / `limit-req` are not exposed as metrics. In-memory state is opaque |
| **Cache hit/miss** | No caching plugin active in this stack, but even when using `proxy-cache`, no hit/miss metrics are exported |
| **Client geo / IP** | No label for client IP or geographic region without a custom plugin |
| **Auth failure breakdown** | 401s appear as `consumer=""` in `apisix_http_status_total`. No separate counter for "key not found" vs "key expired" vs "consumer restricted" |
| **Plugin execution time** | No per-plugin latency breakdown — `type="apisix"` aggregates all plugin overhead into one value |
| **OTLP metrics push** | No native OTLP metrics export. Only Prometheus pull (or StatsD/Datadog via separate plugins) |

---

## Alternative metrics output methods in APISIX

Beyond the `prometheus` plugin, APISIX can push metrics to other backends:

| Method | Plugin / mechanism | Notes |
|---|---|---|
| **StatsD / Datadog** | `datadog` plugin | Pushes metrics to a DogStatsD agent. Supports tags for consumer, route, status |
| **SkyWalking** | `skywalking` plugin | Pushes metrics + traces to Apache SkyWalking OAP. Alternative to the OTEL stack for teams already using SkyWalking |
| **OTLP metrics** | Not yet implemented | On the APISIX roadmap. Would allow pushing metrics directly to any OTEL Collector without a scrape cycle |
| **node-status plugin** | HTTP endpoint `/apisix/status` | Exposes basic NGINX status info in JSON. Not Prometheus format — for health checks, not metrics pipelines |

---

## Alternative gateways and their metrics

### Kong Gateway

Kong's prometheus plugin exposes a structurally similar set to APISIX: per-route/service/consumer request counters, a three-way latency histogram (request/kong/upstream), bandwidth, and upstream node health.

| Capability | Kong | APISIX |
|---|---|---|
| Consumer attribution | Yes (`consumer` label) | Yes (`consumer` label) |
| Latency decomposition | request / kong / upstream | request / apisix / upstream |
| Upstream health | `kong_upstream_target_health` | `apisix_upstream_status` |
| LLM metrics | Yes (v3.8+) | Yes (v3.11+) |
| Database reachability | `kong_datastore_reachable` | `apisix_etcd_reachable` (etcd mode) |
| Exemplars | No | No |
| Shared memory metrics | Yes (Lua VM + shared dicts) | Yes (`apisix_shared_dict_*`) |
| OTLP metrics push | No (prometheus pull only) | No (prometheus pull only) |

**Key difference:** Kong's open-source version routes all requests through a PostgreSQL or Cassandra database on the data path (for plugin config lookup). APISIX uses etcd but caches config in shared memory — no database hit per request. This affects both latency and the `type="apisix"` overhead baseline. Kong Enterprise adds workspace labels and more cluster-level metrics.

### Traefik

Traefik's metrics are scoped to three levels: entrypoint (listener), router, and service.

| Metric scope | Key metrics | Labels |
|---|---|---|
| Entrypoint | `traefik_entrypoint_requests_total`, `traefik_entrypoint_request_duration_seconds` | `code`, `method`, `protocol`, `entrypoint` |
| Router | `traefik_router_requests_total`, `traefik_router_request_duration_seconds` | `code`, `method`, `protocol`, `router`, `service` |
| Service | `traefik_service_requests_total`, `traefik_service_request_duration_seconds`, `traefik_service_server_up` | `code`, `method`, `protocol`, `service` |

**What Traefik doesn't have:**
- No consumer attribution — Traefik has no auth plugin that injects a consumer identity into metrics labels. A client identity requires custom middleware or external auth that sets a header, which Traefik cannot surface as a metric label
- No gateway-vs-upstream latency split. `request_duration_seconds` is end-to-end; there's no separate `type="traefik"` measurement
- No bandwidth counters (bytes in/out)
- No shared memory or internal state metrics

Traefik is the weakest of the three for API management observability. It is optimised for service ingress (automatic service discovery, TLS termination) rather than API gateway concerns (consumer attribution, per-route billing, quota enforcement). For a gateway that needs to answer "which client sent how many requests on which route", Traefik requires external tooling (log parsing, a WAF, or a sidecar).

### Envoy / Envoy Gateway

Envoy's data-plane statistics are extensive but structured differently from APISIX or Kong. Metrics are hierarchical: `envoy_cluster_<cluster_name>_upstream_rq_total`, `envoy_http_<conn_manager>_downstream_rq_<code>`, etc. The cluster name becomes part of the metric name rather than a label, which means Prometheus cardinality explodes with many upstreams.

| Capability | Envoy Gateway | APISIX |
|---|---|---|
| Consumer attribution | No (no auth plugin in data plane by default) | Yes |
| Latency decomposition | connection time, request time separately per cluster | request / apisix / upstream |
| Upstream health | Per-cluster health check counters | `apisix_upstream_status` gauge |
| Configuration complexity | High (xDS, Kubernetes CRDs) | Medium (YAML or Admin API) |
| Cardinality risk | High (cluster names in metric names) | Medium (route/consumer as labels) |
| Exemplars | Yes (via OTel SDK in Envoy itself) | No |

Envoy's control plane (Envoy Gateway) exports only internal operational metrics (xDS server, reconciler queue depth, etc.) — these are not HTTP traffic metrics. The data-plane Prometheus stats require the `/stats/prometheus` admin endpoint or a StatsD sink.

**Consumer attribution:** Envoy has no concept of an authenticated consumer in its base metrics. External auth (ext_authz) validates requests, but the consumer identity is not reflected back into metrics labels without custom Lua/WASM filters.

### Summary comparison

| Capability | APISIX | Kong | Traefik | Envoy |
|---|---|---|---|---|
| Consumer label in metrics | Yes | Yes | No | No |
| Gateway vs upstream latency split | Yes | Yes | No | Partial |
| Upstream node health gauge | Yes | Yes | Yes (server_up) | Yes (cluster counters) |
| Bandwidth counters | Yes | Yes | No | Yes |
| Prometheus native | Yes | Yes (plugin) | Yes (built-in) | Yes (admin endpoint) |
| Exemplars | No | No | No | Yes |
| OTLP metrics push | No | No | No | Yes (via OpenTelemetry) |
| Out-of-box complexity | Low | Medium | Low | High |

For the use case of this POC — per-consumer, per-route API observability with latency decomposition — APISIX and Kong are the two gateways that cover the requirements without custom code. Traefik and Envoy require external instrumentation to achieve the same consumer attribution.
