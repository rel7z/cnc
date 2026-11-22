#!/bin/bash
set -e
DATASET_DIR="/opt/internet-map/datasets"
DB_PATH="/opt/internet-map/data/catalog.db"
MAP_DIR="/opt/internet-map"

echo "[*] Waiting for czdsdump to finish downloading org.txt.gz..."
while pgrep -f "czdsdump download" > /dev/null; do
    SIZE=$(ls -lh "$DATASET_DIR/org.txt.gz" 2>/dev/null | awk '{print $5}' || echo "0")
    echo "[*] Still downloading... Current size: $SIZE"
    sleep 10
done

if [ ! -f "$DATASET_DIR/org.txt.gz" ]; then
    echo "[-] Error: $DATASET_DIR/org.txt.gz not found!"
    exit 1
fi

FILESIZE=$(ls -lh "$DATASET_DIR/org.txt.gz" | awk '{print $5}')
echo "[+] Download finished! File size: $FILESIZE"

echo "[*] Starting import of org.txt.gz into $DB_PATH..."
cd "$MAP_DIR"
python3 org_zone.py "$DATASET_DIR/org.txt.gz" --source "czds-org-$(date +%Y%m%d)" --database "$DB_PATH"

echo "[+] Import complete! Current stats:"
python3 main.py --database "$DB_PATH" stats
