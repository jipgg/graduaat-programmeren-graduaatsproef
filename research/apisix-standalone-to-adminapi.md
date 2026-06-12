# APISIX: Standalone YAML → Admin API (etcd) Translation

When switching from standalone (`config_provider: yaml`) to etcd-backed deployment, configuration splits
across two layers: **static node config** that stays in `config.yaml`, and **dynamic routing state**
that moves into etcd via the Admin API.

---

## What stays in `config.yaml`

`config.yaml` is a static YAML file read at startup — it has no JSON or Admin API equivalent.
The fields that stay there regardless of deployment mode:

- `deployment` — switches between `config_provider: yaml` (standalone) and etcd connection
- `plugin_attr` — node-level plugin settings (prometheus scrape port, otel collector address, histogram buckets)
- `plugins` — allowlist of enabled plugins

These cannot be changed via the Admin API and require a config reload or restart.

---

## `plugin_metadata` → `PUT /apisix/admin/plugin_metadata/{plugin-name}`

`plugin_metadata` in standalone `apisix.yaml` is the direct equivalent of this Admin API resource.

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

## `routes` → `PUT /apisix/admin/routes/{id}`

`PUT /apisix/admin/routes/traffic-events-nl-generic`
```json
{
  "name": "traffic-events-nl-generic",
  "uri": "/traffic-events/v2/nl-generic*",
  "methods": ["GET", "POST", "OPTIONS"],
  "upstream_id": "traffic-events-service",
  "status": 1,
  "plugins": {
    "key-auth": {
      "header": "be-mobile-api-key",
      "query": "be-mobile-api-key"
    },
    "consumer-restriction": {
      "whitelist": [
        "Flitsmeister_Client",
        "TomTom_Integration",
        "Internal_Monitoring",
        "TestClient_Generic"
      ]
    },
    "prometheus": { "prefer_name": true },
    "opentelemetry": {
      "sampler": { "name": "always_on" }
    },
    "proxy-rewrite": {
      "regex_uri": [
        "^/traffic-events/v2/nl-generic/(.*)$",
        "/$1"
      ],
      "headers": {
        "set": {
          "X-Consumer-Username": "$consumer_name",
          "X-Correlation-ID": "$request_id"
        },
        "remove": ["be-mobile-api-key"]
      }
    },
    "request-id": {
      "enable_req_id_in_resp": true,
      "header_name": "X-Correlation-ID"
    },
    "cors": {
      "allow_origins": "*",
      "allow_methods": "GET,POST,OPTIONS",
      "allow_headers": "be-mobile-api-key,content-type,accept-language",
      "allow_credential": false
    }
  }
}
```

The remaining routes (`traffic-events-nl-generic-root`, `route-guidance-generic`,
`route-guidance-generic-root`, `echo-proxy`) follow the same shape — different `id` in the URL path.

---

## `consumers` → `PUT /apisix/admin/consumers/{username}`

Note: the URL uses `username`, not a numeric id.

`PUT /apisix/admin/consumers/Flitsmeister_Client`
```json
{
  "username": "Flitsmeister_Client",
  "plugins": {
    "key-auth": { "key": "flit-key-abc123" }
  }
}
```

Repeat for `TomTom_Integration` (`tomtom-key-def456`), `Internal_Monitoring`
(`monitor-key-ghi789`), and `TestClient_Generic` (`test-key-xyz000`).

---

## `upstreams` → `PUT /apisix/admin/upstreams/{id}`

`PUT /apisix/admin/upstreams/traffic-events-service`
```json
{
  "nodes": { "traffic-events-service:8080": 1 },
  "type": "roundrobin",
  "scheme": "http",
  "checks": {
    "active": {
      "type": "http",
      "http_path": "/healthz",
      "interval": 10,
      "timeout": 2,
      "healthy": { "successes": 2 },
      "unhealthy": { "http_failures": 3 }
    }
  }
}
```

Repeat for `route-guidance-service` and `echo-service` with the same health check shape.

---

## `services` → `PUT /apisix/admin/services/{id}`

Services are optional shared upstream + plugin bundles that routes reference via `service_id`.

`PUT /apisix/admin/services/traffic-events-svc`
```json
{
  "upstream_id": "traffic-events-service",
  "plugins": {
    "prometheus": { "prefer_name": true }
  }
}
```

---

## `global_rules` → `PUT /apisix/admin/global_rules/{id}`

Global rules apply plugins to every request regardless of which route matches.

`PUT /apisix/admin/global_rules/1`
```json
{
  "plugins": {
    "prometheus": { "prefer_name": true }
  }
}
```

---

## `plugin_configs` → `PUT /apisix/admin/plugin_configs/{id}`

Plugin configs are reusable plugin sets that routes reference via `plugin_config_id`.

`PUT /apisix/admin/plugin_configs/common-auth`
```json
{
  "plugins": {
    "key-auth": {
      "header": "be-mobile-api-key",
      "query": "be-mobile-api-key"
    },
    "request-id": {
      "enable_req_id_in_resp": true,
      "header_name": "X-Correlation-ID"
    }
  }
}
```

---

## Quick reference

| `apisix.yaml` key | Admin API endpoint | HTTP method |
|---|---|---|
| `plugin_metadata[].id` | `/apisix/admin/plugin_metadata/{plugin-name}` | PUT |
| `routes[].id` | `/apisix/admin/routes/{id}` | PUT |
| `consumers[].username` | `/apisix/admin/consumers/{username}` | PUT |
| `upstreams[].id` | `/apisix/admin/upstreams/{id}` | PUT |
| `services[].id` | `/apisix/admin/services/{id}` | PUT |
| `global_rules[].id` | `/apisix/admin/global_rules/{id}` | PUT |
| `plugin_configs[].id` | `/apisix/admin/plugin_configs/{id}` | PUT |

| `config.yaml` key | Admin API equivalent |
|---|---|
| `plugin_attr` | **None** — node-level, stays in `config.yaml` |
| `plugins` (allowlist) | **None** — node-level, stays in `config.yaml` |
| `deployment` | **None** — node-level, stays in `config.yaml` |
