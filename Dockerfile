FROM golang:1.25 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go env -w GOPROXY=https://proxy.golang.org,direct
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags "-s -w -X main.Version=${VERSION}" -o /out/server ./cmd/seckill-service

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    netbase \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/server /app/server

EXPOSE 8000
EXPOSE 9000
EXPOSE 2112

VOLUME ["/data/conf"]

CMD ["/app/server", "-conf", "/data/conf"]
