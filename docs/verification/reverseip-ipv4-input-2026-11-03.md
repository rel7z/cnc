# Reverse IP IPv4 preparation rollout

Deployed to the existing DigitalOcean controller on 2026-11-03.

- Controller SHA-256: `88b3aa50a976f204bbaa7e905ea27625a74da87b4397e8a392c951f614830b31`.
- Release and source archive: `/opt/cnc/releases/reverseip-ipv4-20261103/` on the controller. Rollback retained under its `rollback/` directory.
- ReverseIPDomain, THC and DNSRift now prepare their whole input on the controller before worker splitting: normalized domains → all IPv4 answers → global IP dedup → unique per-job chunks. IPv6 is skipped. The original source file is preserved.
- Synthetic tests verified multiple addresses, shared addresses, repeated domains, IDN/URL normalization, direct and mapped IPv4, IPv6 exclusion, invalid input, DNS errors, bounded concurrency, cancellation, zero dispatch after failure, all three provider chunk downloads, and mode-bypass rejection.
- Targeted tests passed under the macOS Go race detector and again using the compiled Linux test binary on the controller. No real reverse provider calls or private programs ran for verification.
- TypeScript validation and production UI build passed. The deployed Tools page was inspected in the browser and displays the new Domain / IPv4 auto-resolve and dedup input label. Job details include preparation counts, warnings and failure reasons.
- Rollout checked that no jobs or worker tasks were active before restarting controller/UI. Two workers reconnected online and idle. Worker binaries were unchanged.
- Controller configuration remained byte-identical; Google Drive remained authorized after rollout.
- Latest production API verified all three reverse-IP tools use spread mode and advertise automatic domain/IPv4 preparation.

See `../reverseip-input.md` for algorithm and limitations. Source changes are in the local working tree and the release's source archive; no GitHub push is claimed.
