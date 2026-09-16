BUN ?= bun
VSCE ?= npx @vscode/vsce
# The `code` CLI is only on PATH if the user ran "Shell Command: Install 'code'
# command in PATH" in VS Code. Fall back to the binary bundled inside the app;
# prefer Insiders, then stable, then whatever is on PATH.
CODE ?= $(shell for c in \
	  "/Applications/Visual Studio Code - Insiders.app/Contents/Resources/app/bin/code" \
	  "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" \
	  code; do command -v "$$c" >/dev/null 2>&1 && { echo "$$c"; break; }; done)
VERSION ?= $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' package.json | head -1)
MODE ?= mock

# Local build output lives outside the repo so branches and worktrees never see
# stale artifacts, and VSIXes never risk being committed. Overridable.
OUTDIR ?= $(HOME)/.vscode-ext
VSIXFILE ?= $(OUTDIR)/pi-go-vscode.vsix

# Unit tests with the `vscode` module mocked (--preload registers the stub
# before any source import resolves). Coverage gate: 90% line coverage.
.PHONY: test coverage
test:
	$(BUN) test --preload ./test/mock-vscode.ts

coverage:
	$(BUN) test --preload ./test/mock-vscode.ts --coverage

.PHONY: build package install release smoke clean

build:
	$(BUN) run compile

package: build
	@mkdir -p $(OUTDIR)
	$(VSCE) package --no-dependencies -o $(VSIXFILE)

install: package
	"$(CODE)" --install-extension "$(VSIXFILE)" --force

# Launch the editor in an isolated profile and assert the extension activates
# and the pi acp-server answers. Modes: mock (default), dev, installed.
smoke: build
	chmod +x scripts/smoke.sh && scripts/smoke.sh $(MODE)

# Production-only VSIX named for the release, e.g. pi-go-vscode-0.3.0.vsix —
# the artifact the CI release workflow attaches to GitHub releases. Unaffected
# by OUTDIR: it must stay at the repo root for the workflow's upload glob.
release: build
	$(VSCE) package --no-dependencies -o pi-go-vscode-$(VERSION).vsix

clean:
	rm -f $(VSIXFILE) pi-go-vscode-*.vsix