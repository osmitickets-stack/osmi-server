# ==========================
# Builder
# ==========================
FROM golang:1.25-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    go build \
    -trimpath \
    -ldflags="-s -w" \
    -o server \
    ./cmd/main.go

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath -ldflags="-s -w" \
    -o reissue-legacy-qr ./cmd/reissue-legacy-qr

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath -ldflags="-s -w" \
    -o reservation-worker ./cmd/worker

# ==========================
# Runtime
# ==========================
FROM alpine:3.20

RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    netcat-openbsd

RUN addgroup -S osmi && adduser -S osmi -G osmi

WORKDIR /app

COPY --from=builder /app/server .
COPY --from=builder /app/reissue-legacy-qr .
COPY --from=builder /app/reservation-worker .

USER osmi

EXPOSE 50051

HEALTHCHECK \
    --interval=30s \
    --timeout=5s \
    --start-period=10s \
    --retries=3 \
    CMD nc -z localhost 50051 || exit 1

CMD ["./server"]
