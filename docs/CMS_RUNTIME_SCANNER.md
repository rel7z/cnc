# CMS and PHP version scanner — 25 October 2026

The Tools launcher now offers **CMS & PHP Version Scanner**. The shallow **CMS
Scanner** includes a **Scan PHP or CMS versions** shortcut. In Version checks,
**Scan PHP versions** adds a `php:php-src` check; the technology selector also
supports PHP directly. Runtime checks do not require CMS classification or a
plugin folder. Exact, inclusive and exclusive comparisons use the same version
rule contract as CMS checks.

## Default for this and future patches

The built-in database template and downloadable `default-version-checks.txt`
contain exactly the owner's corrected seven checks:

```text
#wordpress-core 1
wordpress:core >= 6.9.0 & < 6.9.5
wordpress:core >= 7.0.0 & < 7.0.2

#translate-press
translatepress-multilingual <= 3.3.1

#php
php:php-src >= 8.5.0 & < 8.5.12
php:php-src >= 8.4.0 & < 8.4.27
php:php-src >= 8.3.0 & < 8.3.36
php:php-src >= 8.2.0 & < 8.2.35
```

The plugin shorthand expands to
`wordpress:plugin:translatepress-multilingual`. The old default remains saved
with its contents intact and stops being the auto-loaded default. Other saved
templates remain unchanged. A versioned seed promotes the new seven-check
template once on existing databases as well as fresh installations. Reopening
the database or applying a later patch does not duplicate the template.

## Evidence and exports

PHP detection reads explicit `PHP/x.y.z` products from `X-Powered-By` on a
successful bounded response at the submitted/canonical target path. It accepts
one stable numeric version, including agreeing repeated declarations. Missing,
malformed, conflicting, blocked, failed, oversized or truncated responses do
not publish a PHP match. HTML prose, cookies and `.php` paths alone do not
identify a version. Distribution suffixes and prerelease declarations currently
remain unknown rather than being silently rewritten to a release version.

The PHP manual documents version disclosure through `expose_php`:
https://www.php.net/manual/en/ini.core.php#ini.expose-php .
These are reported runtime versions (`confidence: reported`), not proof of an
installed patch level or vulnerability. Controller result validation reconstructs
this confidence and rejects worker claims that inflate it to authoritative.

Matching PHP results stream as clean URLs in `php-ver.txt` and as
`URL<TAB>version` in `php_versions.txt`. Structured JSONL retains evidence and
matched rule identities. Existing CMS, WordPress core and plugin exports remain
available. The actual worker/controller publication tests verify durable transfer
and result validation; no Drive account or production scan is needed for dummy
acceptance.

TranslatePress has an explicit product-name alias because its published display
name differs from its installed folder slug. Source:
https://wordpress.org/plugins/translatepress-multilingual/ .
Unrelated readme identities remain rejected.

## Verification

- All 46 UI tests and TypeScript checking passed.
- Scanner race tests passed, including PHP transport negative controls,
  preserved target paths, non-CMS sites, exact range boundaries and official
  TranslatePress product-name fixtures.
- Processor store/distribution tests passed, including migration from the old
  default, preserving old/custom data, reinitialization and no duplicate seed.
- Enum worker/controller integration and publication tests passed, including
  PHP reported-confidence verification and retry without scanner re-execution.
- Actual scanner executable: two passes against 29 loopback dummy sites, 58
  duplicate-containing input lines per pass, exactly 17 matching receipts and
  nine PHP rows each; exact URL/version exports and negative controls passed.
- The Linux release repeated both executable dummy passes on the controller
  with the same exact counts, and its production dashboard build passed.

During verification, a dummy TranslatePress site lacked enough structural CMS
identity. Correcting that fixture preserved the detector's existing identity
threshold. Checking the official display name also exposed a real slug/title
mismatch; the explicit product alias and unrelated-readme negative controls now
cover it. Stale core-probe count expectations were updated for the already-added
RSS probe; checks still enforce one scanner execution during transfer recovery.

Local evidence: `docs/verification/cms-runtime-scanner-2026-10-25.json`.
Prepared Linux release: `cms-runtime-1792918948`.

## Deployment status

Release `cms-runtime-1792918948` is active in production following explicit owner
approval on 25 October 2026. Server, processor, dashboard, the worker-download
artifact, the canonical generic scanner and all seven worker cores/scanners
were updated. Running binary/scanner checksums match the release. All seven
workers are online and idle; processor execution remains paused with the same
seven interrupted records and no queued/running tasks. No private processor
tool was accessed or executed.

The browser verified the CMS-to-version-scanner shortcut, PHP controls, the
new auto-loaded seven-check template, exact WordPress/PHP ranges and the
TranslatePress 3.3.1 maximum. Authenticated template access and default TXT
matched; unauthenticated API access returned 401. Previous saved templates and
all three registered executable version pins were preserved.

Repeated executable dummy verification passed twice on the Mac, twice on the
Linux controller release, and twice more on the actually installed worker
scanner. Every pass processed 29 loopback sites/58 duplicate-containing inputs,
returned exactly 17 matching records and nine PHP rows, and rejected negative
controls without extra/duplicate records. This verifies dummy outputs; it does
not claim that a real target scan or Drive upload was performed.

The first activation attempt encountered a pre-existing absent optional generic
package. Automatic rollback restored the previous server/UI/database and all
services; the original artifacts remain under `attempt1-*` in the release.
The corrected rollout conditionally patches an existing package and always
updates the canonical generic scanner used by future package generation.
`readlink -f` was insufficient evidence that the package file existed; explicit
existence checks now guard this step. The second activation passed.

Controller/UI/database rollback:
`/opt/cnc-controller/releases/cms-runtime-1792918948/rollback`.
Each worker retains its old core/scanner and manifest under
`/opt/cnc/rollouts/cms-runtime-1792918948` with normal drain guards preserved.

Final evidence: `docs/verification/cms-runtime-production-2026-10-25.json`.
UI screenshot: `docs/verification/cms-runtime-production.png`.
Remote receipt: release `final-verification.json`.

Rollout entry points:

- `infra/linode/activate-cms-runtime-controller.py --apply` on the controller.
- `infra/linode/rollout-cms-runtime-workers.py` locally; offline dry-run by default.
  `--apply` performs one graceful worker restart at a time after explicit approval.

No credentials or private tool source are included in these scripts.
