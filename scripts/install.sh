#!/usr/bin/env bash
# Install a verified Manta release. Existing node configuration and data stay put.
set -Eeuo pipefail

main() {
    local tag="${1:-}" offline="" asset work target old active="" installed=0 legacy legacy_updated=0 had_target=0
    if [[ "${tag}" == --help || "${tag}" == -h ]]; then
        printf 'Usage: sudo bash scripts/install.sh v1.1-beta.2 [--from-directory DIR]\n'
        return
    fi
    [[ ${#tag} -le 100 && "${tag}" =~ ^v(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)){0,2}(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]] || {
        echo 'Specify an exact release tag such as v1.1-beta.2.' >&2; return 64;
    }
    [[ ${EUID} -eq 0 ]] || { echo 'Run as root on Linux.' >&2; return 77; }
    [[ "$(uname -s)" == Linux ]] || { echo 'For macOS use docs/macos.md.' >&2; return 69; }
    shift
    if [[ $# -gt 0 ]]; then
        [[ $# -eq 2 && "$1" == --from-directory ]] || return 64
        offline="$(realpath "$2")"
    fi
    case "$(uname -m)" in
        x86_64) asset=manta_linux_amd64 ;;
        aarch64|arm64) asset=manta_linux_arm64 ;;
        *) echo 'Supported Linux architectures: amd64 and arm64.' >&2; return 69 ;;
    esac
    umask 077
    work="$(mktemp -d)"
    # This directory is created by this process, never supplied by the caller.
    local cleanup_command
    printf -v cleanup_command 'rm -rf -- %q' "${work}"
    # Capture the local directory now; main's locals are out of scope at EXIT.
    # shellcheck disable=SC2064
    trap "${cleanup_command}" EXIT
    if [[ -n "${offline}" ]]; then
        cp -- "${offline}/${asset}" "${offline}/checksums.txt" "${work}/"
    else
        local base="https://github.com/Aquadactyl/manta/releases/download/${tag}"
        curl --proto '=https' --tlsv1.2 -fSsL "${base}/${asset}" -o "${work}/${asset}"
        curl --proto '=https' --tlsv1.2 -fSsL "${base}/checksums.txt" -o "${work}/checksums.txt"
    fi
    local checksum
    checksum="$(awk -v asset="${asset}" '$2 == asset {print $1}' "${work}/checksums.txt")"
    [[ "${checksum}" =~ ^[a-fA-F0-9]{64}$ ]] || { echo 'Missing or ambiguous release checksum.' >&2; return 65; }
    (cd "${work}" && printf '%s  %s\n' "${checksum}" "${asset}" | sha256sum --check --status)
    chmod 0700 "${work}/${asset}"
    [[ "$("${work}/${asset}" version | head -n 1)" == "manta ${tag#v}" ]] || {
        echo 'Binary version does not match the requested release.' >&2; return 65;
    }
    target="${MANTA_INSTALL_DIR:-/usr/local/bin}/manta"
    [[ ! -L "${target}" ]] || { echo 'Refusing to replace a symlinked daemon executable.' >&2; return 65; }
    mkdir -p -- "$(dirname "${target}")"
    legacy="${MANTA_INSTALL_DIR:-/usr/local/bin}/wings"
    [[ ! -L "${legacy}" ]] || { echo 'Refusing to replace a symlinked legacy executable.' >&2; return 65; }
    old="${target}.previous"
    if [[ -f "${target}" ]]; then
        cp -p -- "${target}" "${old}"
        had_target=1
    fi
    if [[ "${MANTA_MANAGE_SERVICE:-1}" == 1 ]] && command -v systemctl >/dev/null; then
        if systemctl is-active --quiet manta.service; then active=manta.service; fi
        if systemctl is-active --quiet wings.service; then
            [[ -z "${active}" ]] || { echo 'Both daemon services are running; resolve the duplicate before updating.' >&2; return 65; }
            [[ -f "${legacy}" ]] || { echo 'Custom legacy executable: migrate the service manually first.' >&2; return 65; }
            active=wings.service
        fi
    fi
    if [[ -f "${legacy}" ]]; then cp -p -- "${legacy}" "${legacy}.previous"; fi
    # Stage on the destination filesystem so rename is atomic.
    install -m 0755 -- "${work}/${asset}" "${target}.new"
    if [[ -n "${active}" ]]; then systemctl stop "${active}"; fi
    mv -f -- "${target}.new" "${target}"
    if [[ -f "${legacy}" ]]; then
        install -m 0755 -- "${work}/${asset}" "${legacy}.new"
        mv -f -- "${legacy}.new" "${legacy}"
        legacy_updated=1
    fi
    installed=1
    if [[ -n "${active}" ]]; then
        if ! systemctl start "${active}"; then
            if [[ "${had_target}" == 1 && "${installed}" == 1 ]]; then
                cp -p -- "${old}" "${target}"
            else
                rm -f -- "${target}"
            fi
            if [[ "${legacy_updated}" == 1 ]]; then cp -p -- "${legacy}.previous" "${legacy}"; fi
            systemctl start "${active}" || true
            echo "Service failed to start; restored the previous executables. Inspect journalctl -u ${active}." >&2
            return 1
        fi
    fi
    printf 'Installed Manta %s at %s.\n' "${tag#v}" "${target}"
    printf 'New services use manta.service. Configuration and game data were preserved. See docs/INSTALLING.md.\n'
    if [[ "${active}" == wings.service ]]; then
        printf 'Legacy service retained for this update; follow docs/INSTALLING.md#migrate-the-service to switch to manta.service.\n'
    fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
