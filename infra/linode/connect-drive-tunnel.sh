#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
SSH_CONFIG="$ROOT/.linode/ssh_config"

ACTION=start
case "${1:-}" in
  --stop) ACTION=stop; shift ;;
  --install) ACTION=install; shift ;;
  --uninstall) ACTION=uninstall; shift ;;
esac

PORT=${1:-8081}
case "$PORT" in
  *[!0-9]*|'') echo "Port must be numeric" >&2; exit 2 ;;
esac
if [ "$PORT" -lt 1024 ] || [ "$PORT" -gt 65535 ]; then
  echo "Choose a port between 1024 and 65535" >&2
  exit 2
fi

START_URL="http://localhost:${PORT}/drive-local-start"
USER_ID=$(id -u)
# Keep this path short: Unix-domain socket paths have a small limit on macOS.
CONTROL_SOCKET="/tmp/cnc-drive-${USER_ID}-${PORT}.sock"
LAUNCH_LABEL="com.cnc.drive-tunnel.${PORT}"
LAUNCH_DOMAIN="gui/${USER_ID}"
LAUNCH_PLIST="$HOME/Library/LaunchAgents/${LAUNCH_LABEL}.plist"
LAUNCH_LOG="/tmp/cnc-drive-tunnel-${USER_ID}-${PORT}.log"
LAUNCH_ERROR_LOG="/tmp/cnc-drive-tunnel-${USER_ID}-${PORT}.error.log"
LAUNCH_SSH_DIR="$HOME/.ssh/cnc-drive-tunnel"
LAUNCH_SSH_CONFIG="$LAUNCH_SSH_DIR/config"

launch_agent_loaded() {
  command -v launchctl >/dev/null 2>&1 && \
    launchctl print "${LAUNCH_DOMAIN}/${LAUNCH_LABEL}" >/dev/null 2>&1
}

stop_control_master() {
  if [ -S "$CONTROL_SOCKET" ]; then
    ssh -F "$SSH_CONFIG" -S "$CONTROL_SOCKET" -O exit cnc-linode-server >/dev/null 2>&1 || true
    rm -f "$CONTROL_SOCKET"
  fi
}

stop_launch_agent() {
  if launch_agent_loaded; then
    launchctl bootout "${LAUNCH_DOMAIN}/${LAUNCH_LABEL}" >/dev/null 2>&1 || true
  fi
}

if [ "$ACTION" = stop ]; then
  if launch_agent_loaded || [ -S "$CONTROL_SOCKET" ]; then
    stop_launch_agent
    stop_control_master
    printf 'Stopped the Google Drive tunnel on localhost:%s.\n' "$PORT"
  else
    printf 'No managed Google Drive tunnel is running on localhost:%s.\n' "$PORT"
  fi
  exit 0
fi

if [ "$ACTION" = uninstall ]; then
  stop_launch_agent
  stop_control_master
  rm -f "$LAUNCH_PLIST"
  rm -f "$LAUNCH_SSH_CONFIG" "$LAUNCH_SSH_DIR/id_ed25519" "$LAUNCH_SSH_DIR/known_hosts"
  rmdir "$LAUNCH_SSH_DIR" 2>/dev/null || true
  printf 'Removed the automatic Google Drive tunnel for localhost:%s.\n' "$PORT"
  exit 0
fi

tunnel_healthy() {
  curl --silent --show-error --fail --max-time 3 \
    "http://127.0.0.1:${PORT}/login" >/dev/null 2>&1
}

wait_until_healthy() {
  attempts=0
  until tunnel_healthy; do
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 15 ]; then
      printf 'The SSH tunnel started, but localhost:%s did not become ready.\n' "$PORT" >&2
      return 1
    fi
    sleep 1
  done
}

