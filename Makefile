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

# The pure-Go full-stack e2e: submit -> job runs -> done, no Docker, no network.
.PHONY: e2e
e2e:
	go test ./cmd/server/ -run TestServerEndToEnd -v

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

# Run the job-store contract against the running compose Postgres — this is
# where SELECT ... FOR UPDATE SKIP LOCKED gets a real disjointness assertion.
#
# It runs against a scratch database, never the dev one: these contracts
# migrate and truncate whatever database they are pointed at, so aiming them at
# `replichai` destroys the local rows you were working with. The recipe creates
# the scratch database on first use, so a fresh `make up` needs no extra step.
TEST_DB ?= replichai_test
TEST_DATABASE_URL ?= postgres://replichai:replichai@localhost:5432/$(TEST_DB)?sslmode=disable

.PHONY: db-contract
db-contract:
	@$(COMPOSE) exec -T postgres psql -U replichai -d postgres -tAc \
		"SELECT 1 FROM pg_database WHERE datname='$(TEST_DB)'" | grep -q 1 || \
		$(COMPOSE) exec -T postgres createdb -U replichai $(TEST_DB)
	TEST_DATABASE_URL='$(TEST_DATABASE_URL)' \
		go test ./internal/job/ -run TestPGStore -v
