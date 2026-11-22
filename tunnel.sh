#!/bin/bash
# Open SSH tunnel to DigitalOcean controller for Internet Map UI & API
SSH_CONFIG="$HOME/Documents/GitHub/cnc/.digitalocean/new-20261122/ssh_config"

echo "[*] Connecting to DigitalOcean controller (157.245.153.108)..."
echo "[*] Mapping:"
echo "    - Internet Map UI  -> http://127.0.0.1:3501"
echo "    - Internet Map API -> http://127.0.0.1:3500"
echo "    - CNC Dashboard    -> http://127.0.0.1:8119"

while true; do
    ssh -F "$SSH_CONFIG" \
        -o BatchMode=yes \
        -o ExitOnForwardFailure=yes \
        -o ServerAliveInterval=15 \
        -o ServerAliveCountMax=6 \
        -N \
        -L 127.0.0.1:3501:127.0.0.1:3501 \
        -L 127.0.0.1:3500:127.0.0.1:3500 \
        cnc-do-controller
    echo "[!] SSH tunnel disconnected. Reconnecting in 3 seconds..."
    sleep 3
done
