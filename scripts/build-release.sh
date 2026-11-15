#!/usr/bin/env bash
set -Eeuo pipefail
version="${VERSION:?Set VERSION to the release version without its v prefix}"
[[ "${version}" =~ ^[0-9]+(\.[0-9]+){0,2}(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]] || exit 64
output="${OUTPUT_DIR:-dist}"
mkdir -p "${output}"
for platform in linux darwin; do
    for arch in amd64 arm64; do
        CGO_ENABLED=0 GOOS="${platform}" GOARCH="${arch}" go build -trimpath \
            -ldflags="-s -w -X github.com/Aquadactyl/manta/system.Version=${version}" \
            -o "${output}/wings_${platform}_${arch}" .
    done
done
(cd "${output}" && sha256sum wings_linux_amd64 wings_linux_arm64 wings_darwin_amd64 wings_darwin_arm64 > checksums.txt)
