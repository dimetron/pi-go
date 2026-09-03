.PHONY: vulncheck build install test test-unit test-integration test-e2e test-all test-coverage test-ollama check-cve scan sbom lint vet e2e clean sandbox-run sandbox-log eval-run eval-pin eval-judge eval-tools eval-tools-judge record-pgo cache-clean hooks fetch-models fetch-modelsdev-pricing

# No GOEXPERIMENT=simd: Go 1.27 changed the simd/archsimd intrinsics API, and
# gomlx/compute's amd64 matmul kernels (gated on
# `//go:build amd64 && goexperiment.simd`) do not compile against it yet.
# The kernels' measured benefit was noise anyway (6.4 vs 6.6 chunks/sec on an
# M2 Max), so we drop the experiment until gomlx updates for the new API.

# Build verbosity. `-v` is on by default: it names each package as it compiles,
# which is the difference between "the build is working through a cold module
# cache" and "the build is wedged" — the case that matters in a fresh dev
# container. `make build V=1` adds `-x`, which echoes every underlying tool
# invocation; use it when the failure is in how a command was assembled, not in
# what it compiled.
GO_BUILD_FLAGS := -v $(if $(filter-out 0,$(V)),-x,)

# Stamped into `pi version`. Kept in a variable so build and install cannot
# drift into producing differently-stamped binaries.
GO_LDFLAGS := -X github.com/dimetron/pi-go/internal/cli.BuildTag=$$(git rev-parse --short HEAD 2>/dev/null || echo local)

build: cache-clean
	go build $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" ./cmd/pi
	go build $(GO_BUILD_FLAGS) ./cmd/pi-sandbox

# Clean the golangci-lint cache before building. The pre-commit hook runs
# golangci-lint, and a full lint cache (hundreds of MB) can fill the disk and
# make the hook fail with "no space left on device" / "parallel golangci-lint
# is running". Clearing it here keeps the build from wedging on a full cache.
# The Go build cache is deliberately NOT cleaned: it is the whole point of
# incremental builds, and wiping it on every build would make each one cold.
cache-clean:
	@golangci-lint cache clean 2>/dev/null || true
	@echo "golangci-lint cache cleaned"

# Install onto PATH, i.e. $(go env GOBIN) or $GOPATH/bin. This used to be a bare
# `install: build`, which has no recipe — it dropped the binaries in the repo
# root and left nothing on PATH, so `pi` was never a command anywhere.
install:
	go install $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" ./cmd/pi
	go install $(GO_BUILD_FLAGS) ./cmd/pi-sandbox

# hooks: point core.hooksPath at the versioned .githooks/ directory.
#
# The hooks enforce CLAUDE.md's signing rules: commit-msg adds a missing
# Signed-off-by, post-commit warns on unsigned commits, and pre-push
# HARD-FAILS any push containing unsigned or unsigned-off commits.
#
# pre-commit and pre-push also run check-large-files, which refuses oversized
# blobs — GIFs and screen recordings above all, which belong on a release
# rather than in every clone's history forever.
# Run once per clone: `make hooks`.
hooks:
	git config core.hooksPath .githooks
	@echo "hooks installed: pre-commit, commit-msg, post-commit, pre-push (.githooks/)"

# Accelerated build: ONNX Runtime + CoreML (Apple GPU / Neural Engine).
#
# OPT-IN ONLY. The default `make build` stays pure Go: no cgo, no native
# libraries, `go install` works with nothing but a Go toolchain. This target
# trades that away for roughly 3x faster embedding in `pi memory mine`.
#
# One-time setup:
#   brew install onnxruntime
#   make deps-accel        # fetches the prebuilt Rust tokenizers static lib
build-accel: deps-accel
	CGO_ENABLED=1 \
	CGO_CFLAGS="-I$$(brew --prefix onnxruntime)/include" \
	CGO_LDFLAGS="-L$$(brew --prefix onnxruntime)/lib -lonnxruntime -L$$HOME/.pi-go/lib" \
	go build $(GO_BUILD_FLAGS) -tags ORT -o pi ./cmd/pi

