#!/bin/bash

set -e

echo "========================================="
echo "Full CNC System Test"
echo "========================================="
echo

# Clean up
echo "Cleaning up old data..."
rm -rf test_output cnc_data worker_data
pkill -f cnc-server || true
pkill -f cnc-worker || true
sleep 2

# Start server
echo "Starting server..."
./cnc-server -config server_config.json > server.log 2>&1 &
SERVER_PID=$!
sleep 3

if ! ps -p $SERVER_PID > /dev/null; then
  echo "ERROR: Server failed to start"
  cat server.log
  exit 1
fi

echo "Server started (PID: $SERVER_PID)"

# Start worker
echo "Starting worker..."
./cnc-worker -config worker_config.json > worker.log 2>&1 &
WORKER_PID=$!
sleep 3

if ! ps -p $WORKER_PID > /dev/null; then
  echo "ERROR: Worker failed to start"
  cat worker.log
  kill $SERVER_PID 2>/dev/null || true
  exit 1
fi

echo "Worker started (PID: $WORKER_PID)"
echo

# Check registration
echo "Checking worker registration..."
WORKERS=$(curl -s http://localhost:8080/api/workers | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null)
if [ "$WORKERS" != "1" ]; then
  echo "ERROR: Worker not registered (found: $WORKERS)"
  kill $SERVER_PID $WORKER_PID 2>/dev/null || true
  exit 1
fi
echo "✓ Worker registered"
echo

# Submit job
echo "Submitting test job..."
JOB_RESPONSE=$(curl -s -X POST http://localhost:8080/api/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "job": {
      "name": "integration_test",
      "type": "domain_resolve",
      "input_file": "test_domains.txt",
      "output_dir": "test_output",
      "split_size": 300,
      "workers": []
    }
  }')

JOB_ID=$(echo "$JOB_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('job_id', ''))" 2>/dev/null)

if [ -z "$JOB_ID" ]; then
  echo "ERROR: Failed to submit job"
  echo "Response: $JOB_RESPONSE"
  kill $SERVER_PID $WORKER_PID 2>/dev/null || true
  exit 1
fi

echo "✓ Job submitted: $JOB_ID"
echo

# Monitor job
echo "Monitoring job progress..."
for i in {1..30}; do
  sleep 2
  STATUS=$(curl -s http://localhost:8080/api/jobs | python3 -c "import sys,json; d=json.load(sys.stdin)[0]; print(f\"{d['status']}:{d['completed']}/{d['total_tasks']}\")" 2>/dev/null)
  echo "  [$i] Status: $STATUS"
  
  if echo "$STATUS" | grep -q "completed:1/1"; then
    echo
    echo "✓ Job completed successfully!"
    break
  fi
  
  if [ $i -eq 30 ]; then
    echo
    echo "ERROR: Job did not complete in time"
    echo
    echo "Server log (last 20 lines):"
    tail -20 server.log
    echo
    echo "Worker log (last 20 lines):"
    tail -20 worker.log
    kill $SERVER_PID $WORKER_PID 2>/dev/null || true
    exit 1
  fi
done

# Check output
echo
echo "Checking output files..."
if [ ! -f "test_output/resolved_part_0001.txt" ]; then
  echo "ERROR: Output file not found"
  ls -la test_output/
  kill $SERVER_PID $WORKER_PID 2>/dev/null || true
  exit 1
fi

LINES=$(wc -l < test_output/resolved_part_0001.txt)
echo "✓ Output file created with $LINES lines"

# Cleanup
echo
echo "Cleaning up..."
kill $SERVER_PID $WORKER_PID 2>/dev/null || true
sleep 2

echo
echo "========================================="
echo "ALL TESTS PASSED!"
echo "========================================="
