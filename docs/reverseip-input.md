# Reverse IP input preparation

All three CNC reverse-IP tools (ReverseIPDomain, THC, DNSRift) use the same controller preprocessing path before input is divided among workers.

1. Read nonempty, noncomment input lines. Accept domain names, IPv4 addresses and HTTP(S) URLs without credentials. Normalize domain case, trailing dots and IDNs.
2. Deduplicate normalized input targets, so repeated domains trigger one DNS lookup per job.
3. Resolve all IPv4 answers for each domain using the controller resolver. At most 16 lookups run concurrently, each with a five-second timeout. IPv6 is skipped.
4. Canonicalize and deduplicate IPv4 addresses across the whole job, including addresses shared by different domains and literal IP lines. Keep first-target order and sort each domain's answers.
5. Save a private normalized `ips.txt` under `data/reverseip-jobs/<job-id>/`, leaving the original source unchanged. Split only the normalized file. Job-specific chunk and worker-input filenames prevent simultaneous jobs from replacing each other's input.
6. Execute the selected reverse-IP provider on those unique IPv4 chunks. Existing output merge and Drive publication stay in place.

Invalid input and DNS failures are skipped, counted, and included in a bounded list of warnings. If nothing resolves to IPv4, fail the job with a reason and dispatch no provider tasks. Cancelling during DNS preparation stops it and dispatches nothing. The job API and detail page show the preprocessing summary and failures.

IPv4 conversion is based on current DNS answers, including CNAME resolution by the resolver. It cannot discover an origin hidden behind a CDN or addresses absent from that DNS response. Deduplication is per job, across all its workers; different jobs are independent.

Local verification uses injected DNS and synthetic addresses, not live reverse lookups:

```sh
GOCACHE=/private/tmp/cnc-do-go-cache GOTOOLCHAIN=local GOPROXY=off go -C cnc-api test -race . -run '^TestReverseIP' -count=1
```
