#!/bin/bash
# Build MCPB bundles for Claude Desktop
# Usage: ./mcpb/build.sh [version] [--use-dist]
#
# This script creates platform-specific .mcpb bundles for:
# - macOS (darwin) amd64 and arm64
# - Windows amd64
#
# Options:
#   --use-dist  Use binaries from goreleaser's dist/ folder instead of building
#
# Prerequisites:
# - Go toolchain (if building from source)
# - goreleaser dist/ output (if using --use-dist)

set -e

VERSION="${1:-dev}"
USE_DIST=false

# Check for --use-dist flag
for arg in "$@"; do
    if [ "$arg" = "--use-dist" ]; then
        USE_DIST=true
    fi
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
BUILD_DIR="$PROJECT_ROOT/dist/mcpb"
MANIFEST_TEMPLATE="$SCRIPT_DIR/manifest.json"

echo "Building MCPB bundles for mcp-data-platform v${VERSION}"
if [ "$USE_DIST" = true ]; then
    echo "Using pre-built binaries from dist/"
fi

# Clean and create build directory
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

# Platforms to build for (Claude Desktop supports macOS and Windows)
# Format: GOOS:GOARCH:goreleaser_archive_suffix
PLATFORMS=(
    "darwin:amd64:darwin_amd64"
    "darwin:arm64:darwin_arm64"
    "windows:amd64:windows_amd64"
)

for platform in "${PLATFORMS[@]}"; do
    IFS=':' read -r GOOS GOARCH DIST_SUFFIX <<< "$platform"

    PLATFORM_NAME="${GOOS}-${GOARCH}"
    BUNDLE_DIR="$BUILD_DIR/mcp-data-platform-${PLATFORM_NAME}"

    echo ""
    echo "=== Building for ${PLATFORM_NAME} ==="

    # Create bundle directory
    mkdir -p "$BUNDLE_DIR"

    # Determine binary name
    BINARY_NAME="mcp-data-platform"
    if [ "$GOOS" = "windows" ]; then
        BINARY_NAME="mcp-data-platform.exe"
    fi

    if [ "$USE_DIST" = true ]; then
        # Use goreleaser's pre-built binary from archive
        # GoReleaser creates archives like mcp-data-platform_1.0.0_darwin_amd64.tar.gz
        ARCHIVE_PATTERN="$PROJECT_ROOT/dist/mcp-data-platform_${VERSION}_${DIST_SUFFIX}"

        if [ "$GOOS" = "windows" ]; then
            ARCHIVE="${ARCHIVE_PATTERN}.zip"
            if [ -f "$ARCHIVE" ]; then
                echo "Extracting ${BINARY_NAME} from $ARCHIVE..."
                unzip -q -j "$ARCHIVE" "$BINARY_NAME" -d "$BUNDLE_DIR/"
            fi
        else
            ARCHIVE="${ARCHIVE_PATTERN}.tar.gz"
            if [ -f "$ARCHIVE" ]; then
                echo "Extracting ${BINARY_NAME} from $ARCHIVE..."
                tar -xzf "$ARCHIVE" -C "$BUNDLE_DIR/" --strip-components=0 "$BINARY_NAME" 2>/dev/null || \
                tar -xzf "$ARCHIVE" -C "$BUNDLE_DIR/" "$BINARY_NAME" 2>/dev/null || \
                (cd "$BUNDLE_DIR" && tar -xzf "$ARCHIVE" && mv */mcp-data-platform . 2>/dev/null || true)
            fi
        fi

        if [ ! -f "$BUNDLE_DIR/$BINARY_NAME" ]; then
            echo "ERROR: Could not extract binary from $ARCHIVE"
            echo "Make sure goreleaser has run first, or omit --use-dist to build from source"
            exit 1
        fi
    else
        # Build from source
        echo "Compiling ${BINARY_NAME}..."
        CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
            -trimpath \
            -ldflags="-s -w -X github.com/txn2/mcp-data-platform/internal/server.Version=${VERSION}" \
            -o "$BUNDLE_DIR/$BINARY_NAME" \
            "$PROJECT_ROOT/cmd/mcp-data-platform"
    fi

    # Copy and update manifest
    echo "Creating manifest.json..."
    sed "s/\"version\": \"0.0.0\"/\"version\": \"${VERSION}\"/" "$MANIFEST_TEMPLATE" > "$BUNDLE_DIR/manifest.json"

    # Copy icon if available
    ICON_FILE="$PROJECT_ROOT/docs/images/logo.png"
    if [ -f "$ICON_FILE" ]; then
        echo "Copying icon.png..."
        cp "$ICON_FILE" "$BUNDLE_DIR/icon.png"
    fi

    # Create .mcpb bundle (naming matches goreleaser archive pattern)
    MCPB_FILE="$BUILD_DIR/mcp-data-platform_${VERSION}_${GOOS}_${GOARCH}.mcpb"

    echo "Packaging ${MCPB_FILE}..."
    if command -v mcpb &> /dev/null; then
        # Use official mcpb CLI if available
        mcpb pack "$BUNDLE_DIR" "$MCPB_FILE"
    else
        # Fallback to zip (mcpb files are just zip archives)
        echo "Note: mcpb CLI not found, using zip fallback"
        (cd "$BUNDLE_DIR" && zip -r "$MCPB_FILE" .)
    fi

    echo "Created: $MCPB_FILE"
done

echo ""
echo "=== Build complete ==="
echo "MCPB bundles created in: $BUILD_DIR"
ls -la "$BUILD_DIR"/*.mcpb 2>/dev/null || echo "No .mcpb files found"
