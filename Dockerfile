# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o watchdog ./cmd/watchdog/

# Final stage — minimal image, just the binary
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /build/watchdog /usr/local/bin/watchdog

ENTRYPOINT ["watchdog"]
CMD ["--config=/etc/watchdog/watchdog.yml"]
