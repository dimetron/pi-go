#!/bin/bash
# exit on error
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Configuration
REPO="dimetron/pi-go"
BINARY_NAME="pi"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# Helper functions
log_info() {
    echo -e "${BLUE}ℹ️  ${NC}$1" >&2
}

log_success() {
    echo -e "${GREEN}✅ ${NC}$1" >&2
}

log_step() {
    echo -e "${CYAN}🔄 ${NC}$1" >&2
}

log_step_complete() {
    echo -e "\033[1A\033[2K${GREEN}✅ ${NC}$1" >&2
}

log_warn() {
    echo -e "${YELLOW}⚠️  ${NC}$1" >&2
}

log_error() {
    echo -e "${RED}❌ ${NC}$1" >&2
}

log_header() {
    echo -e "\n${BOLD}${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}" >&2
    echo -e "${BOLD}${CYAN}  🚀 pi-go Installer${NC}" >&2
    echo -e "${BOLD}${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n" >&2
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Detect OS and architecture
detect_platform() {
    local os
    local arch

    # Detect OS - use uname -s directly (returns Darwin/Linux/etc)
    case "$(uname -s)" in
        Darwin)
            os="darwin"
            ;;
        Linux)
            os="linux"
            ;;
        CYGWIN*|MINGW32*|MSYS*|MINGW*)
            os="windows"
            ;;
        *)
            log_error "Unsupported operating system: $(uname -s)"
            exit 1
            ;;
    esac

    # Detect architecture
    case "$(uname -m)" in
        x86_64|amd64)
            arch="amd64"
            ;;
        arm64|aarch64)
            arch="arm64"
            ;;
        *)
            log_error "Unsupported architecture: $(uname -m)"
            exit 1
            ;;
    esac

    echo "${os}-${arch}"
}

# Get latest release version from GitHub
get_latest_version() {
    log_step "Fetching latest version from GitHub..."

    local response
    if command_exists curl; then
        response=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest")
    elif command_exists wget; then
        response=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest")
    else
        log_error "Neither curl nor wget is available. Please install one of them."
        exit 1
    fi

    local version=$(echo "$response" | grep '"tag_name"' | sed -E 's/.*"tag_name": "([^"]+)".*/\1/')

    if [ -z "$version" ]; then
        echo "" >&2
        log_error "Failed to get latest version from GitHub API"
        exit 1
    fi

    echo "$version"
}

# Download binary
download_binary() {
    local version="$1"
    local platform="$2"

    # GoReleaser default archive template: {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}
    # ProjectName defaults to the repo name 'pi-go'
    local ext="tar.gz"
    if [[ "$platform" == windows-* ]]; then
        ext="zip"
    fi
    local download_url="https://github.com/${REPO}/releases/download/${version}/pi-go_${version#v}_${platform}.${ext}"
    local temp_file="/tmp/pi-install.$$.archive"

    log_step "Downloading pi-go ${BOLD}${version}${NC} for ${BOLD}${platform}${NC}..."

    if command_exists curl; then
        curl -fsSL -o "$temp_file" "$download_url"
    elif command_exists wget; then
        wget -q -O "$temp_file" "$download_url"
    fi

    if [ ! -f "$temp_file" ]; then
        echo "" >&2
        log_error "Failed to download binary from $download_url"
        exit 1
    fi

    echo "$temp_file"
}

# Install binary
install_binary() {
    local temp_file="$1"
    local install_path="${INSTALL_DIR}/${BINARY_NAME}"

    log_step "Installing to ${BOLD}${install_path}${NC}..."

    # Create install directory if needed
    mkdir -p "$INSTALL_DIR"

    # Extract archive
    if [[ "$temp_file" == *.zip ]]; then
        unzip -o "$temp_file" -d "/tmp/pi-install.$$"
        mv "/tmp/pi-install.$$/pi.exe" "$install_path" 2>/dev/null || mv "/tmp/pi-install.$$/pi" "$install_path"
        rm -rf "/tmp/pi-install.$$"
    else
        tar -xzf "$temp_file" -C /tmp
        local extracted_file=$(tar -tzf "$temp_file" | grep -E '^pi(-exe)?$' | head -1)
        if [ -z "$extracted_file" ]; then
            extracted_file="pi"
        fi
        mv "/tmp/${extracted_file}" "$install_path"
    fi

    chmod +x "$install_path"
    rm -f "$temp_file"

    echo "$install_path"
}

# Verify installation
verify_installation() {
    local install_path="$1"

    log_step "Verifying installation..."

    if [[ ":$PATH:" == *":${INSTALL_DIR}:"* ]]; then
        log_step_complete "Installation verified! ${BOLD}pi${NC} is in PATH"
        echo -e "${BLUE}ℹ️  ${NC}Run ${BOLD}pi${NC} to start" >&2
    else
        echo "" >&2
        log_warn "${BINARY_NAME} installed but ${BOLD}${INSTALL_DIR}${NC} is not in PATH"
        log_info "Add this to your shell profile (~/.bashrc, ~/.zshrc):"
        echo -e "${CYAN}   export PATH=\"${INSTALL_DIR}:\$PATH\"${NC}" >&2
    fi
}

# Main installation function
main() {
    log_header

    # Check prerequisites
    log_step "Checking prerequisites..."
    if ! command_exists curl && ! command_exists wget; then
        echo "" >&2
        log_error "This script requires either curl or wget."
        exit 1
    fi
    log_step_complete "Prerequisites check passed"

    # Detect platform
    log_step "Detecting platform..."
    local platform=$(detect_platform)
    log_step_complete "Platform detected: ${BOLD}${platform}${NC}"

    # Get latest version
    local version=$(get_latest_version)
    log_step_complete "Found latest version: ${BOLD}${version}${NC}"

    # Download binary
    local temp_file=$(download_binary "$version" "$platform")
    log_step_complete "Download complete"

    # Install binary
    local install_path=$(install_binary "$temp_file")
    log_step_complete "Binary installed"

    # Verify
    verify_installation "$install_path"

    echo -e "\n${BOLD}${GREEN}🎉 Installation complete!${NC}" >&2
    echo -e "${GREEN}   Run ${BOLD}pi${NC}${GREEN} to start.${NC}\n" >&2
}

# Handle command line arguments
case "${1:-}" in
    -h|--help)
        echo -e "${BOLD}${CYAN}🚀 pi-go Installer${NC}"
        echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
        echo -e "\n${BOLD}USAGE:${NC}"
        echo -e "  $0 [OPTIONS]"
        echo -e "\n${BOLD}OPTIONS:${NC}"
        echo -e "  -h, --help     Show this help message"
        echo -e "\n${BOLD}ENVIRONMENT VARIABLES:${NC}"
        echo -e "  INSTALL_DIR    Installation directory (default: \$HOME/.local/bin)"
        echo ""
        exit 0
        ;;
    *)
        main "$@"
        ;;
esac
