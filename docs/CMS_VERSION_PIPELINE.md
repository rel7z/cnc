# CMS version rules and public evidence

For the new PHP runtime option and corrected seven-check default, see
[CMS_RUNTIME_SCANNER.md](CMS_RUNTIME_SCANNER.md). Earlier 105-check defaults below
describe historical releases.

The deep enum launcher runs a typed, job-scoped pipeline. The shallow CMS scanner remains separate.

1. The dashboard validates named component rules. Target-scope controls and host, CIDR, protocol, and port filtering have been removed for new scans. Its authenticated launch route signs the exact request body, actor and timestamp.
2. The controller repeats rule validation, accepts syntactically valid HTTP/HTTPS input targets without launch-time DNS resolution, removes credentials/query/fragment data, and captures complete canonical input lines. Invalid URL lines are skipped and counted; they do not cancel valid input. It persists the immutable rule artifact and its SHA-256. Jobs default to no overall deadline. HTTP request timeouts remain configurable from 1 to 120 seconds.
3. Input is split into isolated shards capped at 1,000 lines or 1 MiB, without splitting lines. Workers receive a typed task and launch the scanner using an argument vector in a dedicated directory with job-specific wordlists.
4. Each worker owns one fair request budget, shared across its scanner processes. `ENUM_MAX_INFLIGHT_REQUESTS` defaults to eight and accepts 1–32. Invalid settings fail worker startup. The worker gateway resolves and pins each connection and removes forwarded credentials. The scanner's gateway client reuses a bounded loopback connection pool. DNS or transport failures are reported per target during execution.
5. The scanner collects approved public evidence. All clauses in each rule must match, and all matching rules appear in the same target/component record. Unknown, blocked, malformed and conflicting evidence never matches strict rules. A launch without rules preserves ordinary CMS and admin discovery, including Drupal classification.
6. Completed JSONL files are atomically finalized. Workers persist an outbox and transfer the complete bytes to the controller. The controller validates every record against the artifact and shard input, streams the transfer to disk, checks the digest, and durably accepts the immutable filename before acknowledgement. Repeated delivery has no duplicate effect; different bytes under the same filename fail the shard.
7. Accepted files enter the configured merged directory and wake the existing Drive uploader. Execution and publication statuses remain separate. Each worker admits at most two background enum transfers; uploads never hold CNC execution slots or block processor transfer retries. Worker loss marks started enum tasks interrupted; enum execution is not automatically rerun. Valid completed partial results remain acceptable after cancellation.

## Rule contract

```json
{
  "tool_id": "enum",
  "input_file": "/server/owned-targets.txt",
  "mode": "spread",
  "options": {"threads": 8, "timeout": 10},
  "version_rules": [
    {
      "id": "wp-range",
      "name": "WordPress 6.x",
      "component": "wordpress:core",
      "clauses": [
        {"operator": ">=", "version": "6"},
        {"operator": "<", "version": "7"}
      ]
    }
  ]
}
```

The dashboard supplies trusted audit headers; a direct unsigned enum launch is rejected. Set `CNC_DASHBOARD_AUDIT_SECRET` to the same secret of at least 32 characters on the controller and dashboard. The dashboard can use its existing `CNC_SESSION_SECRET` as the signing secret. Secrets never belong in the launch payload or evidence files.

Supported identities are WordPress core/plugin and Joomla core/component/module/plugin/library/template/package. Joomla plugin identities contain both group and name. Component names are lower-case slugs without Joomla type prefixes. Supported comparison operators are `=`, `<`, `<=`, `>`, `>=`. Versions normalize optional `v` and missing minor/patch parts. Prereleases require all three numeric parts; wildcards and build metadata are rejected. Numeric comparisons do not overflow fixed-width integers. Contradictory ranges and duplicate normalized rule sets are rejected on both client and server.

New scans have no target-scope filtering. Old clients may still send a `target_authorization` field; the controller ignores it for new jobs. Previously queued jobs retain their original immutable artifacts.

## Approved evidence

| Component | Sources, in precedence order |
| --- | --- |
| WordPress core | Explicit REST generator metadata; HTML generator metadata; core readme version; exact core asset banner |
| WordPress plugin | Plain-text readme with one valid Stable Tag; public `package.json` with exact plugin-name identity and version |
| Joomla | Type-specific manifest paths and an extension root with the expected type and one version; core additionally requires `files_joomla` identity |

WordPress asset fallback requires `/wp-includes/js/wp-emoji-release.min.js`, JavaScript content type, a leading `/*! WordPress VERSION */` banner and `wpemoji` content. Asset query-string versions are never inferred. This deliberately narrow fallback can leave a real installation's version unknown.

