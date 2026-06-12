# Security Concerns

This document covers the security surface of the POC stack: what is exposed, what data leaks through observability signals, and what would need to change before any of this reaches a production environment.

---

## Host-exposed ports

Only four ports are bound to the host machine. Everything else (Thanos, Tempo, Loki, Promtail, internal APISIX ports) is confined to the internal `obs` Docker network.

| Port | Service | Who can reach it | Risk |
|---|---|---|---|
| **9080** | APISIX gateway | Anyone on host network | Intended exposure — this is the API |
| **9091** | APISIX Prometheus metrics | Anyone on host network | Information disclosure (see below) |
| **4318** | OTEL Collector OTLP/HTTP | Anyone on host network | Unauthenticated trace ingestion |
| **3000** | Grafana | Anyone on host network | Anonymous admin access |

### 9091 — Prometheus metrics endpoint

Bound to `0.0.0.0` inside the container (`export_addr.ip: "0.0.0.0"`) and mapped to the host. No authentication, no IP restriction.

What an unauthenticated request to `/apisix/prometheus/metrics` reveals:

- All route names (`traffic-events-nl-generic`, `route-guidance-generic`, ...)
- All consumer names as metric label values (`Flitsmeister_Client`, `TomTom_Integration`, ...)
- Upstream node IPs and ports (from `apisix_upstream_status`)
- Per-consumer request volumes and bandwidth — i.e., which client is how active
- NGINX internal connection pool states
- Exact latency distributions per route

In production: scrape should happen from within the private network (otel-collector → apisix:9091 internal), with the host port binding removed. An `ip-restriction` global rule or network-level firewall should block external access.

### 4318 — OTEL Collector OTLP/HTTP receiver

The collector accepts traces from APISIX over this port. It has no authentication. Because it is host-bound, anyone who can reach the machine can push arbitrary spans into Tempo.

Consequences:
- Trace data can be forged (fake routes, fake consumer names, fake latencies)
- A flood of trace pushes can fill Tempo storage or trigger memory_limiter drops on legitimate traces
- The port exposes that this host runs an observability collector, which maps internal service names

In production: OTLP ingestion should happen on an internal network only, never host-bound.

### 3000 — Grafana with anonymous admin

`docker-compose.yml` sets these environment variables on the Grafana service:
```
GF_AUTH_ANONYMOUS_ENABLED=true
GF_AUTH_ANONYMOUS_ORG_ROLE=Admin
```

Any browser hitting port 3000 gets full Admin access: read all dashboards, query all datasources (Thanos/Tempo/Loki), create/delete dashboards, modify datasource credentials, and trigger arbitrary Loki/PromQL/TraceQL queries against the backends.

This is intentional for the POC (no login friction), but it means the entire telemetry history is readable and queryable by anyone with network access. In production, anonymous access should be disabled and role-based auth enforced.

---

## APISIX-specific risks

### API key accepted as a query parameter

All four authenticated routes configure key-auth with:
```json
{
  "key-auth": {
    "header": "be-mobile-api-key",
    "query": "be-mobile-api-key"
  }
}
```

Accepting the key as a query parameter means it appears in:
- APISIX access logs (logged as part of the request URI)
- Browser history and bookmarks
- Server-side access logs of any reverse proxy upstream of APISIX
- HTTP `Referer` headers if a page with the key in its URL links elsewhere
- Any network capture or CDN log

The `proxy-rewrite` plugin removes `be-mobile-api-key` from the forwarded request, so the upstream services never see it. But the damage happens before rewriting — the key is already in logs at the gateway layer.

**Mitigation**: remove `query: be-mobile-api-key` from the key-auth config. Force header-only delivery.

### CORS wildcard on authenticated routes

`traffic-events-nl-generic` and `route-guidance-generic` both set:
```json
{
  "cors": {
    "allow_origins": "*",
    "allow_credential": false
  }
}
```

`allow_credential: false` prevents browsers from sending cookies or auth headers cross-origin, which limits CSRF risk. However, `allow_origins: "*"` combined with key-auth via a query parameter is still a concern: a malicious page can construct a URL with the API key in the query string and trigger cross-origin requests that succeed.

In production: lock `allow_origins` to the specific domains that legitimately call these APIs.

### node-status plugin in the allowlist

`config.yaml` enables the `node-status` plugin in its allowlist (static YAML file):
```
plugins:
  - node-status
```

