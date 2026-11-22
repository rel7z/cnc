#!/usr/bin/env python3
"""Classify supplied HTML records and write JSONL observations."""
import argparse
import json
import os
from pathlib import Path
import tempfile

from main import normalize, wordpress_evidence


def classify(source, destination):
    if source.resolve() == destination.resolve():
        raise ValueError("Input and output paths must differ")
    destination.parent.mkdir(parents=True, exist_ok=True)
    count = 0
    temp = None
    try:
        with source.open(encoding="utf-8-sig") as stream, tempfile.NamedTemporaryFile(
            mode="w", encoding="utf-8", dir=destination.parent,
            prefix=".internet-map-", delete=False,
        ) as output:
            temp = Path(output.name)
            for number, line in enumerate(stream, 1):
                if not line.strip():
                    continue
                record = json.loads(line)
                if not isinstance(record, dict) or not isinstance(record.get("url"), str):
                    raise ValueError(f"Record {number} needs a URL")
                html = record.get("html")
                if not isinstance(html, str):
                    raise ValueError(f"Record {number} needs supplied HTML")
                host, url = normalize(record["url"])
                evidence = wordpress_evidence(html)
                output.write(json.dumps({
                    "url": url, "host": host,
                    "wordpress_evidence": evidence,
                    "wordpress_detected": bool(evidence),
                }) + "\n")
                count += 1
            output.flush()
            os.fsync(output.fileno())
        os.replace(temp, destination)
    finally:
        if temp is not None:
            temp.unlink(missing_ok=True)
    return count


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    print(f"Processed {classify(args.input, args.output)} supplied HTML records.")


if __name__ == "__main__":
    main()
