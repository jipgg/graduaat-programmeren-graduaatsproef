COMPOSE  = docker compose
COMPOSE_FILE = docker-compose.yml

GRAFANA  = http://localhost:3000
METRICS  = http://localhost:9091/apisix/prometheus/metrics

KEY_FLIT    = flit-key-abc123
KEY_TOMTOM  = tomtom-key-def456
KEY_MONITOR = monitor-key-ghi789
KEY_TEST    = test-key-xyz000

.DEFAULT_GOAL := help

# ── Lifecycle ──────────────────────────────────────────────────────────────────

.PHONY: build up down restart logs ps clean

build: ## Rebuild Go service images (no cache)
	$(COMPOSE) -f $(COMPOSE_FILE) build --no-cache traffic-events-service route-guidance-service echo-service

up: ## Build and start the full stack
	$(COMPOSE) -f $(COMPOSE_FILE) build traffic-events-service route-guidance-service echo-service
	$(COMPOSE) -f $(COMPOSE_FILE) up -d
	@echo "Waiting for APISIX..."
	@until curl -sf $(METRICS) > /dev/null 2>&1; do sleep 2; done
	@echo "Ready — Grafana: $(GRAFANA)"

down: ## Stop the stack
	$(COMPOSE) -f $(COMPOSE_FILE) down

restart: ## Restart a service  (make restart svc=apisix)
	$(COMPOSE) -f $(COMPOSE_FILE) restart $(svc)

logs: ## Follow logs for all services
	$(COMPOSE) -f $(COMPOSE_FILE) logs -f

logs-%: ## Follow logs for one service  (make logs-apisix)
	$(COMPOSE) -f $(COMPOSE_FILE) logs -f $*

ps: ## Show container status
	$(COMPOSE) -f $(COMPOSE_FILE) ps

clean: ## Stop and remove volumes
	$(COMPOSE) -f $(COMPOSE_FILE) down -v

# ── Observability ──────────────────────────────────────────────────────────────

.PHONY: metrics

metrics: ## Print apisix_http_* Prometheus metrics
	@curl -s $(METRICS) | grep -E "^apisix_http"

# ── Traffic ────────────────────────────────────────────────────────────────────

.PHONY: test seed

test: ## Smoke-test all consumers — prints HTTP status for each request
	@echo "traffic events — Flitsmeister (expect 200)"
	@curl -s -o /dev/null -w "  GET /events                → %{http_code}\n" \
		-H "be-mobile-api-key: $(KEY_FLIT)" \
		http://localhost:9080/traffic-events/v2/nl-generic/events
	@echo "traffic events — TomTom (expect 200)"
	@curl -s -o /dev/null -w "  GET /events                → %{http_code}\n" \
		-H "be-mobile-api-key: $(KEY_TOMTOM)" \
		http://localhost:9080/traffic-events/v2/nl-generic/events
	@echo "traffic events geo-query — TomTom (expect 200)"
	@curl -s -o /dev/null -w "  POST /events/query         → %{http_code}\n" \
		-X POST \
		-H "be-mobile-api-key: $(KEY_TOMTOM)" \
		-H "Content-Type: application/json" \
		-d '{"bbox_min_lat":50.7,"bbox_max_lat":51.2,"bbox_min_lon":3.5,"bbox_max_lon":5.0}' \
		http://localhost:9080/traffic-events/v2/nl-generic/events/query
	@echo "route guidance — Flitsmeister (expect 200)"
	@curl -s -o /dev/null -w "  POST /route                → %{http_code}\n" \
		-X POST \
		-H "be-mobile-api-key: $(KEY_FLIT)" \
		-H "Content-Type: application/json" \
		-d '{"origin":{"lat":50.85,"lon":4.35},"destination":{"lat":51.22,"lon":4.40},"profile":"car"}' \
		http://localhost:9080/guidance/v2/generic/route
	@echo "route guidance — TomTom (expect 403, not whitelisted)"
	@curl -s -o /dev/null -w "  POST /route                → %{http_code}\n" \
		-X POST \
		-H "be-mobile-api-key: $(KEY_TOMTOM)" \
		-H "Content-Type: application/json" \
		-d '{}' \
		http://localhost:9080/guidance/v2/generic/route
	@echo "route alternatives — Flitsmeister (expect 200)"
	@curl -s -o /dev/null -w "  GET /route/1/alternatives  → %{http_code}\n" \
		-H "be-mobile-api-key: $(KEY_FLIT)" \
		http://localhost:9080/guidance/v2/generic/route/1/alternatives
	@echo "no key (expect 401)"
	@curl -s -o /dev/null -w "  GET /events (no key)       → %{http_code}\n" \
		http://localhost:9080/traffic-events/v2/nl-generic/events
	@echo "correlation ID returned in response headers:"
	@curl -s -D - -o /dev/null -H "be-mobile-api-key: $(KEY_TEST)" \
		http://localhost:9080/traffic-events/v2/nl-generic/events \
		| grep -i x-correlation-id || echo "  (not found)"