If activated on any route or as a global rule, `node-status` exposes server internals at `/apisix/status` with no authentication by default: NGINX version, connection counts, load averages, and uptime. It is not wired to any route in `apisix.yaml` in this POC, so it is dormant — but it is one misconfiguration away from being an unauthenticated information endpoint.

**Mitigation**: remove `node-status` from the plugins allowlist unless actively needed.

### always_on trace sampling

All five routes use `sampler: name: always_on`. Every single request generates a trace span exported to Tempo. This means:

- Full request URLs (including any sensitive query parameters) are recorded in traces
- Consumer usernames appear as span attributes via the `X-Consumer-Username` header passed to upstreams
- Request/response timing is permanently stored
- A traffic spike → proportional Tempo storage spike (no shedding)

For a POC this is correct — you want to see all traces. In production: switch to `parent_base` (respect upstream sampling decision) or `trace_id_ratio` with a low fraction, and add tail-based sampling in the OTEL Collector to keep only error and slow-request traces.

### Plaintext API keys in apisix.yaml

Consumer credentials are stored in cleartext. The Admin API equivalent makes the problem visible:
```json
{
  "username": "Flitsmeister_Client",
  "plugins": {
    "key-auth": { "key": "flit-key-abc123" }
  }
}
```

`apisix.yaml` is committed to git, so all four API keys are in version history. In production: use a secrets manager (Vault, AWS Secrets Manager, Kubernetes Secrets) and inject credentials at deploy time. The etcd-backed Admin API approach allows keys to be written via `PUT /apisix/admin/consumers/{name}` without them appearing in static files.

### No rate limiting

None of the routes configure `limit-req`, `limit-count`, or `limit-conn`, despite these plugins being in the allowlist. An authenticated consumer can send unlimited requests. Combined with always_on tracing, a single misbehaving client can:

- Exhaust upstream service capacity
- Fill Tempo storage
- Skew metrics dashboards (one consumer drowning out all others in graphs)

---

## Sensitive data in telemetry signals

### Traces (APISIX → Tempo)

Each APISIX span includes:
- Full HTTP URL path (e.g. `/traffic-events/v2/nl-generic/events?bbox=...`)
- HTTP method and response status code
- `apisix.route_name` attribute — route name
- `net.peer.ip` / `net.host.name` — source IP
- Traceparent propagated downstream, so upstreams can link their own spans

What it does **not** include by default:
- Request or response body content
- The `be-mobile-api-key` header value (APISIX strips auth headers before tracing the outgoing span)

Risk: if query parameters contain PII (coordinates, user identifiers, vehicle IDs), those appear verbatim in the trace URL. Tempo has no field-level redaction. Coordinate data is present in the POST body of `/events/query` — body content is not traced by the opentelemetry plugin by default, so this specific case is safe, but it is worth verifying for each endpoint.

### Metrics (APISIX → Thanos)

The `consumer` label on `apisix_http_status_total` and `apisix_bandwidth_total` exposes per-consumer request volumes and byte counts to anyone who can query Thanos or read the Prometheus scrape output. This is intentional for the consumer attribution dashboard, but in a multi-tenant context it means one consumer's usage patterns are visible to operators of the metrics backend.

The `node` label on `apisix_http_status_total` contains the upstream IP:port (`traffic-events-service:8080`), leaking internal service topology to anyone with metrics access.

### Logs (zerolog → Loki)

Mock services log:
```json
{"level":"info","service":"traffic-events-service","method":"GET","path":"/events","status":200,"duration_ms":47,"trace_id":"...","correlation_id":"..."}
```

The API key is not logged (it is stripped by `proxy-rewrite` before reaching the upstream). Path parameters are logged (e.g. `/events/42`), which could expose resource identifiers. Loki has no automatic PII scrubbing; it stores whatever Promtail ships verbatim.

---

## What the POC intentionally skips

These are known gaps, documented here for completeness, not oversights:

| Gap | Production approach |
|---|---|
| No TLS anywhere | TLS termination at load balancer or APISIX itself (`ssl` objects in apisix.yaml) |
| No mTLS between services | Service mesh (Istio/Linkerd) or APISIX upstream TLS verification |
| No network egress control | Internal services should not be able to reach the internet |
| Grafana has no login | OIDC or SAML integration; Grafana role-based access per datasource |
| Secrets in git | Secrets manager + deploy-time injection |
| No audit logging | APISIX access log plugin (`file-logger` or `kafka-logger`) to a separate sink |
| Single APISIX instance | HA deployment with multiple data plane nodes behind a load balancer |
| No backup for Thanos/Tempo/Loki data | Object storage backend (S3/GCS) instead of local volumes |
