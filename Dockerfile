# Build the server statically, then ship it on scratch with the method data it
# loads at runtime and the CA roots it needs for HTTPS. `docker compose up` uses
# this to bring the whole app up.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server

FROM scratch
WORKDIR /app
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/server /app/server
# method.Load discovers ./method from the working directory.
COPY method /app/method
EXPOSE 8080
ENTRYPOINT ["/app/server"]
