#!/bin/sh
# Keep the local dashboard available across chat turns for Google authorization.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
STATE="$ROOT/.linode"
SOCKET="$STATE/dashboard-preview.sock"
umask 077
if ! ssh -F "$STATE/ssh_config" -S "$SOCKET" -O check cnc-linode-server >/dev/null 2>&1; then
  ssh -f -N -M -S "$SOCKET" -F "$STATE/ssh_config" \
    -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
    -L 127.0.0.1:3400:127.0.0.1:3000 -L 127.0.0.1:8080:127.0.0.1:3000 cnc-linode-server
fi
printf '%s\n' 'Open http://localhost:8080/dashboard?connectDrive=1'
