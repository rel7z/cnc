#!/usr/bin/env python3
"""Export unique WordPress homepage candidates from a single HTTP Archive crawl."""

import argparse
import csv
from datetime import date
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

QUERY = r"""
WITH homepages AS (
  SELECT
    REGEXP_REPLACE(LOWER(NET.HOST(p.root_page)), r'^www\.', '') AS host,
    p.root_page AS url
  FROM `httparchive.crawl.pages` AS p
  WHERE p.date = @crawl_date
    AND p.client = 'mobile'
    AND p.is_root_page
    AND EXISTS (
      SELECT 1 FROM UNNEST(p.technologies) AS t
      WHERE t.technology = 'WordPress'
    )
),
deduplicated AS (
  SELECT
    host,
    ARRAY_AGG(url ORDER BY STARTS_WITH(url, 'https://') DESC, url LIMIT 1)[OFFSET(0)] AS url
  FROM homepages
  WHERE host IS NOT NULL
    AND REGEXP_CONTAINS(host, r'^[a-z0-9-]+(\.[a-z0-9-]+)+$')
    AND NET.SAFE_IP_FROM_STRING(host) IS NULL
  GROUP BY host
)
SELECT url
FROM deduplicated
ORDER BY FARM_FINGERPRINT(host), host
LIMIT @row_limit
"""


def export(command, destination, limit):
    if destination.exists():
        raise FileExistsError(f"output already exists: {destination}")
    with tempfile.NamedTemporaryFile(
        mode="w", encoding="utf-8", newline="", dir=destination.parent, delete=False
    ) as temporary:
        path = Path(temporary.name)
        try:
            subprocess.run(command, stdout=temporary, check=True)
            temporary.flush()
            os.fsync(temporary.fileno())
            with path.open(encoding="utf-8", newline="") as contents:
                rows = csv.reader(contents)
                if next(rows, None) != ["url"]:
                    raise ValueError("expected a CSV with one url column")
                count = 0
                for row in rows:
                    if len(row) != 1 or not row[0].startswith(("https://", "http://")):
                        raise ValueError("unexpected row in query result")
                    count += 1
                if not 0 < count <= limit:
                    raise ValueError("query returned no rows or exceeded the requested count")
            os.link(path, destination)
            return count
        finally:
            path.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", required=True, help="Existing BigQuery Sandbox or Google Cloud project")
    parser.add_argument("--crawl-date", type=date.fromisoformat, required=True)
    parser.add_argument("--limit", type=int, default=1000)
    parser.add_argument("--max-gib", type=int, default=100, help="Per-query bytes cap in GiB")
    parser.add_argument("--output", type=Path, help="CSV destination. With no output, perform a dry run.")
    args = parser.parse_args()
    if args.limit < 1 or not 1 <= args.max_gib <= 1024:
        parser.error("limit must be positive and max-gib must be between 1 and 1024")
    bq = shutil.which("bq")
    if not bq:
        parser.error("the Google Cloud bq CLI must be installed and authenticated")
    command = [
        bq, f"--project_id={args.project}", "--location=US", "--format=csv", "query",
        "--use_legacy_sql=false", f"--maximum_bytes_billed={args.max_gib * (1 << 30)}",
        f"--parameter=crawl_date:DATE:{args.crawl_date}", f"--parameter=row_limit:INT64:{args.limit}",
    ]
    try:
        if args.output is None:
            subprocess.run([*command, "--dry_run", QUERY], check=True)
        else:
            count = export([*command, f"--max_rows={args.limit}", QUERY], args.output, args.limit)
            print(f"Exported {count:,} candidates to {args.output}")
    except (OSError, ValueError, csv.Error, subprocess.CalledProcessError) as error:
        parser.exit(1, f"wordpress_archive_export: {error}\n")


if __name__ == "__main__":
    main()