if [ "$ACTION" = install ]; then
  if [ "$(uname -s)" != Darwin ] || ! command -v launchctl >/dev/null 2>&1; then
    printf '%s\n' 'Automatic installation currently requires macOS launchd.' >&2
    exit 2
  fi

  stop_launch_agent
  stop_control_master
  mkdir -p "$HOME/Library/LaunchAgents"
  mkdir -p "$LAUNCH_SSH_DIR"
  chmod 0700 "$LAUNCH_SSH_DIR"
  cp "$ROOT/.linode/id_ed25519" "$LAUNCH_SSH_DIR/id_ed25519"
  cp "$ROOT/.linode/known_hosts" "$LAUNCH_SSH_DIR/known_hosts"
  sed \
    -e "s|$ROOT/.linode/id_ed25519|$LAUNCH_SSH_DIR/id_ed25519|g" \
    -e "s|$ROOT/.linode/known_hosts|$LAUNCH_SSH_DIR/known_hosts|g" \
    "$SSH_CONFIG" >"$LAUNCH_SSH_CONFIG"
  chmod 0600 "$LAUNCH_SSH_CONFIG" "$LAUNCH_SSH_DIR/id_ed25519" "$LAUNCH_SSH_DIR/known_hosts"

  escaped_config=$(printf '%s' "$LAUNCH_SSH_CONFIG" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g')
  cat >"$LAUNCH_PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${LAUNCH_LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/ssh</string>
    <string>-F</string>
    <string>${escaped_config}</string>
    <string>-o</string>
    <string>BatchMode=yes</string>
    <string>-o</string>
    <string>ExitOnForwardFailure=yes</string>
    <string>-o</string>
    <string>ServerAliveInterval=30</string>
    <string>-o</string>
    <string>ServerAliveCountMax=3</string>
    <string>-N</string>
    <string>-L</string>
    <string>127.0.0.1:${PORT}:127.0.0.1:80</string>
    <string>cnc-linode-server</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>${LAUNCH_LOG}</string>
  <key>StandardErrorPath</key>
  <string>${LAUNCH_ERROR_LOG}</string>
</dict>
</plist>
EOF

  plutil -lint "$LAUNCH_PLIST" >/dev/null
  launchctl bootstrap "$LAUNCH_DOMAIN" "$LAUNCH_PLIST"
  launchctl enable "${LAUNCH_DOMAIN}/${LAUNCH_LABEL}"
  launchctl kickstart -k "${LAUNCH_DOMAIN}/${LAUNCH_LABEL}"
  wait_until_healthy
  printf 'Installed an automatic Google Drive tunnel on localhost:%s.\n' "$PORT"
fi

if [ "$ACTION" = start ] && [ -f "$LAUNCH_PLIST" ]; then
  if ! launch_agent_loaded; then
    launchctl bootstrap "$LAUNCH_DOMAIN" "$LAUNCH_PLIST"
  fi
  if ! tunnel_healthy; then
    launchctl kickstart -k "${LAUNCH_DOMAIN}/${LAUNCH_LABEL}"
    wait_until_healthy
  fi
elif [ "$ACTION" = start ] && ! tunnel_healthy; then
  # Clean up a stale control socket left by an interrupted SSH process.
  stop_control_master

  ssh -F "$SSH_CONFIG" \
    -o BatchMode=yes \
    -o ExitOnForwardFailure=yes \
    -o ServerAliveInterval=30 \
    -o ServerAliveCountMax=3 \
    -o ControlMaster=yes \
    -o ControlPath="$CONTROL_SOCKET" \
    -fN -L "127.0.0.1:${PORT}:127.0.0.1:80" cnc-linode-server

  wait_until_healthy
fi

if [ "$ACTION" = install ]; then
  printf 'The tunnel will reconnect automatically after SSH or network interruptions.\n'
else
  printf 'Google Drive tunnel is ready in the background.\n'
fi
printf 'Google Drive connection: %s\n' "$START_URL"
printf 'OAuth callback is already configured for localhost:%s.\n' "$PORT"
printf 'Stop it later with: %s --stop %s\n' "$0" "$PORT"

open_connection() {
  if command -v open >/dev/null 2>&1; then
    open "$START_URL"
  elif command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$START_URL"
  else
    printf 'Open this URL: %s\n' "$START_URL"
  fi
}

if [ "${CNC_NO_OPEN:-0}" != 1 ]; then
  open_connection
fi
