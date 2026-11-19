#!/usr/bin/env bash
set -Eeuo pipefail

# Keep MongoDB attached to the container even if an existing configuration forks.
export MONGODB_CONFIG_OVERRIDE_NOFORK=true
cd /home/container

if (( $# > 0 )); then
    exec "$@"
fi

: "${STARTUP:?Set the egg startup command}"
# The egg uses exec so MongoDB receives the container's shutdown signal directly.
# Expand environment variables in the command without printing credentials.
exec /bin/bash -c "$STARTUP"
