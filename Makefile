.PHONY: build install test test-unit test-integration test-e2e test-all test-coverage test-ollama check-cve sbom lint vet e2e clean sandbox-run sandbox-log eval-run eval-pin eval-judge eval-tools eval-tools-judge hooks

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

build:
	go build $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" ./cmd/pi
	go build $(GO_BUILD_FLAGS) ./cmd/pi-sandbox

# Install onto PATH, i.e. $(go env GOBIN) or $GOPATH/bin. This used to be a bare
# `install: build`, which has no recipe — it dropped the binaries in the repo
# root and left nothing on PATH, so `pi` was never a command anywhere.
install:
	go install $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" ./cmd/pi
	go install $(GO_BUILD_FLAGS) ./cmd/pi-sandbox

# hooks: point core.hooksPath at the versioned .githooks/ directory.
#
# The hooks enforce AGENTS.md's signing rules: commit-msg adds a missing
# Signed-off-by, post-commit warns on unsigned commits, and pre-push
# HARD-FAILS any push containing unsigned or unsigned-off commits.
# Run once per clone: `make hooks`.
hooks:
	git config core.hooksPath .githooks
	@echo "hooks installed: commit-msg, post-commit, pre-push (.githooks/)"

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

test-all: test-unit test-integration test-e2e

test-coverage:
	go test -coverprofile=coverage.out -coverpkg=./internal/... ./internal/... && go tool cover -func=coverage.out | tail -1

test-ollama: build
	@bash scripts/test-ollama-e2e.sh

check-cve:
	go mod tidy -v
	grype db update || :
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./... | grep -A7 Vulnerability || :
	grype .

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
	rm -f pi coverage.out sbom.spdx.json

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
