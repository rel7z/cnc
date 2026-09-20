# joomla-scanner

Concurrent Joomla extension detector. Give it a list of sites and a list of extensions — it tells you which ones are installed.

---

## Build

```bash
go build -o joomla-scanner .
```

---

## Usage

```bash
./joomla-scanner -domains joomla.txt -plugins plugins.txt -c 50 -timeout 12s -output results/
```

| Flag | Default | Description |
|------|---------|-------------|
| `-domains` | `joomla.txt` | File with target domains, one per line |
| `-plugins` | `plugins.txt` | File with extensions to probe, one per line |
| `-c` | `50` | Number of concurrent workers |
| `-timeout` | `10s` | HTTP request timeout per probe |
| `-output` | `results` | Directory to write result files into |

---

## plugins.txt format

Each line is one extension. Lines starting with `#` and blank lines are ignored.

### Component
```
com_gridbox
com_gridbox:2.18.1.1
```
Probes `/administrator/components/com_<slug>/<slug>.xml`
(falls back to `/components/com_<slug>/<slug>.xml`)

Most third-party extensions (page builders, e-commerce, forms, etc.) are components.

---

### Library
```
lib_astroid
lib_astroid:3.4.3
```
Probes `/libraries/<slug>/<slug>.xml`

Libraries are shared frameworks installed alongside templates.
**Astroid Framework** (`lib_astroid`) is the most common example — it powers many commercial Joomla templates.

---

### Plugin
```
plugin:system:astroid
plugin:system:astroid:2.0.0
plugin:content:joomla
plugin:authentication:ldap
```
Format: `plugin:<group>:<slug>[:<version>]`

Probes `/plugins/<group>/<slug>/<slug>.xml`

Common groups: `system`, `content`, `authentication`, `editors`, `search`, `user`, `finder`

---

### Module
```
mod_menu
mod_menu:5.1.0
```
Probes `/modules/mod_<slug>/mod_<slug>.xml`

---

### Template
```
tpl_cassiopeia
tpl_shaper_helix
```
Probes `/templates/<slug>/templateDetails.xml`

---

## Version filter (optional)

Append `:<version>` to any entry to get a second output file containing only domains running that exact version:

```
com_gridbox:2.18.1.1
lib_astroid:3.4.3
plugin:system:astroid:2.0.0
```

This creates both:
- `com_gridbox.txt` — all sites with the extension (any version)
- `com_gridbox-2.18.1.1.txt` — only sites running that exact version

Useful for CVE targeting.

---

## Output

Results land in `results/` (or whatever `-output` points to). One file per extension, plain domain list:

```
https://www.example.com
https://www.another-site.org
```

A domain appears in the file if the probe returned **200** (found, version extracted from XML) or **403** (found, but file access is blocked — common on hardened installs).

The terminal summary shows counts:

```
Extension                             TargetVer       Found  Blocked   Matched    Total
──────────────────────────────────────────────────────────────────────────────────────
com_gridbox                           (any)               1        0         -        3
lib_astroid                           (any)               0        3         -        3
```

- **Found** — 200, version read from manifest
- **Blocked** — 403, extension present but manifest access denied
- **Matched** — domains matching the exact version filter (only shown when a version is set)
- **Total** — total domains scanned

---

## Detection method

Joomla extensions ship an XML manifest file that declares the extension name, version, and type. The scanner fetches that file directly — no login, no crawling, no JS execution needed.

| Type | Manifest path |
|------|--------------|
| Component | `/administrator/components/com_<slug>/<slug>.xml` |
| Library | `/libraries/<slug>/<slug>.xml` |
| Plugin | `/plugins/<group>/<slug>/<slug>.xml` |
| Module | `/modules/mod_<slug>/mod_<slug>.xml` |
| Template | `/templates/<slug>/templateDetails.xml` |

HTTPS is tried first, HTTP as fallback.

---

## Example plugins.txt

```
# Components
com_gridbox
com_jevents
com_k2
com_virtuemart

# Libraries / Frameworks
lib_astroid

# System plugins
plugin:system:astroid
plugin:system:cache

# Modules
mod_menu
mod_search

# Templates
tpl_cassiopeia
tpl_shaper_helix
```
