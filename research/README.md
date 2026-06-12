# APISIX Observability POC

Full-stack observability (metrics, traces, logs) for APISIX as an API gateway. Runs locally with Docker Compose, no external dependencies.

**Signal pipeline:**
- Metrics — APISIX prometheus plugin → OTEL Collector → Thanos → Grafana
- Traces — APISIX opentelemetry plugin → OTEL Collector → Tempo → Grafana
- Logs — mock services (zerolog JSON) → Alloy → Loki → Grafana
- Cross-pillar — trace IDs in logs link directly to Tempo spans in Grafana

**11 containers:** APISIX, OTEL Collector, Thanos (receive + query), Tempo, Loki, Alloy, Grafana, and three Go mock upstreams.

---

## Quick start

```bash
make up       # build and start everything
make seed     # fire 60 mixed requests to populate dashboards
make test     # smoke-test all consumer keys and auth rules
make down     # stop
make clean    # stop + remove volumes
```

Grafana: http://localhost:3000 — anonymous admin, no login.

Consumer keys (header or query param `be-mobile-api-key`):

| Consumer | Key | Routes |
|---|---|---|
| Flitsmeister_Client | `flit-key-abc123` | traffic-events, route-guidance |
| TomTom_Integration | `tomtom-key-def456` | traffic-events only |
| Internal_Monitoring | `monitor-key-ghi789` | traffic-events, route-guidance |
| TestClient_Generic | `test-key-xyz000` | traffic-events, route-guidance |

---

## Documentation

| | |
|---|---|
| [poc-findings.md](poc-findings.md) | What was validated, what was observed, config gotchas |
| [observability-stack.md](observability-stack.md) | Component responsibilities, dashboards, and data pipelines |
| [observability-research.md](observability-research.md) | Research notes — plugin choices, stack design decisions, Alloy vs Promtail |
| [apisix-metrics-reference.md](apisix-metrics-reference.md) | Full APISIX metric inventory, gaps, and comparison with Kong / Traefik / Envoy |
| [apisix-plugin-scopes.md](apisix-plugin-scopes.md) | Enabling prometheus and opentelemetry plugins by scope; global_rules vs plugin_configs vs plugin_metadata |
| [apisix-standalone-to-adminapi.md](apisix-standalone-to-adminapi.md) | Translating standalone YAML config to etcd-backed Admin API calls |
| [security-concerns.md](security-concerns.md) | Exposed ports, APISIX-specific risks, sensitive data in telemetry, production gaps |
