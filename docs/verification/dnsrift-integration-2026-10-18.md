# DNSRift reverse IP integration

Deployed on 18 October 2026. The launcher groups three providers under Reverse
IP: ReverseIPDomain, THC.org, and DNSRift. All three default to worker spread mode.
Existing scheduler capacity decisions determine assignment across online nodes.

DNSRift uses `https://api.dnsrift.net/reverse-ip-lookup?ip=...`, reads
`domain_list`, normalizes/deduplicates domains, and writes plain text immediately.
Defaults: two requests in flight per worker, a shared 500 ms request interval,
20-second HTTP timeout and two bounded retries. Retry-After is honored. API/HTTP
errors, invalid JSON, invalid input and exhausted retries return nonzero status;
successful partial output is retained. Responses are capped at 32 MiB.

## Verification

- Provider race tests: actual JSON schema, output file/stdout, deduplication,
  empty results, 429 retry, malformed responses, authentication/HTTP failures,
  wrong-IP responses, timeouts and input validation.
- Controller race tests: registry metadata, command generation and asynchronous
  splitting into distinct chunks. Production UI build passed.
- Live lookup of the supplied `8.8.8.8` example returned 20,751 unique domains
  in 0.82 seconds on the controller (one measurement, not a latency guarantee).
- DNSRift binary installed on all 11 workers with SHA256
  `352f9e1f7a902c78cec94584d908c5525b2707b1847ee810ab43c5b99a48c631`.
  Help checks passed; worker services were not restarted. Existing tool files
  were preserved; rollback copies of replaced files/manifests were retained.
- Live launcher job `job_1792320863_1`: two input IPs assigned to two distinct
  workers, both exit 0, 67,843 raw output lines and 67,820 unique domains.
- Downloaded `dnsrift.txt` from Drive matched canonical local output, Google's
  MD5, and the uploader's SHA256 description:
  `6d4be20e86bc5c7f734c797d741f52b4f56ddc358c096fc1284f751289e40f33`.

Production release: `/opt/cnc/dnsrift-releases/a75241ee6d5e9bc6`.
The actual UI service directory and three-provider runtime catalog were checked
after activation. Future worker packages include DNSRift as a required executable.

The owner authorized stopping the previous THC job for activation and explicitly
did not request a rerun. Its 14,945,759-byte partial `huahua.txt` result remained
in merged storage, with an additional private backup and private job-history
snapshot under `/opt/cnc/data/dnsrift-verification/`. No restricted processor
tool directory was accessed. No browser job page was opened.

Output: https://drive.google.com/file/d/1MF55oSNzztvbXS23agz1_xkJX_Z3nryO/view
