#!/bin/bash

# Build ARM64 Application Signals Collector for macOS
# Usage: ./build-local.sh

set -e

echo "=========================================="
echo "Building ARM64 Application Signals Collector"
echo "=========================================="

# Set variables
IMAGE_NAME="otelcol-appsignals-builder"
CONTAINER_NAME="otelcol-appsignals-build-temp"
OUTPUT_DIR="./output"

# Set build configuration
OCB_VERSION="${OCB_VERSION:-0.121.0}"
GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"

echo "OCB Version: $OCB_VERSION"
echo "Target Platform: $GOOS/$GOARCH"
echo "Building with current branch code"

# Clean and create output directory
echo "Cleaning output directory..."
rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"

echo ""
echo "Step 1/4: Building Docker image..."
# Switch to repo root directory for build
cd ../..
docker build \
    --build-arg OCB_VERSION=$OCB_VERSION \
    --build-arg GOOS=$GOOS \
    --build-arg GOARCH=$GOARCH \
    -f ocb-utils/agentcore/Dockerfile.agentcore-build \
    -t $IMAGE_NAME \
    .
cd ocb-utils/agentcore

echo ""
echo "Step 2/4: Running temporary container..."
docker run --name $CONTAINER_NAME -d $IMAGE_NAME

echo ""
echo "Step 3/4: Copying binary from container to local..."
docker cp $CONTAINER_NAME:/output/otelcol-agentcore "$OUTPUT_DIR/otelcol-agentcore"

echo ""
echo "Step 4/4: Cleaning up temporary container..."
docker rm -f $CONTAINER_NAME

echo ""
echo "=========================================="
echo "✅ Build completed!"
echo "=========================================="
echo ""
echo "Binary location: $OUTPUT_DIR/otelcol-agentcore"
echo ""
echo "Verify file:"
ls -lh "$OUTPUT_DIR/otelcol-agentcore"
echo ""
echo "Usage:"
echo "  $OUTPUT_DIR/otelcol-agentcore --config /path/to/your/config.yaml"
echo ""
