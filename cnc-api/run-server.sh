#!/bin/bash

# CNC Server Auto-Restart Wrapper
# ALWAYS use this script instead of running ./cnc-server directly
# Exit code 42 = restart requested (from UI button)
# Ctrl+C = stop completely

cd "$(dirname "$0")"

echo "========================================"
echo " CNC Server  (auto-restart enabled)"
echo " Press Ctrl+C to stop completely"
echo "========================================"
echo ""

while true; do
    ./cnc-server "$@"
    EXIT_CODE=$?

    if [ $EXIT_CODE -eq 42 ]; then
        echo ""
        echo ">>> Restarting server..."
        echo ""
        sleep 1
    elif [ $EXIT_CODE -eq 0 ] || [ $EXIT_CODE -eq 130 ]; then
        # 0 = clean shutdown, 130 = Ctrl+C
        echo ""
        echo "Server stopped."
        exit 0
    else
        echo ""
        echo "Server crashed (exit $EXIT_CODE) — restarting in 2s..."
        echo ""
        sleep 2
    fi
done
