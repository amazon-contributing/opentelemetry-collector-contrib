#!/bin/bash

while read -r file; do
    # Skip empty lines and comments
    [[ -z "$file" || "$file" =~ ^# ]] && continue
    
    echo "Restoring: $file"
    git restore -s v0.124.1 "$file" || echo "Failed to restore: $file"
done < "$1"
