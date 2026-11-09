# WordPress Generator

## Continuous discovery

The controller service tails usable public CT logs and stores its cursors in a separate SQLite database so snapshot imports do not hold the feed's write lock. CT starts at the current log head and queues DNS names from subsequent certificates. A certificate signals discovery. Homepage verification determines whether it is WordPress.

On Linux, install `python3-cryptography` and place `wordpress_generator.py`, `wordpress_ct.py`, and `wordpress_bulk.py` in `/opt/cnc/wordpress-generator` with the existing candidate catalog. Set `GO_API_URL` to the controller's existing internal API in `generator.env` and use the existing dashboard audit environment file through the unit. The continuous service and the separate source refresh timer are in `infra/digitalocean`. Provide complete reverse-IP exports in the `reverse-ip` inbox using an atomic rename. The refresh timer checks MapTheNet and advances through the latest Common Crawl host graph one shard per run.

Run a single canary with `--once --batch-limit 1000 --fixed-concurrency` after installing the matching controller and worker scanner. Review the job, URL-only Merged output and dashboard status before enabling the continuous service. The default cap is 64 requests per worker. Increase it only after the same-input comparison passes the timeout and rate-limit gates.

Start with HTTP Archive’s existing WordPress detections and use CNC to recheck public homepages. Keep the snapshot observation date separate from live verification and from the site's launch date.

## Import a snapshot

In BigQuery Sandbox, run the query below with a dry run first and set a maximum bytes billed limit within the available free quota. The date shown is a known published snapshot. For a current import, replace it with the latest available complete crawl date. Use `LIMIT 1000` for the initial verification sample and remove the limit when exporting the full corpus. The hash order gives a repeatable sample without favoring alphabetical hostnames.

```sql
WITH homepages AS (
  SELECT
    REGEXP_REPLACE(LOWER(NET.HOST(p.root_page)), r'^www\.', '') AS host,
    p.root_page AS url
  FROM `httparchive.crawl.pages` AS p
  WHERE p.date = DATE '2025-07-01'
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
LIMIT 1000;
```

Download the pilot as CSV. For a full snapshot, use paginated query results if the local console download reaches its size limit. Fetch the pages from the same query job to avoid rescanning the source. Import the URL column and retain the source date with the dataset. Collapse only the conventional `www.` alias and retain other subdomains, which can be distinct sites.