# hugot's ORT/XLA paths statically link the Rust HF tokenizers; only the pure-Go
# path uses the Go tokenizer. Prebuilt for darwin-arm64.
deps-accel:
	@mkdir -p $$HOME/.pi-go/lib
	@test -f $$HOME/.pi-go/lib/libtokenizers.a || ( \
	  echo "fetching libtokenizers..." && \
	  curl -sL -o /tmp/tok.tar.gz https://github.com/daulet/tokenizers/releases/download/v1.27.0/libtokenizers.darwin-arm64.tar.gz && \
	  tar xzf /tmp/tok.tar.gz -C $$HOME/.pi-go/lib && rm -f /tmp/tok.tar.gz )
	@echo "accel deps ready (onnxruntime via brew + libtokenizers)"

	go install ./cmd/pi/
	go install ./cmd/pi-sandbox

run: install
	pi --model minimax-m3:cloud

test: test-unit

test-unit:
	go test ./...

test-integration:
	go test -tags integration ./...

test-e2e:
	go test -tags e2e ./...

# keep old name as alias
e2e: test-e2e

# Manually-run full e2e eval of `/run`. Requires a built binary (make build)
# and an LLM API key in the environment. Runs from the pinned eval/base commit
# so re-runs are comparable. See internal/eval/eval.md for env knobs, the
# golden baseline flow and the LLM judge.
eval-run: build
	PI_EVAL_RUN=1 PI_BINARY=$(abspath ./pi) go test -tags e2e -v -run '^TestEvalRun$$' ./internal/tui/ -timeout 45m

# Pin the eval starting point at the current HEAD (tag eval/base) and run.
# Do this once when establishing a baseline, or deliberately to move it.
eval-pin: build
	PI_EVAL_RUN=1 PI_EVAL_PIN_BASE=1 PI_EVAL_SAVE_GOLDEN=1 PI_BINARY=$(abspath ./pi) \
		go test -tags e2e -v -run '^TestEvalRun$$' ./internal/tui/ -timeout 45m

# Re-evaluate against the recorded golden baseline, with the LLM judge on.
# Override the grader with PI_EVAL_JUDGE_MODEL=<model>.
PI_EVAL_JUDGE_MODEL ?= claude-sonnet-4-6
eval-judge: build
	PI_EVAL_RUN=1 PI_EVAL_BASELINE=eval/golden PI_EVAL_JUDGE_MODEL=$(PI_EVAL_JUDGE_MODEL) \
		PI_BINARY=$(abspath ./pi) go test -tags e2e -v -run '^TestEvalRun$$' ./internal/tui/ -timeout 45m

# Tool-coverage eval: one headless `pi --mode print` scenario per tool family,
# graded deterministically and rolled up into a coverage matrix over every
# registered tool. Requires a built binary and an LLM API key. Knobs:
# PI_EVAL_MODEL, PI_EVAL_SCENARIO=<name,...>, PI_EVAL_THINKING, PI_EVAL_TIMEOUT,
# PI_EVAL_STRICT=1, PI_EVAL_SERIAL=1. See internal/eval/scenarios/README.md.
eval-tools: build
	PI_EVAL_TOOLS=1 PI_BINARY=$(abspath ./pi) go test -tags e2e -v -run '^TestEvalTools$$' \
		./internal/eval/scenarios/ -parallel 4 -timeout 60m

# Same, with the LLM judge grading the suite (PI_EVAL_JUDGE_MODEL to override).
eval-tools-judge: build
	PI_EVAL_TOOLS=1 PI_EVAL_JUDGE_MODEL=$(PI_EVAL_JUDGE_MODEL) PI_BINARY=$(abspath ./pi) \
		go test -tags e2e -v -run '^TestEvalTools$$' ./internal/eval/scenarios/ -parallel 4 -timeout 60m

