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
#   gh             — GitHub CLI (pinned release, below)
#   gcx            — Grafana gcx: Grafana Cloud/LGTM CLI (pinned release, below)
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

# GitHub CLI and gcx, baked in rather than left to `arkade get` at runtime: the
# Substrate sandbox blocks egress the same way it blocks apt, so a tool the
# agent is expected to have has to be in the image.
#
# Both come straight from their GitHub releases, pinned to a version and
# checked against the SHA-256 that release publishes, rather than through
# `arkade get`. Two reasons:
#   - `arkade get gh` resolves against arkade's embedded catalogue, so a pin
#     that falls behind the catalogue still installs — just not the version
#     named in GH_VERSION, leaving the pin and the binary silently disagreeing;
#   - gcx is not in arkade's catalogue at all.
# The archive names carry the architecture (…_linux_amd64.tar.gz /
# …_linux_arm64.tar.gz), and so do the checksums, so a wrong pairing fails the
# build here instead of shipping a binary the worker cannot execute. TARGETARCH
# is set by buildx; plain `docker build` defaults to arm64, matching the local
# Kind cluster. Bump the four ARGs together from the release's checksums file:
#   https://github.com/cli/cli/releases/download/v<gh>/gh_<gh>_checksums.txt
#   https://github.com/grafana/gcx/releases/download/v<gcx>/gcx_<gcx>_checksums.txt
#
# gh's tarball is rooted at gh_<version>_linux_<arch>/ with the binary in bin/;
# gcx's has the binary at the top level. No `#` comments inside this RUN: lines
# joined by a trailing backslash are one shell line, so a `#` would comment out
# everything after it.
ARG GH_VERSION=2.102.0
ARG GH_SHA256_AMD64=bb766f710eef8ede859c18578c72c327597cd4c8a85b06001b1f3843c6019386
ARG GH_SHA256_ARM64=7862c86c72f43df3a2d93ddde6f473285b4e2af61b494849846827e513ef6484
ARG GCX_VERSION=1.6.0
ARG GCX_SHA256_AMD64=335e051cda6edfb4ad5757540208961dbdfab6d89d4af11a4087de6ffa0ec111
ARG GCX_SHA256_ARM64=116fd1a9f25d5f5e29802b705d9964578a01614f94499b85461b6abca1cb4a04
ARG TARGETARCH=arm64
RUN set -eu; \
    case "${TARGETARCH}" in \
        amd64) GH_SHA256="${GH_SHA256_AMD64}"; GCX_SHA256="${GCX_SHA256_AMD64}" ;; \
        arm64) GH_SHA256="${GH_SHA256_ARM64}"; GCX_SHA256="${GCX_SHA256_ARM64}" ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    tmp="$(mktemp -d)"; trap 'rm -rf "${tmp}"' EXIT; \
    curl -fsSL -o "${tmp}/gh.tar.gz" \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    echo "${GH_SHA256}  ${tmp}/gh.tar.gz" | sha256sum -c -; \
    tar -xzf "${tmp}/gh.tar.gz" -C "${tmp}" --strip-components=2 \
        "gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh"; \
    install -m 0755 "${tmp}/gh" /usr/local/bin/gh; \
    curl -fsSL -o "${tmp}/gcx.tar.gz" \
        "https://github.com/grafana/gcx/releases/download/v${GCX_VERSION}/gcx_${GCX_VERSION}_linux_${TARGETARCH}.tar.gz"; \
    echo "${GCX_SHA256}  ${tmp}/gcx.tar.gz" | sha256sum -c -; \
    tar -xzf "${tmp}/gcx.tar.gz" -C "${tmp}" gcx; \
    install -m 0755 "${tmp}/gcx" /usr/local/bin/gcx; \
    gh --version; \
    gcx --version

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
