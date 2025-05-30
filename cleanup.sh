#!/bin/bash

# List of paths to keep (extracted from your replace directives)
declare -a KEEP_PATHS=(
    "exporter/awscloudwatchlogsexporter"
    "exporter/awsemfexporter"
    "exporter/awsxrayexporter"
    "extension/awsmiddleware"
    "extension/awsproxy"
    "internal/aws/awsutil"
    "internal/aws/containerinsight"
    "internal/aws/cwlogs"
    "internal/aws/k8s"
    "internal/aws/proxy"
    "internal/aws/xray"
    "internal/coreinternal"
    "internal/k8sconfig"
    "internal/kubelet"
    "internal/metadataproviders"
    "pkg/resourcetotelemetry"
    "pkg/stanza"
    "pkg/translator/prometheus"
    "processor/resourcedetectionprocessor"
    "receiver/awscontainerinsightreceiver"
    "receiver/awscontainerinsightskueuereceiver"
    "receiver/awsxrayreceiver"
    "receiver/jmxreceiver"
    "receiver/prometheusreceiver"
)

# Function to check if a path should be kept
should_keep() {
    local check_path="$1"
    for keep_path in "${KEEP_PATHS[@]}"; do
        if [[ "$check_path" == "$keep_path"* ]] || [[ "$keep_path" == "$check_path"* ]]; then
            return 0
        fi
    done
    return 1
}

# Find all directories
find . -type d | while read -r dir; do
    # Skip the root directory and .git
    if [[ "$dir" == "." ]] || [[ "$dir" == "./.git"* ]]; then
        continue
    fi

    # Remove leading ./
    dir_clean=${dir#./}

    if ! should_keep "$dir_clean"; then
        echo "Removing: $dir"
        rm -rf "$dir"
    fi
done

echo "Cleanup complete!"
