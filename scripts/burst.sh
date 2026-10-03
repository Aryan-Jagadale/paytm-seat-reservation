#!/usr/bin/env bash

set -euo pipefail

if [ "$#" -ne 1 ]; then
    echo "usage: ./scripts/burst.sh <BASE_URL>"
    echo "example: ./scripts/burst.sh http://127.0.0.1:8081"
    exit 1
fi

BASE_URL="$1"

echo "Running Paytm seat-reservation acceptance burst..."
echo "Base URL: ${BASE_URL}"
echo

go run ./scripts/burst "${BASE_URL}"