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
# Build for linux/amd64 and linux/arm64 (Substrate worker nodes are arm64 in
# the local Kind setup; release publishes both), run as non-root, no cgo
# (default build is pure Go; ORT acceleration is opt-in and not needed for the
# harness).
#
#   docker build -f specs/kagent/Dockerfile -t localhost:5001/dimetron/pi-go-kagent:dev .
#   docker tag ... and push by digest.
#
# With buildx, pass --platform linux/amd64,linux/arm64; TARGETARCH selects the
# pi binary, the Go toolchain, and the arkade asset per architecture.

# syntax=docker/dockerfile:1

FROM golang:1.27-bookworm AS build
WORKDIR /src

# Cache module downloads first.
COPY go.mod go.sum ./
COPY third_party/acp-go-sdk ./third_party/acp-go-sdk
COPY third_party/go-ordered-map ./third_party/go-ordered-map
RUN go mod download

# Build the pure-Go pi binary for the target architecture (TARGETARCH is set
# automatically by buildx; default to arm64 for plain `docker build`).
ARG TARGETARCH=arm64
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" -o /out/pi ./cmd/pi

FROM debian:bookworm-slim

# Runtime tools pi-go's agent calls. The Substrate sandbox blocks apt-get at
# runtime (setgroups is denied in the gVisor sandbox), so everything must be
# baked into the image at build time:
#   git            — clone repos, git diff/log/status tools
#   ripgrep        — `rg`, used by the grep tool
#   gh             — GitHub CLI (installed via arkade, below)
#   curl, wget     — fetch tarballs and API payloads
#   jq             — JSON manipulation
#   python3 + pip  — scripting/parsing helper
#   build-essential — C toolchain for compiling deps that need cgo at runtime
#   unzip, xz-utils, zip, tar — archive extraction for arkade tools
#   vim-tiny       — minimal editor for the bash tool
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        git \
        ripgrep \
        curl \
        wget \
        jq \
        python3 \
        python3-pip \
        build-essential \
        unzip \
        xz-utils \
        zip \
        tar \
        vim-tiny \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 pi

# arkade CLI: used below to bake in the Go toolchain and gh, and available to
# the agent for fetching more CLIs (kubectl, helm, ...) without apt, which the
# sandbox blocks. Pinned to the release so the image is reproducible; arkade
# self-updates inside the sandbox share the same egress limits as apt, so the
# baked binary is what runs.
#
# arkade publishes one binary per architecture: `arkade` (x86_64/amd64) and
# `arkade-arm64`. TARGETARCH is set by buildx; plain `docker build` defaults to
# arm64 and gets the arm64 asset, matching the local Kind cluster.
ARG ARKADE_VERSION=0.11.126
ARG TARGETARCH=arm64
ARG ARKADE_ASSET=arkade
RUN if [ "${TARGETARCH}" = "arm64" ]; then ARKADE_ASSET=arkade-arm64; fi \
    && echo "downloading arkade asset: ${ARKADE_ASSET}" \
    && curl -fsSL -o /usr/local/bin/arkade \
        "https://github.com/alexellis/arkade/releases/download/${ARKADE_VERSION}/${ARKADE_ASSET}" \
    && chmod +x /usr/local/bin/arkade \
    && arkade version

# Go toolchain installed via arkade so the agent can build/run Go projects
# with the same version the repo pins (arkade `system install go` downloads
# the go.dev tarball for the current architecture into /usr/local/go).
RUN arkade system install go --version go1.27.0 --path /usr/local/ --progress=false \
    && /usr/local/go/bin/go version

# GitHub CLI, baked in rather than left to `arkade get` at runtime: the
# Substrate sandbox blocks egress the same way it blocks apt, so a tool the
# agent is expected to have has to be in the image. `arkade get` resolves the
# right asset for TARGETARCH itself; --path puts it on the default PATH so it
# works for any user, not just pi's ~/.arkade/bin.
#
# The version is the git tag, which for cli/cli is v-prefixed — arkade passes
# it through verbatim, so a bare "2.65.0" is a 404 rather than a resolution
# failure. --path must already exist; /usr/local/bin does.
ARG GH_VERSION=v2.65.0
RUN arkade get gh --version "${GH_VERSION}" --path /usr/local/bin --progress=false \
    && gh --version

ENV PATH="${PATH}:/usr/local/go/bin:/home/pi/.arkade/bin:/data/.arkade/bin"

USER pi

WORKDIR /data

# Everything pi keeps under ~/.pi-go — session transcripts, the memory
# store, logs — has to land on the durable mount, or an actor that Substrate
# checkpoints and restores elsewhere, or replaces outright, comes back with
# no record of its conversations. HOME is the one knob all of it hangs off;
# PI_SESSIONS_DIR alone would leave the memory store behind.
ENV HOME=/data
COPY --from=build /out/pi /usr/local/bin/pi

# A2A HTTP/gRPC port (kagent compiler sets PORT=80; readyz stays 8081).
EXPOSE 80 8081

ENV PORT=80

ENTRYPOINT ["pi", "a2a"]
