# APISIX Observability Plugin Configuration

Covers how to enable the `prometheus` and `opentelemetry` plugins at each scope, and how the three global configuration mechanisms relate to each other.

Both plugins support four scopes: **Route**, **Service**, **Plugin Config**, **Global Rule**. Consumers and consumer-groups are not supported by either plugin.

---

## Prometheus plugin by scope

`prefer_name: true` is the only meaningful per-scope attribute — it uses the route/service `name` as the metric label instead of the auto-generated ID.

### Route

`PUT /apisix/admin/routes/traffic-events-nl-generic`
```json
{
  "id": "traffic-events-nl-generic",
  "name": "traffic-events-nl-generic",
  "uri": "/traffic-events/v2/nl-generic*",
  "upstream_id": "traffic-events-service",
  "plugins": {
    "prometheus": { "prefer_name": true }
  }
}
```

### Service

`PUT /apisix/admin/services/traffic-events-service`
```json
{
  "id": "traffic-events-service",
  "name": "traffic-events-service",
  "upstream_id": "traffic-events-service",
  "plugins": {
    "prometheus": { "prefer_name": true }
  }
}
```

Routes then reference the service instead of carrying the plugin themselves:

`PUT /apisix/admin/routes/traffic-events-nl-generic`
```json
{
  "id": "traffic-events-nl-generic",
  "name": "traffic-events-nl-generic",
  "uri": "/traffic-events/v2/nl-generic*",
  "service_id": "traffic-events-service"
}
```

### Plugin Config

`PUT /apisix/admin/plugin_configs/observability`
```json
{
  "id": "observability",
  "plugins": {
    "prometheus": { "prefer_name": true },
    "opentelemetry": { "sampler": { "name": "always_on" } }
  }
}
```

Attach with `PATCH /apisix/admin/routes/{id}`:
```json
{ "plugin_config_id": "observability" }
```

### Global Rule

Enables metrics on all routes in one shot. If a route also defines `prometheus`, the route config wins (`run_policy = "prefer_route"`).

`PUT /apisix/admin/global_rules/1`
```json
{
  "plugins": {
    "prometheus": { "prefer_name": true }
  }
}
```

---

## OpenTelemetry plugin by scope

The otel plugin has no `run_policy` — APISIX's default merge order applies (Route > Plugin Config > Service). The collector address and batch settings live in plugin metadata (see below); per-scope config controls sampling and span attributes only.

### Route

`PUT /apisix/admin/routes/traffic-events-nl-generic`
```json
{
  "id": "traffic-events-nl-generic",
  "name": "traffic-events-nl-generic",
  "uri": "/traffic-events/v2/nl-generic*",
  "upstream_id": "traffic-events-service",
  "plugins": {
    "opentelemetry": {
      "sampler": { "name": "always_on" },
      "additional_attributes": ["http.host", "route_id"]
    }
  }
}
```

### Service

`PUT /apisix/admin/services/traffic-events-service`
```json
{
  "id": "traffic-events-service",
  "name": "traffic-events-service",
  "upstream_id": "traffic-events-service",
  "plugins": {
    "opentelemetry": { "sampler": { "name": "always_on" } }
  }
}
```

### Plugin Config

See prometheus Plugin Config above — both plugins share the same `plugin_config_id` bundle.

### Global Rule

`PUT /apisix/admin/global_rules/1`
```json
{
  "plugins": {
    "opentelemetry": { "sampler": { "name": "always_on" } }
  }
}
```

For production, replace `always_on` with a ratio sampler:

```json
{
  "plugins": {
    "opentelemetry": {
      "sampler": {
        "name": "parent_base",
        "options": {
          "root": {
            "name": "trace_id_ratio",
            "options": { "fraction": 0.1 }
          }
        }
      }
    }
  }
}
```

### Plugin Metadata (global, set once)

Collector address and batch settings are not per-scope — configured once and apply everywhere the plugin is enabled.

`PUT /apisix/admin/plugin_metadata/opentelemetry`
```json
{
  "trace_id_source": "x-request-id",
  "resource": {
    "service.name": "apisix",
    "service.version": "3.15"
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

---

## Global configuration mechanisms

Three mechanisms exist and are not interchangeable.

| Mechanism | Scope | Opt-in required | Purpose |
|---|---|---|---|
| `global_rules` | All routes, every request | No | Blanket plugin enablement |
| `plugin_configs` | Routes that reference it | Yes (`plugin_config_id`) | Reusable plugin bundle |
| `plugin_metadata` | Defaults only, no enablement | N/A | Shared config for plugin instances |

### global_rules — all routes, no opt-in

`PUT /apisix/admin/global_rules/1`
```json
{
  "plugins": {
    "prometheus": { "prefer_name": true },
    "opentelemetry": { "sampler": { "name": "always_on" } },
    "request-id": {
      "header_name": "X-Correlation-ID",
      "enable_req_id_in_resp": true
    }
  }
}
```

**Caveat:** if the same plugin appears in both a global rule and a route, both instances execute — `prometheus` would double-count the request. Use one or the other, not both.

### plugin_configs — reusable bundle, opt-in per route

`PUT /apisix/admin/plugin_configs/observability`
```json
{
  "id": "observability",
  "plugins": {
    "prometheus": { "prefer_name": true },
    "opentelemetry": { "sampler": { "name": "always_on" } }
  }
}
```

Route-level plugins merge with the bundle; route takes precedence on conflicts.

### plugin_metadata — shared defaults, not enablement

Does not activate the plugin. Sets defaults consumed by instances already enabled on routes/services/global rules.

`PUT /apisix/admin/plugin_metadata/prometheus`
```json
{
  "export_uri": "/apisix/prometheus/metrics",
  "metric_prefix": "apisix_"
}
```

---

## Scope comparison

| Scope | Granularity | When to use |
|---|---|---|
| Route | Per route | Different config or sampling rate per endpoint |
| Service | Per service (all its routes) | Group routes and enable once |
| Plugin Config | Reusable bundle | Same plugin set across routes that don't share a service |
| Global Rule | All routes | Blanket coverage — simplest setup |

### Recommendation for this stack

Move `prometheus` and `opentelemetry` to a global rule. Keep `key-auth`, `consumer-restriction`, `proxy-rewrite`, `cors`, and `request-id` per-route since they differ per route. This removes ~10 lines of repetition per route and ensures new routes get observability automatically.