Evidence bodies are capped at 512 KiB. HTTP 403, known block pages, malformed metadata, oversized and truncated bodies are discarded. A randomized missing-page baseline rejects matching HTTP-200 catch-all responses. All agreeing candidates are retained and the lowest rank wins; disagreement produces a null version, null selected evidence and conflicted confidence. Evidence stores only source/rank/version/time and validation kind/content type/byte count, never response bodies or credentials.

Results use `enum-versions-JOB-SHARD.jsonl`. JSONL publications are streamed to disk with a bounded 8 MiB record size, rather than limiting a complete shard to 64 MiB. Worker transfers have a ten-minute attempt timeout; failures remain in the outbox for retry without tool execution. The Drive uploader streams immutable enum JSONL bytes without text merging or line-size truncation. It verifies matching remote objects by SHA-256 and rejects different bytes under the same filename.

## Verification and rollout status

Local scanner and API suites run under the Go race detector. They cover version grammar/precedence/ranges, scope and DNS pinning, immutable artifacts, evidence conflict/rejection, request fairness and cancellation, result identity/privacy, durable recovery, duplicate transfers, filename collisions and transfers larger than the previous 64 MiB limit. UI tests cover canonical rules/scopes, sessions and audit signatures.

The actual browser fixture used the dashboard, controller, six CNC workers and the compiled scanner. Two runs each processed 6,001 targets in seven shards. Records contain both matching rules; all publications were accepted. The observed run published a completed shard before the overall job finished and stayed at or below eight active requests per worker. See `processor/evidence/cms-version-browser-test.json` and its screenshot. The background-transfer path was separately verified across six workers with 6,001 records in `processor/evidence/cms-version-transfer-cluster-test.json`. The first browser run exposed a per-request loopback connection leak; a regression test now proves that 100 sequential probes reuse one connection.

Production release `cms-version-63ac12f949b0c902` is deployed to the controller, dashboard, scanner and all six workers after explicit owner approval. Intake was paused during the idle check and backups, then restored. Binary hashes were checked on every host; controller, dashboard and processor services are active. Dashboard login returns 200 and unauthenticated launch returns 401. State, previous binaries and service configuration remain available for rollback. The dashboard archive extraction directory was corrected during activation; the rollout script now extracts at the release root and checks service, route and worker health before reporting success.

The owned production fixture processed 6,001 targets across seven shards using all six workers. Every shard completed and its publication was accepted. Each worker stayed at or below eight active requests; conflicting plugin evidence was excluded. Results appeared in merged storage and real Google Drive before the whole job finished. All seven distinct Drive files match the merged bytes by SHA-256. See `processor/evidence/cms-version-production.json` and `processor/evidence/cms-version-completion-audit.md`. The temporary fixture server was shut down; evidence files remain available. Runtime credentials, processor routes and restricted private-tool files were preserved, and no Kiro access or firewall change was needed.

The optional-scope release was also verified through an authenticated production dashboard launch with `target_authorization` omitted entirely. The job records an empty scope and no acknowledgement. All six workers produced 6,001 unique records across seven accepted shards, with early merged/Drive visibility and exact Drive hashes. See `processor/evidence/cms-optional-scope-production.json`. Local tests cover ordinary scans without scope, strict-rule scanner execution/publication/retry/recovery without scope, cross-port fixture redirects, and the actual scope editor render/toggle behavior. Explicit constraints still require a complete valid scope and acknowledgement; turning the option off omits stale draft values.

## Friendly dashboard version checks

Open **Tools → Smart CMS & Deep Enumerator → Version checks → Add version check**. New checks default to WordPress plugins. Choose the CMS and what to check, enter the installed plugin/extension folder name (for example `elementor`), and choose Exactly, Older than, At most, Newer than, At least, or Between. Between includes both boundaries. Core checks hide the folder-name field; Joomla plugins additionally require their group.

The form shows a readable summary and automatically generates the check name. Advanced provides an optional custom name and multiple conditions, all of which must match. Expanding or collapsing Advanced does not change conditions. Unsupported versions, missing identities, impossible ranges and duplicate checks have inline errors. Adding no version checks keeps ordinary CMS/admin discovery.

The dashboard sends the existing component/clauses contract; scanner and API behavior is unchanged. Local browser evidence is in `processor/evidence/cms-friendly-version-browser.json`. The UI-only deployment retains the previous dashboard configuration for rollback and checks all controller/worker/scanner hashes before and after activation. Production browser acceptance and release evidence are recorded in `processor/evidence/cms-friendly-version-production.json` and `processor/evidence/cms-friendly-version-deployment.json`.

