#!/usr/bin/env bash
set -Eeuo pipefail
installer="$(cd "$(dirname "$0")" && pwd)/install.sh"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/download" "$work/bin"
printf '#!/bin/sh\necho "manta 1.0-beta"\n' > "$work/download/manta_linux_amd64"
cp "$work/download/manta_linux_amd64" "$work/download/manta_linux_arm64"
(cd "$work/download" && sha256sum manta_linux_* > checksums.txt)
export MANTA_INSTALL_DIR="$work/bin" MANTA_MANAGE_SERVICE=0
printf 'previous executable\n' > "$work/bin/manta"
bash "$installer" v1.0-beta --from-directory "$work/download"
[[ "$("$work/bin/manta" version)" == 'manta 1.0-beta' ]]
[[ "$(cat "$work/bin/manta.previous")" == 'previous executable' ]]
# A fresh install must not create the legacy executable.
[[ ! -e "$work/bin/wings" ]]
# Existing legacy automation is updated without stranding its executable.
printf 'old legacy executable\n' > "$work/bin/wings"
bash "$installer" v1.0-beta --from-directory "$work/download"
[[ "$("$work/bin/wings" version)" == 'manta 1.0-beta' ]]
[[ "$(cat "$work/bin/wings.previous")" == 'old legacy executable' ]]
printf 'tampered\n' >> "$work/download/manta_linux_amd64"
printf 'tampered\n' >> "$work/download/manta_linux_arm64"
if bash "$installer" v1.0-beta --from-directory "$work/download"; then echo 'Accepted checksum mismatch' >&2; exit 1; fi
[[ "$("$work/bin/manta" version)" == 'manta 1.0-beta' ]]
if bash "$installer" 'v1.0;id'; then echo 'Accepted invalid tag' >&2; exit 1; fi
printf 'Manta installer checks passed.\n'