# Record a PGO profile: run the tool-coverage eval suite with --cpuprofile on
# every scenario's pi process, plus the TUI render benchmarks, then merge all
# profiles into cmd/pi/default.pgo. `go build` auto-detects default.pgo in the
# main package dir and enables PGO, so this is the one step that keeps the
# profile fresh.
#
# Two workloads are merged so the profile covers both of the binary's hot
# paths: the headless agent/tool loop (one `pi --mode print` per tool family)
# and the TUI render loop (RenderMessages / collapseBlankLines / matchLexer,
# which a live profile attributed ~24% of CPU to — bubbletea calls View() once
# per token during streaming). A single-workload profile would optimize only
# one half of the program. Requires a built binary and an LLM API key (see
# eval-tools). Knobs: PI_EVAL_MODEL, PI_EVAL_SCENARIO, PI_EVAL_TIMEOUT,
# PI_EVAL_SERIAL=1. See .pi-go/skills/pgo/SKILL.md.
record-pgo: build
	@mkdir -p tmp/pgo
	PI_EVAL_TOOLS=1 PI_EVAL_CPU_PROFILE=$(abspath tmp/pgo) PI_BINARY=$(abspath ./pi) \
		go test -tags e2e -v -run '^TestEvalTools$$' ./internal/eval/scenarios/ -parallel 4 -timeout 60m
	@go test -tags e2e -run '^$$' -bench 'BenchmarkRenderMessagesRunningCached|BenchmarkCollapseBlankLines|BenchmarkMatchLexerCached' \
		-benchtime 2s -cpuprofile $(abspath tmp/pgo)/render.pprof ./internal/tui/
	@go tool pprof -proto tmp/pgo/*.pprof > cmd/pi/default.pgo
	@echo "PGO profile written to cmd/pi/default.pgo ($$(ls -la cmd/pi/default.pgo | awk '{print $$5}') bytes)"
	@rm -rf tmp/pgo

test-all: test-unit test-integration test-e2e

test-coverage:
	go test -coverprofile=coverage.out -coverpkg=./internal/... ./internal/... && go tool cover -func=coverage.out | tail -1

test-ollama: build
	@bash scripts/test-ollama-e2e.sh

# Fails only on vulnerabilities that have a fix released; the rest are printed.
# Same gate CI runs, so a red build reproduces here.
vulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck -format json ./... | go run ./hack/vulngate

check-cve:
	go mod tidy -v
	grype db update || :
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./... | grep -A7 Vulnerability || :
	grype .

# grype scan of the repo, excluding gitignored scratch dirs (tmp/, .worktrees/)
# that hold vendored SDKs and agent worktrees — they are not part of the build.
# Note: grype needs one --exclude per pattern; a comma-joined glob matches nothing.
scan:
	grype . --exclude './tmp/**' --exclude './.worktrees/**'

# Same invocation the release workflow uses for the aggregate SBOM, so a
# release-time syft failure can be reproduced locally.
sbom:
	syft scan dir:. --exclude './hack/**' --source-name pi-go \
		--source-version "$$(git describe --tags --always)" \
		--output spdx-json=sbom.spdx.json

lint:
	golangci-lint run ./...

vet:
	go vet ./...

clean:
	rm -f pi coverage.out sbom.spdx.json cmd/pi/default.pgo

## OSX sandbox — pi-sandbox embeds pi-profile.sb, resolves params, tails denial logs automatically
sandbox-run: install
ifeq ($(shell uname),Darwin)
	pi-sandbox --model glm-5.2:cloud
else
	pi --model glm-5.2:cloud
endif

sandbox-log:
ifeq ($(shell uname),Darwin)
	/usr/bin/log show --predicate 'eventMessage CONTAINS "sandbox" AND eventMessage CONTAINS "deny"' --last $(or $(LAST),1m) --style compact
else
	@echo "sandbox-log is only available on macOS"
endif

# fetch-models: regenerate the embedded per-provider model catalogs under
# internal/provider/modeldata/ from live provider APIs. Providers without an
# API key are skipped with a note. Run before opening a PR that touches the
# model catalog (requirements Q10 of features/TOO/024-mistral-provider).
.PHONY: fetch-models
fetch-models:
	@bash scripts/fetch-models.sh

# fetch-modelsdev-pricing: regenerate the embedded models.dev pricing snapshot
# under internal/provider/modeldata/modelsdev-pricing.json. The runtime
# refreshes it from the same endpoint when it is more than a day old.
.PHONY: fetch-modelsdev-pricing
fetch-modelsdev-pricing:
	@bash scripts/fetch-modelsdev-pricing.sh
