# Shared CMS detection and deep enumeration

CMS Scanner and Deep Enumerator use `detection` for public CMS fingerprints.
A definitive generator/header signal or two independent structural signals are
required. Clear homepage matches use one request; unclear pages use at most two
additional probes. Plain brand mentions, blocked pages, HTTP errors, incomplete
responses, and catch-all probe pages cannot confirm a CMS. Bare hosts try HTTPS
then HTTP after transport failure; explicit schemes stay explicit.

`-auto-concurrency=true` is the default. `-t` (CMS also supports `-c`) is the
maximum, from 1–256, default 256. Automatic admission starts at 64 requests
(or the configured maximum when lower), doubles after a healthy five-second
observation window, and halves on sustained explicit 429/503 responses, bounded
by 16 and the configured maximum. DNS failures, timeouts, and incomplete responses
remain errors but do not throttle unrelated origins. Requests
are scheduled fairly across targets, at most four per origin. The worker-wide
request budget remains authoritative and may be lower than this job's limit.
Its default is 64 across all jobs on one worker; `ENUM_MAX_INFLIGHT_REQUESTS`
accepts 1–256. Deep enumeration admits up to 64 target workflows concurrently.
429/503 responses apply origin-specific Retry-After cooldown and at most one
retry. All waits and reads are cancellable. `-timeout` defaults to 10s.

Connections are reused. Scope enforcement reauthorizes every request and keys
pooled connections by the exact host, port, scheme and authorized IP. The worker
gateway forwards public CMS signal headers and cookies without forwarding client
credentials. A per-target cache retains at most eight completed public responses;
responses with cookies and authenticated requests are not cached.

Deep enumeration confirms a CMS before ordinary WordPress/Joomla discovery.
Explicit version rules retain their component selection and conflict semantics.
Core evidence is collected first to preserve accepted results if later probes
cancel. Up to four component probes proceed concurrently; admin enumeration
uses a missing-page baseline, structural comparison and one finding per target.

CMS filenames stay the same. `cms-unknown.txt` contains successfully fetched
unidentified sites, `cms-blocked.txt` blocked sites, and `cms-errors.txt` failures.
CMS Scanner keeps `unknown.txt` as a compatibility alias. File records stream
while the scanner runs, even if the launch form includes a watcher filename.
Exit codes: 0 complete, 2 partial failure, 1 fatal or entirely failed.

The local 24-target fixture benchmark with four concurrent requests reduced
requests from 48 to 24 and median runtime from 244 ms to 140 ms (1.74x). This is a
controlled clear-CMS workload, not a claim about public-site latency or deep
wordlist scans. Unclear pages intentionally require additional evidence.

The 2026-10-15 localhost comparison used the same responses, input, request
coverage, and output records before and after the request-budget correction.
512 CMS targets took 6.229s before and 1.377s after (4.52x). A 64-target
WordPress core/plugin workload made 512 requests in 5.492s before and 0.737s
after (7.45x). These are controlled 80ms-response fixtures, not public-network
speed guarantees. CPU percentage is not a throughput target: network-bound
work can use available concurrency while CPU remains below 10%.

Dashboard job progress counts completed shards, not individual targets. Older
jobs used fixed 1,000-target / 1 MiB shards. New jobs create one balanced shard
per requested worker, so three workers receive three parts. Line counts differ
by at most one. The existing worker transfer limit is 256 MiB per part; an
oversized part is reported as an error instead of silently creating extra parts.
Existing job snapshots retain their original chunks until explicitly restarted.

CMS and component `.txt` exports contain clean URLs only, one per line. Named
plugin files contain the site URL; `plugins_name.txt` contains plugin directory
URLs. No component, version, confidence, or plugin-name fields are appended.
Structured evidence remains in the private job publication directory and worker
outbox. JSONL is omitted from text output and the Drive watch directory; the
uploader skips legacy enum JSONL exports. Only scheme-default
ports are removed (HTTPS 443 and HTTP 80); nondefault ports are preserved.
Drive text merging also normalizes the known old enum metadata layouts before
deduplication, so older uploaded lines do not reintroduce appended fields.

`admin.txt` contains ordinary login forms. `cpanel.txt` contains cPanel login
pages with explicit branding plus form evidence; cPanel findings are excluded
from ordinary admin output. Inputs must be actual form elements with a password
field and login/identity evidence. Script snippets, articles, password-reset
forms, blocked responses, and missing-page lookalikes do not confirm a login.
