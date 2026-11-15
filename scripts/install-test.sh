#!/usr/bin/env bash
set -Eeuo pipefail
installer="$(cd "$(dirname "$0")" && pwd)/install.sh"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/download" "$work/bin"
printf '#!/bin/sh\necho "manta 1.0-beta"\n' > "$work/download/wings_linux_amd64"
cp "$work/download/wings_linux_amd64" "$work/download/wings_linux_arm64"
(cd "$work/download" && sha256sum wings_linux_* > checksums.txt)
export MANTA_INSTALL_DIR="$work/bin" MANTA_MANAGE_SERVICE=0
printf 'previous executable\n' > "$work/bin/wings"
bash "$installer" v1.0-beta --from-directory "$work/download"
[[ "$("$work/bin/wings" version)" == 'manta 1.0-beta' ]]
[[ "$(cat "$work/bin/wings.previous")" == 'previous executable' ]]
printf 'tampered\n' >> "$work/download/wings_linux_amd64"
printf 'tampered\n' >> "$work/download/wings_linux_arm64"
if bash "$installer" v1.0-beta --from-directory "$work/download"; then echo 'Accepted checksum mismatch' >&2; exit 1; fi
[[ "$("$work/bin/wings" version)" == 'manta 1.0-beta' ]]
if bash "$installer" 'v1.0;id'; then echo 'Accepted invalid tag' >&2; exit 1; fi
printf 'Manta installer checks passed.\n'
