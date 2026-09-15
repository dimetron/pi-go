BUN ?= bun
VSCE ?= npx @vscode/vsce
CODE ?= code
VSIXFILE ?= pi-go-vscode.vsix

.PHONY: build package install clean

build:
	$(BUN) run compile

package: build
	$(VSCE) package --no-dependencies -o $(VSIXFILE)

install: package
	$(CODE) --install-extension $(VSIXFILE) --force

clean:
	rm -f $(VSIXFILE)