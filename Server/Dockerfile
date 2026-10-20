# syntax=docker/dockerfile:1.7

FROM golang:1.25 AS builder
WORKDIR /app
ARG TARGETARCH
ENV GOPROXY=https://goproxy.cn,direct
# onnxruntime_go is a cgo package. The ONNX shared library is loaded at
# runtime, so the builder only needs a C compiler (present in this image).
ENV CGO_ENABLED=1

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOOS=linux GOARCH=${TARGETARCH:-arm64} go build -o /out/xbt-server ./cmd/server

FROM debian:bookworm-slim
WORKDIR /app
ARG TARGETARCH
ARG ONNXRUNTIME_VERSION=1.29.0

# The runtime version must match onnxruntime_go's C API headers (1.29.0).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tar libstdc++6 \
    && case "${TARGETARCH:-arm64}" in \
         amd64) ort_arch=x64 ;; \
         arm64) ort_arch=aarch64 ;; \
         *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o /tmp/onnxruntime.tgz \
         "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-${ort_arch}-${ONNXRUNTIME_VERSION}.tgz" \
    && tar -xzf /tmp/onnxruntime.tgz -C /opt \
    && cp -a "/opt/onnxruntime-linux-${ort_arch}-${ONNXRUNTIME_VERSION}/lib/." /usr/local/lib/ \
    && ldconfig \
    && rm -rf /tmp/onnxruntime.tgz "/opt/onnxruntime-linux-${ort_arch}-${ONNXRUNTIME_VERSION}" \
    && rm -rf /var/lib/apt/lists/*

ENV LD_LIBRARY_PATH=/usr/local/lib
ENV CAPTCHA_ORT_LIB_PATH=/usr/local/lib/libonnxruntime.so

COPY --from=builder /out/xbt-server /usr/local/bin/xbt-server
COPY --from=builder /app/assets /app/assets
COPY config.yaml /app/config.yaml

EXPOSE 3030
CMD ["xbt-server"]
