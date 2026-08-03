# Common tasks. Everything here is free — no API key, no replication cost.

# Detect the compose command: Docker or podman, subcommand or standalone. Make
# recipes run in /bin/sh, which does not see an interactive `docker`->`podman`
# alias, so we resolve a real command here. Override with `make COMPOSE=...`.
COMPOSE ?= $(shell \
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then echo docker compose; \
	elif command -v docker-compose >/dev/null 2>&1; then echo docker-compose; \
	elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then echo podman compose; \
	elif command -v podman-compose >/dev/null 2>&1; then echo podman-compose; \
	else echo docker compose; fi)

# The whole test suite, offline against fakes and the in-process provider.
.PHONY: test
test:
	go test ./...

# Statement coverage across the whole tree, with the Postgres contracts running
# rather than skipping. This is the number to quote: `go test -cover ./...`
# without a database silently skips every PG store contract, which costs
# internal/access alone about forty points and makes the total meaningless.
#
# It needs the scratch database, so it shares db-contract's guard and -p 1.
.PHONY: cover
cover:
	@$(COMPOSE) exec -T postgres psql -U replichai -d postgres -tAc \
		"SELECT 1 FROM pg_database WHERE datname='$(TEST_DB)'" | grep -q 1 || \
		$(COMPOSE) exec -T postgres createdb -U replichai $(TEST_DB)
	TEST_DATABASE_URL='$(TEST_DATABASE_URL)' \
		go test -p 1 -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

# The pure-Go full-stack e2e: submit -> job runs -> done, no Docker, no network.
.PHONY: e2e
e2e:
	go test ./cmd/server/ -run TestServerEndToEnd -v

# The browser layout regression: renders the transcript viewer in headless
# Chrome against a mocked API and measures the event cards. Layout bugs need a
# layout engine, so this is the only test that can catch them. Skips cleanly
# when no Chrome is installed.
.PHONY: web-e2e
web-e2e:
	cd frontend && npm run test:browser

# The compose smoke test: the real Postgres-backed stack, memory provider.
.PHONY: smoke
smoke:
	./scripts/smoke.sh

# Bring the local stack up / down (memory provider by default).
.PHONY: up
up:
	$(COMPOSE) up --build

.PHONY: down
down:
	$(COMPOSE) down -v

# Run the store contracts that need a real Postgres against the running compose
# database — job (where SELECT ... FOR UPDATE SKIP LOCKED gets a real
# disjointness assertion, and the quota a real concurrency one), access, and
# platform. The project and profile contracts are absent on purpose: they cut
# themselves a scratch database from this URL and are already self-contained.
#
# It runs against a scratch database, never the dev one: these contracts
# migrate and truncate whatever database they are pointed at, so aiming them at
# `replichai` destroys the local rows you were working with. The recipe creates
# the scratch database on first use, so a fresh `make up` needs no extra step.
#
# -p 1 is load-bearing. Packages otherwise build and run in parallel, and both
# the access and platform contracts truncate access_grants — sharing one
# database, they would race each other into spurious failures.
#
# These are `:=`, not `?=`, on purpose. Make treats an exported variable as
# already set, so `?=` would inherit a TEST_DATABASE_URL pointing at the dev
# database from your shell — and the guard above would dutifully create the
# scratch database while the tests truncated `replichai` anyway. A command-line
# override (`make db-contract TEST_DB=foo`) still wins over both.
TEST_DB := replichai_test
TEST_DATABASE_URL := postgres://replichai:replichai@localhost:5432/$(TEST_DB)?sslmode=disable
DB_CONTRACT_PKGS ?= ./internal/job/ ./internal/access/ ./internal/platform/

.PHONY: db-contract
db-contract:
	@$(COMPOSE) exec -T postgres psql -U replichai -d postgres -tAc \
		"SELECT 1 FROM pg_database WHERE datname='$(TEST_DB)'" | grep -q 1 || \
		$(COMPOSE) exec -T postgres createdb -U replichai $(TEST_DB)
	TEST_DATABASE_URL='$(TEST_DATABASE_URL)' \
		go test -p 1 $(DB_CONTRACT_PKGS) -run TestPGStore -v
