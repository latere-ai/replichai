# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: AGPL-3.0-or-later

# Stage 1: build the frontend with bun into internal/spa/dist. The output is
# static assets with no architecture, so this stage runs on the build platform.
FROM --platform=$BUILDPLATFORM oven/bun:1 AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bun run build   # vite outputs to ../internal/spa/dist

# Stage 2: build the server statically with the built frontend embedded. It runs
# on the build platform and cross-compiles to the target architecture, so no
# emulation is needed and a linux/amd64 image builds quickly on an arm64 host.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
# The version telemetry reports. .git is excluded from the build context, so
# the toolchain can embed no VCS revision of its own and the value has to come
# in from the builder. Left empty, the binary falls back to its build info.
ARG VERSION=""
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/internal/spa/dist ./internal/spa/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-X main.version=${VERSION}" -o /out/server ./cmd/server

# Stage 3: ship the binary on scratch with the method data and CA roots.
FROM scratch
WORKDIR /app
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/server /app/server
# method.Load discovers ./method from the working directory.
COPY method /app/method
EXPOSE 8080
# Run as a non-root user. scratch has no passwd file, so the user is numeric.
# The server binds a high port, reads /app/method and the CA bundle, and writes
# nothing, so it needs no home directory and owns no files. There is no
# HEALTHCHECK: scratch has no shell or curl to run one with, and the Kubernetes
# probes in deploy/base/deployment.yaml call /healthz instead.
USER 65532:65532
ENTRYPOINT ["/app/server"]
