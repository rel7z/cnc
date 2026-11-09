#!/bin/bash
# Aggressive WordPress discovery - maximum resources

THREADS=${1:-3000}
TIMEOUT=${2:-5}
INPUT=${3:-targets.txt}
OUTPUT=${4:-wordpress_discovered.json}

echo "[*] Starting aggressive WordPress discovery"
echo "[*] Threads: $THREADS | Timeout: ${TIMEOUT}s | Input: $INPUT"

../wp-discovery \
  -input "$INPUT" \
  -out "$OUTPUT" \
  -t "$THREADS" \
  -timeout "$TIMEOUT" \
  -ports "80,443,8080,8888,8081" \
  -ct \
  -dns-rev \
  -level max

echo "[+] Results saved to: $OUTPUT"
echo "[+] Total found:"
jq 'length' "$OUTPUT"
