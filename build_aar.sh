#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

rm -f shampoore.aar shampoore-sources.jar

gomobile bind \
    -target=android/arm64,android/arm \
    -androidapi 21 \
    -ldflags="-checklinkname=0" \
    -o shampoore.aar \
    ./mobile

ls -lh shampoore.aar