[HTTP Archive schema and WordPress query](https://httparchive.org/docs/reference/structs/technology) · [Published crawl date](https://almanac.httparchive.org/en/2025/methodology) · [Sandbox](https://docs.cloud.google.com/bigquery/docs/sandbox) · [Result pagination](https://docs.cloud.google.com/bigquery/docs/paging-results)

If you have the authenticated `bq` CLI, the same query is available as a script. Run without `--output` to see the estimate. Add `--output archive.csv` to execute with the same byte cap. Choose `--max-gib` based on the estimate and your remaining monthly quota.

```bash
python3 infra/wordpress_archive_export.py --project YOUR_SANDBOX_PROJECT --crawl-date 2025-07-01 --limit 1000 --max-gib 100
```

## Merge candidates locally

The importer writes one row per hostname to SQLite and preserves the source of each hostname across reimports. It accepts the Archive's `url` CSV column and CNC reverse-IP TXT files, including THC's optional IP-to-domains format. Use a local database outside the repository for production data.

```bash
python3 infra/wordpress_bulk.py /tmp/wordpress-candidates.db import archive archive.csv --format csv
python3 infra/wordpress_bulk.py /tmp/wordpress-candidates.db import reverse-ip reverse-ip.txt --format txt
python3 infra/wordpress_bulk.py /tmp/wordpress-candidates.db export > candidates.txt
python3 infra/wordpress_bulk.py /tmp/wordpress-candidates.db export --source reverse-ip --exclude-source archive > reverse-ip-increment.txt
```

These exports contain candidates. Their WordPress verification state comes from a separate homepage check.

If a BigQuery project is unavailable, [Tranco](https://tranco-list.eu/) offers a public one-million-domain ranking. Import its headerless CSV with `import ranked tranco.csv --format tranco`. A ranking entry is a candidate and has no CMS or launch-date label. To isolate additions from reverse IP when using both base datasets, also pass `--exclude-source ranked`.

For a larger pool without a cloud account, import the [MapTheNet domain snapshot](https://www.mapthenet.org/data) directly from its compressed CSV. Use only rows whose crawl status was `ok`. MapTheNet does not classify WordPress. Its data is CC BY-SA 4.0. Retain its source and snapshot date with any derived exports and keep its candidate data separate from differently licensed sources.

```bash
python3 infra/wordpress_bulk.py /tmp/mapthenet.db import webgraph domains.csv.gz --format csv --column domain --status ok
python3 infra/wordpress_bulk.py /tmp/mapthenet.db export --source webgraph > webgraph-candidates.txt
```

For recurring updates, run this command from a daily timer on the host that owns the candidate database. It checks the published snapshot date and file size and only fetches the CSV for an unseen snapshot. The database records the SHA-256 of the imported bytes and commits the receipt with the candidate changes. The publisher manifest provides a byte count. To bootstrap with an existing gzip of the currently published snapshot, add `--local-snapshot /path/to/domains.csv.gz`.

```bash
python3 infra/wordpress_bulk.py /tmp/mapthenet.db sync-mapthenet
```

The sync adds candidates while keeping existing source flags. Batch submission and removal handling are separate.

To prepare the full snapshot for CNC, export numbered URL-only batches and a manifest with the row count and SHA-256 of each file. Pass each prior job's input with `--exclude-file` to leave out every attempted hostname, including timeouts. The output directory must be new.

```bash
python3 infra/wordpress_bulk.py /tmp/mapthenet.db batches webgraph /tmp/mapthenet-batches --exclude-file pilot-input.txt
```

To include completed CNC reverse-IP results in the same batch queue, sync their TXT files into the candidate database and add their source at export. The sync accepts plain hostname lists and THC IP maps. It skips files whose SHA-256 has already been imported, even if they were renamed. A hostname found by both sources appears once. Keep the snapshot attribution with the MapTheNet-derived export.

```bash
python3 infra/wordpress_bulk.py /tmp/mapthenet.db sync-reverse-ip reverse-ip.txt
python3 infra/wordpress_bulk.py /tmp/mapthenet.db batches webgraph /tmp/generator-batches --include-source reverse-ip --exclude-file pilot-input.txt
```

A timer can check MapTheNet and import completed CNC exports in the same run. Repeat `--reverse-ip` for each completed export. If MapTheNet is unavailable, any completed reverse-IP imports are retained for the next run.

```bash
python3 infra/wordpress_bulk.py /tmp/mapthenet.db refresh --reverse-ip reverse-ip.txt
```

Newly imported hosts record the UTC date they first entered the database. A later sighting adds its source flag and preserves the original date. Legacy rows have an unknown first-seen date. To prepare a recent-only batch, add `--discovered-since YYYY-MM-DD` to `batches`. This selects hosts first seen by the Generator. Site launch date is a separate fact.

Common Crawl's host graph offers an additional public source over HTTPS. Download the host vertices path list and select one or more gzip shards from it. The importer converts reverse DNS notation back to normal hostnames and tracks Common Crawl separately. The graph includes hosts seen in links, so candidates still need a live homepage check.

```bash
python3 infra/wordpress_bulk.py /tmp/mapthenet.db sync-commoncrawl host-vertices.txt.gz
python3 infra/wordpress_bulk.py /tmp/mapthenet.db batches commoncrawl /tmp/commoncrawl-next --include-source reverse-ip --max-batches 1
```

[Common Crawl October 2026 host graph and download links](https://commoncrawl.org/blog/host--and-domain-level-web-graphs-august-september-and-october-2026)

The sync records each shard's SHA-256 after successful import. To include downloaded shards in a refresh, pass `--commoncrawl host-vertices.txt.gz` once per file.

To prioritize the sources with the higher pilot yield and retain the extra Common Crawl coverage, export two disjoint queues. Both honor the same attempted-host history.

```bash
python3 infra/wordpress_bulk.py /tmp/mapthenet.db batches webgraph /tmp/wp-priority --include-source reverse-ip
python3 infra/wordpress_bulk.py /tmp/mapthenet.db batches commoncrawl /tmp/wp-expansion --exclude-source webgraph --exclude-source reverse-ip
```

## Run the bulk job

1. Upload the deduplicated candidate TXT to the controller using the existing File Manager. In **Tools → WordPress Generator → Bulk job on CNC workers**, enter the controller path and launch. File Manager accepts at most 90 MiB per file. For a larger corpus, export numbered batch files, then submit each file as a bulk job. Add `--max-batches 1` to prepare only the next batch when submitting jobs one at a time. Merge the verified exports by hostname afterward.
2. Workers need the matching scanner release with `CNC_ENUM_CHECKPOINT=1` and `CNC_WORDPRESS_BULK=1`. CNC caps each bulk shard at 100,000 targets, balances shard counts across compatible workers, and assigns bulk shards when those workers are idle. Bulk jobs default to a 64-request concurrency cap per worker and a five-second request timeout. The regular Finder still accepts small lists directly in the dashboard.
3. Read the job-specific `wordpress-<job-id>.txt` from Merged. The scan only checks public homepages. A checkpoint replay may deliver text again. Import one or more result files with `import verified wordpress-<job-id>.txt --format txt`, then use `export --source verified > verified-urls.txt` to get only verified hosts, deduplicated across retries and jobs. Once a batch has attempted every target, run `record-attempted batch-input.txt` against the candidate database. Future batch exports skip those hostnames even if another source rediscovers them. Keep interrupted or unstarted inputs eligible for retry.
4. Measure verified WordPress URLs, inconclusive results and elapsed time on the 1,000-row pilot before scaling. The job summary reports `verified`, `no_marker`, DNS or transport errors, and individual HTTP statuses. Use the breakdown to decide whether an inconclusive cohort merits a later retry. An HTTP 403 or 429 does not trigger a retry against the www alias.

For a terminal job that covered its entire batch, import the Merged TXT and record the input history in one transaction. Use the controller's job state locally to read the processed and verified counts from the shard summaries. Keep the state file private because it contains job tokens. Pass the controller's saved input snapshot to account for controller rejections automatically. With the snapshot, `--skipped` counts only scanner input rejections. Without it, include both kinds of rejection in `--skipped`. Hostname matching accepts a different HTTP scheme or the conventional `www.` alias. A foreign hostname, incomplete shard, or truncated export rejects the import.

```bash
python3 infra/wordpress_bulk.py /tmp/mapthenet.db record-completed batch-input.txt wordpress-job.txt --job-state state.json --controller-input controller-input.txt
```

For a manual import with the same validation, supply `--processed` and `--verified` from the scanner summary in place of `--job-state`.

For expansion, use the existing reverse-IP jobs. Compare **new, verified WordPress hosts per 100 successful IP lookups** against the imported corpus. Deduplicate IPs, omit known CDN edges, and pass new domains through the same homepage check. Record provider errors separately from successful lookups that return zero results.

For a recent-only view, compare successive complete snapshots. Label a newly appearing host as first observed in the crawl. A public launch announcement can supply a confirmed launch date separately.

## Pilot on 8 November 2026

- [Tranco snapshot 94P72](https://tranco-list.eu/list/94P72) imported 999,994 valid, unique candidate hostnames from 1 million rows. A repeatable random sample of 1,000 returned 81 WordPress homepages, with 550 inconclusive checks. The local run took 97 seconds with up to 24 concurrent checks, and checkpoint replay made zero requests.
- [DNSRift](https://dnsrift.net/docs) completed 12 lookups, capped at 20 results each. The IPs came from sites confirmed in the Tranco sample and excluded published Cloudflare ranges and known CDN aliases. The provider returned 157 unique domains. Of these, 122 were outside the Tranco corpus, and 52 verified as WordPress. The cohort is small and intentionally seeded from WordPress, so its yield should not be generalized to other IPs.
- Use the `limit` option in CNC's DNSRift tool with the matching worker binary. A blank or zero value uses the provider default.

A second DNSRift round selected 33 previously unqueried IPv4 addresses from the verified corpus using the same edge exclusions and result cap. All lookups succeeded. The round returned 375 unique domains, of which 300 were new to the database and 81 verified as WordPress. There were 181 inconclusive homepage checks. The measured increment fell from 4.33 to 2.45 verified new sites per successful IP lookup as the corpus expanded. The combined verified export contains 214 hosts.

### Account-free MapTheNet pilot

[MapTheNet](https://www.mapthenet.org/data), snapshot 2026-10-30, CC BY-SA 4.0. The downloaded domain CSV contained 14,444,633 rows. Of these, 9,254,183 had crawl status `ok`. Normalization and deduplication produced 9,249,077 candidate hostnames in SQLite. A reproducible random sample of 1,000 of those rows produced 193 verified WordPress homepages, 525 accessible pages without a marker, and 282 inconclusive outcomes. The run took 80.38 seconds locally with 24 concurrent checks. At the observed 19.3% yield, one million verified URLs would require about 5.18 million comparable candidates. That figure is a sample-based estimate and needs a larger production-worker pilot.

A follow-up pilot sampled 4,000 fresh candidates from the next MapTheNet batch and ran the homepage checker directly on two idle DigitalOcean worker hosts. Each host received 2,000 distinct candidates and used a 64-request cap. The two runs took 45.309 and 45.651 seconds of scanner time, producing 797 verified WordPress hosts, 1,909 successful checks without a marker, and 1,294 inconclusive checks. This measures the isolated scanner on the worker hardware. It does not measure the dashboard job transport. Removing all 4,000 attempted hosts from the 250,000-candidate batch leaves 246,000 for a first bulk job.

A later pilot sampled 20,000 distinct, previously untried candidates across the 37 combined MapTheNet and reverse-IP batches. The same two workers used a 64-request cap each and finished in about 225 seconds wall time. Of 19,999 homepages checked, 4,532 verified as WordPress, 10,278 showed no marker, and 5,189 were inconclusive. One malformed hostname was skipped before requesting it. The combined TXT reached 7,289 verified hostnames. The scanner ran in temporary directories on the workers, separate from CNC job dispatch.

The first complete Common Crawl vertex shard added 4,994,488 hostnames outside MapTheNet and reverse IP. A staging CNC job sampled 4,000 from the new source and verified 227 as WordPress. It recorded 1,741 checks without a marker and 2,032 inconclusive checks in 111.238 seconds of scanner time. This measures one Common Crawl shard. Its 5.7% verification yield was lower than the earlier combined MapTheNet and reverse-IP pilot, so keep source-level counts when choosing scan order.

Two independent staging CNC jobs ran 50,000 priority candidates each with 64 concurrent requests per worker. The controllers rejected one hostname each before scanning. The scanners verified 24,116 of 99,998 checked hosts, with 56,821 checks without a marker and 19,061 inconclusive checks. The longer shard took 17m40s of scanner time. Both jobs published URL-only output to Merged.
