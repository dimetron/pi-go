#!/bin/bash
set -e

# Configuration
REPO="dimetron/pi-go"
BINARY_NAME="pi"

# Detect OS and Arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
    linux) ;;
    darwin) ;;
    windows) ;;
    *) echo "Unsupported OS: $OS"; exit 1;;
esac

case "$ARCH" in
    x86_64) ARCH="amd64";;
    aarch64|arm64) ARCH="arm64";;
    *) echo "Unsupported Arch: $ARCH"; exit 1;;
esac

echo "Detected system: $OS/$ARCH"

# Get latest version from GitHub
LATEST_VERSION=$(curl -s https://api.github.com/repos/$REPO/releases/latest | grep '"tag_name":' | sed -E 's/.*"tag_name": "([^"]+)".*/\1/')

if [ -z "$LATEST_VERSION" ]; then
    echo "Error: Could not find latest release version."
    exit 1
fi

echo "Installing version $LATEST_VERSION..."

# GoReleaser default archive template: {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}
# ProjectName defaults to the repo name 'pi-go'
ASSET_NAME="pi-go_${LATEST_VERSION}_${OS}_${ARCH}.tar.gz"
if [ "$OS" == "windows" ]; then
    ASSET_NAME="pi-go_${LATEST_VERSION}_${OS}_${ARCH}.zip"
fi

URL="https://github.com/${REPO}/releases/download/${LATEST_VERSION}/${ASSET_NAME}"

echo "Downloading $URL..."
curl -L "$URL" -o "pi-install-tmp.archive"

# Install logic
if [ "$OS" == "windows" ]; then
    unzip pi-install-tmp.archive -d pi-install-tmp
    mv pi-install-tmp/pi.exe .
    echo "Binary unpacked as pi.exe. Please move it to your PATH."
else
    tar -xzf pi-install-tmp.archive
    
    # Determine installation directory
    # Try /usr/local/bin first if writable, otherwise ~/.local/bin
    if [ -w "/usr/local/bin" ]; then
        INSTALL_DIR="/usr/local/bin"
    else
        INSTALL_DIR="$HOME/.local/bin"
    fi
    
    mkdir -p "$INSTALL_DIR"
    mv pi "$INSTALL_DIR/"
    chmod +x "$INSTALL_DIR/pi"
    
    echo "Successfully installed $BINARY_NAME to $INSTALL_DIR"
    
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        echo "Warning: $INSTALL_DIR is not in your PATH."
        echo "Add it by adding this to your shell config (.bashrc, .zshrc):"
        echo "export PATH=\"\$PATH:$INSTALL_DIR\""
    fi
fi

rm -f pi-install-tmp.archive
rm -rf pi-install-tmp
