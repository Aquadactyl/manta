#!/usr/bin/env bash
set -Eeuo pipefail
installer="$(cd "$(dirname "$0")" && pwd)/install.sh"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/download" "$work/bin" "$work/mock"
printf '#!/bin/sh\necho "manta 1.1-beta.2"\n' > "$work/download/manta_linux_amd64"
cp "$work/download/manta_linux_amd64" "$work/download/manta_linux_arm64"
(cd "$work/download" && sha256sum manta_linux_* > checksums.txt)
cat > "$work/mock/systemctl" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$SERVICE_LOG"
case "$1" in
    is-active) [[ "${ACTIVE_SERVICE}" == both || "$3" == "${ACTIVE_SERVICE}" ]] ;;
    stop) exit 0 ;;
    start) [[ "${FAIL_START:-0}" != 1 ]] ;;
    *) exit 64 ;;
esac
MOCK
chmod +x "$work/mock/systemctl"
export PATH="$work/mock:$PATH" MANTA_INSTALL_DIR="$work/bin" MANTA_MANAGE_SERVICE=1 SERVICE_LOG="$work/service.log"

# Failed canonical restart restores the previous executable.
export ACTIVE_SERVICE=manta.service FAIL_START=1
printf 'previous native\n' > "$work/bin/manta"
if bash "$installer" v1.1-beta.2 --from-directory "$work/download"; then exit 1; fi
[[ "$(cat "$work/bin/manta")" == 'previous native' ]]
grep -q '^stop manta.service$' "$SERVICE_LOG"

# Legacy updates retain the original executable for rollback, and do not leave
# a newly installed canonical binary behind after a failed service restart.
rm "$work/bin/manta" "$work/bin/manta.previous"
printf 'previous legacy\n' > "$work/bin/wings"
export ACTIVE_SERVICE=wings.service
if bash "$installer" v1.1-beta.2 --from-directory "$work/download"; then exit 1; fi
[[ ! -e "$work/bin/manta" && "$(cat "$work/bin/wings")" == 'previous legacy' ]]
grep -q '^stop wings.service$' "$SERVICE_LOG"

export FAIL_START=0
bash "$installer" v1.1-beta.2 --from-directory "$work/download"
[[ "$("$work/bin/manta" version)" == 'manta 1.1-beta.2' ]]
[[ "$("$work/bin/wings" version)" == 'manta 1.1-beta.2' ]]

# Reject two active services before stopping either one.
export ACTIVE_SERVICE=both
: > "$SERVICE_LOG"
if bash "$installer" v1.1-beta.2 --from-directory "$work/download"; then exit 1; fi
! grep -q '^stop ' "$SERVICE_LOG"
printf 'Manta service update and rollback checks passed.\n'