seed: ## Fire 60 mixed requests across all consumers to populate Grafana dashboards
	@echo "Seeding dashboard traffic (60 requests, ~30s)..."
	@for i in $$(seq 1 10); do \
		curl -sf -H "be-mobile-api-key: $(KEY_FLIT)" \
			http://localhost:9080/traffic-events/v2/nl-generic/events > /dev/null; \
		curl -sf -H "be-mobile-api-key: $(KEY_FLIT)" \
			http://localhost:9080/traffic-events/v2/nl-generic/events/1 > /dev/null; \
		curl -sf -H "be-mobile-api-key: $(KEY_TOMTOM)" \
			http://localhost:9080/traffic-events/v2/nl-generic/events > /dev/null; \
		curl -sf -X POST \
			-H "be-mobile-api-key: $(KEY_TOMTOM)" \
			-H "Content-Type: application/json" \
			-d '{"bbox_min_lat":50.7,"bbox_max_lat":51.2,"bbox_min_lon":3.5,"bbox_max_lon":5.0}' \
			http://localhost:9080/traffic-events/v2/nl-generic/events/query > /dev/null; \
		curl -sf -X POST \
			-H "be-mobile-api-key: $(KEY_FLIT)" \
			-H "Content-Type: application/json" \
			-d '{"origin":{"lat":50.85,"lon":4.35},"destination":{"lat":51.22,"lon":4.40},"profile":"car"}' \
			http://localhost:9080/guidance/v2/generic/route > /dev/null; \
		curl -s -o /dev/null \
			-X POST \
			-H "be-mobile-api-key: $(KEY_TOMTOM)" \
			http://localhost:9080/guidance/v2/generic/route; \
		curl -sf -H "be-mobile-api-key: $(KEY_MONITOR)" \
			http://localhost:9080/traffic-events/v2/nl-generic/events > /dev/null; \
		curl -sf -H "be-mobile-api-key: $(KEY_TEST)" \
			http://localhost:9080/guidance/v2/generic/route/1/alternatives > /dev/null; \
		sleep 2; \
	done
	@echo "Done — open $(GRAFANA)"

# ── Help ───────────────────────────────────────────────────────────────────────

.PHONY: help

help:
	@echo ""
	@echo "Lifecycle"
	@echo "  make up                   build + start all containers"
	@echo "  make down                 stop all containers"
	@echo "  make clean                stop + remove volumes"
	@echo "  make build                rebuild Go service images (no cache)"
	@echo "  make restart svc=<name>   restart one container"
	@echo "  make ps                   container status"
	@echo ""
	@echo "Logs"
	@echo "  make logs                 follow all logs"
	@echo "  make logs-apisix          follow apisix logs"
	@echo "  make logs-otel-collector  follow collector logs"
	@echo "  make logs-traffic-events-service"
	@echo "  make logs-route-guidance-service"
	@echo ""
	@echo "Observability"
	@echo "  make metrics              print apisix_http_* metrics"
	@echo ""
	@echo "Traffic"
	@echo "  make test                 smoke-test all consumer keys + auth rules"
	@echo "  make seed                 fire 60 mixed requests to populate dashboards"
	@echo ""
	@echo "Consumer keys"
	@echo "  Flitsmeister_Client  $(KEY_FLIT)"
	@echo "  TomTom_Integration   $(KEY_TOMTOM)"
	@echo "  Internal_Monitoring  $(KEY_MONITOR)"
	@echo "  TestClient_Generic   $(KEY_TEST)"
	@echo ""
