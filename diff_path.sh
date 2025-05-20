#!/bin/bash

# Array to store failed paths
declare -a failed_paths

# Create tmp directory if it doesn't exist
mkdir -p /tmp

while read -r filepath; do
    # Skip empty lines and comments
    [[ -z "$filepath" || "$filepath" =~ ^# ]] && continue
    
    # Extract first and second parts of the path
    IFS='/' read -r first second rest <<< "$filepath"
    
    # Create directory name
    dir_name="${first}_${second}"
    mkdir -p "/tmp/$dir_name"
    
    # Create file name from rest of path (replace / with _)
    file_name="${rest//\//_}"
    
    echo "Processing: $filepath"
    
    # Perform diff and save to file
    if git diff "upstream/release/v0.124.x:$filepath" "origin/bump-v0.124.1:$filepath" > "/tmp/$dir_name/$file_name.diff" 2>/dev/null; then
        echo "Diff saved to: /tmp/$dir_name/$file_name.diff"
    else
        echo "Failed to diff: $filepath"
        failed_paths+=("$filepath")
    fi

done < "$1"

# Print failed paths at the end
if [ ${#failed_paths[@]} -ne 0 ]; then
    echo -e "\nThe following paths failed:"
    printf '%s\n' "${failed_paths[@]}"
fi
