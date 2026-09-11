# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: AGPL-3.0-or-later

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
	@go tool lateregate test

# fmt formats all Go sources in place.
.PHONY: fmt
fmt:
	gofmt -w .

# gofmt over the tree, through the shared checker.
.PHONY: fmt-check
fmt-check:
	@go tool lateregate fmt-check

# hooks installs the repository git hooks: pre-commit runs the cheap gates over
# the staged Go files (gofmt, and code a standard library call already covers);
# pre-push runs golangci-lint over the packages the push changes. Both call
# lateregate, which runs the linter through `go run` at a pinned version, so
# nothing beyond the Go toolchain needs installing. It also restores the local
# CLAUDE.md -> AGENTS.md symlink, which is gitignored and so absent in a fresh
# clone.
.PHONY: hooks
hooks:
	git config core.hooksPath .githooks
	@[ -e CLAUDE.md ] || [ -L CLAUDE.md ] || ln -s AGENTS.md CLAUDE.md
	@echo "installed git hooks (core.hooksPath=.githooks)"

# Fails on code a standard library call already covers. Overlaps the modernize
# linter but carries fixers it does not.
.PHONY: lint-modernize
lint-modernize:
	@go tool lateregate modernize

# The spec tree, through the shared linter: frontmatter parses, every spec
# carries a title and a status from the closed vocabulary, and the dependency
# graph is acyclic.
.PHONY: spec-lint
spec-lint:
	@go tool lateregate spec-lint

# Every source file carries the SPDX notice declared in .lateregate.yaml.
.PHONY: license
license:
	@go tool lateregate license

# The suite with only the Go toolchain on PATH. A test that depends on what
# happens to be installed passes locally and fails on a runner.
.PHONY: test-hermetic
test-hermetic:
	@go tool lateregate hermetic

# Renders .golangci.yml from the shared config plus the golangci: section of
# .lateregate.yaml. The file is generated and gitignored: change the linting
# there, never in the rendered file.
.PHONY: lint-config
lint-config:
	@go tool lateregate golangci

# Named cover-pg, not cover: the shared go-verify pipeline probes for a `cover`
# target and runs it on a runner that has no database, where this measures the
# wrong thing. Coverage here needs the scratch Postgres, so it stays local.
#
# Statement coverage across the whole tree, with the Postgres contracts running
# rather than skipping. This is the number to quote: `go test -cover ./...`
# without a database silently skips every PG store contract, which costs
# internal/access alone about forty points and makes the total meaningless.
#
# It needs the scratch database, so it shares db-contract's guard and -p 1.
.PHONY: cover-pg
cover-pg:
	@$(COMPOSE) exec -T postgres psql -U replichai -d postgres -tAc \
		"SELECT 1 FROM pg_database WHERE datname='$(TEST_DB)'" | grep -q 1 || \
		$(COMPOSE) exec -T postgres createdb -U replichai $(TEST_DB)
	TEST_DATABASE_URL='$(TEST_DATABASE_URL)' \
		go test -p 1 -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

.PHONY: lint-otel
# lint-otel keeps outbound HTTP instrumented so traces propagate across
# services. It fails on two shapes: an http.Client literal that sets no
# Transport field, and any use of http.DefaultClient. The gate parses each
# non-test Go file, so a Transport field several lines below the opening brace
# still counts, a comment describing this rule does not trip it, and
# Timeout: cfg.Transport.Timeout does not pass for a Transport field.
lint-otel:
	@go tool lateregate otel-client

.PHONY: lint
lint:
	@go tool lateregate lint

# lint-dead fails on code nothing reaches. -test counts the test binaries as
# roots too, so a seam only the suite exercises — a fake, a fixture helper, a
# constructor a test wires — is reachable and passes; what fails is a function
# no main, no test and no exported path can arrive at. The tool is pinned in
# go.mod like every other gate, so `go tool` finds it without an install.
.PHONY: lint-dead
lint-dead:
	@go tool deadcode -test ./...

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
# database: job (where SELECT ... FOR UPDATE SKIP LOCKED gets a real
# disjointness assertion, and the quota a real concurrency one), access,
# platform, credit and project. job and access run directly against the
# database in the URL and migrate and truncate it; platform, credit and project
# each cut themselves a private database on the same server (replichai_*_test)
# and leave the URL's own tables alone. profile has the same shape and is not in
# the list; run it with `make db-contract DB_CONTRACT_PKGS=./internal/profile/`.
#
# It runs against a scratch database, never the dev one: these contracts
# migrate and truncate whatever database they are pointed at, so aiming them at
# `replichai` destroys the local rows you were working with. The recipe creates
# the scratch database on first use, so a fresh `make up` needs no extra step.
#
# -p 1 is required. Packages otherwise build and run in parallel, and job and
# access both migrate and truncate the one database in the URL — sharing it,
# they would race each other into spurious failures.
#
# These are `:=`, not `?=`, on purpose. Make treats an exported variable as
# already set, so `?=` would inherit a TEST_DATABASE_URL pointing at the dev
# database from your shell — and the guard above would dutifully create the
# scratch database while the tests truncated `replichai` anyway. A command-line
# override (`make db-contract TEST_DB=foo`) still wins over both.
TEST_DB := replichai_test
TEST_DATABASE_URL := postgres://replichai:replichai@localhost:5432/$(TEST_DB)?sslmode=disable
DB_CONTRACT_PKGS ?= ./internal/job/ ./internal/access/ ./internal/platform/ ./internal/credit/ ./internal/project/

.PHONY: db-contract
db-contract:
	@$(COMPOSE) exec -T postgres psql -U replichai -d postgres -tAc \
		"SELECT 1 FROM pg_database WHERE datname='$(TEST_DB)'" | grep -q 1 || \
		$(COMPOSE) exec -T postgres createdb -U replichai $(TEST_DB)
	TEST_DATABASE_URL='$(TEST_DATABASE_URL)' \
		go test -p 1 $(DB_CONTRACT_PKGS) -run TestPGStore -v

# The whole shared bar. Every gate lives in lateregate, pinned as a tool in
# go.mod; this target is a name for `go tool lateregate` and nothing else.
# The plan: `go tool lateregate list`. One gate: `go tool lateregate <gate>`.
check:
	@go tool lateregate
