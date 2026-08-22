# Stage 1: build the frontend with bun into internal/spa/dist. Output is static
# assets (arch-independent), so this runs on the native build platform.
FROM --platform=$BUILDPLATFORM oven/bun:1 AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bun run build   # vite outputs to ../internal/spa/dist

# Stage 2: build the server statically, embedding the built frontend. Runs on
# the native build platform and cross-compiles to the target arch — no
# emulation, so a linux/amd64 image builds fast on an arm64 host.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/internal/spa/dist ./internal/spa/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -o /out/server ./cmd/server

# Stage 3: ship the binary on scratch with the method data and CA roots.
FROM scratch
WORKDIR /app
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/server /app/server
# method.Load discovers ./method from the working directory.
COPY method /app/method
EXPOSE 8080
ENTRYPOINT ["/app/server"]
