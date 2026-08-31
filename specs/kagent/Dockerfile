# Multi-stage OCI image for pi-go as a kagent Substrate harness runtime.
#
# The image runs `pi a2a`, an HTTP+gRPC A2A server that satisfies the kagent
# v2 runtime contract:
#   - JSON-RPC A2A + gRPC A2A on $PORT (default 80, kagent compiler sets PORT=80)
#   - agent card at /.well-known/agent-card.json (from KAGENT_AGENT_CARD_JSON
#     when the compiler injects it)
#   - readiness at /readyz on :8081 (Substrate ActorTemplate probe)
#   - durable pi state below /data (Substrate durable dir mount)
#
# Build for the local Kind/Substrate architecture (linux/arm64), run as
# non-root, no cgo (default build is pure Go; ORT acceleration is opt-in and
# not needed for the harness).
#
#   docker build -f specs/kagent/Dockerfile -t localhost:5001/dimetron/pi-go-kagent:dev .
#   docker tag ... and push by digest.

# syntax=docker/dockerfile:1

FROM golang:1.27-bookworm AS build
WORKDIR /src

# Cache module downloads first.
COPY go.mod go.sum ./
COPY third_party/acp-go-sdk ./third_party/acp-go-sdk
RUN go mod download

# Build the pure-Go pi binary for linux/arm64.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w" -o /out/pi ./cmd/pi

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 pi

WORKDIR /data
COPY --from=build /out/pi /usr/local/bin/pi

USER pi

# A2A HTTP/gRPC port (kagent compiler sets PORT=80; readyz stays 8081).
EXPOSE 80 8081

ENV PORT=80

ENTRYPOINT ["pi", "a2a"]
