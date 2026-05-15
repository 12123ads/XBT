# syntax=docker/dockerfile:1.7

FROM golang:1.25 AS builder
WORKDIR /app
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-arm64} go build -o /out/xbt-server ./cmd/server

FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/xbt-server /usr/local/bin/xbt-server
COPY config.yaml /app/config.yaml

EXPOSE 3030
CMD ["xbt-server"]
