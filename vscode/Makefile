BUN ?= bun
VSCE ?= npx @vscode/vsce
CODE ?= code
VSIXFILE ?= pi-go-vscode.vsix
VERSION ?= $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' package.json | head -1)

.PHONY: build package install release clean

build:
	$(BUN) run compile

package: build
	$(VSCE) package --no-dependencies -o $(VSIXFILE)

install: package
	$(CODE) --install-extension $(VSIXFILE) --force

# Production-only VSIX named for the release, e.g. pi-go-vscode-0.3.0.vsix —
# the artifact the CI release workflow attaches to GitHub releases.
release: build
	$(VSCE) package --no-dependencies -o pi-go-vscode-$(VERSION).vsix

clean:
	rm -f $(VSIXFILE) pi-go-vscode-*.vsix