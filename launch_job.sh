curl -X POST http://157.230.57.107:8080/api/tools/enum/launch \
  -H "Content-Type: application/json" \
  -d '{
    "mode": "spread",
    "command": "./tools/cnc-enum -l {input} -t 50 -timeout 10s >&2",
    "input_file": "/root/merged/xzzxxz.txt",
    "workers": 6
  }'
