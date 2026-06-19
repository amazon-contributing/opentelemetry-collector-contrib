#!/usr/bin/env bash

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

set -e

DIR="$1"
CONFIG_IN="cmd/$DIR/builder-config.yaml"
CONFIG_OUT="cmd/$DIR/builder-config-replaced.yaml"

cp "$CONFIG_IN" "$CONFIG_OUT"

local_mods=$(find . -type f -name "go.mod" -exec dirname {} \; | sort)
for mod_path in $local_mods; do
    mod=${mod_path#"."} # remove initial dot
    echo "  - github.com/open-telemetry/opentelemetry-collector-contrib$mod => ../..$mod" >> "$CONFIG_OUT"
done

# Also add replaces for amazon-contributing modules that use a different module path prefix.
for mod_path in $local_mods; do
    mod=${mod_path#"."} # remove initial dot
    mod_file="$mod_path/go.mod"
    if grep -q '^module github.com/amazon-contributing/' "$mod_file" 2>/dev/null; then
        amazon_mod=$(head -1 "$mod_file" | sed 's/^module //')
        echo "  - $amazon_mod => ../..$mod" >> "$CONFIG_OUT"
    fi
done
echo "Wrote replace statements to $CONFIG_OUT"
