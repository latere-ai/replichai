# Stage 1: build the frontend with bun into internal/spa/dist.
FROM oven/bun:1 AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bun run build   # vite outputs to ../internal/spa/dist

# Stage 2: build the server statically, embedding the built frontend.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/internal/spa/dist ./internal/spa/dist
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server

# Stage 3: ship the binary on scratch with the method data and CA roots.
FROM scratch
WORKDIR /app
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/server /app/server
# method.Load discovers ./method from the working directory.
COPY method /app/method
EXPOSE 8080
ENTRYPOINT ["/app/server"]
