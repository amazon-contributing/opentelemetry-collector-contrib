#!/usr/bin/env bash
#
# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

contrib_dir="$(dirname -- "$0")/../../.."
initial_checkout_size=$(du -sm "$contrib_dir" 2>/dev/null | cut -f1 || echo "0")
space_before_cleanup=$(df -m / | tail -1 | awk '{print $4}')

echo "=== Disk Space Cleanup Report ==="
echo "Initial checkout size: ${initial_checkout_size} MiB"
echo "Available space before cleanup: ${space_before_cleanup} MiB"

# The Android SDK is the biggest culprit for the lack of disk space in CI.
# It is installed into /usr/local/lib/android manually (ie. not with apt) by this script:
# https://github.com/actions/runner-images/blob/main/images/ubuntu/scripts/build/install-android-sdk.sh

echo "Deleting unused Android SDK and tools..."
if [ -d "/usr/local/lib/android" ]; then
    android_size=$(du -sm /usr/local/lib/android 2>/dev/null | cut -f1 || echo "0")
    echo "Android SDK size: ${android_size} MiB"
    sudo rm -rf /usr/local/lib/android
    echo "✓ Android SDK removed"
else
    echo "ℹ Android SDK directory not found, skipping cleanup"
fi

# Additional cleanup for other large directories commonly found in CI
echo "Cleaning up additional large directories..."
cleanup_dirs=(
    "/usr/share/dotnet"
    "/usr/local/share/boost"
    "/usr/local/lib/node_modules"
    "/opt/ghc"
)

total_freed=0
for dir in "${cleanup_dirs[@]}"; do
    if [ -d "$dir" ]; then
        dir_size=$(du -sm "$dir" 2>/dev/null | cut -f1 || echo "0")
        if [ "$dir_size" -gt 100 ]; then  # Only remove if > 100MB
            echo "Removing $dir (${dir_size} MiB)..."
            sudo rm -rf "$dir" || echo "⚠ Failed to remove $dir"
            total_freed=$((total_freed + dir_size))
        fi
    fi
done

free_space=$(df -m / | tail -1 | awk '{print $4}')
freed_space=$((free_space - space_before_cleanup))
echo "Freed ${freed_space} MiB of disk space (${total_freed} MiB from additional cleanup)"

# Hypothetical free space with the cleanup but without checkout
baseline_space=$((free_space + initial_checkout_size))
echo "BASELINE_SPACE=${baseline_space}" >> "$GITHUB_ENV"

echo "=== Final Status ==="
echo "Available space after cleanup: ${free_space} MiB"
echo "Baseline space (for monitoring): ${baseline_space} MiB"
echo "================================="