The Add version check button supports public HTTP origins as well as HTTPS and localhost. Check IDs use randomUUID when available and a unique local fallback otherwise; these identifiers are not authentication tokens. Regression tests exercise the actual click handler with randomUUID absent. Public-origin browser acceptance is recorded in `processor/evidence/cms-version-button-http-fix.json`.

### Saved version-check templates

The **Templates** controls save reusable groups of checks in the processor's
SQLite database. Enter a **Template name** and use **Save as new**, or select
and **Load** a template, edit its checks/name, and click **Update**. Loading
replaces the current checks, with confirmation for unsaved edits. **Delete**
requires confirmation and keeps the current form intact. Saving/loading never
launches a scan. Templates exclude target scope, input/output paths, and other
launch options. Empty check groups cannot be saved.

Draft order, blank automatic names, inclusive ranges, custom names and Advanced
AND conditions are preserved. Each load creates fresh editable check IDs. A
revision conflict leaves edits intact: refresh the list, then load the latest
template before updating. Names are unique without regard to ASCII case.

Dashboard CRUD: `GET/POST /api/version-check-templates` and
`GET/PUT/DELETE /api/version-check-templates/{id}`. The authenticated server-only
proxy calls equivalent `/v1` processor routes. Create/update bodies contain
`name` and `checks` (editable rule drafts); update/delete also require the last
`revision`. Responses include `id`, `name`, `checks`, `revision`, `created_at`
and `updated_at`. Invalid saves return 400, missing IDs 404, conflicting
revisions/names 409 and unavailable storage 503. Dashboard writes require the
existing signed session and same origin; processor bearer tokens never reach
the browser. The UI service loads the existing processor environment file.

`infra/linode/rollout-version-templates.py` deploys only processor and dashboard,
pauses intake, drains running jobs, backs up SQLite, preserves rollback binaries
and UI service configuration, and restores intake. The schema is additive; a
binary rollback retains the database to avoid losing newly accepted results.
The database snapshot is retained for deliberate recovery. No private-tool
source or Kiro content is included in the release or source sync.

### TXT template files

**Upload TXT** loads a valid UTF-8 `.txt` file's name and checks into the form.
Existing unsaved edits require confirmation before replacement. Uploading does
not write to SQLite or launch a job. Use **Save as new** to persist the uploaded
group, or explicitly select an existing template and confirm **Update** to
replace it. Revision conflicts still protect against stale overwrites.
**Download TXT** exports current checks including unsaved edits.

The portable format is `elementor < 3.25.0`, one check per line. A bare folder
name means a WordPress plugin. Operators are `=`, `<`, `<=`, `>`, and `>=`.
Join AND conditions with `&`; `>= 3.24.0 & <= 3.25.0` is an inclusive range.
Use full identities for core and Joomla components, such as `wordpress:core`
and `joomla:plugin:system:example`. An optional `| Custom name` suffix preserves
custom check names; otherwise names are automatic. The optional first header
`# template: My name` supplies the template name; without it the filename is
used. Blank lines and `#` comments are ignored. Quoted names support escaped
newlines and quotation marks. Limits remain 256 KiB, 100 checks, and 32 conditions
per check. Parser errors identify the file line without discarding current edits.

The dashboard's **TXT format guide** downloads a valid commented example
covering every supported comparison and component type. The repository samples
are `cnc-ui/public/version-template-format.txt` and
`cnc-ui/public/version-template-example.txt`. Database/API representations and
scanner matching semantics remain unchanged; portable TXT omits database IDs,
revisions, timestamps, scope, and launch settings.

## WordPress core version scanner

In **Tools → Smart CMS & Deep Enumerator → Version checks**, click **Scan
WordPress core versions**. This adds a ready-to-run core check with **Any
detected version** selected; no plugin name or version entry is required.
Choose Exactly, Older than, At least, or Between to restrict the results.
To scan core alone, clear other checks before adding the core check. Saved
version templates preserve this check using the existing rule contract.

Confirmed matches stream to `wordpress_core_versions.txt` as `URL<TAB>version`,
in addition to the existing CMS URL list and structured evidence receipt.
The scanner now checks the RSS `/feed/` generator as well as existing REST,
HTML generator, readme, and explicit core-banner evidence. RSS parsing requires
one channel generator identifying wordpress.org with one valid version.
This follows WordPress's documented RSS generator format:
https://developer.wordpress.org/reference/functions/get_the_generator/ .
Hidden, malformed, blocked, ambiguous, and conflicting versions do not produce
confirmed version rows; CMS discovery can still identify those sites.

Verification: UI shortcut click/normalization/filter tests, TypeScript checks,
and enum HTTP-fixture tests cover feed-only detection, comparison exclusion,
hidden versions, conflicts, and structured/text output. No private processor
tools are needed or accessed. This feature has not yet been deployed.
