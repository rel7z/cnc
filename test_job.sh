#!/bin/bash

# Test job submission script

SERVER="http://localhost:8080"

echo "========================================="
echo "Testing CNC Job Submission"
echo "========================================="
echo

# Create test output directory
mkdir -p test_output

# Submit a domain resolve job
echo "Submitting domain resolution job..."
JOB_RESPONSE=$(curl -s -X POST "$SERVER/api/jobs" \
  -H "Content-Type: application/json" \
  -d '{
    "job": {
      "name": "test_domain_resolve",
      "type": "domain_resolve",
      "input_file": "test_domains.txt",
      "output_dir": "test_output",
      "split_size": 1024,
      "workers": []
    }
  }')

echo "Response: $JOB_RESPONSE"
JOB_ID=$(echo $JOB_RESPONSE | python3 -c "import sys, json; print(json.load(sys.stdin).get('job_id', ''))" 2>/dev/null)

if [ -z "$JOB_ID" ]; then
  echo "ERROR: Failed to get job ID"
  exit 1
fi

echo "Job ID: $JOB_ID"
echo

# Monitor job status
echo "Monitoring job status..."
for i in {1..30}; do
  sleep 2
  STATUS=$(curl -s "$SERVER/api/stats" | python3 -m json.tool 2>/dev/null)
  echo "[$i] Stats: $STATUS"
  
  # Check if job completed
  COMPLETED=$(echo "$STATUS" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d['tasks_completed'])" 2>/dev/null)
  TOTAL=$(echo "$STATUS" | python3 -c "import sys, json; d=json.load(sys.stdin); print(d['tasks_total'])" 2>/dev/null)
  
  if [ "$COMPLETED" == "$TOTAL" ] && [ "$TOTAL" != "0" ]; then
    echo
    echo "Job completed!"
    break
  fi
done

echo
echo "Final job status:"
curl -s "$SERVER/api/jobs" | python3 -m json.tool

echo
echo "Output files:"
ls -lh test_output/
