#!/bin/bash
set -e

# Define version
VERSION="1.2.2"
PROJECT_NAME="localsend-cli"

# Output directory for debs
OUTPUT_DIR="bin/deb"
mkdir -p "$OUTPUT_DIR"

# Array of architectures to build
# Format: "debian_arch:go_binary_suffix"
ARCHS=(
    "amd64:linux-amd64"
    "arm64:linux-arm64"
    "armhf:linux-armv7"
    "armel:linux-armv6"
)

for pair in "${ARCHS[@]}"; do
    DEB_ARCH="${pair%%:*}"
    GO_SUFFIX="${pair##*:}"
    
    BINARY_SOURCE="bin/localsend_go-${GO_SUFFIX}"
    
    if [ ! -f "$BINARY_SOURCE" ]; then
        echo "Warning: Binary $BINARY_SOURCE not found, skipping $DEB_ARCH..."
        continue
    fi

    echo "Packaging for $DEB_ARCH..."

    # Create temp build dir
    BUILD_DIR="build_tmp/${PROJECT_NAME}_${VERSION}_${DEB_ARCH}"
    rm -rf "build_tmp"
    mkdir -p "$BUILD_DIR"

    # Copy debian template
    cp -r debian/* "$BUILD_DIR/"
    
    # Remove any existing binary in the template
    rm -f "$BUILD_DIR/usr/local/bin/localsend-cli"

    # Update Control file
    sed -i "s/^Architecture: .*/Architecture: ${DEB_ARCH}/" "$BUILD_DIR/DEBIAN/control"

    # Copy binary
    mkdir -p "$BUILD_DIR/usr/local/bin"
    cp "$BINARY_SOURCE" "$BUILD_DIR/usr/local/bin/localsend-cli"
    chmod 755 "$BUILD_DIR/usr/local/bin/localsend-cli"

    # Calculate installed size (in KB)
    INSTALLED_SIZE=$(du -s "$BUILD_DIR" | cut -f1)
    # Append Installed-Size to control file (optional but good practice)
    # sed -i "/^Description:/i Installed-Size: ${INSTALLED_SIZE}" "$BUILD_DIR/DEBIAN/control" 
    # (Skipping Installed-Size modification to keep it simple, dpkg usually handles it or ignores it, but strictly it should be there. 
    # Let's just rely on basic valid control file.)

    # Build .deb
    dpkg-deb --build --root-owner-group "$BUILD_DIR" "${OUTPUT_DIR}/${PROJECT_NAME}_${VERSION}_${DEB_ARCH}.deb"

    echo "Created ${OUTPUT_DIR}/${PROJECT_NAME}_${VERSION}_${DEB_ARCH}.deb"
done

# Cleanup
rm -rf build_tmp

echo "All packages created in $OUTPUT_DIR"
