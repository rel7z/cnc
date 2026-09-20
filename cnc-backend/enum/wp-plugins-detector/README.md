# wp-scanner

Fast, concurrent WordPress plugin detector.

## Build
```sh
go build -o wp-scanner
```

## Usage
```sh
./wp-scanner [flags]
```

### Flags
- `-domains string` : File containing domains (default: `wordpress.txt`)
- `-plugins string` : File containing plugin slugs (default: `plugins.txt`)
- `-c int`          : Number of concurrent workers (default: 50)
- `-timeout dur`    : HTTP request timeout (default: `10s`)
- `-output string`  : Directory to write per-plugin result files (default: `results`)

## Input Formats

**Domains (`wordpress.txt`)**
One domain per line. Lines starting with `#` are ignored.
```text
example.com
https://anotherexample.com
```

**Plugins (`plugins.txt`)**
Plugin slugs. Target specific versions with `slug:version`.
```text
akismet
contact-form-7:5.4.1
woocommerce
```

## Output
Streamed in real-time to the specified `-output` directory. One file per scanned plugin containing discovered URLs.
Prints summary table on completion showing Found, Blocked, and Matched counts.
