# Common tasks. Everything here is free — no API key, no replication cost.

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
	docker compose up --build

.PHONY: down
down:
	docker compose down -v

# Run the job-store contract against the running compose Postgres — this is
# where SELECT ... FOR UPDATE SKIP LOCKED gets a real disjointness assertion.
.PHONY: db-contract
db-contract:
	TEST_DATABASE_URL='postgres://replichai:replichai@localhost:5432/replichai?sslmode=disable' \
		go test ./internal/job/ -run TestPGStore -v
